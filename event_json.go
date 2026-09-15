package goodall

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
)

// goodall's JSON shape for an event is an object whose first member is "type"
// and whose remaining members are the event's fields in declaration order,
// with zero-valued members left out. It is the format the chat layer writes to
// a front end over SSE or NDJSON, so it is goodall's own — providers
// neutralise onto it and never see it again.
//
// Marshalling needs no options: each event type carries a MarshalJSONTo that
// writes its tag, mirroring the block model, so a []Event or a struct holding
// one encodes with plain json.Marshal. Unmarshalling does, because
// encoding/json/v2 cannot pick a concrete type for an interface on its own;
// UnmarshalEvent and Events.UnmarshalJSONFrom apply them.

// taggedEvent writes an event's fields after its type tag. It mirrors the
// block model's tagged: the body is a defined type without marshal methods, so
// embedding it here cannot recurse. The two cannot be one generic type without
// widening tagged's tag parameter, which belongs to the block leaf's file.
type taggedEvent[T any] struct {
	Type EventType `json:"type"`
	Body T         `json:",embed"`
}

// Each body type strips the marshal method from its event so the event can be
// encoded through taggedEvent without calling itself.
type (
	messageStartBody   MessageStart
	blockStartBody     BlockStart
	textDeltaBody      TextDelta
	thinkingDeltaBody  ThinkingDelta
	signatureDeltaBody SignatureDelta
	toolInputDeltaBody ToolInputDelta
	blockStopBody      BlockStop
	messageDeltaBody   MessageDelta
	messageStopBody    MessageStop
	unknownEventBody   UnknownEvent
	turnStartBody      TurnStart
	toolCallStartBody  ToolCallStart
	toolCallEndBody    ToolCallEnd
	turnEndBody        TurnEnd
	doneBody           Done
	stoppedBody        Stopped
)

// MarshalJSONTo writes the event as a "message_start" object.
func (e MessageStart) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[messageStartBody]{EventMessageStart, messageStartBody(e)})
}

// MarshalJSONTo writes the event as a "block_start" object.
func (e BlockStart) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[blockStartBody]{EventBlockStart, blockStartBody(e)})
}

// MarshalJSONTo writes the event as a "text_delta" object.
func (e TextDelta) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[textDeltaBody]{EventTextDelta, textDeltaBody(e)})
}

// MarshalJSONTo writes the event as a "thinking_delta" object.
func (e ThinkingDelta) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[thinkingDeltaBody]{EventThinkingDelta, thinkingDeltaBody(e)})
}

// MarshalJSONTo writes the event as a "signature_delta" object.
func (e SignatureDelta) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[signatureDeltaBody]{EventSignatureDelta, signatureDeltaBody(e)})
}

// MarshalJSONTo writes the event as a "tool_input_delta" object.
func (e ToolInputDelta) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[toolInputDeltaBody]{EventToolInputDelta, toolInputDeltaBody(e)})
}

// MarshalJSONTo writes the event as a "block_stop" object.
func (e BlockStop) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[blockStopBody]{EventBlockStop, blockStopBody(e)})
}

// MarshalJSONTo writes the event as a "message_delta" object.
func (e MessageDelta) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[messageDeltaBody]{EventMessageDelta, messageDeltaBody(e)})
}

// MarshalJSONTo writes the event as a "message_stop" object.
func (e MessageStop) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[messageStopBody]{EventMessageStop, messageStopBody(e)})
}

// MarshalJSONTo writes the event as an "unknown" object, keeping the
// provider's own tag in event_type and its bytes in raw.
func (e UnknownEvent) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[unknownEventBody]{EventUnknown, unknownEventBody(e)})
}

// MarshalJSONTo writes the event as a "turn_start" object.
func (e TurnStart) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[turnStartBody]{EventTurnStart, turnStartBody(e)})
}

// MarshalJSONTo writes the event as a "tool_call_start" object.
func (e ToolCallStart) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[toolCallStartBody]{EventToolCallStart, toolCallStartBody(e)})
}

// MarshalJSONTo writes the event as a "tool_call_end" object.
func (e ToolCallEnd) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[toolCallEndBody]{EventToolCallEnd, toolCallEndBody(e)})
}

// MarshalJSONTo writes the event as a "turn_end" object.
func (e TurnEnd) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[turnEndBody]{EventTurnEnd, turnEndBody(e)})
}

// MarshalJSONTo writes the event as a "done" object.
func (e Done) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[doneBody]{EventDone, doneBody(e)})
}

// MarshalJSONTo writes the event as a "stopped" object.
func (e Stopped) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, taggedEvent[stoppedBody]{EventStopped, stoppedBody(e)})
}

// Events is a list of events. It is a named type because decoding an interface
// needs the type-tag dispatch this package installs; a plain []Event encodes
// correctly but cannot be decoded by encoding/json/v2 on its own. Any struct
// with an Events field marshals and unmarshals with no options.
type Events []Event

// UnmarshalJSONFrom decodes a JSON array of events, choosing each event's
// concrete type from its "type" member.
func (e *Events) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	return json.UnmarshalDecode(dec, (*[]Event)(e), eventUnmarshalers)
}

// UnmarshalEvent decodes one event from the JSON object a provider or the chat
// layer wrote. A type goodall does not recognise yields an UnknownEvent
// holding the original bytes rather than an error, so a front end that reads
// its own stream back never loses an event (invariant 6).
func UnmarshalEvent(data []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(data, &e, eventUnmarshalers); err != nil {
		return nil, err
	}
	return e, nil
}

// eventUnmarshalers dispatches on the "type" member. It is a package-level
// value because building the option set on every decode would allocate on a
// hot path. The block dispatch is joined into the same Unmarshalers rather
// than added as a second option: a later WithUnmarshalers replaces an earlier
// one instead of adding to it, which silently disables the event dispatch.
var eventUnmarshalers = json.WithUnmarshalers(json.JoinUnmarshalers(
	json.UnmarshalFromFunc(unmarshalEvent),
	json.UnmarshalFromFunc(unmarshalBlock),
))

// unmarshalEvent reads one event and picks its concrete type from the "type"
// member.
func unmarshalEvent(dec *jsontext.Decoder, e *Event) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	// ReadValue's buffer is only valid until the next read, and
	// UnknownEvent keeps these bytes.
	raw = bytes.Clone(raw)

	var probe struct {
		Type EventType `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		*e = UnknownEvent{Raw: raw}
		return nil
	}
	switch probe.Type {
	case EventMessageStart:
		return decodeEvent(e, raw, func(v messageStartBody) Event { return MessageStart(v) })
	case EventBlockStart:
		return decodeEvent(e, raw, func(v blockStartBody) Event { return BlockStart(v) })
	case EventTextDelta:
		return decodeEvent(e, raw, func(v textDeltaBody) Event { return TextDelta(v) })
	case EventThinkingDelta:
		return decodeEvent(e, raw, func(v thinkingDeltaBody) Event { return ThinkingDelta(v) })
	case EventSignatureDelta:
		return decodeEvent(e, raw, func(v signatureDeltaBody) Event { return SignatureDelta(v) })
	case EventToolInputDelta:
		return decodeEvent(e, raw, func(v toolInputDeltaBody) Event { return ToolInputDelta(v) })
	case EventBlockStop:
		return decodeEvent(e, raw, func(v blockStopBody) Event { return BlockStop(v) })
	case EventMessageDelta:
		return decodeEvent(e, raw, func(v messageDeltaBody) Event { return MessageDelta(v) })
	case EventMessageStop:
		return decodeEvent(e, raw, func(v messageStopBody) Event { return MessageStop(v) })
	case EventUnknown:
		return decodeEvent(e, raw, func(v unknownEventBody) Event { return UnknownEvent(v) })
	case EventTurnStart:
		return decodeEvent(e, raw, func(v turnStartBody) Event { return TurnStart(v) })
	case EventToolCallStart:
		return decodeEvent(e, raw, func(v toolCallStartBody) Event { return ToolCallStart(v) })
	case EventToolCallEnd:
		return decodeEvent(e, raw, func(v toolCallEndBody) Event { return ToolCallEnd(v) })
	case EventTurnEnd:
		return decodeEvent(e, raw, func(v turnEndBody) Event { return TurnEnd(v) })
	case EventDone:
		return decodeEvent(e, raw, func(v doneBody) Event { return Done(v) })
	case EventStopped:
		return decodeEvent(e, raw, func(v stoppedBody) Event { return Stopped(v) })
	default:
		*e = UnknownEvent{EventType: probe.Type, Raw: raw}
		return nil
	}
}

// decodeEvent decodes raw into an event's body type, which has no unmarshal
// method of its own, and converts it back to the event. The "type" member is
// an unknown member to the body, and json/v2 ignores unknown members. The
// block dispatch travels along for BlockStart's Block field, which is an
// interface of its own.
func decodeEvent[Body any](out *Event, raw jsontext.Value, convert func(Body) Event) error {
	var v Body
	if err := json.Unmarshal(raw, &v, blockUnmarshalers); err != nil {
		return fmt.Errorf("goodall: decoding a %T event: %w", convert(v), err)
	}
	*out = convert(v)
	return nil
}
