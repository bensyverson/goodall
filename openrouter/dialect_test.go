package openrouter

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/bensyverson/goodall"
)

func TestDialectString(t *testing.T) {
	for _, tc := range []struct {
		dialect Dialect
		want    string
	}{
		{OpenRouter, "openrouter"},
		{OpenAI, "openai"},
		{LMStudio, "lmstudio"},
		{Generic, "generic"},
		{Dialect("vllm"), "vllm"},
	} {
		if got := tc.dialect.String(); got != tc.want {
			t.Errorf("Dialect(%q).String() = %q, want %q", string(tc.dialect), got, tc.want)
		}
	}
}

func TestDialectQuirks(t *testing.T) {
	for _, tc := range []struct {
		dialect Dialect
		want    Quirks
	}{
		{OpenRouter, Quirks{
			Reasoning:        ReasoningObject,
			ReasoningDetails: true,
			CacheControl:     true,
			Routing:          true,
			Plugins:          true,
			SessionID:        true,
			Debug:            true,
			Metadata:         true,
			UsageCost:        true,
		}},
		{OpenAI, Quirks{
			Reasoning:   ReasoningEffortField,
			OutputCap:   CapMaxCompletionTokens,
			StreamUsage: true,
			Metadata:    true,
		}},
		{LMStudio, Quirks{Reasoning: ReasoningEffortField, StreamUsage: true}},
		{Generic, Quirks{}},
		{Dialect("something-new"), Quirks{}},
	} {
		if got := tc.dialect.Quirks(); got != tc.want {
			t.Errorf("%s.Quirks() = %+v, want %+v", tc.dialect, got, tc.want)
		}
	}
}

// everyFeatureRequest is a request that sets every neutral feature the
// translation can express, so a dialect test can assert on the whole body.
func everyFeatureRequest(t *testing.T) *goodall.Request {
	t.Helper()
	tool, err := goodall.NewTool("lookup", "Look something up", func(_ context.Context, in struct {
		Query string `json:"query" desc:"what to look up"`
	}) (goodall.ToolResult, error) {
		_ = in
		return goodall.ToolResult{}, nil
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	return &goodall.Request{
		Model:  "some/model",
		System: []goodall.Text{{Text: "Be helpful."}, {Text: "Be brief.", Cache: &goodall.CacheControl{}}},
		Messages: []goodall.Message{
			goodall.UserMessage(
				goodall.Text{Text: "look at this", Cache: &goodall.CacheControl{TTL: goodall.CacheTTL1h}},
				goodall.Image{Source: goodall.BytesSource("image/png", []byte{1, 2, 3})},
				goodall.Document{Source: goodall.URLSource("https://example.com/paper.pdf"), Title: "paper.pdf"},
			),
			goodall.AssistantMessage(
				goodall.Thinking{Text: "pondering", Signature: "sig"},
				goodall.RedactedThinking{Data: "opaque"},
				goodall.Text{Text: "one moment"},
				goodall.ToolUse{ID: "call_1", Name: "lookup", Input: []byte(`{"query":"x"}`)},
			),
			goodall.UserMessage(
				goodall.ToolResult{ToolUseID: "call_1", Content: goodall.Blocks{goodall.Text{Text: "found it"}}},
				goodall.Text{Text: "thanks"},
			),
		},
		Tools:         []goodall.Tool{tool},
		ToolChoice:    goodall.ChooseAny().Serial(),
		MaxTokens:     256,
		Thinking:      goodall.ThinkingConfig{Effort: goodall.EffortHigh, Display: goodall.DisplayOmitted},
		Cache:         goodall.CacheManual,
		StopSequences: []string{"STOP"},
		Metadata:      map[string]string{"user_id": "u-1", "tenant": "t-1"},
		Extensions: Extensions{
			Routing:   &ProviderRouting{Order: []string{"anthropic"}, Sort: SortThroughput},
			Models:    []string{"other/model"},
			Plugins:   []Plugin{{ID: PluginFileParser}},
			SessionID: "sess-1",
			Debug:     Debug{EchoUpstreamBody: true},
		},
	}
}

// openRouterOnlyMembers are the request members no OpenAI-compatible server
// outside OpenRouter is asked to understand. Each is written with its colon,
// so a member name is not confused with the same word as a value — "user" is
// also a role.
var openRouterOnlyMembers = []string{
	`"reasoning":`,
	`"reasoning_effort":`,
	`"reasoning_details":`,
	`"cache_control":`,
	`"provider":`,
	`"models":`,
	`"plugins":`,
	`"session_id":`,
	`"debug":`,
	`"metadata":`,
	`"user":`,
}

func TestGenericDialectSendsNoOpenRouterOnlyMembers(t *testing.T) {
	req := everyFeatureRequest(t)
	wire, err := translateRequest(req, Generic, true)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, member := range openRouterOnlyMembers {
		if bytes.Contains(body, []byte(member)) {
			t.Errorf("Generic request contains %s\nbody: %s", member, body)
		}
	}
}

func TestOpenRouterDialectSendsItsOwnMembers(t *testing.T) {
	req := everyFeatureRequest(t)
	wire, err := translateRequest(req, OpenRouter, true)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, member := range []string{
		`"reasoning":`, `"reasoning_details":`, `"cache_control":`, `"provider":`,
		`"models":`, `"plugins":`, `"session_id":`, `"debug":`, `"metadata":`, `"user":`,
	} {
		if !bytes.Contains(body, []byte(member)) {
			t.Errorf("OpenRouter request is missing %s\nbody: %s", member, body)
		}
	}
	if bytes.Contains(body, []byte(`"reasoning_effort":`)) {
		t.Errorf("OpenRouter request should carry the reasoning object, not reasoning_effort\nbody: %s", body)
	}
}

func TestTheOpenAIDialectCapsTheAnswerWithMaxCompletionTokens(t *testing.T) {
	// Recorded 2026-09-15: api.openai.com refuses max_tokens on its
	// reasoning models with HTTP 400 "Unsupported parameter: 'max_tokens'
	// is not supported with this model. Use 'max_completion_tokens'
	// instead." OpenRouter accepts either, so only the OpenAI dialect
	// changes the member name.
	req := &goodall.Request{
		Model:     "gpt-6-astra",
		MaxTokens: 512,
		Messages:  []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
	}
	for _, tc := range []struct {
		dialect Dialect
		want    string
		absent  string
	}{
		{OpenAI, `"max_completion_tokens":512`, `"max_tokens"`},
		{OpenRouter, `"max_tokens":512`, `"max_completion_tokens"`},
		{LMStudio, `"max_tokens":512`, `"max_completion_tokens"`},
		{Generic, `"max_tokens":512`, `"max_completion_tokens"`},
	} {
		t.Run(tc.dialect.String(), func(t *testing.T) {
			wire, err := translateRequest(req, tc.dialect, false)
			if err != nil {
				t.Fatalf("translating: %v", err)
			}
			body, err := json.Marshal(wire)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}
			if !bytes.Contains(body, []byte(tc.want)) {
				t.Errorf("the body does not carry %s:\n%s", tc.want, body)
			}
			if bytes.Contains(body, []byte(tc.absent)) {
				t.Errorf("the body carries %s, which this dialect does not take:\n%s", tc.absent, body)
			}
		})
	}
}

func TestTheDialectsThatMustAskForStreamedUsageDoSo(t *testing.T) {
	// Recorded 2026-09-15: a streamed call to api.openai.com returns no
	// usage chunk at all unless the request asked for one, so every
	// streamed OpenAI-dialect answer reported zero tokens. OpenRouter
	// always sends usage and documents the option as a no-op, so it is not
	// sent there; Generic keeps to the members every server implements.
	req := &goodall.Request{
		Model:    "some/model",
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
	}
	for _, tc := range []struct {
		dialect Dialect
		want    bool
	}{
		{OpenAI, true},
		{LMStudio, true},
		{OpenRouter, false},
		{Generic, false},
	} {
		t.Run(tc.dialect.String(), func(t *testing.T) {
			streamed, err := translateRequest(req, tc.dialect, true)
			if err != nil {
				t.Fatalf("translating the streamed request: %v", err)
			}
			body, err := json.Marshal(streamed)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}
			asked := bytes.Contains(body, []byte(`"stream_options":{"include_usage":true}`))
			if asked != tc.want {
				t.Errorf("stream_options asked = %v, want %v:\n%s", asked, tc.want, body)
			}

			// A blocking call reports usage without being asked, and
			// stream_options on a non-streamed request is an error on
			// some servers.
			blocking, err := translateRequest(req, tc.dialect, false)
			if err != nil {
				t.Fatalf("translating the blocking request: %v", err)
			}
			body, err = json.Marshal(blocking)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}
			if bytes.Contains(body, []byte(`"stream_options"`)) {
				t.Errorf("a blocking request carries stream_options:\n%s", body)
			}
		})
	}
}
