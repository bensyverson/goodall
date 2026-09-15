package chat_test

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/internal/fake"
)

// agentFor is an agent over a scripted provider, with the given tools.
func agentFor(script []fake.Turn, tools ...goodall.Tool) (*goodall.Agent, *fake.Provider) {
	p := &fake.Provider{Script: script}
	return &goodall.Agent{Provider: p, Model: "test-model", Tools: tools}, p
}

// serviceFor is a service over an agent and a fresh memory store. Every
// service is shut down when the test ends, so a leaked run fails the test
// rather than outliving it.
func serviceFor(t *testing.T, agent *goodall.Agent, opts ...chat.ServiceOption) (*chat.Service, *chat.MemoryStore) {
	t.Helper()
	store := chat.NewMemoryStore()
	svc := chat.NewService(agent, store, opts...)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := svc.Shutdown(ctx); err != nil {
			t.Errorf("shutting the service down at the end of the test: %v", err)
		}
	})
	return svc, store
}

// newThread is a created thread, which must succeed.
func newThread(t *testing.T, svc *chat.Service) *chat.Thread {
	t.Helper()
	thread, err := svc.Create(t.Context())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return thread
}

// send is a Send of one line of text, which must succeed.
func send(t *testing.T, svc *chat.Service, threadID, text string) string {
	t.Helper()
	runID, err := svc.Send(t.Context(), threadID, goodall.Text{Text: text})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if runID == "" {
		t.Error("Send returned an empty run id")
	}
	return runID
}

// waitForVersion polls the store until the thread reaches version v, which is
// how a test waits for a run the service owns: the run is on its own
// goroutine and nothing in the public API blocks until it has persisted.
func waitForVersion(t *testing.T, svc *chat.Service, threadID string, v int) *chat.Thread {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		thread, err := svc.Get(context.Background(), threadID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if thread.Version >= v {
			return thread
		}
		if time.Now().After(deadline) {
			t.Fatalf("the thread is still at version %d after ten seconds, want %d", thread.Version, v)
		}
		time.Sleep(time.Millisecond)
	}
}

// costOf is a reported cost in dollars.
func costOf(t *testing.T, amount string) goodall.Cost {
	t.Helper()
	d, err := goodall.ParseDecimal(amount)
	if err != nil {
		t.Fatal(err)
	}
	return goodall.Cost{Amount: d, Currency: "USD", Reported: true}
}

// gateTool is a tool that parks until the returned func is called. It is how
// these tests hold a run open at a known point without a clock: started is
// closed when the run reaches the tool, and release lets it go.
func gateTool(t *testing.T) (tool goodall.Tool, started <-chan struct{}, release func()) {
	t.Helper()
	gate := make(chan struct{})
	reached := make(chan struct{})
	var open, arrive sync.Once
	tool, err := goodall.NewTool("gate", "Wait until the test lets go.", func(ctx context.Context, in struct {
		Note string `json:"note" desc:"ignored"`
	}) (goodall.ToolResult, error) {
		arrive.Do(func() { close(reached) })
		select {
		case <-gate:
			return goodall.TextResult("released"), nil
		case <-ctx.Done():
			return goodall.ToolResult{}, ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	release = func() { open.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return tool, reached, release
}

// gatedScript is the script the tool-gated tests share: a first turn that
// calls the gate tool and a second that answers.
func gatedScript(answer string) []fake.Turn {
	return []fake.Turn{
		fake.Answer(goodall.StopToolUse, goodall.Text{Text: "checking"}, fake.Use("tu_1", "gate", `{"note":"x"}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: answer}),
	}
}

// subscription is a pull-based reader over a service subscription, so a test
// can attach, read to an exact point, and leave the rest arriving.
type subscription struct {
	t    *testing.T
	next func() (goodall.Event, error, bool)
	stop func()
	seen []goodall.Event
}

// subscribe attaches to a thread's run for the length of the test.
func subscribe(t *testing.T, svc *chat.Service, threadID string) *subscription {
	t.Helper()
	return subscribeWith(t, t.Context(), svc, threadID)
}

// subscribeWith attaches with a context the test controls, which is how the
// detaching test pulls the plug on one subscriber alone.
func subscribeWith(t *testing.T, ctx context.Context, svc *chat.Service, threadID string) *subscription {
	t.Helper()
	next, stop := iter.Pull2(iter.Seq2[goodall.Event, error](svc.Subscribe(ctx, threadID)))
	t.Cleanup(stop)
	return &subscription{t: t, next: next, stop: stop}
}

// until reads events until one of the given type arrives and returns
// everything read so far.
func (s *subscription) until(kind goodall.EventType) []goodall.Event {
	s.t.Helper()
	for {
		ev, err, ok := s.next()
		if err != nil {
			s.t.Fatalf("the subscription yielded an error while waiting for %s: %v", kind, err)
		}
		if !ok {
			s.t.Fatalf("the subscription ended before %s arrived", kind)
		}
		s.seen = append(s.seen, ev)
		if ev.Type() == kind {
			return s.seen
		}
	}
}

// take reads exactly n events and returns everything read so far.
func (s *subscription) take(n int) []goodall.Event {
	s.t.Helper()
	for range n {
		ev, err, ok := s.next()
		if err != nil {
			s.t.Fatalf("the subscription yielded an error: %v", err)
		}
		if !ok {
			s.t.Fatalf("the subscription ended after %d of %d events", len(s.seen), n)
		}
		s.seen = append(s.seen, ev)
	}
	return s.seen
}

// drain reads to the end and reports the error the stream ended with, if any.
func (s *subscription) drain() ([]goodall.Event, error) {
	s.t.Helper()
	for {
		ev, err, ok := s.next()
		if err != nil {
			return s.seen, err
		}
		if !ok {
			return s.seen, nil
		}
		s.seen = append(s.seen, ev)
	}
}

// terminal is the run's one terminal event, which every subscription that
// read to the end must have seen (invariant 10).
func terminal(t *testing.T, events []goodall.Event) goodall.Event {
	t.Helper()
	var found goodall.Event
	for _, ev := range events {
		switch ev.(type) {
		case goodall.Done, goodall.Stopped:
			if found != nil {
				t.Fatalf("the subscription carried two terminal events: %T and %T", found, ev)
			}
			found = ev
		}
	}
	if found == nil {
		t.Fatalf("the subscription carried no terminal event, in %d events", len(events))
	}
	return found
}

// lastMessage folds events into the message the most recent message_start
// opened, which is how a front end rebuilds an answer from a replay.
func lastMessage(t *testing.T, events []goodall.Event) goodall.Message {
	t.Helper()
	var acc goodall.Accumulator
	for _, ev := range events {
		if ev.Type() == goodall.EventMessageStart {
			acc = goodall.Accumulator{}
		}
		if err := acc.Apply(ev); err != nil {
			t.Fatalf("applying %s: %v", ev.Type(), err)
		}
	}
	return acc.Message()
}

// echoTool answers with the text it was given.
func echoTool(t *testing.T) goodall.Tool {
	t.Helper()
	tool, err := goodall.NewTool("echo", "Echo the text back to the model.", func(ctx context.Context, in struct {
		Text string `json:"text" desc:"the text to echo"`
	}) (goodall.ToolResult, error) {
		return goodall.TextResult("echo: " + in.Text), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}
