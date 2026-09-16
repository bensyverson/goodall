package chat_test

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"reflect"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
)

// The strings a stream must never carry to a front end. Each is planted in the
// events below, and every assertion about the redacted stream is "this string
// is nowhere in what the writer wrote".
const (
	canaryInput  = "CANARY-SECRET-INPUT"
	canaryResult = "CANARY-SECRET-RESULT"
	canaryAnswer = "CANARY-SECRET-ANSWER"
)

// canaryEvents is mixedEvents with a secret planted wherever the stream is
// supposed to withhold one: the tool call's input, the fragments it streamed
// in, the tool's result, and the assistant message the terminal events carry.
func canaryEvents() []goodall.Event {
	input := `{"text":"` + canaryInput + `"}`
	use := goodall.ToolUse{ID: "toolu_1", Name: "echo", Input: jsontext.Value(input)}
	answer := goodall.AssistantMessage(goodall.Text{Text: canaryAnswer})
	response := goodall.Response{
		ID:         "msg_01",
		Model:      "claude-opus-5",
		Message:    answer,
		StopReason: goodall.StopToolUse,
		Usage:      goodall.Usage{Input: 12, Output: 7},
	}
	conv := goodall.Conversation{}.Append(goodall.UserMessage(goodall.Text{Text: "hi"}), answer)
	return []goodall.Event{
		goodall.TurnStart{Turn: 1},
		goodall.MessageStart{ID: "msg_01", Model: "claude-opus-5", Usage: goodall.Usage{Input: 12}},
		goodall.BlockStart{Index: 1, Block: goodall.ToolUse{ID: "toolu_1", Name: "echo"}},
		goodall.ToolInputDelta{Index: 1, PartialJSON: input},
		goodall.BlockStop{Index: 1},
		goodall.MessageStop{},
		goodall.ToolCallStart{ToolUse: use},
		goodall.ToolCallEnd{ToolUse: use, Result: goodall.TextResult(canaryResult)},
		goodall.TurnEnd{Turn: 1, Response: response},
		goodall.Done{Result: goodall.Result{
			Response:     &response,
			Conversation: conv,
			Usage:        goodall.Usage{Input: 12, Output: 7},
			StopReason:   goodall.StopEndTurn,
			Pending:      []goodall.ToolUse{use},
		}},
	}
}

// collect reads a stream to the end, failing on an error.
func collect(t *testing.T, events goodall.Stream) []goodall.Event {
	t.Helper()
	var got []goodall.Event
	for ev, err := range events {
		if err != nil {
			t.Fatalf("the stream yielded an error: %v", err)
		}
		got = append(got, ev)
	}
	return got
}

// eventOfType is the one event of the given type in the slice.
func eventOfType(t *testing.T, events []goodall.Event, kind goodall.EventType) goodall.Event {
	t.Helper()
	var found goodall.Event
	for _, ev := range events {
		if ev.Type() != kind {
			continue
		}
		if found != nil {
			t.Fatalf("the stream carried two %s events", kind)
		}
		found = ev
	}
	if found == nil {
		t.Fatalf("the stream carried no %s event", kind)
	}
	return found
}

// TestRedactedStreamCarriesNoToolInputOrResult is the criterion: a front end
// reading the wire meets a tool call's id and name and nothing it passed or
// got back. The same events written unredacted carry the secrets, which is
// what makes the first half of this test mean something.
func TestRedactedStreamCarriesNoToolInputOrResult(t *testing.T) {
	events := canaryEvents()

	var redacted bytes.Buffer
	if err := chat.WriteSSE(&redacted, chat.Redact(streamOf(events...), displayedOptions)); err != nil {
		t.Fatalf("WriteSSE over the redacted stream: %v", err)
	}
	for _, secret := range []string{canaryInput, canaryResult, canaryAnswer} {
		if bytes.Contains(redacted.Bytes(), []byte(secret)) {
			t.Errorf("the redacted stream carries %q:\n%s", secret, redacted.String())
		}
	}
	for _, kept := range []string{"toolu_1", "echo"} {
		if !bytes.Contains(redacted.Bytes(), []byte(kept)) {
			t.Errorf("the redacted stream dropped %q, which a front end needs:\n%s", kept, redacted.String())
		}
	}

	var raw bytes.Buffer
	if err := chat.WriteSSE(&raw, streamOf(events...)); err != nil {
		t.Fatalf("WriteSSE over the raw stream: %v", err)
	}
	for _, secret := range []string{canaryInput, canaryResult, canaryAnswer} {
		if !bytes.Contains(raw.Bytes(), []byte(secret)) {
			t.Fatalf("the unredacted stream does not carry %q, so the assertions above prove nothing", secret)
		}
	}
}

// TestRedactKeepsToolCallIdentityAndStatus is what a front end is left with:
// which tool ran, under which id, and whether it failed.
func TestRedactKeepsToolCallIdentityAndStatus(t *testing.T) {
	use := goodall.ToolUse{ID: "toolu_1", Name: "echo", Input: jsontext.Value(`{"text":"` + canaryInput + `"}`)}
	failed := goodall.ToolResult{ToolUseID: "toolu_1", IsError: true, Content: goodall.Blocks{goodall.Text{Text: canaryResult}}}
	got := collect(t, chat.Redact(streamOf(
		goodall.ToolCallStart{ToolUse: use},
		goodall.ToolCallEnd{ToolUse: use, Result: failed},
	), displayedOptions))

	if len(got) != 2 {
		t.Fatalf("Redact yielded %d events for two, %v", len(got), types(got))
	}
	start, ok := got[0].(goodall.ToolCallStart)
	if !ok {
		t.Fatalf("the first event is a %T, want a ToolCallStart", got[0])
	}
	want := goodall.ToolCallStart{ToolUse: goodall.ToolUse{ID: "toolu_1", Name: "echo"}}
	if !reflect.DeepEqual(start, want) {
		t.Errorf("the redacted call is %#v, want %#v", start, want)
	}
	end, ok := got[1].(goodall.ToolCallEnd)
	if !ok {
		t.Fatalf("the second event is a %T, want a ToolCallEnd", got[1])
	}
	wantEnd := goodall.ToolCallEnd{
		ToolUse: goodall.ToolUse{ID: "toolu_1", Name: "echo"},
		Result:  goodall.ToolResult{ToolUseID: "toolu_1", IsError: true},
	}
	if !reflect.DeepEqual(end, wantEnd) {
		t.Errorf("the redacted result is %#v, want %#v", end, wantEnd)
	}
}

// TestRedactDropsToolInputDeltas is the fragment path: the input never travels
// whole, so it must not travel in pieces either.
func TestRedactDropsToolInputDeltas(t *testing.T) {
	got := collect(t, chat.Redact(streamOf(
		goodall.BlockStart{Index: 0, Block: goodall.ToolUse{ID: "toolu_1", Name: "echo"}},
		goodall.ToolInputDelta{Index: 0, PartialJSON: `{"text":"`},
		goodall.ToolInputDelta{Index: 0, PartialJSON: canaryInput + `"}`},
		goodall.BlockStop{Index: 0},
	), displayedOptions))

	want := []goodall.Event{
		goodall.BlockStart{Index: 0, Block: goodall.ToolUse{ID: "toolu_1", Name: "echo"}},
		goodall.BlockStop{Index: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Redact yielded\n%#v\nwant\n%#v", got, want)
	}
}

// TestRedactKeepsTheLedgerAndDropsTheAnswer is what the turn and terminal
// events are for once the message is gone: the tokens, the money and the stop
// reason still travel, because a front end bills and renders on them.
func TestRedactKeepsTheLedgerAndDropsTheAnswer(t *testing.T) {
	usage := goodall.Usage{Input: 12, Output: 7}
	cost := costOf(t, "0.0123")
	response := goodall.Response{
		ID:               "msg_01",
		Model:            "claude-opus-5",
		Message:          goodall.AssistantMessage(goodall.Text{Text: canaryAnswer}),
		StopReason:       goodall.StopEndTurn,
		StopSequence:     "STOP",
		NativeStopReason: "stop",
		Usage:            usage,
		Cost:             cost,
	}
	got := collect(t, chat.Redact(streamOf(
		goodall.TurnEnd{Turn: 2, Response: response},
	), displayedOptions))

	end, ok := got[0].(goodall.TurnEnd)
	if !ok {
		t.Fatalf("the event is a %T, want a TurnEnd", got[0])
	}
	want := goodall.TurnEnd{Turn: 2, Response: goodall.Response{
		ID:               "msg_01",
		Model:            "claude-opus-5",
		StopReason:       goodall.StopEndTurn,
		StopSequence:     "STOP",
		NativeStopReason: "stop",
		Usage:            usage,
		Cost:             cost,
	}}
	if !reflect.DeepEqual(end, want) {
		t.Errorf("the redacted turn is\n%#v\nwant\n%#v", end, want)
	}
}

// TestRedactDropsTheHistoryFromTheTerminalEvents is the gap the web example
// found: Done and Stopped carry the whole conversation, which the front end is
// never trusted with, and Stopped's own prose about why the run ended is not
// content, so it stays.
func TestRedactDropsTheHistoryFromTheTerminalEvents(t *testing.T) {
	response := goodall.Response{
		ID:      "msg_01",
		Message: goodall.AssistantMessage(goodall.Text{Text: canaryAnswer}),
		Usage:   goodall.Usage{Output: 7},
	}
	result := goodall.Result{
		Response: &response,
		Conversation: goodall.Conversation{}.Append(
			goodall.UserMessage(goodall.Text{Text: "hi"}),
			goodall.AssistantMessage(goodall.Text{Text: canaryAnswer}),
		),
		Usage:      goodall.Usage{Input: 12, Output: 7},
		StopReason: goodall.StopEndTurn,
		Pending: []goodall.ToolUse{
			{ID: "toolu_1", Name: "echo", Input: jsontext.Value(`{"text":"` + canaryInput + `"}`)},
		},
	}
	wantResult := goodall.Result{
		Response:   &goodall.Response{ID: "msg_01", Usage: goodall.Usage{Output: 7}},
		Usage:      goodall.Usage{Input: 12, Output: 7},
		StopReason: goodall.StopEndTurn,
		Pending:    []goodall.ToolUse{{ID: "toolu_1", Name: "echo"}},
	}

	got := collect(t, chat.Redact(streamOf(
		goodall.Done{Result: result},
		goodall.Stopped{
			Cause:   goodall.StopCauseError,
			Message: "the provider is overloaded",
			Kind:    goodall.KindOverloaded,
			Result:  result,
		},
	), displayedOptions))

	done, ok := got[0].(goodall.Done)
	if !ok {
		t.Fatalf("the first event is a %T, want a Done", got[0])
	}
	if !reflect.DeepEqual(done, goodall.Done{Result: wantResult}) {
		t.Errorf("the redacted Done is\n%#v\nwant\n%#v", done.Result, wantResult)
	}
	stopped, ok := got[1].(goodall.Stopped)
	if !ok {
		t.Fatalf("the second event is a %T, want a Stopped", got[1])
	}
	wantStopped := goodall.Stopped{
		Cause:   goodall.StopCauseError,
		Message: "the provider is overloaded",
		Kind:    goodall.KindOverloaded,
		Result:  wantResult,
	}
	if !reflect.DeepEqual(stopped, wantStopped) {
		t.Errorf("the redacted Stopped is\n%#v\nwant\n%#v", stopped, wantStopped)
	}
}

// TestRedactFollowsTheAgentsThinkingDisplay is the view's rule on a stream: an
// omitted thinking block is still a block, just with nothing to read.
func TestRedactFollowsTheAgentsThinkingDisplay(t *testing.T) {
	events := []goodall.Event{
		goodall.BlockStart{Index: 0, Block: goodall.Thinking{}},
		goodall.ThinkingDelta{Index: 0, Text: "weighing it"},
		goodall.BlockStop{Index: 0},
	}

	shown := collect(t, chat.Redact(streamOf(events...), displayedOptions))
	if !reflect.DeepEqual(shown, events) {
		t.Errorf("an agent that displays its thinking got\n%#v\nwant\n%#v", shown, events)
	}

	hidden := collect(t, chat.Redact(streamOf(events...), chat.ViewOptions{ThinkingDisplay: goodall.DisplayOmitted}))
	want := []goodall.Event{
		goodall.BlockStart{Index: 0, Block: goodall.Thinking{}},
		goodall.ThinkingDelta{Index: 0},
		goodall.BlockStop{Index: 0},
	}
	if !reflect.DeepEqual(hidden, want) {
		t.Errorf("an agent that omits its thinking got\n%#v\nwant\n%#v", hidden, want)
	}
}

// TestRedactPassesEverythingElseThrough keeps the redaction narrow: an event
// that carries nothing the back end owns arrives as itself.
func TestRedactPassesEverythingElseThrough(t *testing.T) {
	events := []goodall.Event{
		goodall.TurnStart{Turn: 1},
		goodall.MessageStart{ID: "msg_01", Model: "claude-opus-5", Usage: goodall.Usage{Input: 12}},
		goodall.BlockStart{Index: 0, Block: goodall.Text{}},
		goodall.TextDelta{Index: 0, Text: "Hello, world"},
		goodall.SignatureDelta{Index: 0, Signature: "sig"},
		goodall.BlockStop{Index: 0, Raw: jsontext.Value(`{"type":"thinking"}`)},
		goodall.MessageDelta{StopReason: goodall.StopEndTurn, Usage: goodall.Usage{Output: 7}},
		goodall.MessageStop{},
		goodall.UnknownEvent{EventType: "ping", Raw: jsontext.Value(`{"type":"ping"}`)},
	}
	got := collect(t, chat.Redact(streamOf(events...), displayedOptions))
	if !reflect.DeepEqual(got, events) {
		t.Errorf("Redact changed an event it does not own:\ngot  %#v\nwant %#v", got, events)
	}
}

// TestRedactPassesAnErrorThrough is the failure path: an error frame carries
// nothing sensitive, so it reaches the writer as it was.
func TestRedactPassesAnErrorThrough(t *testing.T) {
	var got []goodall.Event
	var gotErr error
	for ev, err := range chat.Redact(failingStream(chat.ErrSubscriberOverflow, goodall.TextDelta{Text: "half"}), displayedOptions) {
		if err != nil {
			gotErr = err
			continue
		}
		got = append(got, ev)
	}
	if !errors.Is(gotErr, chat.ErrSubscriberOverflow) {
		t.Errorf("Redact ended with %v, want the stream's own error", gotErr)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], goodall.TextDelta{Text: "half"}) {
		t.Errorf("Redact yielded %#v before the error, want the delta", got)
	}
}

// TestRedactStopsWhenTheConsumerDoes keeps the mapping a well-behaved
// iterator: a consumer that breaks unwinds the stream underneath it.
func TestRedactStopsWhenTheConsumerDoes(t *testing.T) {
	var unwound bool
	source := goodall.Stream(func(yield func(goodall.Event, error) bool) {
		defer func() { unwound = true }()
		for range 5 {
			if !yield(goodall.TextDelta{Text: "a"}, nil) {
				return
			}
		}
	})
	var seen int
	for range chat.Redact(source, displayedOptions) {
		seen++
		break
	}
	if seen != 1 {
		t.Errorf("the consumer read %d events before breaking, want 1", seen)
	}
	if !unwound {
		t.Error("Redact left the source stream running after the consumer stopped")
	}
}

// TestRedactLeavesTheSourceEventsAlone is what makes it safe to redact one
// subscriber's copy: the events are shared with every other reader of the run,
// so the mapping copies rather than clearing fields in place.
func TestRedactLeavesTheSourceEventsAlone(t *testing.T) {
	events := canaryEvents()
	want := canaryEvents()
	collect(t, chat.Redact(streamOf(events...), displayedOptions))
	if !reflect.DeepEqual(events, want) {
		t.Errorf("Redact changed the events it was given:\ngot  %#v\nwant %#v", events, want)
	}
}
