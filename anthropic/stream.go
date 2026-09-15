package anthropic

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/sse"
)

// Stream sends the request and yields the events it produces, which is the
// provider path goodall.Provider requires.
//
// The stream owns the HTTP response: ranging to the end, breaking out early
// and canceling ctx all close the body. It yields at most one error, which
// ends it — a translation failure before the call, the transport's
// *goodall.APIError for a failing status, an error event mid-stream, or the
// context's own error.
//
// Nothing runs until the first iteration: the request is sent on the
// consumer's goroutine, so there is no goroutine to leak if the stream is
// never read.
func (c *Client) Stream(ctx context.Context, req *goodall.Request) goodall.Stream {
	return func(yield func(goodall.Event, error) bool) {
		call, err := c.messageRequest(req, true)
		if err != nil {
			yield(nil, err)
			return
		}
		resp, err := c.http.Do(ctx, call)
		if err != nil {
			yield(nil, err)
			return
		}
		// The defer is what makes the promise above true for every exit,
		// including the consumer breaking out of the range.
		defer resp.Body.Close()

		for raw, err := range sse.Events(resp.Body) {
			if err != nil {
				// A canceled read reports the context's error, but
				// a framing error that raced the cancellation would
				// not; the caller cares which one happened.
				if ctxErr := ctx.Err(); ctxErr != nil {
					err = ctxErr
				}
				yield(nil, err)
				return
			}
			events, apiErr, decodeErr := decodeStreamEvent(raw, resp.Header)
			for _, ev := range events {
				if !yield(ev, nil) {
					return
				}
			}
			if apiErr != nil {
				yield(nil, apiErr)
				return
			}
			if decodeErr != nil {
				yield(nil, decodeErr)
				return
			}
		}
	}
}

// decodeStreamEvent turns one server-sent event into the neutral events it
// carries. It returns at most one event today; the slice is there because a
// provider event that fans out into two would otherwise have to choose one.
//
// An error event returns the provider's own *goodall.APIError, which ends the
// stream: HTTP 200 is not success, and an overload mid-generation is the
// service refusing, not a broken stream.
func decodeStreamEvent(raw sse.Event, header http.Header) ([]goodall.Event, *goodall.APIError, error) {
	data := []byte(raw.Data)
	switch streamEventType(raw.Type) {
	case streamMessageStart:
		var w wireMessageStartEvent
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, nil, malformed(raw, err)
		}
		return []goodall.Event{goodall.MessageStart{
			ID:    w.Message.ID,
			Model: w.Message.Model,
			Usage: w.Message.Usage.neutral(),
		}}, nil, nil

	case streamContentBlockStart:
		var w wireContentBlockStartEvent
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, nil, malformed(raw, err)
		}
		return []goodall.Event{goodall.BlockStart{
			Index: w.Index,
			Block: neutralBlock(w.ContentBlock.Block),
		}}, nil, nil

	case streamContentBlockDelta:
		var w wireContentBlockDeltaEvent
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, nil, malformed(raw, err)
		}
		return []goodall.Event{neutralDelta(w, data)}, nil, nil

	case streamContentBlockStop:
		var w wireContentBlockStopEvent
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, nil, malformed(raw, err)
		}
		// Raw stays empty: Anthropic's thinking block is rebuilt exactly
		// from its text and its signature, so carrying the bytes as well
		// would give one block two sources of truth.
		return []goodall.Event{goodall.BlockStop{Index: w.Index}}, nil, nil

	case streamMessageDelta:
		var w wireMessageDeltaEvent
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, nil, malformed(raw, err)
		}
		// NativeStopReason stays empty: Anthropic's wire string is the
		// StopReason, not a router's normalization of someone else's.
		return []goodall.Event{goodall.MessageDelta{
			StopReason:   w.Delta.StopReason,
			StopSequence: w.Delta.StopSequence,
			Usage:        w.Usage.neutral(),
		}}, nil, nil

	case streamMessageStop:
		return []goodall.Event{goodall.MessageStop{}}, nil, nil

	case streamPing:
		// A keep-alive. It is a documented event carrying nothing about
		// the message, so it is skipped rather than surfaced as unknown:
		// an UnknownEvent means "goodall did not recognize this", and a
		// consumer would learn nothing from one every few seconds.
		return nil, nil, nil

	case streamError:
		return nil, apiErrorFrom(statusInBody, header, data), nil

	default:
		// Anthropic adds event types without notice and the docs require
		// them to be tolerated, so an unrecognized one travels whole.
		return []goodall.Event{goodall.UnknownEvent{
			EventType: goodall.EventType(raw.Type),
			Raw:       rawValue(raw.Data),
		}}, nil, nil
	}
}

// neutralDelta maps one content_block_delta onto its neutral event. A delta
// type goodall does not model — citations today, whatever Anthropic adds
// tomorrow — becomes an UnknownEvent carrying the whole event's bytes, so
// nothing is dropped and a consumer can act on it without this package
// knowing what it means.
func neutralDelta(w wireContentBlockDeltaEvent, data []byte) goodall.Event {
	switch w.Delta.Type {
	case deltaText:
		return goodall.TextDelta{Index: w.Index, Text: w.Delta.Text}
	case deltaThinking:
		// An empty thinking delta is expected rather than an anomaly:
		// display "omitted" still opens the block and signs it.
		return goodall.ThinkingDelta{Index: w.Index, Text: w.Delta.Thinking}
	case deltaSignature:
		return goodall.SignatureDelta{Index: w.Index, Signature: w.Delta.Signature}
	case deltaInputJSON:
		return goodall.ToolInputDelta{Index: w.Index, PartialJSON: w.Delta.PartialJSON}
	default:
		return goodall.UnknownEvent{
			EventType: goodall.EventType(w.Delta.Type),
			Raw:       rawValue(string(data)),
		}
	}
}

// malformed is the error for an event whose data this package could not read.
// It is a *goodall.ProtocolError rather than an *APIError because it names a
// disagreement about the wire format, which no caller can fix by retrying.
func malformed(raw sse.Event, err error) error {
	return &goodall.ProtocolError{
		Event:  raw.Type,
		Reason: "the event's data could not be decoded: " + err.Error(),
	}
}

// rawValue keeps an event's bytes for an UnknownEvent. Anything that is not a
// JSON value is quoted into one, because every event must stay serializable
// (invariant 4) and an event carrying invalid bytes would fail to marshal at
// whatever boundary it reached.
func rawValue(data string) jsontext.Value {
	if v := jsontext.Value(data); v.IsValid() {
		return v
	}
	quoted, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	return jsontext.Value(quoted)
}
