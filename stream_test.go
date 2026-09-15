package goodall

import (
	json "encoding/json/v2"
	"errors"
	"reflect"
	"testing"
)

// streamOf yields the given events and nothing else.
func streamOf(events ...Event) Stream {
	return func(yield func(Event, error) bool) {
		for _, e := range events {
			if !yield(e, nil) {
				return
			}
		}
	}
}

// TestStreamCollect drives the scripted provider stream to the response a
// caller would have got from a blocking call: one accumulator serves both
// paths, so there is one result type by construction.
func TestStreamCollect(t *testing.T) {
	got, err := streamOf(scriptedEvents()...).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := scriptedResponse()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Collect() =\n\t%#v\nwant\n\t%#v", got, want)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != scriptedResponseJSON {
		t.Errorf("Marshal =\n\t%s\nwant\n\t%s", encoded, scriptedResponseJSON)
	}
}

// TestStreamEarlyBreakRunsProducerCleanup is the reason the stream is an
// iterator and not a channel: a consumer that stops reading unwinds the
// producer on its own goroutine, so the HTTP body is closed rather than
// leaked.
func TestStreamEarlyBreakRunsProducerCleanup(t *testing.T) {
	cleanups := 0
	s := Stream(func(yield func(Event, error) bool) {
		defer func() { cleanups++ }()
		for _, e := range scriptedEvents() {
			if !yield(e, nil) {
				return
			}
		}
	})

	var seen int
	for range s {
		seen++
		break
	}
	if seen != 1 {
		t.Fatalf("read %d events, want 1", seen)
	}
	if cleanups != 1 {
		t.Errorf("the producer's cleanup ran %d times, want 1", cleanups)
	}
}

// TestStreamCollectCleansUpAfterAnError checks the same unwinding on the path
// Collect takes when a stream fails partway.
func TestStreamCollectCleansUpAfterAnError(t *testing.T) {
	cleanups := 0
	boom := errors.New("connection reset")
	s := Stream(func(yield func(Event, error) bool) {
		defer func() { cleanups++ }()
		for _, e := range scriptedEvents()[:8] {
			if !yield(e, nil) {
				return
			}
		}
		if !yield(nil, boom) {
			return
		}
		t.Error("Collect kept reading after a yielded error")
	})

	got, err := s.Collect()
	if !errors.Is(err, boom) {
		t.Fatalf("Collect error = %v, want %v", err, boom)
	}
	if cleanups != 1 {
		t.Errorf("the producer's cleanup ran %d times, want 1", cleanups)
	}
	if got == nil {
		t.Fatal("Collect returned no response; a canceled stream must hand back what arrived")
	}
	if !got.Message.Partial {
		t.Error("Message.Partial = false on a stream that failed")
	}
	if got.Message.Text() != "Let me check " {
		t.Errorf("Text() = %q, want the text that had arrived", got.Message.Text())
	}
	if got.ID != "msg_01" {
		t.Errorf("ID = %q, want msg_01", got.ID)
	}
}

// TestStreamCollectWithoutMessageStop: a stream that simply stops is not a
// finished message, and saying so is what tells a caller to treat the text as
// cut off rather than complete.
func TestStreamCollectWithoutMessageStop(t *testing.T) {
	events := scriptedEvents()
	got, err := streamOf(events[:len(events)-1]...).Collect()
	if err == nil {
		t.Fatal("Collect succeeded on a stream with no message_stop")
	}
	if _, ok := errors.AsType[*ProtocolError](err); !ok {
		t.Errorf("Collect error is %T (%v), want *ProtocolError", err, err)
	}
	if got == nil || !got.Message.Partial {
		t.Errorf("Collect returned %#v, want a partial response", got)
	}
}

// TestStreamCollectRejectsABrokenStream: an accumulator error surfaces from
// Collect with the partial message, rather than a plausible wrong one.
func TestStreamCollectRejectsABrokenStream(t *testing.T) {
	got, err := streamOf(
		MessageStart{ID: "msg_01"},
		TextDelta{Index: 0, Text: "hi"},
	).Collect()
	if _, ok := errors.AsType[*ProtocolError](err); !ok {
		t.Fatalf("Collect error is %T (%v), want *ProtocolError", err, err)
	}
	if got == nil || got.ID != "msg_01" {
		t.Errorf("Collect returned %#v, want the partial response", got)
	}
}

// TestStreamCollectResult reads a run's result off the terminal event, which
// is where the conversation and the totals live.
func TestStreamCollectResult(t *testing.T) {
	want := Result{
		Response:     scriptedResponse(),
		Conversation: sampleConversation(),
		Usage:        Usage{Input: 50, Output: 260},
		Cost:         costOf("0.0246"),
		StopReason:   StopEndTurn,
	}
	got, err := streamOf(
		TurnStart{Turn: 1},
		MessageStart{ID: "msg_01"},
		MessageStop{},
		TurnEnd{Turn: 1},
		Done{Result: want},
	).CollectResult()
	if err != nil {
		t.Fatalf("CollectResult: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("CollectResult() =\n\t%#v\nwant\n\t%#v", *got, want)
	}
}

// TestStreamCollectResultFromStopped: an early exit is still a result, so a
// canceled or budgeted-out run hands back the conversation rather than
// nothing (invariant 10).
func TestStreamCollectResultFromStopped(t *testing.T) {
	want := Result{Conversation: sampleConversation(), Pending: []ToolUse{{ID: "toolu_9", Name: "send_email"}}}
	got, err := streamOf(
		TurnStart{Turn: 1},
		Stopped{Cause: StopCauseDeferred, Result: want},
	).CollectResult()
	if err != nil {
		t.Fatalf("CollectResult: %v", err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("CollectResult() =\n\t%#v\nwant\n\t%#v", *got, want)
	}
}

// TestStreamCollectResultWithoutATerminalEvent: invariant 10 says every exit
// path ends in a terminal event, so a run stream that just stops is a bug
// worth naming.
func TestStreamCollectResultWithoutATerminalEvent(t *testing.T) {
	got, err := streamOf(TurnStart{Turn: 1}, MessageStop{}).CollectResult()
	if err == nil {
		t.Fatalf("CollectResult succeeded and gave %#v, want an error", got)
	}
	if _, ok := errors.AsType[*ProtocolError](err); !ok {
		t.Errorf("CollectResult error is %T (%v), want *ProtocolError", err, err)
	}
}

// TestStreamCollectResultPropagatesAnError: a run stream that fails reports
// the failure rather than an absent terminal event.
func TestStreamCollectResultPropagatesAnError(t *testing.T) {
	boom := errors.New("connection reset")
	_, err := Stream(func(yield func(Event, error) bool) {
		yield(TurnStart{Turn: 1}, nil)
		yield(nil, boom)
	}).CollectResult()
	if !errors.Is(err, boom) {
		t.Errorf("CollectResult error = %v, want %v", err, boom)
	}
}
