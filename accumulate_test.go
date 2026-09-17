package goodall

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// scriptedEvents is one complete assistant turn as a provider streams it: a
// thinking block with a signature, a text block, and a tool call whose input
// arrives in fragments. It is the fixture every collection test works from.
func scriptedEvents() []Event {
	return []Event{
		MessageStart{ID: "msg_01", Model: "claude-opus-5", Usage: Usage{Input: 25, CacheRead: 1024}},
		BlockStart{Index: 0, Block: Thinking{}},
		ThinkingDelta{Index: 0, Text: "The user wants "},
		ThinkingDelta{Index: 0, Text: "the weather."},
		SignatureDelta{Index: 0, Signature: "ErUBCkYIBRgCKkB"},
		BlockStop{Index: 0},
		BlockStart{Index: 1, Block: Text{}},
		TextDelta{Index: 1, Text: "Let me check "},
		TextDelta{Index: 1, Text: "the forecast."},
		BlockStop{Index: 1},
		BlockStart{Index: 2, Block: ToolUse{ID: "toolu_1", Name: "get_weather"}},
		ToolInputDelta{Index: 2, PartialJSON: `{"city"`},
		ToolInputDelta{Index: 2, PartialJSON: `:"Paris"}`},
		BlockStop{Index: 2},
		MessageDelta{
			StopReason: StopToolUse,
			Usage:      Usage{Input: 25, Output: 130, CacheRead: 1024, Reasoning: 64},
			Cost:       costOf("0.0123"),
		},
		MessageStop{},
	}
}

// scriptedResponse is the response those events must collect to, built by
// hand so the accumulator is compared against a value nobody generated.
func scriptedResponse() *Response {
	return &Response{
		ID:    "msg_01",
		Model: "claude-opus-5",
		Message: AssistantMessage(
			Thinking{Text: "The user wants the weather.", Signature: "ErUBCkYIBRgCKkB"},
			Text{Text: "Let me check the forecast."},
			ToolUse{ID: "toolu_1", Name: "get_weather", Input: jsontext.Value(`{"city":"Paris"}`)},
		),
		StopReason: StopToolUse,
		Usage:      Usage{Input: 25, Output: 130, CacheRead: 1024, Reasoning: 64},
		Cost:       costOf("0.0123"),
	}
}

// scriptedResponseJSON is the same response on the wire, so the assertion
// covers the bytes a front end receives and not only the Go value.
const scriptedResponseJSON = `{"id":"msg_01","model":"claude-opus-5","message":{"role":"assistant","content":[` +
	`{"type":"thinking","text":"The user wants the weather.","signature":"ErUBCkYIBRgCKkB"},` +
	`{"type":"text","text":"Let me check the forecast."},` +
	`{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Paris"}}` +
	`]},"stop_reason":"tool_use","usage":{"input_tokens":25,"output_tokens":130,"cache_read_tokens":1024,"reasoning_tokens":64},` +
	`"cost":{"amount":0.0123,"currency":"USD","reported":true}}`

// applyAll folds every event into a fresh accumulator, failing on the first
// error.
func applyAll(t *testing.T, events ...Event) *Accumulator {
	t.Helper()
	var a Accumulator
	for i, e := range events {
		if err := a.Apply(e); err != nil {
			t.Fatalf("Apply(%d, %s): %v", i, e.Type(), err)
		}
	}
	return &a
}

// TestAccumulatorBuildsTheScriptedMessage is the core contract: the events a
// provider streams fold into exactly the message that provider would have
// returned in one piece.
func TestAccumulatorBuildsTheScriptedMessage(t *testing.T) {
	a := applyAll(t, scriptedEvents()...)
	if !a.Done() {
		t.Error("Done() = false after message_stop")
	}
	got, want := a.Response(), scriptedResponse()
	if got.ID != want.ID || got.Model != want.Model {
		t.Errorf("ID/Model = %q/%q, want %q/%q", got.ID, got.Model, want.ID, want.Model)
	}
	if got.StopReason != want.StopReason || got.StopSequence != want.StopSequence {
		t.Errorf("stop = %q/%q, want %q/%q", got.StopReason, got.StopSequence, want.StopReason, want.StopSequence)
	}
	if got.Usage != want.Usage {
		t.Errorf("Usage = %+v, want %+v", got.Usage, want.Usage)
	}
	if got.Cost != want.Cost {
		t.Errorf("Cost = %+v, want %+v", got.Cost, want.Cost)
	}
	if got.Message.Partial {
		t.Error("Message.Partial = true after message_stop")
	}
	if !reflect.DeepEqual(got.Message, want.Message) {
		t.Errorf("Message =\n\t%#v\nwant\n\t%#v", got.Message, want.Message)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != scriptedResponseJSON {
		t.Errorf("Marshal =\n\t%s\nwant\n\t%s", encoded, scriptedResponseJSON)
	}
}

// TestAccumulatorMessageIsPartialUntilStop checks the flag a UI renders a
// cut-off answer from, and that Response is a usable snapshot throughout
// rather than nil until the end.
func TestAccumulatorMessageIsPartialUntilStop(t *testing.T) {
	events := scriptedEvents()
	a := applyAll(t, events[:len(events)-1]...)
	if a.Done() {
		t.Error("Done() = true before message_stop")
	}
	resp := a.Response()
	if resp == nil {
		t.Fatal("Response() = nil before message_stop")
	}
	if !resp.Message.Partial {
		t.Error("Message.Partial = false before message_stop")
	}
	if resp.StopReason != StopToolUse {
		t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
	}
}

// TestAccumulatorShowsTheOpenBlock checks that a UI can render text as it
// arrives: the open block appears in Message with what has arrived so far.
func TestAccumulatorShowsTheOpenBlock(t *testing.T) {
	a := applyAll(t, scriptedEvents()[:8]...) // through the first text delta
	m := a.Message()
	if len(m.Content) != 2 {
		t.Fatalf("Message has %d blocks, want 2", len(m.Content))
	}
	if txt, ok := m.Content[1].(Text); !ok || txt.Text != "Let me check " {
		t.Errorf("open block = %#v, want Text{\"Let me check \"}", m.Content[1])
	}
	if !m.Partial {
		t.Error("Partial = false with a block still open")
	}
}

// TestAccumulatorPrefixExcludesTheOpenBlock checks the resumable prefix: only
// blocks the provider finished, because a half-arrived block cannot be
// replayed.
func TestAccumulatorPrefixExcludesTheOpenBlock(t *testing.T) {
	a := applyAll(t, scriptedEvents()[:8]...) // through the first text delta
	prefix := a.Prefix()
	if len(prefix) != 1 {
		t.Fatalf("Prefix has %d blocks, want 1", len(prefix))
	}
	if th, ok := prefix[0].(Thinking); !ok || th.Signature == "" {
		t.Errorf("Prefix[0] = %#v, want the finished thinking block", prefix[0])
	}
}

// TestAccumulatorJustFinishedEdgeFiresOnce is what lets the loop start a tool
// the moment its block closes: the edge is readable after the stop event and
// gone after the next event.
func TestAccumulatorJustFinishedEdgeFiresOnce(t *testing.T) {
	var a Accumulator
	events := scriptedEvents()
	var edges []Block
	for _, e := range events {
		if err := a.Apply(e); err != nil {
			t.Fatalf("Apply(%s): %v", e.Type(), err)
		}
		blk, ok := a.JustFinished()
		if ok != (e.Type() == EventBlockStop) {
			t.Fatalf("after %s JustFinished ok = %v", e.Type(), ok)
		}
		if ok {
			edges = append(edges, blk)
		}
	}
	if len(edges) != 3 {
		t.Fatalf("saw %d finished blocks, want 3", len(edges))
	}
	tu, ok := edges[2].(ToolUse)
	if !ok {
		t.Fatalf("last edge is %T, want ToolUse", edges[2])
	}
	if string(tu.Input) != `{"city":"Paris"}` {
		t.Errorf("Input = %s, want {\"city\":\"Paris\"}", tu.Input)
	}
}

// TestAccumulatorMessageSnapshotsAreIndependent guards value semantics: a
// message handed out earlier must not change as more events arrive, and a
// caller who edits it must not reach into the accumulator.
func TestAccumulatorMessageSnapshotsAreIndependent(t *testing.T) {
	events := scriptedEvents()
	a := applyAll(t, events[:8]...) // through the first text delta
	snapshot := a.Message()
	if len(snapshot.Content) != 2 {
		t.Fatalf("the snapshot has %d blocks, want 2", len(snapshot.Content))
	}

	snapshot.Role = RoleUser
	snapshot.Content[0] = Text{Text: "tampered"}

	for _, e := range events[8:] {
		if err := a.Apply(e); err != nil {
			t.Fatalf("Apply(%s): %v", e.Type(), err)
		}
	}

	if snapshot.Role != RoleUser || len(snapshot.Content) != 2 {
		t.Errorf("the snapshot changed as events arrived: %#v", snapshot)
	}
	if txt, ok := snapshot.Content[1].(Text); !ok || txt.Text != "Let me check " {
		t.Errorf("the snapshot's open block grew: %#v", snapshot.Content[1])
	}
	if !reflect.DeepEqual(a.Message(), scriptedResponse().Message) {
		t.Errorf("editing a snapshot reached the accumulator: %#v", a.Message())
	}
}

// TestAccumulatorUsage records what the two providers do. Anthropic sends the
// input side on message_start and the cumulative counts on message_delta, and
// OpenRouter sends one trailing usage frame; a later report therefore replaces
// an earlier one rather than adding to it. A count the later report leaves at
// zero means "not reported here", never "reset", because counts only grow
// within a message, so the earlier figure survives.
func TestAccumulatorUsage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []Event
		want   Usage
	}{
		{
			name: "a cumulative delta replaces the start",
			events: []Event{
				MessageStart{Usage: Usage{Input: 25, CacheRead: 1024}},
				MessageDelta{Usage: Usage{Input: 25, Output: 130, CacheRead: 1024}},
			},
			want: Usage{Input: 25, Output: 130, CacheRead: 1024},
		},
		{
			name: "a delta that reports only output keeps the input side",
			events: []Event{
				MessageStart{Usage: Usage{Input: 25, CacheRead: 1024}},
				MessageDelta{Usage: Usage{Output: 130}},
			},
			want: Usage{Input: 25, Output: 130, CacheRead: 1024},
		},
		{
			name: "a second delta replaces the first",
			events: []Event{
				MessageDelta{Usage: Usage{Output: 130}},
				MessageDelta{Usage: Usage{Input: 25, Output: 200}},
			},
			want: Usage{Input: 25, Output: 200},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := applyAll(t, tc.events...)
			if got := a.Response().Usage; got != tc.want {
				t.Errorf("Usage = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestAccumulatorIgnoresLoopEvents: a run stream carries the loop's events
// alongside the provider's, and one accumulator sees both.
func TestAccumulatorKeepsTheNativeStopReason(t *testing.T) {
	var acc Accumulator
	events := []Event{
		MessageStart{ID: "gen_1"},
		MessageDelta{StopReason: StopEndTurn, NativeStopReason: "end_turn"},
		MessageDelta{Usage: Usage{Output: 3}},
		MessageStop{},
	}
	for _, ev := range events {
		if err := acc.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	resp := acc.Response()
	if resp.StopReason != StopEndTurn || resp.NativeStopReason != "end_turn" {
		t.Errorf("stop reasons = %q / native %q, want end_turn / end_turn", resp.StopReason, resp.NativeStopReason)
	}
}

func TestAccumulatorIgnoresLoopEvents(t *testing.T) {
	a := applyAll(t,
		TurnStart{Turn: 1},
		MessageStart{ID: "msg_01"},
		BlockStart{Block: Text{}},
		TextDelta{Text: "hi"},
		BlockStop{},
		MessageStop{},
		ToolCallStart{ToolUse: ToolUse{ID: "toolu_1"}},
		// A nested event is a loop event too, so a child's whole message
		// is ignored rather than folded into the parent's.
		ToolEvent{ToolUseID: "toolu_1", Name: "research", Event: TextDelta{Text: "not mine"}},
		ToolEvent{ToolUseID: "toolu_1", Name: "research", Event: MessageStop{}},
		TurnEnd{Turn: 1},
		Done{},
	)
	if got := a.Message().Text(); got != "hi" {
		t.Errorf("Text() = %q, want hi", got)
	}
}

// TestAccumulatorEmptyToolInputIsAnObject: a tool that takes no arguments
// streams no input fragments at all, and an empty input must reach the tool as
// an empty object rather than as invalid JSON.
func TestAccumulatorEmptyToolInputIsAnObject(t *testing.T) {
	a := applyAll(t,
		BlockStart{Block: ToolUse{ID: "toolu_1", Name: "now"}},
		BlockStop{},
	)
	blocks := a.Prefix()
	if len(blocks) != 1 {
		t.Fatalf("Prefix has %d blocks, want 1", len(blocks))
	}
	tu, ok := blocks[0].(ToolUse)
	if !ok {
		t.Fatalf("block is %T, want ToolUse", blocks[0])
	}
	if string(tu.Input) != `{}` {
		t.Errorf("Input = %s, want {}", tu.Input)
	}
}

// TestAccumulatorThinkingTakesRawFromBlockStop covers the OpenRouter case: the
// neutral text and signature cannot rebuild a reasoning_details entry, so the
// provider hands the finished block over at the stop event.
func TestAccumulatorThinkingTakesRawFromBlockStop(t *testing.T) {
	raw := jsontext.Value(`{"type":"reasoning.text","id":"r_1","format":"anthropic-claude-v1","index":0,"text":"why"}`)
	a := applyAll(t,
		BlockStart{Block: Thinking{}},
		ThinkingDelta{Text: "why"},
		SignatureDelta{Signature: "sig"},
		BlockStop{Raw: raw},
	)
	prefix := a.Prefix()
	if len(prefix) != 1 {
		t.Fatalf("Prefix has %d blocks, want 1", len(prefix))
	}
	th, ok := prefix[0].(Thinking)
	if !ok {
		t.Fatalf("block is %T, want Thinking", prefix[0])
	}
	want := Thinking{Text: "why", Signature: "sig", Raw: raw}
	if !reflect.DeepEqual(th, want) {
		t.Errorf("block = %#v, want %#v", th, want)
	}
}

// TestAccumulatorProtocolErrors is the table of streams that break their own
// rules. Every one is a *ProtocolError rather than a silently wrong message,
// because a stream goodall cannot fold is a bug in the provider layer.
func TestAccumulatorProtocolErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []Event
	}{
		{
			name:   "a text delta for an index that was never opened",
			events: []Event{MessageStart{}, TextDelta{Index: 0, Text: "hi"}},
		},
		{
			name:   "a block stop for an index that was never opened",
			events: []Event{MessageStart{}, BlockStop{Index: 3}},
		},
		{
			name:   "a delta for a block that already closed",
			events: []Event{BlockStart{Block: Text{}}, BlockStop{}, TextDelta{Text: "hi"}},
		},
		{
			name:   "a second block start on an open index",
			events: []Event{BlockStart{Block: Text{}}, BlockStart{Block: Text{}}},
		},
		{
			name:   "a text delta into an open tool call",
			events: []Event{BlockStart{Block: ToolUse{ID: "toolu_1"}}, TextDelta{Text: "hi"}},
		},
		{
			name:   "a thinking delta into an open text block",
			events: []Event{BlockStart{Block: Text{}}, ThinkingDelta{Text: "hm"}},
		},
		{
			name:   "a signature delta into an open text block",
			events: []Event{BlockStart{Block: Text{}}, SignatureDelta{Signature: "s"}},
		},
		{
			name:   "a tool input delta into an open thinking block",
			events: []Event{BlockStart{Block: Thinking{}}, ToolInputDelta{PartialJSON: "{}"}},
		},
		{
			name:   "a delta into a redacted thinking block, which arrives whole",
			events: []Event{BlockStart{Block: RedactedThinking{Data: "x"}}, ThinkingDelta{Text: "hm"}},
		},
		{
			name:   "a provider event after message stop",
			events: []Event{MessageStop{}, MessageDelta{StopReason: StopEndTurn}},
		},
		{
			name:   "a second message start",
			events: []Event{MessageStart{ID: "msg_01"}, MessageStart{ID: "msg_02"}},
		},
		{
			name: "a tool input cut off mid-object",
			events: []Event{
				BlockStart{Block: ToolUse{ID: "toolu_1", Name: "get_weather"}},
				ToolInputDelta{PartialJSON: `{"city":`},
				BlockStop{},
			},
		},
		{
			name: "a tool input that is not JSON at all",
			events: []Event{
				BlockStart{Block: ToolUse{ID: "toolu_1", Name: "get_weather"}},
				ToolInputDelta{PartialJSON: `city=Paris`},
				BlockStop{},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var a Accumulator
			var err error
			for _, e := range tc.events {
				if err = a.Apply(e); err != nil {
					break
				}
			}
			if err == nil {
				t.Fatalf("the stream was accepted; message = %#v", a.Message())
			}
			pe, ok := errors.AsType[*ProtocolError](err)
			if !ok {
				t.Fatalf("Apply returned %T (%v), want *ProtocolError", err, err)
			}
			if pe.Reason == "" {
				t.Error("the error carries no reason")
			}
		})
	}
}

// TestProtocolErrorNamesTheEventAndBlock keeps the message useful: a provider
// author reading it must know which event and which block index broke.
func TestProtocolErrorNamesTheEventAndBlock(t *testing.T) {
	var a Accumulator
	err := a.Apply(TextDelta{Index: 7, Text: "hi"})
	pe, ok := errors.AsType[*ProtocolError](err)
	if !ok {
		t.Fatalf("Apply returned %T, want *ProtocolError", err)
	}
	if pe.Event != string(EventTextDelta) {
		t.Errorf("Event = %q, want %q", pe.Event, EventTextDelta)
	}
	if pe.Index != 7 {
		t.Errorf("Index = %d, want 7", pe.Index)
	}
	for _, want := range []string{"goodall", "text_delta", "7"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestAccumulatorCompactsToolInput: the streamed fragments of a tool call keep
// whatever whitespace the provider used, and the blocking path compacts, so
// the two paths would disagree byte for byte on the same call unless the
// accumulator compacts too.
func TestAccumulatorCompactsToolInput(t *testing.T) {
	var acc Accumulator
	events := []Event{
		MessageStart{ID: "msg_1"},
		BlockStart{Index: 0, Block: ToolUse{ID: "toolu_1", Name: "get_weather"}},
		ToolInputDelta{Index: 0, PartialJSON: `{"city": `},
		ToolInputDelta{Index: 0, PartialJSON: ` "Paris" }`},
		BlockStop{Index: 0},
		MessageDelta{StopReason: StopToolUse},
		MessageStop{},
	}
	for _, ev := range events {
		if err := acc.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	uses := acc.Message().ToolUses()
	if len(uses) != 1 {
		t.Fatalf("tool uses = %d, want 1", len(uses))
	}
	if got, want := string(uses[0].Input), `{"city":"Paris"}`; got != want {
		t.Errorf("input = %s, want %s", got, want)
	}
}
