package goodall

import (
	"context"
	"encoding/json/jsontext"
)

// Hooks are the control points of a run: four optional funcs the loop calls at
// the moments where a caller may want a say. They are control, not
// observation — a consumer that only wants to watch reads the event stream,
// which never changes what happens.
//
// Every hook is optional; a nil func is a no-op, so the zero Hooks is exactly
// the loop with no hooks at all. Every hook error ends the run with
// Stopped{Cause: StopCauseHook} carrying the error's message, after the loop
// has committed whatever the conversation needs to stay sendable: the run
// never continues past a hook that said no, and never leaves a tool_use
// without a tool_result (invariants 9 and 10).
//
// Hooks are called on the run's own goroutine, one at a time and in the order
// the loop does the work, so a hook needs no locking of its own to see a
// consistent picture of one run. An Agent holds no per-run state, though, so
// hooks on an Agent shared between goroutines are called from all of them.
type Hooks struct {
	// BeforeSend runs once per turn, before anything is sent. It sees the
	// request's parameters — model, system, tools, thinking, cache — and
	// the new turn the run is about to commit: the caller's input on the
	// first turn, the tool-results message on later ones, and nil when
	// the run continues from a conversation that already ends in a user
	// message.
	//
	// req.Messages is empty while the hook runs and the loop fills it in
	// afterwards from the conversation plus the new turn, so a hook can
	// shape what is about to be said and nothing that was already said:
	// history is append-only, because preserved thinking binds each
	// block to the prefix that produced it (invariant 1). Editing
	// newTurn's blocks is the supported way to shape a turn, and the
	// edit is what gets sent and what gets committed.
	BeforeSend func(ctx context.Context, req *Request, newTurn *Message) error
	// AfterReceive runs when a turn's stream has completed, before the
	// turn is committed and before its tool calls are considered. It sees
	// the whole response — the message, the stop reason and the usage —
	// because what a hook acts on is usually why the model stopped.
	//
	// The response is the loop's own, so a hook that writes through the
	// pointer shapes what is emitted, committed and acted on: redacting
	// the message here keeps the redacted text out of the history. An
	// error ends the run with the assistant turn committed and an error
	// result for each of its calls.
	AfterReceive func(ctx context.Context, resp *Response) error
	// BeforeToolCall runs for every tool call of a turn, in the order the
	// model made them and before any tool of that turn starts, and
	// answers with a Decision: [Allow], [Deny], [Modify] or [Defer].
	//
	// Asking about every call first is what makes approval possible: a
	// decision cannot be taken back once a tool has had its side effect,
	// so the loop collects the whole turn's decisions before it runs any
	// of them. An error ends the run with no tool executed.
	BeforeToolCall func(ctx context.Context, use ToolUse) (Decision, error)
	// AfterToolCall runs on each result as it arrives, before the result
	// is emitted and before it is appended. The result is the loop's own,
	// so a hook that writes through the pointer shapes what the consumer
	// sees and what the model is told — truncating a huge result, or
	// redacting what should not enter the history.
	//
	// It runs for every result the loop records for the turn, including
	// the error result a denied call produces, so every ToolCallEnd has
	// passed the hook. An error ends the run once the turn's other tools
	// have finished; the results that had already passed the hook are
	// kept and every other call gets an error result in their place.
	AfterToolCall func(ctx context.Context, use ToolUse, result *ToolResult) error
}

// decisionKind is what a [Decision] decided. It is a typed constant rather
// than a pair of booleans because the four outcomes are mutually exclusive,
// and its zero value is the one that behaves like no hook at all.
type decisionKind int

const (
	// decisionAllow runs the call as the model made it.
	decisionAllow decisionKind = iota
	// decisionDeny answers the call with an error result instead of
	// running it.
	decisionDeny
	// decisionModify runs the call with a replacement input.
	decisionModify
	// decisionDefer pauses the turn for approval.
	decisionDefer
)

// String names the kind, for logs.
func (k decisionKind) String() string {
	switch k {
	case decisionDeny:
		return "deny"
	case decisionModify:
		return "modify"
	case decisionDefer:
		return "defer"
	default:
		return "allow"
	}
}

// Decision is what a [Hooks.BeforeToolCall] hook decided about one tool call.
// Build one with [Allow], [Deny], [Modify] or [Defer]; the zero Decision
// allows, so a hook that has no opinion about a call can return one.
type Decision struct {
	kind   decisionKind
	reason string
	input  jsontext.Value
}

// Allow runs the call exactly as the model made it, which is what the loop
// does with no hook at all.
func Allow() Decision { return Decision{kind: decisionAllow} }

// Deny answers the call with an error result carrying the reason, without
// running the tool. The model reads the reason on its next turn and is
// expected to act on it, so it should say what was refused and what the model
// could do instead.
//
// A denied call was never about to run, so it is never announced with a
// ToolCallStart; its ToolCallEnd still reports the error result, which is
// what a UI draws.
func Deny(reason string) Decision { return Decision{kind: decisionDeny, reason: reason} }

// Modify runs the call with input in place of the model's own — sandboxing a
// path, filling in a caller's identifier, clamping a limit.
//
// The conversation keeps the model's original call untouched, because history
// is what the model actually said (invariant 1); the run's events carry the
// input that is really running, so a UI shows what happened.
func Modify(input jsontext.Value) Decision { return Decision{kind: decisionModify, input: input} }

// Defer pauses the run for approval: no tool of the turn runs, the assistant
// turn is committed on its own, and the run ends with
// Stopped{Cause: StopCauseDeferred} whose Result.Pending lists every tool call
// of the turn, in order. [Agent.Resume] carries on from there with the results
// the approver decided on.
//
// One deferred call holds the whole turn, and the allow, deny and modify
// decisions taken for its other calls are discarded: approval is a pause at a
// turn boundary, a person approves the turn they are shown, and a tool that
// had already run could not be un-run if they then said no.
func Defer() Decision { return Decision{kind: decisionDefer} }

// String names the decision, for logs.
func (d Decision) String() string { return d.kind.String() }

// beforeSend runs the BeforeSend hook, if there is one.
func (r *run) beforeSend(ctx context.Context, req *Request, newTurn *Message) error {
	if r.agent.Hooks.BeforeSend == nil {
		return nil
	}
	return r.agent.Hooks.BeforeSend(ctx, req, newTurn)
}

// afterReceive runs the AfterReceive hook, if there is one.
func (r *run) afterReceive(ctx context.Context, resp *Response) error {
	if r.agent.Hooks.AfterReceive == nil {
		return nil
	}
	return r.agent.Hooks.AfterReceive(ctx, resp)
}

// beforeToolCall runs the BeforeToolCall hook, if there is one. With no hook
// every call is allowed, which is the loop's behavior without hooks.
func (r *run) beforeToolCall(ctx context.Context, use ToolUse) (Decision, error) {
	if r.agent.Hooks.BeforeToolCall == nil {
		return Allow(), nil
	}
	return r.agent.Hooks.BeforeToolCall(ctx, use)
}

// afterToolCall runs the AfterToolCall hook, if there is one.
func (r *run) afterToolCall(ctx context.Context, use ToolUse, result *ToolResult) error {
	if r.agent.Hooks.AfterToolCall == nil {
		return nil
	}
	return r.agent.Hooks.AfterToolCall(ctx, use, result)
}
