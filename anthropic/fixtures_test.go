package anthropic

import (
	json "encoding/json/v2"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// These tests read the recordings scripts/record-fixtures made from the live
// API (see testdata/README.md for the date and the model). They are what
// makes the offline suite a statement about Anthropic rather than about what
// the fixture author believed Anthropic does: every byte here came off the
// wire.
//
// They run offline. Nothing in this file makes a network call; the recordings
// are replayed through an httptest.Server, which is the same path a live
// response takes.

// The answers the recorded questions got, which are what the assertions below
// are worth asserting: a model that stopped reading its input would not
// produce them.
const (
	recordedImageColor = "orange"
	recordedPDFPhrase  = "Marmalade on the tortoise."
	recordedToolCity   = "Paris"
	// recordedThinkingTokens is the thinking_tokens the recorded thinking
	// turn reported. Anthropic breaks the count out under
	// output_tokens_details, and goodall.Usage has a field for it.
	recordedThinkingTokens = 50
)

// recordedStreams is every streamed recording, by fixture name.
func recordedStreams(t *testing.T) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join("testdata", "live_*.sse"))
	if err != nil {
		t.Fatalf("globbing the recordings: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no live_*.sse recordings; run scripts/record-fixtures")
	}
	for i, name := range names {
		names[i] = filepath.Base(name)
	}
	return names
}

// collectFixture replays one recorded stream through the client and returns
// what a consumer would have seen.
func collectFixture(t *testing.T, name string) (*goodall.Response, error) {
	t.Helper()
	client, _ := serveFixture(t, name, "text/event-stream")
	return client.Stream(t.Context(), simpleRequest()).Collect()
}

func TestEveryRecordedStreamCollectsWithoutAProtocolError(t *testing.T) {
	for _, name := range recordedStreams(t) {
		t.Run(name, func(t *testing.T) {
			resp, err := collectFixture(t, name)
			if err != nil {
				t.Fatalf("collecting %s: %v", name, err)
			}
			if resp.ID == "" {
				t.Error("the collected response has no message id, so message_start was not applied")
			}
			if resp.StopReason == goodall.StopNone {
				t.Error("the collected response has no stop reason, so message_delta was not applied")
			}
			if resp.Message.Partial {
				t.Error("the collected message is marked partial, so the recording ended early")
			}
		})
	}
}

func TestTheRecordedThinkingTurnCarriesASignedBlockAndAParseableToolCall(t *testing.T) {
	resp, err := collectFixture(t, "live_thinking_tool_use.sse")
	if err != nil {
		t.Fatalf("collecting the thinking recording: %v", err)
	}
	if resp.StopReason != goodall.StopToolUse {
		t.Errorf("stop reason = %s, want %s", resp.StopReason, goodall.StopToolUse)
	}

	var thinking goodall.Thinking
	var found bool
	for _, block := range resp.Message.Content {
		if tb, ok := block.(goodall.Thinking); ok {
			thinking, found = tb, true
		}
	}
	if !found {
		t.Fatal("the recorded thinking turn produced no thinking block")
	}
	if thinking.Text == "" {
		t.Error("the thinking block has no text, though the request asked for summarized display")
	}
	if thinking.Signature == "" {
		t.Error("the thinking block has no signature; without one it cannot be replayed on the next turn")
	}

	uses := resp.Message.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("the recorded turn made %d tool calls, want 1", len(uses))
	}
	if uses[0].Name != "get_weather" {
		t.Errorf("tool call name = %q, want get_weather", uses[0].Name)
	}
	var input struct {
		City string `json:"city"`
	}
	if err := json.Unmarshal(uses[0].Input, &input); err != nil {
		t.Fatalf("the tool input did not parse as JSON: %v", err)
	}
	if input.City != recordedToolCity {
		t.Errorf("tool input city = %q, want %q", input.City, recordedToolCity)
	}
}

func TestTheRecordedThinkingTurnReportsItsThinkingTokens(t *testing.T) {
	resp, err := collectFixture(t, "live_thinking_tool_use.sse")
	if err != nil {
		t.Fatalf("collecting the thinking recording: %v", err)
	}
	if resp.Usage.Reasoning != recordedThinkingTokens {
		t.Errorf("Usage.Reasoning = %d, want %d: Anthropic breaks the thinking tokens out under output_tokens_details",
			resp.Usage.Reasoning, recordedThinkingTokens)
	}
	if resp.Usage.Reasoning > resp.Usage.Output {
		t.Errorf("Usage.Reasoning (%d) exceeds Usage.Output (%d); the thinking tokens are part of the output, not extra",
			resp.Usage.Reasoning, resp.Usage.Output)
	}
}

func TestTheRecordedSecondTurnShowsAReplayedThinkingBlockWasAccepted(t *testing.T) {
	// The request fixture is half the proof: it is the body goodall sent,
	// and it carries the previous turn's thinking block with its
	// signature.
	request := string(fixture(t, "live_thinking_tool_use_turn2.request.json"))
	if !strings.Contains(request, `"type": "thinking"`) || !strings.Contains(request, `"signature"`) {
		t.Fatal("the recorded second turn did not replay a signed thinking block, so it proves nothing about replay")
	}

	// The response is the other half: a signature Anthropic rejects comes
	// back as an error, not as an answer.
	resp, err := collectFixture(t, "live_thinking_tool_use_turn2.sse")
	if err != nil {
		t.Fatalf("collecting the second turn: %v", err)
	}
	if resp.StopReason != goodall.StopEndTurn {
		t.Errorf("stop reason = %s, want %s: the second turn should answer once it has the tool result",
			resp.StopReason, goodall.StopEndTurn)
	}
	if resp.Message.Text() == "" {
		t.Error("the second turn produced no text")
	}
}

func TestTheRecordedMediaAnswersNameWhatWasAsked(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    string
	}{
		{"the generated PNG's color", "live_image.sse", recordedImageColor},
		{"the generated PDF's one line", "live_pdf.sse", recordedPDFPhrase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := collectFixture(t, tt.fixture)
			if err != nil {
				t.Fatalf("collecting %s: %v", tt.fixture, err)
			}
			got := resp.Message.Text()
			if !strings.Contains(strings.ToLower(got), strings.ToLower(tt.want)) {
				t.Errorf("the answer was %q, which does not contain %q", got, tt.want)
			}
		})
	}
}

func TestTheRecordedErrorDecodesToAnAPIError(t *testing.T) {
	body := fixture(t, "live_error.json")
	client, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write(body)
	})

	_, err := client.Complete(t.Context(), simpleRequest())
	if err == nil {
		t.Fatal("a recorded 404 produced no error")
	}
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("the error was %T, want *goodall.APIError", err)
	}
	if apiErr.Kind != goodall.KindNotFound {
		t.Errorf("kind = %s, want %s", apiErr.Kind, goodall.KindNotFound)
	}
	if apiErr.Status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", apiErr.Status, http.StatusNotFound)
	}
	if apiErr.Type != string(errorNotFound) {
		t.Errorf("type = %q, want %q", apiErr.Type, errorNotFound)
	}
	if apiErr.RequestID == "" {
		t.Error("the recorded envelope carries a request_id; the error dropped it")
	}
}

func TestTheRecordedBlockingAnswersAgreeWithTheirStreamedSiblings(t *testing.T) {
	pairs := []struct {
		streamed string
		blocking string
	}{
		{"live_text.sse", "live_text_blocking.json"},
		{"live_tool_use.sse", "live_tool_use_blocking.json"},
	}
	for _, pair := range pairs {
		t.Run(pair.blocking, func(t *testing.T) {
			streamed, err := collectFixture(t, pair.streamed)
			if err != nil {
				t.Fatalf("collecting %s: %v", pair.streamed, err)
			}

			client, _ := serveFixture(t, pair.blocking, "application/json")
			blocking, err := client.Complete(t.Context(), simpleRequest())
			if err != nil {
				t.Fatalf("completing %s: %v", pair.blocking, err)
			}

			if blocking.StopReason != streamed.StopReason {
				t.Errorf("stop reason = %s blocking, %s streamed; the two paths must agree",
					blocking.StopReason, streamed.StopReason)
			}
			if blocking.Model != streamed.Model {
				t.Errorf("model = %q blocking, %q streamed", blocking.Model, streamed.Model)
			}
			if len(blocking.Message.Content) != len(streamed.Message.Content) {
				t.Errorf("the blocking answer has %d blocks and the streamed one %d",
					len(blocking.Message.Content), len(streamed.Message.Content))
			}
		})
	}
}
