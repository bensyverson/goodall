package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// responsesStream replays stream bytes through a client under the OpenAI dialect.
func responsesStream(t *testing.T, body []byte) goodall.Stream {
	t.Helper()
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		serveStream(w, body)
	}, WithDialect(OpenAI))
	return c.Stream(t.Context(), simpleRequest())
}

// streamEvents reads the data payloads of a recorded stream.
func streamEvents(t *testing.T, raw []byte) []map[string]jsontext.Value {
	t.Helper()
	var out []map[string]jsontext.Value
	for line := range strings.Lines(string(raw)) {
		data, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data: ")
		if !ok {
			continue
		}
		var event map[string]jsontext.Value
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("a recorded frame is not JSON: %v", err)
		}
		out = append(out, event)
	}
	return out
}

// eventType is the type member of one decoded frame.
func eventType(t *testing.T, event map[string]jsontext.Value) string {
	t.Helper()
	var name string
	if err := json.Unmarshal(event["type"], &name); err != nil {
		t.Fatal(err)
	}
	return name
}

// terminalBody is the whole response a recorded stream's last event carries, which
// is the body a blocking call over the same exchange would have read.
func terminalBody(t *testing.T, raw []byte) []byte {
	t.Helper()
	for _, event := range streamEvents(t, raw) {
		switch eventType(t, event) {
		case "response.completed", "response.incomplete":
			return event["response"]
		}
	}
	t.Fatal("the recording has no terminal event")
	return nil
}

// recordedStreams names every stream recorded against api.openai.com. The or_ ones
// are OpenRouter's /responses, which goodall does not speak.
func recordedStreams(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "scripts", "probe-responses", "testdata", "*.response.sse"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, path := range paths {
		if name := filepath.Base(path); !strings.HasPrefix(name, "or_") {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		t.Fatal("no recorded streams")
	}
	return names
}

// withoutCiphertext is a reasoning item's bytes with its encrypted_content blanked,
// and the length of what was there.
func withoutCiphertext(t *testing.T, raw jsontext.Value) (string, int) {
	t.Helper()
	var item struct {
		Encrypted string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatal(err)
	}
	if item.Encrypted == "" {
		t.Fatalf("the item has no ciphertext: %s", raw)
	}
	return strings.Replace(string(raw), item.Encrypted, "", 1), len(item.Encrypted)
}

// The reasoning item on output_item.done and on the terminal response are the same
// but for encrypted_content, re-encrypted to the same length, so no recording can
// show Raw equal across the two paths in those bytes. All else is compared exactly.
func TestEveryRecordedStreamCollectsToWhatCompleteReadsOfItsTerminalBody(t *testing.T) {
	for _, name := range recordedStreams(t) {
		t.Run(name, func(t *testing.T) {
			raw := probeRecording(t, name)
			collected, err := responsesStream(t, raw).Collect()
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			completed, err := responsesCompleted(t, terminalBody(t, raw))
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if collected.ID != completed.ID || collected.Model != completed.Model {
				t.Errorf("id %q/%q, model %q/%q", collected.ID, completed.ID, collected.Model, completed.Model)
			}
			if collected.StopReason != completed.StopReason || collected.NativeStopReason != completed.NativeStopReason {
				t.Errorf("stop %q/%q, native %q/%q", collected.StopReason, completed.StopReason, collected.NativeStopReason, completed.NativeStopReason)
			}
			if collected.Usage != completed.Usage || collected.Cost != completed.Cost {
				t.Errorf("usage %+v/%+v, cost %+v/%+v", collected.Usage, completed.Usage, collected.Cost, completed.Cost)
			}
			if collected.Usage.Output == 0 {
				t.Error("the stream reported no output tokens")
			}
			if collected.Message.Role != completed.Message.Role {
				t.Errorf("role %q/%q", collected.Message.Role, completed.Message.Role)
			}
			if len(collected.Message.Content) != len(completed.Message.Content) || len(collected.Message.Content) == 0 {
				t.Fatalf("blocks %s / %s", blockKinds(collected.Message), blockKinds(completed.Message))
			}
			for i := range collected.Message.Content {
				got, want := collected.Message.Content[i], completed.Message.Content[i]
				gotThinking, isThinking := got.(goodall.Thinking)
				if !isThinking {
					if !reflect.DeepEqual(got, want) {
						t.Errorf("block %d: %#v, want %#v", i, got, want)
					}
					continue
				}
				wantThinking := want.(goodall.Thinking)
				if gotThinking.Text != wantThinking.Text {
					t.Errorf("thinking %d text:\n got %q\nwant %q", i, gotThinking.Text, wantThinking.Text)
				}
				gotBytes, gotLen := withoutCiphertext(t, gotThinking.Raw)
				wantBytes, wantLen := withoutCiphertext(t, wantThinking.Raw)
				if gotBytes != wantBytes || gotLen != wantLen {
					t.Errorf("thinking %d Raw differs beyond its ciphertext:\n got %s\nwant %s", i, gotBytes, wantBytes)
				}
			}
		})
	}
}

func TestNoRecordedStreamProducesAnUnknownEvent(t *testing.T) {
	for _, name := range recordedStreams(t) {
		t.Run(name, func(t *testing.T) {
			for ev, err := range responsesStream(t, probeRecording(t, name)) {
				if err != nil {
					t.Fatal(err)
				}
				if unknown, ok := ev.(goodall.UnknownEvent); ok {
					t.Errorf("unknown event %q: %s", unknown.EventType, unknown.Raw)
				}
			}
		})
	}
}

// itemOn is the item on the first event of a type, at an output index.
func itemOn(t *testing.T, raw []byte, event string, index string) jsontext.Value {
	t.Helper()
	for _, frame := range streamEvents(t, raw) {
		if eventType(t, frame) == event && string(frame["output_index"]) == index {
			return frame["item"]
		}
	}
	t.Fatalf("no %s at output index %s", event, index)
	return nil
}

// A reasoning item taken from added replays as lost reasoning, with no error: the
// replay_added recording read 135 input tokens and spent 21 reasoning tokens, and
// replay_done read 188 and spent none.
func TestRawHoldsTheBytesOfOutputItemDoneAndNotAdded(t *testing.T) {
	raw := probeRecording(t, "two_turn_stream_gpt-6-astra.turn1.response.sse")
	resp, err := responsesStream(t, raw).Collect()
	if err != nil {
		t.Fatal(err)
	}
	thinking := thinkingOf(resp.Message)
	if len(thinking) != 1 {
		t.Fatalf("thinking = %+v", thinking)
	}
	done := compacted(t, itemOn(t, raw, "response.output_item.done", "0"))
	added := compacted(t, itemOn(t, raw, "response.output_item.added", "0"))
	if done == added {
		t.Fatal("the recording's two items are the same, so this test proves nothing")
	}
	if string(thinking[0].Raw) != done {
		t.Errorf("Raw is not the done item.\n got %s\nwant %s", thinking[0].Raw, done)
	}
	if _, length := withoutCiphertext(t, thinking[0].Raw); length != 2316 {
		t.Errorf("ciphertext = %d characters, want the done item's 2316 and not the added item's 1228", length)
	}
	if !strings.Contains(thinking[0].Text, "prime") {
		t.Errorf("text = %q, want the summary", thinking[0].Text)
	}
}

func TestACollectedTurnReplaysItsDoneItemByteForByte(t *testing.T) {
	raw := probeRecording(t, "replay_done.turn1.response.sse")
	resp, err := responsesStream(t, raw).Collect()
	if err != nil {
		t.Fatal(err)
	}
	sent := itemsSent(t, sentBody(t, nextTurn(&goodall.Request{
		Model:    "gpt-6-astra",
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "the prompt"})},
	}, resp)))
	if len(sent) != 4 {
		t.Fatalf("items = %d: %v", len(sent), sent)
	}
	if want := compacted(t, itemOn(t, raw, "response.output_item.done", "0")); sent[1] != want {
		t.Errorf("the reasoning item changed:\n got %s\nwant %s", sent[1], want)
	}
	// The live turn two was sent these bytes, and read them as a full replay.
	var recorded struct {
		Input []jsontext.Value `json:"input"`
	}
	if err := json.Unmarshal(probeRecording(t, "replay_done.turn2.request.json"), &recorded); err != nil {
		t.Fatal(err)
	}
	if want := compacted(t, recorded.Input[1]); sent[1] != want {
		t.Errorf("goodall's item is not the one the API accepted:\n got %s\nwant %s", sent[1], want)
	}
}

func TestARecordedParallelTurnKeepsItsCallsApart(t *testing.T) {
	resp, err := responsesStream(t, probeRecording(t, "parallel.turn1.response.sse")).Collect()
	if err != nil {
		t.Fatal(err)
	}
	uses := resp.Message.ToolUses()
	if len(uses) != 2 || uses[0].ID == uses[1].ID {
		t.Fatalf("calls = %+v", uses)
	}
	if !strings.Contains(string(uses[0].Input), "Paris") || !strings.Contains(string(uses[1].Input), "Lima") {
		t.Errorf("inputs = %s, %s", uses[0].Input, uses[1].Input)
	}
	if resp.StopReason != goodall.StopToolUse {
		t.Errorf("stop = %q", resp.StopReason)
	}
}

func TestARecordedIncompleteStreamMayHoldOnlyReasoning(t *testing.T) {
	raw := probeRecording(t, "incomplete.response.sse")
	resp, err := responsesStream(t, raw).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != goodall.StopMaxTokens || resp.NativeStopReason != "max_output_tokens" {
		t.Errorf("stop = %q, native %q", resp.StopReason, resp.NativeStopReason)
	}
	thinking := thinkingOf(resp.Message)
	if len(thinking) != 1 || len(resp.Message.Content) != 1 {
		t.Fatalf("blocks = %s", blockKinds(resp.Message))
	}
	if want := compacted(t, itemOn(t, raw, "response.output_item.done", "0")); string(thinking[0].Raw) != want {
		t.Errorf("Raw = %s\nwant %s", thinking[0].Raw, want)
	}
	if want := (goodall.Usage{Input: 15, Output: 16, Reasoning: 16}); resp.Usage != want {
		t.Errorf("usage = %+v, want %+v", resp.Usage, want)
	}
}
