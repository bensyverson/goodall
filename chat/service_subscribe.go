package chat

import (
	"context"

	"github.com/bensyverson/goodall"
)

// Subscribe follows the thread's run: first the events it has already
// produced, verbatim and in order, then the events that arrive after the
// subscription attached, ending with the run's one terminal event.
//
// The catch-up is the buffered events themselves rather than a summary of
// them, so a client that folds the stream through a [goodall.Accumulator]
// rebuilds exactly the partial message an earlier subscriber is looking at —
// the same text, the same open block, the same Partial. A client attaching
// mid-answer therefore needs no second code path: it applies what arrives.
//
// A thread with no run in flight yields nothing and ends at once. That is not
// an error: the thread is the record, and a caller that finds an empty
// subscription reads the thread with [Service.Get]. The same is true of a
// thread the store does not hold.
//
// Subscribers are independent. Canceling ctx detaches this one and does
// nothing to the run or to anybody else's; so does breaking out of the range.
// A subscriber that stops reading for long enough to fill its buffer is
// dropped with [ErrSubscriberOverflow] rather than pacing the run — attaching
// again replays the run so far.
func (s *Service) Subscribe(ctx context.Context, threadID string) goodall.Stream {
	return func(yield func(goodall.Event, error) bool) {
		s.mu.Lock()
		run := s.runs[threadID]
		s.mu.Unlock()
		if run == nil {
			return
		}

		sub := &subscriber{ch: make(chan goodall.Event, run.buffer)}
		backlog, live := run.attach(sub)
		follow(ctx, run, sub, backlog, live)(yield)
	}
}

// Run is a run the service has started, handed back by [Service.Send] and
// [Service.Resolve]: its id, and its events from the very first one.
type Run struct {
	// ID identifies the run in the service's logs.
	ID string
	// Events is the run's stream, attached before the run began, so it
	// sees every event however quickly the run finishes. It is a
	// subscription: single-consumer, read once, and detached — not
	// stopped — by breaking out of the range; a second range over it
	// finds nothing more. It ends with the run's terminal event, or with
	// [ErrSubscriberOverflow] if the reader fell too far behind. The
	// terminal event is delivered only after the thread has been
	// persisted and freed, so a reader may stop at it and send again at
	// once. Leaving it unread is fine too, which is what an HTTP handler
	// that answers only the id does: the subscription is dropped when its
	// bounded buffer fills, and nothing leaks.
	Events goodall.Stream
}

// follow is the body of every subscription: the backlog the attach handed
// back, then the live tail until the run closes the channel or ctx ends.
// [Service.Subscribe] and [Service.Send] differ only in when they attached.
func follow(ctx context.Context, run *activeRun, sub *subscriber, backlog []goodall.Event, live bool) goodall.Stream {
	return func(yield func(goodall.Event, error) bool) {
		if live {
			defer run.detach(sub)
		}
		for _, ev := range backlog {
			if !yield(ev, nil) {
				return
			}
		}
		if !live {
			return
		}

		for {
			select {
			case ev, ok := <-sub.ch:
				if !ok {
					if run.overflowed(sub) {
						yield(nil, ErrSubscriberOverflow)
					}
					return
				}
				if !yield(ev, nil) {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}
}
