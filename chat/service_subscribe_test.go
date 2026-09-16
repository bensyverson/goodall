package chat_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/internal/fake"
)

// TestSubscribeToAnIdleThreadEndsAtOnce is the documented answer for a client
// that attaches when nothing is running: no events, no error, read the thread.
func TestSubscribeToAnIdleThreadEndsAtOnce(t *testing.T) {
	agent, _ := agentFor(nil)
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	for _, id := range []string{thread.ID, "no-such-thread"} {
		count := 0
		for ev, err := range svc.Subscribe(t.Context(), id) {
			t.Errorf("an idle thread yielded %T, %v", ev, err)
			count++
		}
		if count != 0 {
			t.Errorf("subscribing to %q yielded %d events, want none", id, count)
		}
	}
}

// TestSubscribeSeesTheWholeRun is the everyday path: a client attaches while
// the run is in flight and reads it to its one terminal event.
func TestSubscribeSeesTheWholeRun(t *testing.T) {
	tool, _, release := gateTool(t)
	agent, _ := agentFor(gatedScript("all done"), tool)
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "hi")
	sub := subscribe(t, svc, thread.ID)
	sub.until(goodall.EventToolCallStart)
	release()

	events, err := sub.drain()
	if err != nil {
		t.Fatalf("the subscription ended with %v", err)
	}
	if _, ok := terminal(t, events).(goodall.Done); !ok {
		t.Errorf("the run ended in %T, want Done", terminal(t, events))
	}
	if got := lastMessage(t, events); got.Text() != "all done" {
		t.Errorf("the last message rebuilt from the stream is %q, want %q", got.Text(), "all done")
	}

	// Every kind of event the loop and the provider produce reached the
	// subscriber, in order.
	var kinds []goodall.EventType
	for _, ev := range events {
		kinds = append(kinds, ev.Type())
	}
	for _, want := range []goodall.EventType{
		goodall.EventTurnStart, goodall.EventMessageStart, goodall.EventBlockStart,
		goodall.EventTextDelta, goodall.EventBlockStop, goodall.EventMessageDelta,
		goodall.EventMessageStop, goodall.EventTurnEnd, goodall.EventToolCallStart,
		goodall.EventToolCallEnd, goodall.EventDone,
	} {
		if !hasType(kinds, want) {
			t.Errorf("the subscription carried no %s event; it carried %v", want, kinds)
		}
	}

	stored := waitForVersion(t, svc, thread.ID, 2)
	if stored.Conversation.Len() != 4 {
		t.Errorf("the stored conversation has %d messages, want the question, the request, the result and the answer", stored.Conversation.Len())
	}
}

// TestSubscribeMidRunReplaysThenGoesLive is the criterion: a client attaching
// while the model is mid-answer is handed the run so far — verbatim, so its
// accumulator rebuilds exactly the message the earlier subscriber built — and
// then the events that arrive after it attached.
func TestSubscribeMidRunReplaysThenGoesLive(t *testing.T) {
	tool, _, release := gateTool(t)
	agent, _ := agentFor(gatedScript("the rest of the answer"), tool)
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "hi")
	first := subscribe(t, svc, thread.ID)
	early := first.until(goodall.EventToolCallStart)
	earlyMessage := lastMessage(t, early)
	if earlyMessage.Text() != "checking" {
		t.Fatalf("the first subscriber rebuilt %q, want %q", earlyMessage.Text(), "checking")
	}

	// The run is parked inside the tool, so the catch-up is exactly what
	// the first subscriber has already seen.
	late := subscribe(t, svc, thread.ID)
	catchUp := late.take(len(early))
	if !reflect.DeepEqual(catchUp, early) {
		t.Fatalf("the catch-up is not the run so far:\ngot  %v\nwant %v", types(catchUp), types(early))
	}
	if got := lastMessage(t, catchUp); !reflect.DeepEqual(got, earlyMessage) {
		t.Errorf("the catch-up rebuilds %+v, want the message the first subscriber built, %+v", got, earlyMessage)
	}

	release()

	lateEvents, err := late.drain()
	if err != nil {
		t.Fatalf("the late subscription ended with %v", err)
	}
	firstEvents, err := first.drain()
	if err != nil {
		t.Fatalf("the first subscription ended with %v", err)
	}
	if !reflect.DeepEqual(lateEvents, firstEvents) {
		t.Errorf("the two subscribers saw different runs:\n%v\n%v", types(lateEvents), types(firstEvents))
	}
	if len(lateEvents) <= len(catchUp) {
		t.Fatal("the late subscriber saw nothing after its catch-up")
	}
	live := lateEvents[len(catchUp):]
	if !hasType(types(live), goodall.EventTextDelta) {
		t.Errorf("the live tail carried no text delta; it carried %v", types(live))
	}
	if got := lastMessage(t, lateEvents); got.Text() != "the rest of the answer" {
		t.Errorf("the late subscriber rebuilt %q, want %q", got.Text(), "the rest of the answer")
	}
}

// TestSubscribeMidRunCarriesTheQuestion is the criterion: a client that
// attaches after a run started and before it ended can render the question that
// prompted the answer. The thread is not written until the run ends, so the
// backlog is the only place the question can come from — and it survives the
// redaction a front end reads through, because a person's own words are not
// something the back end owns.
func TestSubscribeMidRunCarriesTheQuestion(t *testing.T) {
	const question = "what did the tool say?"
	tool, started, release := gateTool(t)
	agent, _ := agentFor(gatedScript("all done"), tool)
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, question)
	<-started

	// The run is parked inside the tool, so this subscriber gets the run so
	// far as its catch-up and nothing live.
	late := subscribe(t, svc, thread.ID)
	backlog := late.until(goodall.EventToolCallStart)

	committed, ok := eventOfType(t, backlog, goodall.EventTurnCommitted).(goodall.TurnCommitted)
	if !ok {
		t.Fatalf("the backlog's turn_committed is not a TurnCommitted")
	}
	if committed.Turn != 1 {
		t.Errorf("the committed turn is numbered %d, want the first", committed.Turn)
	}
	if committed.Message.Role != goodall.RoleUser {
		t.Errorf("the committed turn is from %q, want the user", committed.Message.Role)
	}
	if got := committed.Message.Text(); got != question {
		t.Errorf("the late subscriber was handed %q, want the question that started the run", got)
	}

	redacted := collect(t, chat.Redact(streamOf(backlog...), svc.ViewOptions()))
	through, ok := eventOfType(t, redacted, goodall.EventTurnCommitted).(goodall.TurnCommitted)
	if !ok {
		t.Fatalf("the redacted turn_committed is not a TurnCommitted")
	}
	if got := through.Message.Text(); got != question {
		t.Errorf("the redacted turn says %q, want the question a front end renders", got)
	}

	release()
	if _, err := late.drain(); err != nil {
		t.Fatalf("the late subscription ended with %v", err)
	}
	waitForVersion(t, svc, thread.ID, 2)
}

// TestStopMidRunKeepsThePartialAnswer is the stop path: the partial message
// survives, is marked partial, and is persisted so the thread stays sendable.
func TestStopMidRunKeepsThePartialAnswer(t *testing.T) {
	agent, _ := agentFor([]fake.Turn{fake.Stalled(goodall.Text{Text: "half an answer"})})
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "hi")
	sub := subscribe(t, svc, thread.ID)
	partial := sub.until(goodall.EventBlockStop)
	if got := lastMessage(t, partial); got.Text() != "half an answer" || !got.Partial {
		t.Fatalf("mid-run the message is %q (partial=%v), want %q and partial", got.Text(), got.Partial, "half an answer")
	}

	if err := svc.Stop(thread.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	events, err := sub.drain()
	if err != nil {
		t.Fatalf("the subscription ended with %v", err)
	}
	stopped, ok := terminal(t, events).(goodall.Stopped)
	if !ok {
		t.Fatalf("the run ended in %T, want Stopped", terminal(t, events))
	}
	if stopped.Cause != goodall.StopCauseCanceled {
		t.Errorf("the run stopped because %q (%s), want canceled", stopped.Cause, stopped.Message)
	}

	stored := waitForVersion(t, svc, thread.ID, 2)
	last, _ := stored.Conversation.Last()
	if !last.Partial {
		t.Errorf("the stored answer is not marked partial: %+v", last)
	}
	if last.Text() != "half an answer" {
		t.Errorf("the stored answer is %q, want %q", last.Text(), "half an answer")
	}
}

// TestSubscriberLeavingDoesNotStopTheRun is what "runs are owned by the
// service" buys: the client puts the phone in its pocket and the answer still
// lands.
func TestSubscriberLeavingDoesNotStopTheRun(t *testing.T) {
	tool, _, release := gateTool(t)
	agent, _ := agentFor(gatedScript("finished anyway"), tool)
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "hi")
	ctx, cancel := context.WithCancel(context.Background())
	sub := subscribeWith(t, ctx, svc, thread.ID)
	sub.until(goodall.EventToolCallStart)
	cancel()
	sub.stop()
	release()

	stored := waitForVersion(t, svc, thread.ID, 2)
	answer, _ := stored.Conversation.Last()
	if answer.Text() != "finished anyway" {
		t.Errorf("the stored answer is %q, want %q", answer.Text(), "finished anyway")
	}
}

// TestASlowSubscriberDoesNotStallTheRun is the overflow policy: a client that
// stops reading is dropped with an error of its own, and the run carries on
// and persists.
func TestASlowSubscriberDoesNotStallTheRun(t *testing.T) {
	tool, started, release := gateTool(t)
	agent, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, goodall.Text{Text: "checking"}, fake.Use("tu_1", "gate", `{"note":"x"}`)),
		fake.Answer(goodall.StopEndTurn,
			goodall.Text{Text: "one"}, goodall.Text{Text: "two"}, goodall.Text{Text: "three"},
			goodall.Text{Text: "four"}, goodall.Text{Text: "five"}),
	}, tool)
	svc, _ := serviceFor(t, agent, chat.WithSubscriberBuffer(1))
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "hi")
	<-started

	// The run is parked in the tool, so this subscriber attaches to a
	// catch-up and nothing live; it reads one event and then stops.
	slow := subscribe(t, svc, thread.ID)
	slow.take(1)

	// From here the slow subscriber reads nothing while the second turn
	// streams, which is far more than its one-event buffer.
	release()

	stored := waitForVersion(t, svc, thread.ID, 2)
	answer, _ := stored.Conversation.Last()
	if answer.Text() != "onetwothreefourfive" {
		t.Errorf("the stored answer is %q, want the whole second turn", answer.Text())
	}

	if _, err := slow.drain(); !errors.Is(err, chat.ErrSubscriberOverflow) {
		t.Errorf("the slow subscription ended with %v, want ErrSubscriberOverflow", err)
	}
}

// TestSubscribeAfterTheRunEnded documents the other end of the same policy: a
// run nobody is watching is gone once it has persisted, and the thread is the
// record.
func TestSubscribeAfterTheRunEnded(t *testing.T) {
	agent, _ := agentFor([]fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "hello"})})
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "hi")
	waitForVersion(t, svc, thread.ID, 2)

	for ev, err := range svc.Subscribe(t.Context(), thread.ID) {
		t.Errorf("subscribing after the run ended yielded %T, %v", ev, err)
	}
}

// types names an event slice's types, which is what a failure message needs.
func types(events []goodall.Event) []goodall.EventType {
	out := make([]goodall.EventType, len(events))
	for i, ev := range events {
		out[i] = ev.Type()
	}
	return out
}

// hasType reports whether kinds holds want.
func hasType(kinds []goodall.EventType, want goodall.EventType) bool {
	return slices.Contains(kinds, want)
}
