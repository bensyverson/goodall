package chat_test

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/internal/sse"
)

// mixedEvents is one of everything a front end meets on a run stream: the
// loop's own events around the provider's, a tool call with its input, and a
// terminal event carrying a result.
func mixedEvents() []goodall.Event {
	conv := goodall.Conversation{}.Append(
		goodall.UserMessage(goodall.Text{Text: "hi"}),
		goodall.AssistantMessage(goodall.Text{Text: "Hello, world"}),
	)
	use := goodall.ToolUse{ID: "toolu_1", Name: "echo", Input: jsontext.Value(`{"text":"hi"}`)}
	return []goodall.Event{
		goodall.TurnStart{Turn: 1},
		goodall.MessageStart{ID: "msg_01", Model: "claude-opus-5", Usage: goodall.Usage{Input: 12}},
		goodall.BlockStart{Index: 0, Block: goodall.Text{}},
		goodall.TextDelta{Index: 0, Text: "Hello,\nworld"},
		goodall.BlockStop{Index: 0},
		goodall.BlockStart{Index: 1, Block: goodall.ToolUse{ID: "toolu_1", Name: "echo"}},
		goodall.ToolInputDelta{Index: 1, PartialJSON: `{"text":"hi"}`},
		goodall.BlockStop{Index: 1},
		goodall.MessageDelta{StopReason: goodall.StopToolUse, Usage: goodall.Usage{Output: 7}},
		goodall.MessageStop{},
		goodall.ToolCallStart{ToolUse: use},
		goodall.ToolCallEnd{ToolUse: use, Result: goodall.TextResult("echo: hi")},
		goodall.TurnEnd{Turn: 1, Response: goodall.Response{
			ID:         "msg_01",
			Model:      "claude-opus-5",
			Message:    goodall.AssistantMessage(goodall.Text{Text: "Hello, world"}),
			StopReason: goodall.StopToolUse,
		}},
		goodall.Done{Result: goodall.Result{Conversation: conv, StopReason: goodall.StopEndTurn}},
	}
}

// streamOf yields the events and then ends, as a finished run stream does.
func streamOf(events ...goodall.Event) goodall.Stream {
	return func(yield func(goodall.Event, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// failingStream yields the events and then fails, as a dropped subscription
// does.
func failingStream(err error, events ...goodall.Event) goodall.Stream {
	return func(yield func(goodall.Event, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
		yield(nil, err)
	}
}

// TestWriteSSEFramesEachEventUnderItsOwnName is the wire format itself: the
// event's type names the frame, its own JSON is the data, and a blank line
// ends it.
func TestWriteSSEFramesEachEventUnderItsOwnName(t *testing.T) {
	var buf bytes.Buffer
	if err := chat.WriteSSE(&buf, streamOf(goodall.TextDelta{Text: "hi"}, goodall.MessageStop{})); err != nil {
		t.Fatalf("WriteSSE: %v", err)
	}
	const want = "event: text_delta\ndata: {\"type\":\"text_delta\",\"text\":\"hi\"}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	if buf.String() != want {
		t.Errorf("WriteSSE wrote\n%q\nwant\n%q", buf.String(), want)
	}
}

// TestWriteSSERoundTripsThroughTheSSEParser is the criterion: what the writer
// produces parses back, through the framer both providers use and through
// goodall's own event decoder, into the events it was given.
func TestWriteSSERoundTripsThroughTheSSEParser(t *testing.T) {
	events := mixedEvents()
	var buf bytes.Buffer
	if err := chat.WriteSSE(&buf, streamOf(events...)); err != nil {
		t.Fatalf("WriteSSE: %v", err)
	}
	got := decodeSSE(t, buf.Bytes())
	if !reflect.DeepEqual(got, events) {
		t.Errorf("the stream did not survive the round trip:\ngot  %#v\nwant %#v", got, events)
	}
}

// TestWriteSSEFromASubscription runs the same round trip over a real run: the
// service's subscription is written to the wire and read back as itself.
func TestWriteSSEFromASubscription(t *testing.T) {
	tool, _, release := gateTool(t)
	agent, _ := agentFor(gatedScript("all done"), tool)
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "hi")
	// Attaching before the gate is released is what makes the tail
	// deterministic: the subscription is live by the time WriteSSE reads.
	sub := subscribe(t, svc, thread.ID)
	sub.until(goodall.EventToolCallStart)

	var seen []goodall.Event
	var buf bytes.Buffer
	go release()
	if err := chat.WriteSSE(&buf, tee(sub.stream(), &seen)); err != nil {
		t.Fatalf("WriteSSE: %v", err)
	}
	if len(seen) < 5 {
		t.Fatalf("the subscription carried %d events after the tool call, want the second turn", len(seen))
	}
	if _, ok := terminal(t, seen).(goodall.Done); !ok {
		t.Errorf("the written stream did not end in Done: %v", types(seen))
	}
	got := decodeSSE(t, buf.Bytes())
	if !reflect.DeepEqual(got, seen) {
		t.Errorf("the subscription did not survive the round trip:\ngot  %#v\nwant %#v", got, seen)
	}
}

// TestWriteSSEEndsADroppedSubscriptionWithAnErrorFrame is the failure path: a
// stream error becomes one frame the client can render, and the writer
// reports success because the error reached the client.
func TestWriteSSEEndsADroppedSubscriptionWithAnErrorFrame(t *testing.T) {
	var buf bytes.Buffer
	stream := failingStream(chat.ErrSubscriberOverflow, goodall.TextDelta{Text: "half"})
	if err := chat.WriteSSE(&buf, stream); err != nil {
		t.Fatalf("WriteSSE returned %v, want nil: the error reached the client", err)
	}
	frames := parseSSE(t, buf.Bytes())
	if len(frames) != 2 {
		t.Fatalf("WriteSSE wrote %d frames, want the delta and the error:\n%s", len(frames), buf.String())
	}
	last := frames[1]
	if last.Type != "stream_error" {
		t.Errorf("the failure frame is named %q, want %q", last.Type, "stream_error")
	}
	want := `{"type":"stream_error","message":"` + chat.ErrSubscriberOverflow.Error() + `","code":"subscriber_overflow"}`
	if last.Data != want {
		t.Errorf("the failure frame carries\n\t%s\nwant\n\t%s", last.Data, want)
	}
}

// TestWireWritersCodeAnUnexpectedFailureGenerically keeps a failure nobody
// anticipated distinguishable from the one the service documents.
func TestWireWritersCodeAnUnexpectedFailureGenerically(t *testing.T) {
	var buf bytes.Buffer
	if err := chat.WriteNDJSON(&buf, failingStream(errors.New("boom"))); err != nil {
		t.Fatalf("WriteNDJSON returned %v, want nil", err)
	}
	var frame struct {
		Type    string             `json:"type"`
		Message string             `json:"message"`
		Code    chat.WireErrorCode `json:"code"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &frame); err != nil {
		t.Fatalf("decoding the error frame %q: %v", buf.String(), err)
	}
	if frame.Type != "stream_error" || frame.Message != "boom" || frame.Code != chat.WireErrorStream {
		t.Errorf("the error frame is %+v, want a generic stream error", frame)
	}
}

// TestWriteNDJSONWritesOneObjectPerLine is the other wire format: the same
// event JSON, one per line, decoding back to the events written.
func TestWriteNDJSONWritesOneObjectPerLine(t *testing.T) {
	events := mixedEvents()
	var buf bytes.Buffer
	if err := chat.WriteNDJSON(&buf, streamOf(events...)); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != len(events) {
		t.Fatalf("WriteNDJSON wrote %d lines for %d events:\n%s", len(lines), len(events), buf.String())
	}
	got := make([]goodall.Event, 0, len(lines))
	for i, line := range lines {
		ev, err := goodall.UnmarshalEvent([]byte(line))
		if err != nil {
			t.Fatalf("decoding line %d, %q: %v", i, line, err)
		}
		got = append(got, ev)
	}
	if !reflect.DeepEqual(got, events) {
		t.Errorf("the stream did not survive the round trip:\ngot  %#v\nwant %#v", got, events)
	}
}

// TestWireWritersFlushEveryFrame is what makes a stream a stream: a front end
// sees each event as it happens rather than when the response buffer fills.
func TestWireWritersFlushEveryFrame(t *testing.T) {
	for _, c := range []struct {
		name  string
		write func(*flushRecorder, goodall.Stream) error
	}{
		{"sse", func(w *flushRecorder, s goodall.Stream) error { return chat.WriteSSE(w, s) }},
		{"ndjson", func(w *flushRecorder, s goodall.Stream) error { return chat.WriteNDJSON(w, s) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := &flushRecorder{}
			stream := failingStream(chat.ErrSubscriberOverflow,
				goodall.TextDelta{Text: "a"}, goodall.TextDelta{Text: "b"})
			if err := c.write(w, stream); err != nil {
				t.Fatalf("writing: %v", err)
			}
			if w.flushes != 3 {
				t.Errorf("the writer flushed %d times for two events and an error frame", w.flushes)
			}
		})
	}
}

// TestWireWritersReportAWriteFailure is the one error a writer does return:
// the client's connection is gone, which the handler must know about.
func TestWireWritersReportAWriteFailure(t *testing.T) {
	broken := errors.New("connection reset")
	for _, c := range []struct {
		name  string
		write func(*failingWriter, goodall.Stream) error
	}{
		{"sse", func(w *failingWriter, s goodall.Stream) error { return chat.WriteSSE(w, s) }},
		{"ndjson", func(w *failingWriter, s goodall.Stream) error { return chat.WriteNDJSON(w, s) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			var unwound bool
			stream := goodall.Stream(func(yield func(goodall.Event, error) bool) {
				defer func() { unwound = true }()
				for range 5 {
					if !yield(goodall.TextDelta{Text: "a"}, nil) {
						return
					}
				}
			})
			if err := c.write(&failingWriter{err: broken}, stream); !errors.Is(err, broken) {
				t.Errorf("writing to a broken connection gave %v, want the write error", err)
			}
			if !unwound {
				t.Error("the writer left the stream running after the connection broke")
			}
		})
	}
}

// tee records every event that passes through the stream, so a test can
// compare what was written with what was read back.
func tee(events goodall.Stream, seen *[]goodall.Event) goodall.Stream {
	return func(yield func(goodall.Event, error) bool) {
		for ev, err := range events {
			if err == nil {
				*seen = append(*seen, ev)
			}
			if !yield(ev, err) {
				return
			}
		}
	}
}

// stream reads the rest of the subscription as a goodall.Stream, which is how
// a test hands a half-read subscription to a wire writer.
func (s *subscription) stream() goodall.Stream {
	return func(yield func(goodall.Event, error) bool) {
		for {
			ev, err, ok := s.next()
			if !ok {
				return
			}
			if !yield(ev, err) {
				return
			}
		}
	}
}

// parseSSE reads the writer's output back through the framer the provider
// packages use.
func parseSSE(t *testing.T, out []byte) []sse.Event {
	t.Helper()
	var frames []sse.Event
	for ev, err := range sse.Events(bytes.NewReader(out)) {
		if err != nil {
			t.Fatalf("parsing the SSE stream: %v", err)
		}
		frames = append(frames, ev)
	}
	return frames
}

// decodeSSE parses the writer's output and decodes each frame back into the
// event it was written from, checking the frame's name against it.
func decodeSSE(t *testing.T, out []byte) []goodall.Event {
	t.Helper()
	var events []goodall.Event
	for _, frame := range parseSSE(t, out) {
		ev, err := goodall.UnmarshalEvent([]byte(frame.Data))
		if err != nil {
			t.Fatalf("decoding the %q frame %q: %v", frame.Type, frame.Data, err)
		}
		if frame.Type != ev.Type().String() {
			t.Errorf("the frame is named %q and carries a %s event", frame.Type, ev.Type())
		}
		events = append(events, ev)
	}
	return events
}

// flushRecorder is an io.Writer that is also an http.Flusher, which is how a
// test asserts that every frame reaches the client on its own.
type flushRecorder struct {
	bytes.Buffer
	flushes int
}

// Flush counts a flush.
func (f *flushRecorder) Flush() { f.flushes++ }

// failingWriter is a connection that has already gone away.
type failingWriter struct{ err error }

// Write always fails.
func (w *failingWriter) Write(p []byte) (int, error) { return 0, w.err }
