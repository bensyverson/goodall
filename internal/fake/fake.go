// Package fake is a scripted [goodall.Provider] for tests. A run is a list of
// turns, one per model call, and each turn is a list of events the provider
// yields: no network, no timing and no SSE framing, so a test of the agent
// loop asserts on the loop rather than on a provider.
//
// It also records every request it received, which is how a test checks the
// messages the loop sent and that the prefix stayed append-only (invariant 8).
//
// It lives under internal so it is not part of goodall's public API, and the
// packages that test against it — the loop and the chat layer — import it from
// their external test packages.
package fake

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"sync"

	"github.com/bensyverson/goodall"
)

const (
	// MessageID is the response id every scripted turn reports.
	MessageID = "msg_fake"
	// Model is the model name every scripted turn reports, which is the
	// model that answered rather than the one the request asked for.
	Model = "fake-model"
)

// Turn is one scripted model call: the events the provider yields, and
// optionally how the stream misbehaves. The zero Turn yields nothing at all,
// which the loop sees as a stream that ended before message_stop.
type Turn struct {
	// Events are yielded in order, each as an event with a nil error.
	Events []goodall.Event
	// Err, when set, is yielded after the events and ends the stream. It
	// is what a mid-stream provider failure looks like.
	Err error
	// ErrAfter caps how many of Events are yielded before Err. Zero means
	// all of them, so a turn that just fails at the end needs only Err.
	ErrAfter int
	// BlockUntilCanceled parks after the events until the context is
	// canceled, then yields the context's error. It is how a stream that
	// stalls mid-message is scripted.
	BlockUntilCanceled bool
}

// Answer scripts a complete turn: a message carrying the blocks, in order,
// then the stop reason. Text, thinking and tool-use blocks are delivered the
// way a provider delivers them — opened empty, filled by deltas, then closed —
// so the accumulator does the same work it does on a real stream.
func Answer(reason goodall.StopReason, blocks ...goodall.Block) Turn {
	events := []goodall.Event{goodall.MessageStart{ID: MessageID, Model: Model}}
	events = append(events, blockEvents(blocks)...)
	events = append(events,
		goodall.MessageDelta{StopReason: reason},
		goodall.MessageStop{},
	)
	return Turn{Events: events}
}

// Stalled scripts a turn that delivers the blocks complete and then hangs
// until the context is canceled: the message never reaches message_delta or
// message_stop. It is how a run is canceled or timed out mid-stream with
// complete tool calls already in the partial message.
func Stalled(blocks ...goodall.Block) Turn {
	events := []goodall.Event{goodall.MessageStart{ID: MessageID, Model: Model}}
	events = append(events, blockEvents(blocks)...)
	return Turn{Events: events, BlockUntilCanceled: true}
}

// Broken scripts a turn that delivers the blocks complete and then fails with
// err, which is what a connection reset or a mid-stream error frame looks
// like to the loop.
func Broken(err error, blocks ...goodall.Block) Turn {
	events := []goodall.Event{goodall.MessageStart{ID: MessageID, Model: Model}}
	events = append(events, blockEvents(blocks)...)
	return Turn{Events: events, Err: err}
}

// Using returns the turn with usage reported on its message_delta, replacing
// the first one it finds. A turn that has no message_delta — a stalled or
// broken one — gains one carrying the usage, since a provider reports what it
// spent before it fails.
func (t Turn) Using(u goodall.Usage) Turn {
	events := make([]goodall.Event, len(t.Events))
	copy(events, t.Events)
	for i, ev := range events {
		if d, ok := ev.(goodall.MessageDelta); ok {
			d.Usage = u
			events[i] = d
			t.Events = events
			return t
		}
	}
	t.Events = append(events, goodall.MessageDelta{Usage: u})
	return t
}

// Costing returns the turn with a cost reported on its message_delta, on the
// same terms as [Turn.Using].
func (t Turn) Costing(c goodall.Cost) Turn {
	events := make([]goodall.Event, len(t.Events))
	copy(events, t.Events)
	for i, ev := range events {
		if d, ok := ev.(goodall.MessageDelta); ok {
			d.Cost = c
			events[i] = d
			t.Events = events
			return t
		}
	}
	t.Events = append(events, goodall.MessageDelta{Cost: c})
	return t
}

// Use is a tool call with its input written as JSON text, which is how a test
// reads more clearly than a jsontext.Value literal.
//
// The input must be a complete JSON value even when the turn is scripting a
// cut-off answer: the accumulator refuses to finish a tool call whose input is
// not complete, so a literally truncated input never reaches the stop table.
func Use(id, name, input string) goodall.ToolUse {
	return goodall.ToolUse{ID: id, Name: name, Input: jsontext.Value(input)}
}

// blockEvents is the provider-side delivery of a finished block list.
func blockEvents(blocks []goodall.Block) []goodall.Event {
	var out []goodall.Event
	for i, b := range blocks {
		switch v := b.(type) {
		case goodall.Text:
			text := v.Text
			v.Text = ""
			out = append(out,
				goodall.BlockStart{Index: i, Block: v},
				goodall.TextDelta{Index: i, Text: text},
				goodall.BlockStop{Index: i},
			)
		case goodall.Thinking:
			text, sig := v.Text, v.Signature
			v.Text, v.Signature = "", ""
			out = append(out,
				goodall.BlockStart{Index: i, Block: v},
				goodall.ThinkingDelta{Index: i, Text: text},
				goodall.SignatureDelta{Index: i, Signature: sig},
				goodall.BlockStop{Index: i},
			)
		case goodall.ToolUse:
			input := v.Input
			v.Input = nil
			out = append(out, goodall.BlockStart{Index: i, Block: v})
			if len(input) > 0 {
				out = append(out, goodall.ToolInputDelta{Index: i, PartialJSON: string(input)})
			}
			out = append(out, goodall.BlockStop{Index: i})
		default:
			out = append(out, goodall.BlockStart{Index: i, Block: b}, goodall.BlockStop{Index: i})
		}
	}
	return out
}

// Provider is a goodall.Provider that reads its answers from a script. It
// satisfies [goodall.Completer] too, by collecting its own stream, so a test
// of the blocking path needs no second fake.
//
// A Provider is safe for concurrent use: it locks around the script cursor and
// the recorded requests, so a test may run two agents against one script.
type Provider struct {
	// Script is the turns, one per model call, in the order they answer.
	// A call past the end of the script yields an error naming the
	// overrun, which is what a loop that looped too many times looks like.
	Script []Turn

	mu       sync.Mutex
	requests []*goodall.Request
	calls    int
	cleanups int
}

// Stream records the request and yields the next turn of the script.
func (p *Provider) Stream(ctx context.Context, req *goodall.Request) goodall.Stream {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	n := p.calls
	p.calls++
	var turn Turn
	scripted := n < len(p.Script)
	if scripted {
		turn = p.Script[n]
	}
	total := len(p.Script)
	p.mu.Unlock()

	return func(yield func(goodall.Event, error) bool) {
		defer func() {
			p.mu.Lock()
			p.cleanups++
			p.mu.Unlock()
		}()
		if !scripted {
			yield(nil, fmt.Errorf("fake: the script has %d turns and turn %d was requested", total, n+1))
			return
		}
		limit := len(turn.Events)
		if turn.Err != nil && turn.ErrAfter > 0 && turn.ErrAfter < limit {
			limit = turn.ErrAfter
		}
		for _, ev := range turn.Events[:limit] {
			if !yield(ev, nil) {
				return
			}
		}
		if turn.Err != nil {
			yield(nil, turn.Err)
			return
		}
		if turn.BlockUntilCanceled {
			<-ctx.Done()
			yield(nil, ctx.Err())
		}
	}
}

// Complete is the blocking path, which is the streaming path collected.
func (p *Provider) Complete(ctx context.Context, req *goodall.Request) (*goodall.Response, error) {
	return p.Stream(ctx, req).Collect()
}

// Requests is every request the provider received, in order. The requests
// themselves are read-only, so a test may inspect them freely.
func (p *Provider) Requests() []*goodall.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*goodall.Request, len(p.requests))
	copy(out, p.requests)
	return out
}

// Calls is how many times Stream was called.
func (p *Provider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// Cleanups is how many of those streams ran to their end or were unwound by a
// consumer that broke out early. A test that breaks out of a run stream checks
// that this reached Calls, which is how a leaked provider stream is caught.
func (p *Provider) Cleanups() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cleanups
}
