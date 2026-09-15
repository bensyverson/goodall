package chat

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"

	"github.com/bensyverson/goodall"
)

// EventError is the wire tag of the error frame the writers in this package
// emit. It is not a [goodall.Event]: a run reports its own endings as Done or
// Stopped events, and this is the other thing that can happen to a *stream* —
// the subscription was dropped, or the producer broke its contract. It shares
// the events' "type" member so a front end meets every frame in one switch.
//
// It is "stream_error" rather than "error" because a browser's EventSource
// already delivers its own connection failures to a listener named "error";
// a frame under that name would land in the same handler as a dropped
// connection, and every front end would have to tell the two apart by hand.
const EventError goodall.EventType = "stream_error"

// WireErrorCode classifies an error frame in terms a front end can act on,
// beside the message, which is prose and may change.
type WireErrorCode string

const (
	// WireErrorSubscriberOverflow is [ErrSubscriberOverflow]: this client
	// fell too far behind and was dropped. Subscribing again replays the
	// run so far, so a front end reconnects rather than reporting a fault.
	WireErrorSubscriberOverflow WireErrorCode = "subscriber_overflow"
	// WireErrorStream is any other failure of the stream. The run's own
	// endings never arrive this way, so a front end treats it as the
	// connection having gone wrong rather than the answer having failed.
	WireErrorStream WireErrorCode = "stream_error"
)

// WireError is the last frame of a stream that ended in an error. It is a
// value, not an error type: it exists to be marshalled to a front end, which
// reads it as {"type":"stream_error","message":…,"code":…}.
type WireError struct {
	// Type is always [EventError].
	Type goodall.EventType `json:"type"`
	// Message is what went wrong, in prose, for a log or a toast.
	Message string `json:"message,omitzero"`
	// Code is the classification to switch on.
	Code WireErrorCode `json:"code,omitzero"`
}

// WriteSSE writes the events to w as a Server-Sent Events stream: one frame
// per event, named by the event's type, carrying the event's own JSON, and
// terminated by a blank line. It is what an HTTP handler wires a run stream or
// a [Service.Subscribe] stream to.
//
// A stream that ends in an error becomes one final frame named "stream_error",
// carrying a [WireError], and WriteSSE returns nil: the failure was delivered,
// which is the writer's whole job. The error it does return is a failure to
// write — the client's connection has gone — and the stream is unwound on the
// way out, so nothing is left running behind it.
//
// Each frame is flushed when w can be flushed (an [net/http.ResponseWriter]
// can), because a stream buffered until the response ends is not a stream.
// The writer sets no headers and sends no id or catch-up marker; the handler
// owns the response, and nothing in the service marks where a subscription's
// backlog ends.
func WriteSSE(w io.Writer, events goodall.Stream) error {
	return writeStream(w, writeSSEFrame, events)
}

// WriteNDJSON writes the events to w as newline-delimited JSON: one event
// object per line, in the same shape SSE carries in its data field. It is the
// format for a consumer that would rather read lines than frames — a CLI, a
// log, a `fetch` that streams the body itself.
//
// It ends, flushes and fails on exactly the same terms as [WriteSSE]: a stream
// error becomes one final {"type":"stream_error",…} line and a nil return, and only a
// failure to write is reported.
func WriteNDJSON(w io.Writer, events goodall.Stream) error {
	return writeStream(w, writeNDJSONFrame, events)
}

// frameWriter is one of the two wire formats: it writes the named, already
// encoded value as one frame. Keeping the format behind a function is what
// lets the two writers above differ in one word, so the ending, the error
// frame and the flushing are written once and cannot drift apart.
type frameWriter func(w io.Writer, name string, data []byte) error

// writeStream is the body of both writers: every event as a frame, a stream
// error as the one error frame that ends the stream, and nothing returned for
// a failure the client was told about.
func writeStream(w io.Writer, format frameWriter, events goodall.Stream) error {
	for ev, err := range events {
		if err != nil {
			return writeFrame(w, format, EventError, errorFrame(err))
		}
		if wErr := writeFrame(w, format, ev.Type(), ev); wErr != nil {
			return wErr
		}
	}
	return nil
}

// writeFrame encodes one value, writes it in the given format and flushes.
// An encoding failure is a bug in the event family rather than something the
// client can be told about, so it is returned rather than framed.
func writeFrame(w io.Writer, format frameWriter, name goodall.EventType, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("chat: encoding a %s frame: %w", name, err)
	}
	if err := format(w, name.String(), data); err != nil {
		return fmt.Errorf("chat: writing a %s frame: %w", name, err)
	}
	flush(w)
	return nil
}

// writeSSEFrame writes one Server-Sent Event.
//
// The data is split on newlines and written one "data:" line per segment, as
// the spec requires. goodall's event JSON is always a single line, so the loop
// runs once — the split is there because a payload that gained a newline would
// otherwise be silently truncated at it by every conforming parser.
func writeSSEFrame(w io.Writer, name string, data []byte) error {
	var buf bytes.Buffer
	buf.Grow(len(data) + len(name) + 16)
	buf.WriteString("event: ")
	buf.WriteString(name)
	buf.WriteByte('\n')
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		buf.WriteString("data: ")
		buf.Write(line)
		buf.WriteByte('\n')
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// writeNDJSONFrame writes one JSON object and the newline that delimits it. The
// name is already in the object's "type" member, so it is not repeated.
func writeNDJSONFrame(w io.Writer, _ string, data []byte) error {
	var buf bytes.Buffer
	buf.Grow(len(data) + 1)
	buf.Write(data)
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// errorFrame classifies a stream error for the front end.
func errorFrame(err error) WireError {
	code := WireErrorStream
	if errors.Is(err, ErrSubscriberOverflow) {
		code = WireErrorSubscriberOverflow
	}
	return WireError{Type: EventError, Message: err.Error(), Code: code}
}

// flusher is what an [net/http.ResponseWriter] satisfies. It is declared here
// rather than imported so that the chat layer does not pull net/http into
// every consumer's binary for one method; the shape is http.Flusher's exactly,
// and a Flush that returns an error — bufio.Writer's — deliberately does not
// match, since the writers above have nowhere to report one.
type flusher interface {
	Flush()
}

// flush pushes a frame to the client when the writer can be pushed.
func flush(w io.Writer) {
	if f, ok := w.(flusher); ok {
		f.Flush()
	}
}
