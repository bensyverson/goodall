package goodall

import (
	"iter"
	"strconv"
	"strings"
)

// ProtocolError is a stream that broke the event contract: a delta for a block
// nobody opened, a delta of the wrong kind for the block that is open, a tool
// call whose input never became valid JSON, a stream that ended without a
// terminal event. It is always used as a pointer, so
// errors.AsType[*goodall.ProtocolError](err) extracts it.
//
// It is a distinct type from *APIError because it names a bug in a provider
// layer rather than something the model or the service did, and because a
// caller must never retry it expecting a different answer.
type ProtocolError struct {
	// Event is the wire tag of the event that broke the contract, empty
	// when the fault is the shape of the stream as a whole.
	Event string
	// Index is the content block the event addressed, meaningful only for
	// the events that carry one.
	Index int
	// Reason says what was wrong, in the terms a provider author needs.
	Reason string
}

// Error renders the error as, at most,
// "goodall: text_delta for block 7: no block is open at this index".
func (e *ProtocolError) Error() string {
	var b strings.Builder
	b.WriteString("goodall: ")
	if e.Event != "" {
		b.WriteString(e.Event)
		if indexedEvent(EventType(e.Event)) {
			b.WriteString(" for block ")
			b.WriteString(strconv.Itoa(e.Index))
		}
		b.WriteString(": ")
	}
	b.WriteString(e.Reason)
	return b.String()
}

// indexedEvent reports whether an event addresses a content block, which is
// what decides whether the index in an error message means anything.
func indexedEvent(t EventType) bool {
	switch t {
	case EventBlockStart, EventBlockStop, EventTextDelta, EventThinkingDelta,
		EventSignatureDelta, EventToolInputDelta:
		return true
	}
	return false
}

// Stream is a sequence of events from a provider or from an agent run. It is
// an iterator rather than a channel because an iterator is synchronous and
// owns its cleanup: a consumer that breaks out early unwinds the producer on
// its own goroutine, so the HTTP response body is closed at the break instead
// of being leaked behind a goroutine nobody drains.
//
// A Stream has one consumer and is read once. Yielding a non-nil error ends
// it; there is no event after one.
type Stream iter.Seq2[Event, error]

// Collect drives a provider stream to its end and returns the whole response,
// which is what makes a blocking call just a streaming call nobody watched.
//
// It always returns a response, never nil. A stream that fails partway hands
// back everything that arrived, with Message.Partial set, alongside the error
// — a cancelled answer is still worth showing, and the usage reported so far
// is still worth billing. A stream that simply ends without MessageStop is a
// *ProtocolError for the same reason: silence is not a finished message.
func (s Stream) Collect() (*Response, error) {
	var acc Accumulator
	for ev, err := range s {
		if err != nil {
			return acc.Response(), err
		}
		if err := acc.Apply(ev); err != nil {
			return acc.Response(), err
		}
	}
	if !acc.Done() {
		return acc.Response(), &ProtocolError{
			Event:  string(EventMessageStop),
			Reason: "the stream ended before message_stop, so the message is incomplete",
		}
	}
	return acc.Response(), nil
}

// CollectResult drives a run stream to its terminal event and returns the
// result that event carries: the last response, the conversation the run
// produced and the totals over every turn.
//
// Invariant 10 says every exit path ends in a Done or a Stopped, including
// cancellation, so a run stream that ends without one is a *ProtocolError
// rather than an empty result. Use Collect for a provider stream; this is for
// the stream an Agent returns.
func (s Stream) CollectResult() (*Result, error) {
	var out *Result
	for ev, err := range s {
		if err != nil {
			return out, err
		}
		switch e := ev.(type) {
		case Done:
			result := e.Result
			out = &result
		case Stopped:
			result := e.Result
			out = &result
		}
	}
	if out == nil {
		return nil, &ProtocolError{
			Reason: "the run stream ended without a done or stopped event",
		}
	}
	return out, nil
}
