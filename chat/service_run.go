package chat

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"uuid"

	"github.com/bensyverson/goodall"
)

// activeRun is one run in flight: the events it has produced, so a subscriber
// arriving late can catch up, and the subscribers following it live.
//
// The buffered events are the run's alone and are released with it. A run that
// has ended is removed from the service's table, so a client that attaches
// after the end reads the thread instead — the thread is the record, and
// keeping a finished run's events would be a cache with no eviction.
type activeRun struct {
	id       string
	threadID string
	cancel   context.CancelFunc
	buffer   int

	mu     sync.Mutex
	events []goodall.Event
	subs   map[*subscriber]struct{}
	ended  bool
}

// subscriber is one attached client: a bounded channel and the fact of having
// overflowed it, which is what the reader reports when the channel closes.
type subscriber struct {
	ch chan goodall.Event
	// overflow is written by the run under the run's mutex and read by the
	// subscriber under the same mutex, which is what makes it safe to
	// distinguish a dropped subscription from a finished one.
	overflow bool
}

// newActiveRun is a registered, not-yet-started run.
func newActiveRun(threadID string, cancel context.CancelFunc, buffer int) *activeRun {
	return &activeRun{
		id:       uuid.NewV7().String(),
		threadID: threadID,
		cancel:   cancel,
		buffer:   buffer,
		subs:     make(map[*subscriber]struct{}),
	}
}

// emit records an event and fans it out. A subscriber whose buffer is full is
// dropped rather than waited for: the run must never be paced by a client,
// because a phone on a train would otherwise stall the model.
func (r *activeRun) emit(ev goodall.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	for sub := range r.subs {
		select {
		case sub.ch <- ev:
		default:
			sub.overflow = true
			delete(r.subs, sub)
			close(sub.ch)
		}
	}
}

// attach registers sub and hands back the events so far. The two happen under
// one lock, so the catch-up and the live tail meet exactly: no event is
// replayed twice and none is missed. A run that has already ended registers
// nothing and reports false, and the caller replays the backlog and stops.
func (r *activeRun) attach(sub *subscriber) ([]goodall.Event, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	backlog := slices.Clone(r.events)
	if r.ended {
		return backlog, false
	}
	r.subs[sub] = struct{}{}
	return backlog, true
}

// detach removes a subscriber that left, which is what makes cancelling a
// subscription free of consequence for the run.
func (r *activeRun) detach(sub *subscriber) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, sub)
}

// overflowed reports whether the subscriber was dropped for falling behind,
// rather than ending because the run did.
func (r *activeRun) overflowed(sub *subscriber) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sub.overflow
}

// end closes every subscription. It runs after the thread has been persisted,
// so a client that reads its subscription to the end and then reads the
// thread sees the answer it just watched arrive.
func (r *activeRun) end() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ended = true
	for sub := range r.subs {
		delete(r.subs, sub)
		close(sub.ch)
	}
}

// drive is the run's own goroutine: read the stream to its terminal event,
// fan every event out, persist what the run produced, then release the run.
func (s *Service) drive(ctx context.Context, run *activeRun, thread *Thread, start func(context.Context) goodall.Stream) {
	defer s.wg.Done()
	// Cancelling on the way out closes the provider's request when the
	// stream ended for any other reason, and releases the context.
	defer run.cancel()

	s.log(ctx, slog.LevelDebug, "chat: run started", "run", run.id, "thread", run.threadID)

	var result *goodall.Result
	for ev, err := range start(ctx) {
		if err != nil {
			// Agent.Run and Agent.Resume never yield an error
			// (invariant 10). A provider layer that broke that
			// contract must not take the service down with it.
			s.log(ctx, slog.LevelError, "chat: the run stream yielded an error, which it must never do",
				"run", run.id, "thread", run.threadID, "err", err)
			break
		}
		run.emit(ev)
		switch e := ev.(type) {
		case goodall.Done:
			done := e.Result
			result = &done
		case goodall.Stopped:
			stopped := e.Result
			result = &stopped
		}
	}

	if result != nil {
		s.persist(run, thread, result)
	}
	s.release(run)
}

// release frees the thread for the next run and closes every subscription. It
// happens after persistence, so a client that is refused a Send with
// ErrThreadBusy is refused only while there is really something in flight.
func (s *Service) release(run *activeRun) {
	s.mu.Lock()
	if s.runs[run.threadID] == run {
		delete(s.runs, run.threadID)
	}
	s.mu.Unlock()
	run.end()
}

// persist writes what the run produced: the conversation it built, and its
// usage and cost added to the thread's running totals.
//
// It writes on a context that cannot be cancelled, because the run's own
// context is already cancelled by the time a stopped or shut-down run gets
// here, and a cancelled write would lose the answer the loop kept for exactly
// this reason. A version conflict — somebody else wrote the thread while the
// run was in flight — is retried once against the thread as it now stands;
// past that the service logs and gives up rather than writing in a loop, and
// the answer is in the subscribers' streams but not in the store.
func (s *Service) persist(run *activeRun, thread *Thread, result *goodall.Result) {
	ctx := context.WithoutCancel(s.ctx)
	apply := func(t *Thread) {
		t.Conversation = result.Conversation
		t.Usage = t.Usage.Add(result.Usage)
		t.Cost = t.Cost.Add(result.Cost)
	}

	apply(thread)
	err := s.store.Put(ctx, thread)
	if errors.Is(err, ErrVersionConflict) {
		s.log(ctx, slog.LevelWarn, "chat: the thread changed while the run was in flight; re-reading",
			"run", run.id, "thread", run.threadID)
		fresh, readErr := s.store.Get(ctx, thread.ID)
		if readErr != nil {
			s.log(ctx, slog.LevelError, "chat: re-reading the thread failed, so the run was not persisted",
				"run", run.id, "thread", run.threadID, "err", readErr)
			return
		}
		apply(fresh)
		err = s.store.Put(ctx, fresh)
	}
	if err != nil {
		s.log(ctx, slog.LevelError, "chat: persisting the thread failed",
			"run", run.id, "thread", run.threadID, "err", err)
		return
	}
	s.log(ctx, slog.LevelDebug, "chat: run persisted",
		"run", run.id, "thread", run.threadID, "stop_reason", string(result.StopReason))
}
