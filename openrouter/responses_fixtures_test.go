package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// These read what scripts/record-fixtures -provider openai recorded through the
// production client, where the probe's recordings in scripts/probe-responses were
// sent by a hand-built request. Together they say the wire reads what the API sends
// and that the API accepts what goodall itself builds.

// recordedResponsesStream collects one live_openai_ stream under the OpenAI dialect.
func recordedResponsesStream(t *testing.T, name string) *goodall.Response {
	t.Helper()
	resp, err := responsesStream(t, fixture(t, name)).Collect()
	if err != nil {
		t.Fatalf("collecting %s: %v", name, err)
	}
	return resp
}

func TestEveryRecordedResponsesStreamCollectsWithoutAProtocolError(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "live_openai_*.sse"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no live_openai_*.sse recordings; run scripts/record-fixtures -provider openai")
	}
	for _, path := range paths {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			resp := recordedResponsesStream(t, name)
			if resp.ID == "" {
				t.Error("the collected response has no id")
			}
			if resp.StopReason == goodall.StopNone {
				t.Error("the collected response has no stop reason")
			}
			if resp.Message.Partial {
				t.Error("the collected message is marked partial, so the recording ended early")
			}
			if resp.Usage.Output == 0 {
				t.Error("the collected response reports no output tokens")
			}
			if resp.Cost.Reported {
				t.Error("a cost was reported, though api.openai.com sends no figure")
			}
		})
	}
}

func TestTheRecordedResponsesAnswersReadTheirInput(t *testing.T) {
	for name, want := range map[string]string{
		"live_openai_text.sse":  "paris",
		"live_openai_image.sse": recordedImageColor,
		"live_openai_pdf.sse":   strings.ToLower(recordedPDFPhrase),
	} {
		t.Run(name, func(t *testing.T) {
			resp := recordedResponsesStream(t, name)
			if resp.StopReason != goodall.StopEndTurn {
				t.Errorf("stop reason = %s, want %s", resp.StopReason, goodall.StopEndTurn)
			}
			if got := strings.ToLower(resp.Message.Text()); !strings.Contains(got, want) {
				t.Errorf("text = %q, want it to contain %q", got, want)
			}
		})
	}
}

func TestTheRecordedBlockingResponsesAnswerReadsLikeTheStream(t *testing.T) {
	resp, err := responsesCompleted(t, fixture(t, "live_openai_text_blocking.json"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != goodall.StopEndTurn {
		t.Errorf("stop reason = %s, want %s", resp.StopReason, goodall.StopEndTurn)
	}
	if !strings.Contains(strings.ToLower(resp.Message.Text()), "paris") {
		t.Errorf("text = %q", resp.Message.Text())
	}
	if resp.Usage.Output == 0 {
		t.Error("the blocking response reports no output tokens")
	}
}

func TestTheRecordedResponsesToolCallsAskAboutTheCity(t *testing.T) {
	for _, name := range []string{"live_openai_tool_use.sse", "live_openai_strict_tool_use.sse"} {
		t.Run(name, func(t *testing.T) {
			resp := recordedResponsesStream(t, name)
			if resp.StopReason != goodall.StopToolUse {
				t.Errorf("stop reason = %s, want %s", resp.StopReason, goodall.StopToolUse)
			}
			uses := resp.Message.ToolUses()
			if len(uses) != 1 {
				t.Fatalf("tool uses = %d, want 1", len(uses))
			}
			if uses[0].Name != "get_weather" || !strings.Contains(string(uses[0].Input), recordedToolCity) {
				t.Errorf("tool use = %s(%s)", uses[0].Name, uses[0].Input)
			}
		})
	}
}

// A tool's strict is always stated, since leaving it out lets OpenAI rewrite the
// schema, so both recorded requests must say which they asked for.
func TestTheRecordedResponsesRequestsStateStrict(t *testing.T) {
	for name, want := range map[string]bool{
		"live_openai_tool_use.request.json":        false,
		"live_openai_strict_tool_use.request.json": true,
	} {
		var sent struct {
			Tools []struct {
				Strict *bool `json:"strict"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(fixture(t, name), &sent); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(sent.Tools) != 1 || sent.Tools[0].Strict == nil || *sent.Tools[0].Strict != want {
			t.Errorf("%s: tools = %+v, want strict stated as %v", name, sent.Tools, want)
		}
	}
}

// The second recorded turn answering is the proof the API accepted what goodall
// built, and this holds it to carrying the reasoning: the server would have taken
// the request without it and said nothing.
func TestTheRecordedResponsesTurnReplaysItsReasoningByteForByte(t *testing.T) {
	first := recordedResponsesStream(t, "live_openai_thinking_tool_use.sse")
	if first.StopReason != goodall.StopToolUse {
		t.Fatalf("stop reason = %s, want %s", first.StopReason, goodall.StopToolUse)
	}
	var raw jsontext.Value
	for _, blk := range first.Message.Content {
		if thinking, ok := blk.(goodall.Thinking); ok && len(thinking.Raw) > 0 {
			raw = thinking.Raw
		}
	}
	if raw == nil {
		t.Fatal("the recorded turn has no reasoning item, so this exchange proves nothing; re-record it")
	}
	var item struct {
		Encrypted string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatal(err)
	}
	if item.Encrypted == "" {
		t.Fatalf("the reasoning item has no ciphertext: %s", raw)
	}

	want := compacted(t, raw)
	var replayed bool
	for _, sent := range itemsSent(t, string(fixture(t, "live_openai_thinking_tool_use_turn2.request.json"))) {
		replayed = replayed || compacted(t, jsontext.Value(sent)) == want
	}
	if !replayed {
		t.Errorf("turn two's request does not carry the reasoning item as turn one's stream gave it:\n%s", want)
	}

	second := recordedResponsesStream(t, "live_openai_thinking_tool_use_turn2.sse")
	if second.StopReason != goodall.StopEndTurn || second.Message.Text() == "" {
		t.Errorf("turn two: stop = %s, text = %q", second.StopReason, second.Message.Text())
	}
}

func TestTheRecordedResponsesErrorSurfacesItsSentence(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write(fixture(t, "live_openai_error.json"))
	}, WithDialect(OpenAI))
	_, err := c.Complete(t.Context(), simpleRequest())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
	}
	if apiErr.Kind != goodall.KindNotFound || apiErr.Type != "model_not_found" {
		t.Errorf("APIError = %+v", apiErr)
	}
	if !strings.Contains(apiErr.Message, "does not exist") {
		t.Errorf("Message = %q, want the sentence the server sent", apiErr.Message)
	}
	if len(apiErr.Raw) == 0 {
		t.Error("Raw is empty, want the envelope kept whole")
	}
}
