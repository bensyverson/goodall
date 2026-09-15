package goodall

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

// eventCase pairs an event value with the exact bytes goodall must produce for
// it. Both directions are asserted, so the table is the specification of the
// event wire shape the chat layer serialises to a front end.
type eventCase struct {
	name  string
	event Event
	want  string
}

// costOf builds a reported cost in US dollars for the tables below.
func costOf(amount string) Cost {
	d, err := ParseDecimal(amount)
	if err != nil {
		panic(err)
	}
	return Cost{Amount: d, Currency: "USD", Reported: true}
}

// sampleConversation is one user turn, used wherever a Result needs a history.
func sampleConversation() Conversation {
	return Conversation{}.Append(UserMessage(Text{Text: "hi"}))
}

const sampleConversationJSON = `[{"role":"user","content":[{"type":"text","text":"hi"}]}]`

func eventCases() []eventCase {
	return []eventCase{
		{
			name:  "message start",
			event: MessageStart{ID: "msg_01", Model: "claude-opus-5", Usage: Usage{Input: 25, CacheRead: 1024}},
			want:  `{"type":"message_start","id":"msg_01","model":"claude-opus-5","usage":{"input_tokens":25,"cache_read_tokens":1024}}`,
		},
		{
			name:  "block start opening a text block at index zero",
			event: BlockStart{Block: Text{}},
			want:  `{"type":"block_start","block":{"type":"text","text":""}}`,
		},
		{
			name:  "block start opening a tool call",
			event: BlockStart{Index: 1, Block: ToolUse{ID: "toolu_1", Name: "get_weather"}},
			want:  `{"type":"block_start","index":1,"block":{"type":"tool_use","id":"toolu_1","name":"get_weather"}}`,
		},
		{
			name:  "block start opening a thinking block",
			event: BlockStart{Index: 2, Block: Thinking{}},
			want:  `{"type":"block_start","index":2,"block":{"type":"thinking"}}`,
		},
		{
			name:  "block start opening a redacted thinking block",
			event: BlockStart{Index: 3, Block: RedactedThinking{Data: "EncryptedPayload"}},
			want:  `{"type":"block_start","index":3,"block":{"type":"redacted_thinking","data":"EncryptedPayload"}}`,
		},
		{
			name: "block start opening a block goodall does not know",
			event: BlockStart{Index: 4, Block: Unknown{
				Type: "server_tool_use",
				Raw:  jsontext.Value(`{"type":"server_tool_use","id":"srvtoolu_1"}`),
			}},
			want: `{"type":"block_start","index":4,"block":{"type":"server_tool_use","id":"srvtoolu_1"}}`,
		},
		{
			name:  "text delta",
			event: TextDelta{Text: "Hello, "},
			want:  `{"type":"text_delta","text":"Hello, "}`,
		},
		{
			name:  "thinking delta",
			event: ThinkingDelta{Index: 2, Text: "weighing the options"},
			want:  `{"type":"thinking_delta","index":2,"text":"weighing the options"}`,
		},
		{
			name:  "signature delta",
			event: SignatureDelta{Index: 2, Signature: "ErUBCkYIBRgCKkB"},
			want:  `{"type":"signature_delta","index":2,"signature":"ErUBCkYIBRgCKkB"}`,
		},
		{
			name:  "tool input delta",
			event: ToolInputDelta{Index: 1, PartialJSON: `{"city":`},
			want:  `{"type":"tool_input_delta","index":1,"partial_json":"{\"city\":"}`,
		},
		{
			name:  "block stop",
			event: BlockStop{Index: 1},
			want:  `{"type":"block_stop","index":1}`,
		},
		{
			name: "block stop carrying the provider's finished block",
			event: BlockStop{Index: 2, Raw: jsontext.Value(
				`{"type":"reasoning.text","id":"r_1","format":"anthropic-claude-v1","index":0}`)},
			want: `{"type":"block_stop","index":2,"raw":{"type":"reasoning.text","id":"r_1","format":"anthropic-claude-v1","index":0}}`,
		},
		{
			name: "message delta",
			event: MessageDelta{
				StopReason: StopToolUse,
				Usage:      Usage{Input: 25, Output: 130, Reasoning: 64},
				Cost:       costOf("0.0123"),
			},
			want: `{"type":"message_delta","stop_reason":"tool_use","usage":{"input_tokens":25,"output_tokens":130,"reasoning_tokens":64},"cost":{"amount":0.0123,"currency":"USD","reported":true}}`,
		},
		{
			name:  "message delta naming the stop sequence",
			event: MessageDelta{StopReason: StopSequence, StopSequence: "END"},
			want:  `{"type":"message_delta","stop_reason":"stop_sequence","stop_sequence":"END"}`,
		},
		{
			name:  "message delta carrying the upstream provider's own stop reason",
			event: MessageDelta{StopReason: StopEndTurn, NativeStopReason: "end_turn"},
			want:  `{"type":"message_delta","stop_reason":"end_turn","native_stop_reason":"end_turn"}`,
		},
		{
			name:  "message stop",
			event: MessageStop{},
			want:  `{"type":"message_stop"}`,
		},
		{
			name:  "an event goodall does not know",
			event: UnknownEvent{EventType: "ping", Raw: jsontext.Value(`{"type":"ping"}`)},
			want:  `{"type":"unknown","event_type":"ping","raw":{"type":"ping"}}`,
		},
		{
			name:  "turn start",
			event: TurnStart{Turn: 1},
			want:  `{"type":"turn_start","turn":1}`,
		},
		{
			name: "tool call start",
			event: ToolCallStart{ToolUse: ToolUse{
				ID: "toolu_1", Name: "get_weather", Input: jsontext.Value(`{"city":"Paris"}`),
			}},
			want: `{"type":"tool_call_start","tool_use":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Paris"}}}`,
		},
		{
			name: "tool call end",
			event: ToolCallEnd{
				ToolUse: ToolUse{ID: "toolu_1", Name: "get_weather", Input: jsontext.Value(`{"city":"Paris"}`)},
				Result:  ToolResult{ToolUseID: "toolu_1", Content: Blocks{Text{Text: "18C"}}},
			},
			want: `{"type":"tool_call_end","tool_use":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Paris"}},"result":{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"18C"}]}}`,
		},
		{
			name: "turn end",
			event: TurnEnd{Turn: 1, Response: Response{
				ID:         "msg_01",
				Model:      "claude-opus-5",
				Message:    AssistantMessage(Text{Text: "18C in Paris."}),
				StopReason: StopEndTurn,
				Usage:      Usage{Input: 25, Output: 130},
			}},
			want: `{"type":"turn_end","turn":1,"response":{"id":"msg_01","model":"claude-opus-5","message":{"role":"assistant","content":[{"type":"text","text":"18C in Paris."}]},"stop_reason":"end_turn","usage":{"input_tokens":25,"output_tokens":130}}}`,
		},
		{
			name: "done",
			event: Done{Result: Result{
				Conversation: sampleConversation(),
				Usage:        Usage{Input: 25, Output: 130},
				Cost:         costOf("0.0123"),
				StopReason:   StopEndTurn,
			}},
			want: `{"type":"done","result":{"conversation":` + sampleConversationJSON +
				`,"usage":{"input_tokens":25,"output_tokens":130},"cost":{"amount":0.0123,"currency":"USD","reported":true},"stop_reason":"end_turn"}}`,
		},
		{
			name: "stopped on cancellation",
			event: Stopped{
				Cause:   StopCauseCancelled,
				Message: "the caller cancelled the run",
				Result:  Result{Conversation: sampleConversation()},
			},
			want: `{"type":"stopped","cause":"cancelled","message":"the caller cancelled the run","result":{"conversation":` +
				sampleConversationJSON + `}}`,
		},
		{
			name: "stopped by an error, which travels as a message and a kind",
			event: Stopped{
				Cause:   StopCauseError,
				Message: "anthropic: overloaded (529 overloaded_error)",
				Kind:    KindOverloaded,
			},
			want: `{"type":"stopped","cause":"error","message":"anthropic: overloaded (529 overloaded_error)","kind":"overloaded","result":{}}`,
		},
		{
			name: "stopped with the tool calls a hook deferred",
			event: Stopped{
				Cause:  StopCauseDeferred,
				Result: Result{Pending: []ToolUse{{ID: "toolu_9", Name: "send_email"}}},
			},
			want: `{"type":"stopped","cause":"deferred","result":{"pending":[{"type":"tool_use","id":"toolu_9","name":"send_email"}]}}`,
		},
	}
}

// TestEventMarshalBytes asserts the exact JSON goodall writes for every event,
// with plain json.Marshal and no options.
func TestEventMarshalBytes(t *testing.T) {
	for _, c := range eventCases() {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.event)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("Marshal =\n\t%s\nwant\n\t%s", got, c.want)
			}
		})
	}
}

// TestEventTableCoversEveryType is the guard against an event type being added
// without a wire shape: every constant goodall defines appears in the table.
func TestEventTableCoversEveryType(t *testing.T) {
	all := []EventType{
		EventMessageStart, EventBlockStart, EventTextDelta, EventThinkingDelta,
		EventSignatureDelta, EventToolInputDelta, EventBlockStop, EventMessageDelta,
		EventMessageStop, EventUnknown, EventTurnStart, EventToolCallStart,
		EventToolCallEnd, EventTurnEnd, EventDone, EventStopped,
	}
	seen := map[EventType]bool{}
	for _, c := range eventCases() {
		seen[c.event.Type()] = true
	}
	for _, want := range all {
		if !seen[want] {
			t.Errorf("no table case for %s", want)
		}
	}
	if len(all) != 16 {
		t.Errorf("the event family has %d types, want 16", len(all))
	}
}

// TestEventRoundTrip asserts every event decodes back to an equal value and
// re-encodes byte-for-byte, through UnmarshalEvent and through Events.
func TestEventRoundTrip(t *testing.T) {
	for _, c := range eventCases() {
		t.Run(c.name, func(t *testing.T) {
			got, err := UnmarshalEvent([]byte(c.want))
			if err != nil {
				t.Fatalf("UnmarshalEvent: %v", err)
			}
			if !reflect.DeepEqual(got, c.event) {
				t.Fatalf("UnmarshalEvent gave\n\t%#v\nwant\n\t%#v", got, c.event)
			}
			again, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(again) != c.want {
				t.Errorf("re-encoded\n\t%s\nwant\n\t%s", again, c.want)
			}

			var list Events
			if err := json.Unmarshal([]byte("["+c.want+"]"), &list); err != nil {
				t.Fatalf("Unmarshal into Events: %v", err)
			}
			if len(list) != 1 || !reflect.DeepEqual(list[0], c.event) {
				t.Fatalf("Events decoded %#v, want one %#v", list, c.event)
			}
		})
	}
}

// TestEventsMarshalAsPlainSlice proves a bare []Event encodes without options,
// which is what lets the chat layer write a stream with any encoder.
func TestEventsMarshalAsPlainSlice(t *testing.T) {
	got, err := json.Marshal([]Event{TextDelta{Text: "a"}, MessageStop{}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `[{"type":"text_delta","text":"a"},{"type":"message_stop"}]`
	if string(got) != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}
}

// TestUnknownEventFromUnrecognisedType is the guard for "unknown event types
// are surfaced, never dropped": a tag the library does not know becomes an
// UnknownEvent carrying the provider's tag and the original bytes.
func TestUnknownEventFromUnrecognisedType(t *testing.T) {
	const in = `{"type":"citations_delta","index":0,"delta":{"citation":{"cited_text":"x"}}}`
	got, err := UnmarshalEvent([]byte(in))
	if err != nil {
		t.Fatalf("UnmarshalEvent: %v", err)
	}
	u, ok := got.(UnknownEvent)
	if !ok {
		t.Fatalf("decoded %T, want UnknownEvent", got)
	}
	if u.EventType != "citations_delta" {
		t.Errorf("EventType = %q, want citations_delta", u.EventType)
	}
	if string(u.Raw) != in {
		t.Errorf("Raw = %s, want %s", u.Raw, in)
	}
}

// TestUnknownEventRawIsNotAliased guards against retaining the decoder's
// scratch buffer: a jsontext.Value read out of a decoder is voided by the next
// read, so the bytes of one event must not be overwritten by the next.
func TestUnknownEventRawIsNotAliased(t *testing.T) {
	var want, parts []string
	// Long enough, and read a byte at a time, so a streaming decoder must
	// refill and reuse its buffer while these events are still held.
	for i := range 40 {
		e := fmt.Sprintf(`{"type":"alpha%d","padding":%q}`, i, strings.Repeat("x", 64))
		parts = append(parts, e)
		want = append(want, e)
	}
	in := "[" + strings.Join(parts, ",") + "]"

	var got Events
	if err := json.UnmarshalRead(iotest.OneByteReader(strings.NewReader(in)), &got); err != nil {
		t.Fatalf("UnmarshalRead: %v", err)
	}
	for i, w := range want {
		u, ok := got[i].(UnknownEvent)
		if !ok {
			t.Fatalf("event %d is %T, want UnknownEvent", i, got[i])
		}
		if string(u.Raw) != w {
			t.Fatalf("event %d Raw = %s, want %s", i, u.Raw, w)
		}
	}
}

// TestMalformedEventIsAnError draws the line between "a type goodall does not
// know", which becomes an UnknownEvent, and "an event of a known type whose
// body is wrong", which is a decode error naming the event.
func TestMalformedEventIsAnError(t *testing.T) {
	got, err := UnmarshalEvent([]byte(`{"type":"text_delta","index":"one"}`))
	if err == nil {
		t.Fatalf("UnmarshalEvent succeeded and gave %#v, want an error", got)
	}
	if !strings.Contains(err.Error(), "goodall.TextDelta") {
		t.Errorf("error %q does not name the event type", err)
	}
}

// TestEventTypeClassification checks the split a run stream depends on: the
// loop's six events are distinguishable from a provider's without a type
// switch.
func TestEventTypeClassification(t *testing.T) {
	for _, tc := range []struct {
		event Event
		loop  bool
	}{
		{MessageStart{}, false},
		{BlockStart{}, false},
		{MessageStop{}, false},
		{UnknownEvent{}, false},
		{TurnStart{}, true},
		{ToolCallStart{}, true},
		{ToolCallEnd{}, true},
		{TurnEnd{}, true},
		{Done{}, true},
		{Stopped{}, true},
	} {
		t.Run(string(tc.event.Type()), func(t *testing.T) {
			if got := tc.event.Type().FromLoop(); got != tc.loop {
				t.Errorf("FromLoop() = %v, want %v", got, tc.loop)
			}
			if !tc.event.Type().Known() {
				t.Errorf("%s is not Known", tc.event.Type())
			}
		})
	}
	if EventType("citations_delta").Known() {
		t.Error("an unrecognised type reports itself Known")
	}
}
