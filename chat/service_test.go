package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/internal/fake"
)

// TestCreateStoresAnEmptyThread is the starting point of every chat: a thread
// with an id, no history and a version the caller can write against.
func TestCreateStoresAnEmptyThread(t *testing.T) {
	agent, _ := agentFor(nil)
	svc, _ := serviceFor(t, agent)

	thread := newThread(t, svc)
	if thread.ID == "" {
		t.Fatal("Create returned a thread with no id")
	}
	if thread.Version != 1 {
		t.Errorf("a created thread is at version %d, want 1", thread.Version)
	}
	if thread.Conversation.Len() != 0 {
		t.Errorf("a created thread has %d messages, want none", thread.Conversation.Len())
	}

	got, err := svc.Get(t.Context(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != thread.ID {
		t.Errorf("Get returned thread %q, want %q", got.ID, thread.ID)
	}
}

// TestSendPersistsWithNoSubscriber is the "runs are owned by the service"
// decision at its plainest: nobody watches, and the answer still lands in the
// store with its usage and its cost.
func TestSendPersistsWithNoSubscriber(t *testing.T) {
	usage := goodall.Usage{Input: 10, Output: 5}
	cost := costOf(t, "0.0025")
	agent, provider := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "hello"}).Using(usage).Costing(cost),
	})
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "hi")

	stored := waitForVersion(t, svc, thread.ID, 2)
	if stored.Conversation.Len() != 2 {
		t.Fatalf("the stored conversation has %d messages, want the question and the answer", stored.Conversation.Len())
	}
	last, _ := stored.Conversation.Last()
	if last.Role != goodall.RoleAssistant || last.Text() != "hello" {
		t.Errorf("the stored answer is a %s saying %q, want an assistant saying %q", last.Role, last.Text(), "hello")
	}
	if last.Partial {
		t.Error("the stored answer is marked partial, but the run finished")
	}
	if stored.Usage != usage {
		t.Errorf("the thread's usage is %+v, want %+v", stored.Usage, usage)
	}
	if stored.Cost != cost {
		t.Errorf("the thread's cost is %+v, want %+v", stored.Cost, cost)
	}
	if provider.Calls() != 1 {
		t.Errorf("the provider was called %d times, want 1", provider.Calls())
	}
	if provider.Cleanups() != provider.Calls() {
		t.Errorf("%d of %d provider streams were unwound; a run must leave none behind", provider.Cleanups(), provider.Calls())
	}
}

// TestASecondSendOnABusyThreadIsRefused is invariant 12's "a thread has one
// active run", and the busy flag clearing once the run has persisted.
func TestASecondSendOnABusyThreadIsRefused(t *testing.T) {
	agent, _ := agentFor([]fake.Turn{
		fake.Stalled(goodall.Text{Text: "thinking"}),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "second answer"}),
	})
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "hi")
	if _, err := svc.Send(t.Context(), thread.ID, goodall.Text{Text: "again"}); !errors.Is(err, chat.ErrThreadBusy) {
		t.Fatalf("the second Send = %v, want ErrThreadBusy", err)
	}

	// Let the first turn arrive before stopping it, so the run really is
	// mid-answer rather than canceled before it asked anything.
	subscribe(t, svc, thread.ID).until(goodall.EventBlockStop)
	if err := svc.Stop(thread.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitForVersion(t, svc, thread.ID, 2)

	// Once the run has ended and persisted, the thread takes work again.
	if _, err := svc.Send(t.Context(), thread.ID, goodall.Text{Text: "again"}); err != nil {
		t.Fatalf("the Send after the run ended = %v, want it accepted", err)
	}
	waitForVersion(t, svc, thread.ID, 3)
}

// TestSendOnAMissingThreadFails keeps the store's answer rather than
// inventing a thread.
func TestSendOnAMissingThreadFails(t *testing.T) {
	agent, _ := agentFor(nil)
	svc, _ := serviceFor(t, agent)

	if _, err := svc.Send(t.Context(), "no-such-thread", goodall.Text{Text: "hi"}); !errors.Is(err, chat.ErrThreadNotFound) {
		t.Fatalf("Send on a missing thread = %v, want ErrThreadNotFound", err)
	}
}

// TestStopOnAnIdleThread says plainly that there was nothing to stop, rather
// than pretending it stopped something.
func TestStopOnAnIdleThread(t *testing.T) {
	agent, _ := agentFor(nil)
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	if err := svc.Stop(thread.ID); !errors.Is(err, chat.ErrThreadIdle) {
		t.Errorf("Stop on an idle thread = %v, want ErrThreadIdle", err)
	}
	if err := svc.Stop("no-such-thread"); !errors.Is(err, chat.ErrThreadIdle) {
		t.Errorf("Stop on an unknown thread = %v, want ErrThreadIdle", err)
	}
}

// TestTotalsAccumulateAcrossSends is the per-thread ledger: a thread's usage
// and cost are the sum over every run it has ever carried.
func TestTotalsAccumulateAcrossSends(t *testing.T) {
	first := goodall.Usage{Input: 10, Output: 5}
	second := goodall.Usage{Input: 7, Output: 3, CacheRead: 100}
	agent, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "one"}).Using(first).Costing(costOf(t, "0.001")),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "two"}).Using(second).Costing(costOf(t, "0.002")),
	})
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "first question")
	waitForVersion(t, svc, thread.ID, 2)
	send(t, svc, thread.ID, "second question")
	stored := waitForVersion(t, svc, thread.ID, 3)

	if want := first.Add(second); stored.Usage != want {
		t.Errorf("the thread's usage is %+v, want %+v", stored.Usage, want)
	}
	if want := costOf(t, "0.003"); stored.Cost != want {
		t.Errorf("the thread's cost is %+v, want %+v", stored.Cost, want)
	}
	if stored.Conversation.Len() != 4 {
		t.Errorf("the stored conversation has %d messages, want two questions and two answers", stored.Conversation.Len())
	}
}

// TestResolveContinuesADeferredRun is the approval path end to end: a hook
// defers the turn, the run ends with the calls pending, and the caller's
// results carry it to Done.
func TestResolveContinuesADeferredRun(t *testing.T) {
	agent, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, goodall.Text{Text: "may I?"}, fake.Use("tu_1", "echo", `{"text":"hi"}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, echoTool(t))
	agent.Hooks = goodall.Hooks{
		BeforeToolCall: func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
			return goodall.Defer(), nil
		},
	}
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "please echo")
	deferred := waitForVersion(t, svc, thread.ID, 2)
	if deferred.Conversation.Len() != 2 {
		t.Fatalf("the deferred thread has %d messages, want the question and the model's request", deferred.Conversation.Len())
	}
	last, _ := deferred.Conversation.Last()
	if len(last.ToolUses()) != 1 {
		t.Fatalf("the deferred thread's last message asks for %d tool calls, want 1", len(last.ToolUses()))
	}

	result := goodall.TextResult("approved: hi")
	result.ToolUseID = "tu_1"
	if _, err := svc.Resolve(t.Context(), thread.ID, result); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	stored := waitForVersion(t, svc, thread.ID, 3)
	if stored.Conversation.Len() != 4 {
		t.Fatalf("the resolved thread has %d messages, want the question, the request, the results and the answer", stored.Conversation.Len())
	}
	answer, _ := stored.Conversation.Last()
	if answer.Text() != "done" {
		t.Errorf("the resolved answer is %q, want %q", answer.Text(), "done")
	}
	results := stored.Conversation.At(2)
	if results.Role != goodall.RoleUser {
		t.Errorf("the results message is a %s, want a user turn", results.Role)
	}
}

// TestResolveOnAThreadThatIsNotWaitingFails names why rather than starting a
// run that would only fail.
func TestResolveOnAThreadThatIsNotWaitingFails(t *testing.T) {
	agent, provider := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "hello"}),
	})
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	if _, err := svc.Resolve(t.Context(), thread.ID, goodall.TextResult("x")); !errors.Is(err, chat.ErrNotResolvable) {
		t.Fatalf("Resolve on an empty thread = %v, want ErrNotResolvable", err)
	}

	send(t, svc, thread.ID, "hi")
	waitForVersion(t, svc, thread.ID, 2)
	if _, err := svc.Resolve(t.Context(), thread.ID, goodall.TextResult("x")); !errors.Is(err, chat.ErrNotResolvable) {
		t.Fatalf("Resolve on a finished thread = %v, want ErrNotResolvable", err)
	}
	if provider.Calls() != 1 {
		t.Errorf("the provider was called %d times, want 1: a refused Resolve starts no run", provider.Calls())
	}
}

// TestShutdownDrains is what lets a server stop: every run is canceled, and
// Shutdown returns only once what they produced is in the store.
func TestShutdownDrains(t *testing.T) {
	agent, _ := agentFor([]fake.Turn{fake.Stalled(goodall.Text{Text: "half an answer"})})
	svc, _ := serviceFor(t, agent)
	thread := newThread(t, svc)

	send(t, svc, thread.ID, "hi")
	sub := subscribe(t, svc, thread.ID)
	sub.until(goodall.EventBlockStop)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	stored, err := svc.Get(context.Background(), thread.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Version != 2 {
		t.Errorf("the thread is at version %d after Shutdown, want 2: Shutdown returns once the run has persisted", stored.Version)
	}
	if _, err := svc.Send(context.Background(), thread.ID, goodall.Text{Text: "again"}); !errors.Is(err, chat.ErrServiceClosed) {
		t.Errorf("Send after Shutdown = %v, want ErrServiceClosed", err)
	}
	if _, err := svc.Resolve(context.Background(), thread.ID, goodall.TextResult("x")); !errors.Is(err, chat.ErrServiceClosed) {
		t.Errorf("Resolve after Shutdown = %v, want ErrServiceClosed", err)
	}
}
