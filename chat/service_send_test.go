package chat_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// gatedStore is a MemoryStore whose Put parks until the test lets it go, so a
// test can hold the service between the run's last event and its persistence
// and see which the client meets first.
type gatedStore struct {
	*chat.MemoryStore
	entered chan struct{} // closed when the first gated Put begins
	release chan struct{} // closed by the test to let the Put through
	once    sync.Once
	gating  atomic.Bool
}

func newGatedStore() *gatedStore {
	return &gatedStore{
		MemoryStore: chat.NewMemoryStore(),
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
}

// Put parks while gating is set; the create and the test's own writes go
// straight through.
func (g *gatedStore) Put(ctx context.Context, thread *chat.Thread) error {
	if g.gating.Load() {
		g.once.Do(func() { close(g.entered) })
		<-g.release
	}
	return g.MemoryStore.Put(ctx, thread)
}

// TestTheTerminalEventArrivesAfterTheThreadIsPersistedAndFreed is the CLI's
// shape: a client that stops reading the moment it sees Done and sends the
// next line at once. The thread must already be persisted and free by the
// time the terminal event is delivered, or the next Send races into
// ErrThreadBusy and a Get right after Done reads the thread as it was.
func TestTheTerminalEventArrivesAfterTheThreadIsPersistedAndFreed(t *testing.T) {
	agent, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "one"}),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "two"}),
	})
	store := newGatedStore()
	svc := chat.NewService(agent, store)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = svc.Shutdown(ctx)
	})
	thread := newThread(t, svc)
	store.gating.Store(true)

	run := send(t, svc, thread.ID, "first")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range run.Events {
			if ev.Type() == goodall.EventDone {
				return
			}
		}
	}()

	// The run reaches persistence and parks there. Done must not have been
	// delivered yet: the client is still ranging.
	select {
	case <-store.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the run never reached the store")
	}
	select {
	case <-done:
		t.Fatal("Done was delivered before the thread was persisted")
	case <-time.After(50 * time.Millisecond):
	}

	close(store.release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Done never arrived after the store let the write through")
	}

	// By the time Done is in hand the thread is persisted and free.
	stored, err := svc.Get(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Version != 2 {
		t.Errorf("after Done the thread is at version %d, want 2", stored.Version)
	}
	if _, err := svc.Send(t.Context(), thread.ID, goodall.Text{Text: "second"}); err != nil {
		t.Fatalf("Send right after Done: %v", err)
	}
	final := waitForVersion(t, svc, thread.ID, 3)
	last, _ := final.Conversation.Last()
	if last.Text() != "two" {
		t.Errorf("the second answer is %q, want %q", last.Text(), "two")
	}
}
