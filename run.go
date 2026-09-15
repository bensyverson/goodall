package goodall

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
)

// errRunTimeout is the cause a run's own timeout cancels its context with, so
// the loop can tell its wall-clock budget from the caller cancelling.
var errRunTimeout = errors.New("goodall: the run's timeout elapsed")

// errConsumerLeft is the internal signal that the consumer stopped reading.
// It never reaches a caller: the run returns without a terminal event,
// because a consumer that broke out of the stream is not listening for one.
var errConsumerLeft = errors.New("goodall: the consumer stopped reading the run")

// run is one call to Agent.Run. Every piece of a run's state lives here, on a
// value that exists only for the length of the run, which is what lets one
// Agent serve any number of concurrent runs (invariant 12).
type run struct {
	agent *Agent
	yield func(Event, error) bool
	alive bool // false once the consumer has stopped reading

	ctx    context.Context    // the run's own context, for the logger
	cancel context.CancelFunc // ends the run's context, so a consumer who left stops the tools
	conv   Conversation
	tools  map[string]Tool
	usage  Usage
	cost   Cost
	last   *Response // the last completed turn, nil until one finishes
	turn   int       // how many turns have started
}

// execute is the loop itself: check the budget, send, forward every event,
// then act on the stop reason. It ends in exactly one terminal event unless
// the consumer stopped reading, in which case it ends in none.
func (r *run) execute(ctx context.Context, input []Block) {
	r.ctx = ctx
	if err := r.prepare(input); err != nil {
		r.fail(StopCauseError, KindInvalidRequest, err.Error())
		return
	}

	runCtx, cancel := r.withTimeout(ctx)
	// Cancelling on the way out is what stops the provider's request when
	// the consumer breaks out of the stream early; a tool still running at
	// that moment is cancelled by runTools before it waits.
	defer cancel()
	r.ctx, r.cancel = runCtx, cancel

	for {
		if limit := r.agent.Budget.Turns(); r.turn >= limit {
			r.stop(StopCauseTurnLimit, "the run used its budget of "+strconv.Itoa(limit)+" turns")
			return
		}
		if r.agent.Budget.OverTokens(r.usage) {
			r.stop(StopCauseTokenLimit, fmt.Sprintf("the run spent %d tokens, past its budget of %d",
				TokensSpent(r.usage), r.agent.Budget.MaxTokens))
			return
		}
		if runCtx.Err() != nil {
			cause, kind, message := r.diagnose(runCtx, runCtx.Err())
			r.fail(cause, kind, message)
			return
		}

		r.turn++
		if !r.emit(TurnStart{Turn: r.turn}) {
			return
		}
		r.agent.log(runCtx, slog.LevelDebug, "goodall: turn start", "turn", r.turn, "model", r.agent.Model)

		req := r.request()
		// HOOK INSERTION POINT (mi7b09): BeforeSend(ctx, req) runs here,
		// with the request built and nothing sent yet.
		resp, err := r.streamTurn(runCtx, req)
		if err != nil {
			if errors.Is(err, errConsumerLeft) {
				return
			}
			r.failTurn(runCtx, resp, err)
			return
		}
		// HOOK INSERTION POINT (mi7b09): AfterReceive(ctx, &resp.Message)
		// runs here, with the turn complete and nothing appended yet.

		r.last = resp
		r.usage = r.usage.Add(resp.Usage)
		r.cost = r.cost.Add(resp.Cost)
		if !r.emit(TurnEnd{Turn: r.turn, Response: *resp}) {
			return
		}

		outcome := classifyStop(resp.StopReason, len(resp.Message.ToolUses()))
		switch outcome.Action {
		case actionDone:
			r.commit(resp.Message, unrunReason(resp.StopReason))
			r.done()
			return
		case actionStop:
			r.commit(resp.Message, unrunReason(resp.StopReason))
			r.stop(outcome.Cause, outcome.Message)
			return
		case actionResend:
			r.conv = r.conv.Append(resp.Message)
		case actionRunTools:
			if !r.runTools(runCtx, resp.Message) {
				return
			}
		}
	}
}

// prepare validates the agent and appends the input. The two required fields
// and a tool set the loop cannot index are the only things that can be wrong
// before the first request, and all three are the caller's mistake, so they
// end the run with KindInvalidRequest rather than being sent to a provider.
func (r *run) prepare(input []Block) error {
	if r.agent.Provider == nil {
		return errors.New("goodall: the agent has no provider, so there is nothing to send the conversation to")
	}
	if r.agent.Model == "" {
		return errors.New("goodall: the agent has no model")
	}
	r.tools = make(map[string]Tool, len(r.agent.Tools))
	for i, tool := range r.agent.Tools {
		if tool == nil {
			return fmt.Errorf("goodall: tool %d is nil", i)
		}
		name := tool.Name()
		if _, duplicate := r.tools[name]; duplicate {
			return fmt.Errorf("goodall: two of the agent's tools are named %q, so the loop cannot tell which one the model called", name)
		}
		r.tools[name] = tool
	}
	if len(input) > 0 {
		r.conv = r.conv.Append(UserMessage(input...))
	}
	return nil
}

// withTimeout derives the run's context. The wall-clock budget carries its own
// cause so that a run which ran out of time is distinguishable from a caller
// who cancelled, which the two StopCauses then report separately.
func (r *run) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if d := r.agent.Budget.Timeout; d > 0 {
		return context.WithTimeoutCause(ctx, d, errRunTimeout)
	}
	return context.WithCancel(ctx)
}

// request builds one turn's request. It is rebuilt each turn from the same
// fields in the same order, so the prefix a provider caches is byte-stable
// between turns (invariant 8).
func (r *run) request() *Request {
	req := &Request{
		Model:     r.agent.Model,
		Messages:  r.conv.Messages(),
		Tools:     r.agent.Tools,
		MaxTokens: r.agent.MaxTokens,
		Thinking:  r.agent.Thinking,
		Cache:     r.agent.Cache,
	}
	if r.agent.System != "" {
		req.System = []Text{{Text: r.agent.System}}
	}
	return req
}

// streamTurn sends one request and forwards every event it produces to the
// consumer verbatim while folding it into an accumulator, so a consumer sees
// exactly what the provider sent and the loop acts on one authoritative
// reading of it.
//
// The response it returns is never nil: a turn that failed partway still
// carries what arrived, with Message.Partial set.
func (r *run) streamTurn(ctx context.Context, req *Request) (*Response, error) {
	var acc Accumulator
	var failure error
	for ev, err := range r.agent.Provider.Stream(ctx, req) {
		if err != nil {
			failure = err
			break
		}
		if !r.emit(ev) {
			return acc.Response(), errConsumerLeft
		}
		if err := acc.Apply(ev); err != nil {
			failure = err
			break
		}
	}
	if failure != nil {
		return acc.Response(), failure
	}
	if !acc.Done() {
		return acc.Response(), &ProtocolError{
			Event:  string(EventMessageStop),
			Reason: "the stream ended before message_stop, so the message is incomplete",
		}
	}
	return acc.Response(), nil
}

// commit appends the assistant turn and, for any tool call the loop will not
// run, one error result apiece — as a single unit, so no tool_use is ever
// left dangling and the conversation can be sent again as it stands
// (invariant 9).
func (r *run) commit(msg Message, why string) {
	if len(msg.Content) == 0 {
		// An empty assistant turn is not a turn; appending one would
		// leave a conversation no provider would accept.
		return
	}
	messages := []Message{msg}
	if results := unrunResults(msg, why); len(results) > 0 {
		messages = append(messages, UserMessage(results...))
	}
	r.conv = r.conv.Append(messages...)
}

// unrunResults is one error result for each tool call in the message.
func unrunResults(msg Message, why string) []Block {
	var out []Block
	for _, use := range msg.ToolUses() {
		result := ErrorResult(why)
		result.ToolUseID = use.ID
		out = append(out, result)
	}
	return out
}

// failTurn ends the run on a turn that did not finish: the partial assistant
// message is kept, its complete tool calls get error results, and the
// terminal event says whether the caller cancelled, the clock ran out or
// something broke.
func (r *run) failTurn(runCtx context.Context, resp *Response, err error) {
	cause, kind, message := r.diagnose(runCtx, err)
	if resp != nil {
		r.commit(resp.Message, unrunEnded)
	}
	r.fail(cause, kind, message)
}

// diagnose reads an ended turn: why it ended, how to classify it, and what to
// tell a person reading the terminal event.
//
// The context is asked first, because a provider whose stream simply stops on
// a cancelled request reports nothing useful — and a run that ends silently
// on cancellation was the predecessor's worst bug. A deadline on the caller's
// own context counts as a timeout too: the cause really was a clock, and the
// caller knows whose it was.
func (r *run) diagnose(runCtx context.Context, err error) (StopCause, ErrorKind, string) {
	if runCtx.Err() != nil {
		switch cause := context.Cause(runCtx); {
		case errors.Is(cause, errRunTimeout):
			return StopCauseTimeout, KindUnknown, "the run passed its timeout of " + r.agent.Budget.Timeout.String()
		case errors.Is(cause, context.DeadlineExceeded):
			return StopCauseTimeout, KindUnknown, "the run passed the deadline on the caller's context"
		default:
			return StopCauseCancelled, KindUnknown, "the run was cancelled"
		}
	}
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		return StopCauseError, apiErr.Kind, apiErr.Error()
	}
	return StopCauseError, KindUnknown, err.Error()
}

// result is what every terminal event carries.
func (r *run) result() Result {
	out := Result{
		Response:     r.last,
		Conversation: r.conv,
		Usage:        r.usage,
		Cost:         r.cost,
	}
	if r.last != nil {
		out.StopReason = r.last.StopReason
	}
	// HOOK INSERTION POINT (mi7b09): Pending is filled here from the tool
	// calls a hook deferred.
	return out
}

// emit hands one event to the consumer, and reports whether the consumer is
// still reading. Once it is not, nothing more is emitted: a run whose consumer
// left has nobody to tell.
func (r *run) emit(ev Event) bool {
	if !r.alive {
		return false
	}
	r.alive = r.yield(ev, nil)
	return r.alive
}

// done ends a run that reached a natural end.
func (r *run) done() {
	r.agent.log(r.ctx, slog.LevelInfo, "goodall: run done",
		"turns", r.turn, "tokens", TokensSpent(r.usage))
	r.emit(Done{Result: r.result()})
}

// stop ends a run that stopped early for a reason that is not a failure: a
// budget, a refusal, a cut-off turn, a stop reason goodall does not know.
func (r *run) stop(cause StopCause, message string) {
	r.agent.log(r.ctx, slog.LevelInfo, "goodall: run stopped",
		"cause", cause.String(), "message", message, "turns", r.turn, "tokens", TokensSpent(r.usage))
	r.emit(Stopped{Cause: cause, Message: message, Result: r.result()})
}

// fail ends a run that broke, was cancelled or ran out of time. The kind is
// the provider's classification when there was one, so a caller can decide
// whether trying again is worth anything.
func (r *run) fail(cause StopCause, kind ErrorKind, message string) {
	r.agent.log(r.ctx, slog.LevelInfo, "goodall: run failed",
		"cause", cause.String(), "kind", kind.String(), "message", message, "turns", r.turn)
	r.emit(Stopped{Cause: cause, Message: message, Kind: kind, Result: r.result()})
}
