package chat_test

import (
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/internal/fake"
)

// TestSendReturnsAStreamAttachedBeforeTheRunStarts is the Send-then-Subscribe
// race, closed: the stream Send hands back was attached before the run began,
// so a caller that reads it after the run has already finished and persisted
// still sees every event, terminal event included. Against the fake provider
// the run finishes in microseconds, which is exactly the case a late
// Subscribe would have missed.
func TestSendReturnsAStreamAttachedBeforeTheRunStarts(t *testing.T) {
	agent, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "hello"}),
	})
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	run, err := svc.Send(t.Context(), thread.ID, goodall.Text{Text: "hi"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if run.ID == "" {
		t.Error("Send returned a run with no id")
	}

	// The run is over and the thread persisted before the stream is read.
	waitForVersion(t, svc, thread.ID, 2)

	var events []goodall.Event
	for ev, err := range run.Events {
		if err != nil {
			t.Fatalf("the run's stream yielded an error: %v", err)
		}
		events = append(events, ev)
	}
	if len(events) == 0 {
		t.Fatal("the run's stream yielded nothing, so it was attached after the run ended")
	}
	last := events[len(events)-1]
	if last.Type() != goodall.EventDone {
		t.Errorf("the run's stream ended with %s, want %s", last.Type(), goodall.EventDone)
	}
	var sawText bool
	for _, ev := range events {
		if d, ok := ev.(goodall.TextDelta); ok && d.Text == "hello" {
			sawText = true
		}
	}
	if !sawText {
		t.Error("the run's stream carried no text delta, so it did not start from the beginning")
	}
}

// TestResolveReturnsAStreamAttachedBeforeTheRunStarts is the same guarantee
// for the other way a run starts.
func TestResolveReturnsAStreamAttachedBeforeTheRunStarts(t *testing.T) {
	agent, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "resolved"}),
	})
	svc, store := serviceFor(t, agent)
	thread := newThread(t, svc)
	thread.Conversation = goodall.Conversation{}.Append(
		goodall.UserMessage(goodall.Text{Text: "please"}),
		goodall.AssistantMessage(fake.Use("tu_1", "gate", `{"note":"x"}`)),
	)
	if err := store.Put(t.Context(), thread); err != nil {
		t.Fatalf("Put: %v", err)
	}

	result := goodall.TextResult("approved")
	result.ToolUseID = "tu_1"
	run, err := svc.Resolve(t.Context(), thread.ID, result)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	waitForVersion(t, svc, thread.ID, thread.Version+1)

	var last goodall.Event
	for ev, err := range run.Events {
		if err != nil {
			t.Fatalf("the run's stream yielded an error: %v", err)
		}
		last = ev
	}
	if last == nil || last.Type() != goodall.EventDone {
		t.Fatalf("the run's stream ended with %v, want %s", last, goodall.EventDone)
	}
}

// TestLeavingTheSendStreamDoesNotStopTheRun keeps the ownership rule: the
// stream Send returns is a subscription like any other, so a caller that
// stops reading it detaches and the run finishes and persists regardless.
func TestLeavingTheSendStreamDoesNotStopTheRun(t *testing.T) {
	gate, started, release := gateTool(t)
	agent, _ := agentFor(gatedScript("done"), gate)
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	run, err := svc.Send(t.Context(), thread.ID, goodall.Text{Text: "hi"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	for ev := range run.Events {
		if ev.Type() == goodall.EventTurnStart {
			break
		}
	}
	<-started
	release()

	stored := waitForVersion(t, svc, thread.ID, 2)
	last, _ := stored.Conversation.Last()
	if last.Text() != "done" {
		t.Errorf("the stored answer is %q, want %q: leaving the stream disturbed the run", last.Text(), "done")
	}
}

// TestSendThenSubscribeStillWorksWhileTheRunIsInFlight is the other consumer
// shape, unchanged: an HTTP handler that starts a run and a second request
// that subscribes to it.
func TestSendThenSubscribeStillWorksWhileTheRunIsInFlight(t *testing.T) {
	gate, started, release := gateTool(t)
	agent, _ := agentFor(gatedScript("done"), gate)
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	run := send(t, svc, thread.ID, "hi")
	<-started
	sub := subscribe(t, svc, thread.ID)
	release()
	events, err := sub.drain()
	if err != nil {
		t.Fatalf("the subscription ended with %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Type() != goodall.EventDone {
		t.Fatal("the subscription did not see the run to its end")
	}
	_ = run
}

// TestRunEventsIsSingleUse documents that a run's stream is a subscription:
// once read to the end, a second range over it finds nothing more.
func TestRunEventsIsSingleUse(t *testing.T) {
	agent, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "hello"}),
	})
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	run := send(t, svc, thread.ID, "hi")
	first := 0
	for range run.Events {
		first++
	}
	second := 0
	for range run.Events {
		second++
	}
	if first == 0 {
		t.Fatal("the first read saw nothing")
	}
	if second != 0 {
		t.Errorf("the second read saw %d events, want none: the stream is single-use", second)
	}
	var _ *chat.Run = run
}
