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
// Subscribers are independent. Cancelling ctx detaches this one and does
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
