package openrouter

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// probeRecording reads one exchange recorded by scripts/probe-responses.
func probeRecording(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "scripts", "probe-responses", "testdata", name))
	if err != nil {
		t.Fatalf("reading recording %s: %v", name, err)
	}
	return body
}

// sentBy returns the path and body one request sends under the OpenAI dialect. The
// server answers with a recorded response, so the call itself succeeds.
func sentBy(t *testing.T, req *goodall.Request) (string, string) {
	t.Helper()
	var path, body string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request: %v", err)
		}
		body = string(raw)
		w.Write(probeRecording(t, "two_turn_block_gpt-6-astra.turn2.response.json"))
	}, WithDialect(OpenAI))
	if _, err := c.Complete(t.Context(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return path, body
}

// sentBody is only the body of sentBy.
func sentBody(t *testing.T, req *goodall.Request) string {
	t.Helper()
	_, body := sentBy(t, req)
	return body
}

func weatherLookup(t *testing.T, opts ...goodall.ToolOption) goodall.Tool {
	t.Helper()
	tool, err := goodall.NewTool("get_weather", "Get the weather for a city", func(_ context.Context, in struct {
		City string `json:"city"`
	}) (goodall.ToolResult, error) {
		_ = in
		return goodall.ToolResult{}, nil
	}, opts...)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	return tool
}

func TestTheOpenAIDialectPostsToResponses(t *testing.T) {
	path, _ := sentBy(t, &goodall.Request{
		Model:    "gpt-6-astra",
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
	})
	if path != "/responses" {
		t.Errorf("path = %q, want /responses", path)
	}
}

func TestAResponsesRequestIsExactlyThisShape(t *testing.T) {
	got := sentBody(t, &goodall.Request{
		Model:     "gpt-6-astra",
		System:    []goodall.Text{{Text: "Be helpful."}, {Text: "Be brief."}},
		MaxTokens: 512,
		Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized},
		Metadata:  map[string]string{"user_id": "u-1", "tenant": "t-1", "app": "a-1"},
		Messages:  []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
	})
	want := `{"model":"gpt-6-astra","instructions":"Be helpful.\n\nBe brief.",` +
		`"reasoning":{"effort":"low","summary":"auto"},"max_output_tokens":512,` +
		`"metadata":{"app":"a-1","tenant":"t-1"},"safety_identifier":"u-1","store":false,` +
		`"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	if got != want {
		t.Errorf("body:\n got %s\nwant %s", got, want)
	}
}

func TestAConversationBecomesItemsInBlockOrder(t *testing.T) {
	reasoning := `{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"abc"}`
	got := sentBody(t, &goodall.Request{
		Model: "gpt-6-astra",
		Messages: []goodall.Message{
			goodall.UserMessage(
				goodall.Text{Text: "look"},
				goodall.Image{Source: goodall.BytesSource("image/png", []byte{1, 2, 3})},
				goodall.Image{Source: goodall.URLSource("https://example.com/a.png")},
				goodall.Document{Source: goodall.BytesSource("application/pdf", []byte{1}), Title: "note.pdf"},
				goodall.Document{Source: goodall.BytesSource("application/pdf", []byte{1})},
				goodall.Document{Source: goodall.URLSource("https://example.com/b.pdf"), Title: "b.pdf"},
			),
			goodall.AssistantMessage(
				goodall.Thinking{Text: "shown", Raw: jsontext.Value(reasoning)},
				goodall.Text{Text: "one"},
				goodall.Text{Text: "two"},
				goodall.ToolUse{ID: "call_1", Name: "get_weather", Input: jsontext.Value(`{"city":"Paris"}`)},
				goodall.ToolUse{ID: "call_2", Name: "nothing"},
			),
			goodall.UserMessage(
				goodall.Text{Text: "and then"},
				goodall.ToolResult{ToolUseID: "call_1", Content: goodall.Blocks{goodall.Text{Text: "clear"}}},
				goodall.ToolResult{ToolUseID: "call_2", IsError: true, Content: goodall.Blocks{goodall.Text{Text: "no"}}},
			),
		},
	})
	want := `"input":[` +
		`{"type":"message","role":"user","content":[` +
		`{"type":"input_text","text":"look"},` +
		`{"type":"input_image","image_url":"data:image/png;base64,AQID"},` +
		`{"type":"input_image","image_url":"https://example.com/a.png"},` +
		`{"type":"input_file","filename":"note.pdf","file_data":"data:application/pdf;base64,AQ=="},` +
		`{"type":"input_file","filename":"document","file_data":"data:application/pdf;base64,AQ=="},` +
		`{"type":"input_file","file_url":"https://example.com/b.pdf"}]},` +
		reasoning + `,` +
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"one"},{"type":"output_text","text":"two"}]},` +
		`{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"},` +
		`{"type":"function_call","call_id":"call_2","name":"nothing","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"call_1","output":"clear"},` +
		`{"type":"function_call_output","call_id":"call_2","output":"Error: no"},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"and then"}]}]}`
	if !strings.HasSuffix(got, want) {
		t.Errorf("input:\n got %s\nwant the suffix %s", got, want)
	}
}

func TestASystemMessageInTheConversationKeepsItsRole(t *testing.T) {
	got := sentBody(t, &goodall.Request{
		Model:    "gpt-6-astra",
		Messages: []goodall.Message{{Role: goodall.RoleSystem, Content: goodall.Blocks{goodall.Text{Text: "be kind"}}}},
	})
	want := `{"type":"message","role":"system","content":[{"type":"input_text","text":"be kind"}]}`
	if !strings.Contains(got, want) {
		t.Errorf("body %s lacks %s", got, want)
	}
}

func TestAToolResultWithAnImageSendsAPartList(t *testing.T) {
	got := sentBody(t, &goodall.Request{
		Model: "gpt-6-luna",
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.ToolResult{ToolUseID: "call_1", IsError: true, Content: goodall.Blocks{
				goodall.Text{Text: "see"},
				goodall.Image{Source: goodall.BytesSource("image/png", []byte{1, 2, 3})},
				goodall.Document{Source: goodall.BytesSource("application/pdf", []byte{1})},
				goodall.Thinking{Text: "hm"},
			}},
		)},
	})
	want := `{"type":"function_call_output","call_id":"call_1","output":[` +
		`{"type":"input_text","text":"Error: "},` +
		`{"type":"input_text","text":"see"},` +
		`{"type":"input_image","image_url":"data:image/png;base64,AQID"},` +
		`{"type":"input_file","filename":"document","file_data":"data:application/pdf;base64,AQ=="},` +
		`{"type":"input_text","text":"[thinking]"}]}`
	if !strings.Contains(got, want) {
		t.Errorf("body:\n got %s\nwant it to hold %s", got, want)
	}
}

func TestAnEmptyToolResultStillSendsItsOutput(t *testing.T) {
	got := sentBody(t, &goodall.Request{
		Model:    "gpt-6-luna",
		Messages: []goodall.Message{goodall.UserMessage(goodall.ToolResult{ToolUseID: "call_1"})},
	})
	if want := `{"type":"function_call_output","call_id":"call_1","output":""}`; !strings.Contains(got, want) {
		t.Errorf("body %s lacks %s, and the wire requires the member", got, want)
	}
}

func TestAProviderFileIDIsRefusedOnResponses(t *testing.T) {
	for name, block := range map[string]goodall.Block{
		"image":    goodall.Image{Source: goodall.FileSource("file_1")},
		"document": goodall.Document{Source: goodall.FileSource("file_1")},
	} {
		t.Run(name, func(t *testing.T) {
			c := New(WithDialect(OpenAI))
			_, _, err := c.requestBody(&goodall.Request{
				Model:    "gpt-6-luna",
				Messages: []goodall.Message{goodall.UserMessage(block)},
			}, false)
			if !errors.Is(err, goodall.KindInvalidRequest) {
				t.Errorf("err = %v, want it to wrap KindInvalidRequest", err)
			}
		})
	}
}

// The blocks here are not OpenAI's reasoning items: a Claude block with a
// signature, one from OpenRouter's reasoning_details, unreadable bytes, and a
// redacted one. An unrecognized block is not among them: it goes back, whoever's.
func TestAHistoryFromAnotherProviderStaysSendable(t *testing.T) {
	got := sentBody(t, &goodall.Request{
		Model: "gpt-6-astra",
		Messages: []goodall.Message{
			goodall.UserMessage(goodall.Text{Text: "hi"}),
			goodall.AssistantMessage(
				goodall.Thinking{Text: "claude thought", Signature: "sig"},
				goodall.Thinking{Text: "router thought", Raw: jsontext.Value(`{"type":"reasoning.text","text":"router thought"}`)},
				goodall.Thinking{Raw: jsontext.Value(`not json`)},
				goodall.RedactedThinking{Data: "opaque", Raw: jsontext.Value(`{"type":"reasoning","encrypted_content":"x"}`)},
				goodall.Text{Text: "the answer"},
			),
		},
	})
	want := `"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},` +
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"the answer"}]}]}`
	if !strings.HasSuffix(got, want) {
		t.Errorf("input:\n got %s\nwant the suffix %s", got, want)
	}
}

func TestReasoningCarriesEffortAndSummary(t *testing.T) {
	never := &goodall.ModelInfo{Capabilities: goodall.Capabilities{ThinkingOff: goodall.ThinkingOffNever}}
	for _, tc := range []struct {
		name string
		cfg  goodall.ThinkingConfig
		info *goodall.ModelInfo
		want string
	}{
		{"unset sends nothing", goodall.ThinkingConfig{}, nil, ""},
		{"a default display leaves the summary to the provider", goodall.ThinkingConfig{Effort: goodall.EffortLow}, nil, `{"effort":"low"}`},
		{"omitted asks for no summary", goodall.ThinkingConfig{Effort: goodall.EffortHigh, Display: goodall.DisplayOmitted}, nil, `{"effort":"high"}`},
		{"summarized asks for one", goodall.ThinkingConfig{Effort: goodall.EffortMax, Display: goodall.DisplaySummarized}, nil, `{"effort":"max","summary":"auto"}`},
		{"summarized with a default effort", goodall.ThinkingConfig{Display: goodall.DisplaySummarized}, nil, `{"summary":"auto"}`},
		{"off is none", goodall.ThinkingConfig{Effort: goodall.EffortOff, Display: goodall.DisplaySummarized}, nil, `{"effort":"none"}`},
		{"off where it cannot be turned off sends nothing", goodall.ThinkingConfig{Effort: goodall.EffortOff, Display: goodall.DisplaySummarized}, never, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sentBody(t, &goodall.Request{
				Model:     "gpt-6-astra",
				Thinking:  tc.cfg,
				ModelInfo: tc.info,
				Messages:  []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
			})
			if tc.want == "" {
				if strings.Contains(got, `"reasoning"`) {
					t.Errorf("body %s carries reasoning", got)
				}
				return
			}
			if want := `"reasoning":` + tc.want + `,`; !strings.Contains(got, want) {
				t.Errorf("body %s lacks %s", got, want)
			}
		})
	}
}

func TestACachePolicySendsNoMarkersOnResponses(t *testing.T) {
	got := sentBody(t, &goodall.Request{
		Model:  "gpt-6-luna",
		Cache:  goodall.CacheAuto,
		System: []goodall.Text{{Text: "s", Cache: &goodall.CacheControl{}}},
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.Text{Text: "hi", Cache: &goodall.CacheControl{}},
		)},
	})
	if strings.Contains(got, "cache") {
		t.Errorf("body %s carries a cache member", got)
	}
}

func TestAnotherProvidersExtensionsAreRefusedOnResponses(t *testing.T) {
	c := New(WithDialect(OpenAI))
	_, _, err := c.requestBody(&goodall.Request{
		Model:      "gpt-6-luna",
		Extensions: foreignExtension{},
		Messages:   []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
	}, false)
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("err = %v, want it to wrap KindInvalidRequest", err)
	}
}

func TestTwoConsecutiveRequestsShareAByteIdenticalPrefix(t *testing.T) {
	first := &goodall.Request{
		Model:     "gpt-6-astra",
		System:    []goodall.Text{{Text: "Be brief."}},
		Tools:     []goodall.Tool{weatherLookup(t)},
		MaxTokens: 512,
		Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized},
		Metadata:  map[string]string{"c": "3", "a": "1", "b": "2", "user_id": "u"},
		Messages:  []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
	}
	second := *first
	second.Messages = append(append([]goodall.Message{}, first.Messages...),
		goodall.AssistantMessage(
			goodall.Thinking{Raw: jsontext.Value(`{"type":"reasoning","encrypted_content":"x"}`)},
			goodall.ToolUse{ID: "call_1", Name: "get_weather", Input: jsontext.Value(`{"city":"Paris"}`)},
		),
		goodall.UserMessage(goodall.ToolResult{ToolUseID: "call_1", Content: goodall.Blocks{goodall.Text{Text: "clear"}}}),
	)
	one, again, two := sentBody(t, first), sentBody(t, first), sentBody(t, &second)
	if one != again {
		t.Errorf("the same request made different bytes:\n%s\n%s", one, again)
	}
	prefix := strings.TrimSuffix(one, "]}")
	if prefix == one {
		t.Fatalf("the input is not the last member, so the conversation cannot extend the prefix: %s", one)
	}
	if !strings.HasPrefix(two, prefix) {
		t.Errorf("the next turn does not extend the last:\nfirst  %s\nsecond %s", one, two)
	}
}

func TestTheReasoningItemTravelsAsItsOwnBytes(t *testing.T) {
	// Escapes, member order and non-ASCII are all things a re-encode would change,
	// and the server would say nothing if it did.
	raw := `{"summary":[{"text":"café caf` + "é" + `\n\"q\"","type":"summary_text"}],"type":"reasoning","id":"rs_1","encrypted_content":"a/b+c=="}`
	got := sentBody(t, &goodall.Request{
		Model: "gpt-6-astra",
		Messages: []goodall.Message{
			goodall.UserMessage(goodall.Text{Text: "hi"}),
			goodall.AssistantMessage(goodall.Thinking{Raw: jsontext.Value(raw)}),
		},
	})
	if !strings.Contains(got, raw) {
		t.Errorf("the reasoning item was changed on the way out:\n got %s\nwant it to hold %s", got, raw)
	}
}
