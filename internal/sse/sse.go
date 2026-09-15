// Package sse implements a WHATWG-conformant Server-Sent Events framer,
// shared by the Anthropic and OpenRouter provider packages so the line
// splitting, comment skipping and field parsing rules are written and
// tested exactly once.
//
// https://html.spec.whatwg.org/multipage/server-sent-events.html#parsing-an-event-stream
package sse

import (
	"bufio"
	"bytes"
	"io"
	"iter"
	"strconv"
	"strings"
	"time"
)

const (
	// initialLineBuffer matches bufio.Scanner's own default buffer size, so
	// an ordinary stream of small events costs no extra allocation; larger
	// lines grow the buffer from here.
	initialLineBuffer = 4096
	// maxLineBuffer bounds a single line well above real payloads (base64
	// images, large tool-call JSON deltas) while still refusing a stream
	// that never terminates a line, rather than growing without limit.
	maxLineBuffer = 64 << 20 // 64 MiB
)

// bom is the UTF-8 encoding of U+FEFF, stripped when it opens the stream.
var bom = []byte{0xEF, 0xBB, 0xBF}

var (
	colonSep    = []byte(":")
	spacePrefix = []byte(" ")
)

// Event is one dispatched Server-Sent Event.
type Event struct {
	// Type is the event's type: "message" when the stream set none.
	Type string
	// Data is every data line the event carried, joined with "\n", with
	// the one trailing newline the join adds removed.
	Data string
	// ID is the id this event's own fields set, or empty if none did.
	//
	// The spec's "last event id" buffer survives a dispatch that sets no
	// id and gets attached to it anyway; this parser does not carry an id
	// forward; every Event reflects only its own fields. Retry follows the
	// same rule for the same reason: an Event is self-contained rather
	// than depending on an earlier one in the stream.
	ID string
	// Retry is the reconnection time this event's own retry field set, or
	// zero if it set none.
	Retry time.Duration
}

// Events parses r as a Server-Sent Events stream and yields one Event per
// dispatch, per the WHATWG parsing algorithm:
// https://html.spec.whatwg.org/multipage/server-sent-events.html#parsing-an-event-stream
//
// It is a pull iterator that does all its work on the caller's goroutine:
// nothing runs and nothing is buffered between iterations, so a consumer
// that stops ranging simply stops calling Read on r — there is no goroutine
// to leak. The caller still owns closing r.
//
// On a read error, Events yields (Event{}, err) exactly once and then
// stops. A stream that ends mid-event — EOF with data already buffered but
// no terminating blank line — is discarded per spec rather than yielded as
// a partial event; callers detect a missing terminal event themselves.
//
// Events makes one deliberate deviation from the spec, carried over from a
// Swift predecessor's experience with real servers: when a server sends the
// next event's "event:" field without a blank line separating it from the
// previous event's data, the pending event is dispatched immediately, as if
// the blank line had been there.
func Events(r io.Reader) iter.Seq2[Event, error] {
	return eventsWithLimit(r, maxLineBuffer)
}

// eventsWithLimit is Events with an overridable line-length cap, so a test
// can prove the "yield an error, never stop silently" behavior for an
// oversized line without allocating a real 64 MiB line.
func eventsWithLimit(r io.Reader, maxLine int) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		br := bufio.NewReader(r)
		if peeked, err := br.Peek(len(bom)); err == nil && bytes.Equal(peeked, bom) {
			_, _ = br.Discard(len(bom))
		}

		initial := min(maxLine, initialLineBuffer)
		scanner := bufio.NewScanner(br)
		scanner.Buffer(make([]byte, initial), maxLine)
		scanner.Split(splitSSELines)

		var p parser
		for scanner.Scan() {
			if ev, ok := p.process(scanner.Bytes()); ok {
				if !yield(ev, nil) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			yield(Event{}, err)
		}
	}
}

// parser holds one pending event's buffers between dispatches.
type parser struct {
	eventType string
	data      strings.Builder
	id        string
	retry     time.Duration
}

// process applies one already-line-terminated-and-stripped line and reports
// the event it dispatched, if processing this line caused a dispatch.
func (p *parser) process(line []byte) (Event, bool) {
	if len(line) == 0 {
		return p.dispatch()
	}
	if line[0] == ':' {
		return Event{}, false
	}

	field, value, _ := bytes.Cut(line, colonSep)
	value = bytes.TrimPrefix(value, spacePrefix)

	switch string(field) {
	case "event":
		// Deviation from spec: a server that starts the next event without
		// a blank line still gets the pending one dispatched first.
		ev, dispatched := Event{}, false
		if p.data.Len() > 0 {
			ev, dispatched = p.dispatch()
		}
		p.eventType = string(value)
		return ev, dispatched
	case "data":
		p.data.Write(value)
		p.data.WriteByte('\n')
	case "id":
		if !bytes.ContainsRune(value, 0) {
			p.id = string(value)
		}
	case "retry":
		if ms, ok := parseRetryMillis(value); ok {
			p.retry = ms
		}
	}
	return Event{}, false
}

// dispatch implements the spec's "dispatch the event" step. Per spec, a
// dispatch with an empty data buffer still resets the buffers and yields
// nothing.
func (p *parser) dispatch() (Event, bool) {
	defer p.reset()
	if p.data.Len() == 0 {
		return Event{}, false
	}
	eventType := p.eventType
	if eventType == "" {
		eventType = "message"
	}
	return Event{
		Type:  eventType,
		Data:  strings.TrimSuffix(p.data.String(), "\n"),
		ID:    p.id,
		Retry: p.retry,
	}, true
}

// reset clears every buffer between events, including id and retry: this
// parser's decided deviation from the spec is that nothing carries forward.
func (p *parser) reset() {
	p.eventType = ""
	p.data.Reset()
	p.id = ""
	p.retry = 0
}

// parseRetryMillis parses a retry field's value per spec: only a non-empty,
// all-ASCII-digit string counts; anything else is ignored rather than
// erroring, leaving the previous retry value (if any) in place.
func parseRetryMillis(value []byte) (time.Duration, bool) {
	if len(value) == 0 {
		return 0, false
	}
	for _, b := range value {
		if b < '0' || b > '9' {
			return 0, false
		}
	}
	ms, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(ms) * time.Millisecond, true
}

// splitSSELines is bufio.ScanLines extended to also terminate a line at a
// lone CR: the WHATWG event-stream spec ends a line at CR, LF or CRLF, but
// ScanLines only recognizes LF (trimming an immediately preceding CR). A CR
// as the very last byte of the buffer is ambiguous — the matching LF may
// simply not have arrived yet in this read — so that case asks the Scanner
// for more data unless this is the final read.
func splitSSELines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		switch b {
		case '\n':
			return i + 1, data[:i], nil
		case '\r':
			if i+1 < len(data) {
				if data[i+1] == '\n' {
					return i + 2, data[:i], nil
				}
				return i + 1, data[:i], nil
			}
			if atEOF {
				return i + 1, data[:i], nil
			}
			// The CRLF pair may straddle a read boundary: request more
			// data rather than guessing that this trailing CR is alone.
			return 0, nil, nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
