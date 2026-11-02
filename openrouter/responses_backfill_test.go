// Streams written by hand, none of them observed: every recorded item had its text
// in deltas. They hold Collect to what Complete reads when an item's content arrives
// only on a later event.

package openrouter

import (
	"encoding/json/jsontext"
	"reflect"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// authoredAdded is the added event for an empty item of a type, at output index 0.
func authoredAdded(item string) string {
	return `{"type":"response.output_item.added","output_index":0,"item":` + item + `}`
}

// authoredDone is the done event for an item, at output index 0.
func authoredDone(item string) string {
	return `{"type":"response.output_item.done","output_index":0,"item":` + item + `}`
}

// wantParity collects a stream, reads its terminal body with Complete, and fails if
// the two paths disagree on the message or the stop reason.
func wantParity(t *testing.T, body []byte) *goodall.Response {
	t.Helper()
	collected, err := responsesStream(t, body).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	completed, err := responsesCompleted(t, terminalBody(t, body))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if collected.StopReason != completed.StopReason {
		t.Errorf("stop = %q streamed, %q blocking", collected.StopReason, completed.StopReason)
	}
	if !reflect.DeepEqual(collected.Message.Content, completed.Message.Content) {
		t.Errorf("blocks:\nstreamed %#v\nblocking %#v", collected.Message.Content, completed.Message.Content)
	}
	return collected
}

const authoredEmptyMessage = `{"type":"message","role":"assistant","content":[]}`

func TestAMessageWhoseTextCameOnlyOnDoneKeepsItInContentOrder(t *testing.T) {
	item := `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"one"},{"type":"output_text","text":""},{"type":"output_text","text":"two"}]}`
	resp := wantParity(t, frames(authoredCreated, authoredAdded(authoredEmptyMessage), authoredDone(item), authoredCompleted(item)))
	if got := blockKinds(resp.Message); got != "goodall.Text,goodall.Text" {
		t.Errorf("blocks = %s", got)
	}
	if resp.Message.Text() != "onetwo" {
		t.Errorf("text = %q", resp.Message.Text())
	}
}

func TestAPartThatHadDeltasIsNotRepeatedFromDone(t *testing.T) {
	item := `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"one"},{"type":"output_text","text":"two"}]}`
	resp := wantParity(t, frames(
		authoredCreated,
		authoredAdded(authoredEmptyMessage),
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"one"}`,
		authoredDone(item),
		authoredCompleted(item),
	))
	if resp.Message.Text() != "onetwo" {
		t.Errorf("text = %q", resp.Message.Text())
	}
}

func TestARefusalSeenOnlyOnDoneIsARefusal(t *testing.T) {
	item := `{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot."}]}`
	resp := wantParity(t, frames(authoredCreated, authoredAdded(authoredEmptyMessage), authoredDone(item), authoredCompleted(item)))
	if resp.StopReason != goodall.StopRefusal || resp.Message.Text() != "I cannot." {
		t.Errorf("stop %q, text %q", resp.StopReason, resp.Message.Text())
	}
}

func TestAnEmptyRefusalPartIsARefusalWithNoBlock(t *testing.T) {
	item := `{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":""}]}`
	resp := wantParity(t, frames(authoredCreated, authoredAdded(authoredEmptyMessage), authoredDone(item), authoredCompleted(item)))
	if resp.StopReason != goodall.StopRefusal || len(resp.Message.Content) != 0 {
		t.Errorf("stop %q, blocks %#v", resp.StopReason, resp.Message.Content)
	}
}

func TestASummarySeenOnlyOnDoneIsTheThinkingText(t *testing.T) {
	item := `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"First."},{"type":"summary_text","text":"Second."}],"encrypted_content":"x"}`
	resp := wantParity(t, frames(
		authoredCreated,
		authoredAdded(`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"short"}`),
		authoredDone(item),
		authoredCompleted(item),
	))
	if thinking := thinkingOf(resp.Message); len(thinking) != 1 || thinking[0].Text != "First.\n\nSecond." {
		t.Errorf("thinking = %+v", thinking)
	}
}

// The terminal event is the last word on an item that never had a done event, so
// the same backfill applies, and for a call it is what stops a tool running on {}.
func TestAnItemWithNoDoneIsFilledFromTheTerminalEvent(t *testing.T) {
	for name, tc := range map[string]struct{ added, item string }{
		"text":    {authoredEmptyMessage, `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"one"}]}`},
		"refusal": {authoredEmptyMessage, `{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot."}]}`},
		"call": {
			`{"type":"function_call","call_id":"call_a","name":"one","arguments":""}`,
			`{"type":"function_call","call_id":"call_a","name":"one","arguments":"{\"a\":1}"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			wantParity(t, frames(authoredCreated, authoredAdded(tc.added), authoredCompleted(tc.item)))
		})
	}
}

// An output item this wire does not know becomes an Unknown block, and the server
// accepts a lost item with a 200, so dropping one would be silent. A foreign Unknown
// sent this way draws a 400, which is the failure to prefer.
func TestAnUnknownBlockGoesBackVerbatimAndInOrder(t *testing.T) {
	raw := `{"id":"x_1","type":"shiny_new_call","note":"café"}`
	got := itemsSent(t, sentBody(t, &goodall.Request{
		Model: "gpt-6-astra",
		Messages: []goodall.Message{
			goodall.AssistantMessage(
				goodall.Text{Text: "before"},
				goodall.Unknown{Type: "shiny_new_call", Raw: jsontext.Value(raw)},
				goodall.Text{Text: "after"},
			),
		},
	}))
	if len(got) != 3 || !strings.Contains(got[0], "before") || got[1] != raw || !strings.Contains(got[2], "after") {
		t.Errorf("items = %v, want the unknown item's own bytes between the two messages", got)
	}
}

func TestAnUnknownBlockFromTheWireRoundTrips(t *testing.T) {
	item := `{"type":"shiny_new_call","id":"x_1"}`
	resp, err := responsesStream(t, frames(
		authoredCreated,
		authoredAdded(`{"type":"shiny_new_call","id":"x_1","status":"in_progress"}`),
		authoredDone(item),
		authoredCompleted(item),
	)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	got := itemsSent(t, sentBody(t, &goodall.Request{Model: "gpt-6-astra", Messages: []goodall.Message{resp.Message}}))
	if len(got) != 1 || got[0] != item {
		t.Errorf("items = %v, want %s", got, item)
	}
}

func TestAnUnknownBlockWithNoUsableBytesIsLeftOut(t *testing.T) {
	got := itemsSent(t, sentBody(t, &goodall.Request{
		Model: "gpt-6-astra",
		Messages: []goodall.Message{
			goodall.UserMessage(goodall.Text{Text: "hi"}),
			goodall.AssistantMessage(
				goodall.Unknown{Type: "empty"},
				goodall.Unknown{Type: "broken", Raw: jsontext.Value(`not json`)},
			),
		},
	}))
	if len(got) != 1 {
		t.Errorf("items = %v, want only the user message", got)
	}
}
