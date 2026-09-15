package goodall

import (
	"context"
	"log/slog"
)

// Agent is the loop: a provider, a model, a system prompt and a set of tools,
// run until the model has finished answering or the budget says stop. It is
// the product of this library, and everything else in the package is either
// what it sends, what it receives or what it emits.
//
// An Agent holds no per-run state (invariant 12), so one value serves every
// goroutine in a process and two runs never see each other. Configure it once
// and call Run as often as you like:
//
//	agent := &goodall.Agent{Provider: p, Model: "claude-opus-5", Tools: tools}
//	for ev, err := range agent.Run(ctx, conv, goodall.Text{Text: "hello"}) {
//	    …
//	}
//
// The zero value is not usable: a run needs at least a Provider and a Model,
// and says so through its terminal event rather than by panicking.
type Agent struct {
	// Provider is the model API the run talks to.
	Provider Provider
	// Model is the provider's model identifier, verbatim.
	Model string
	// System is the standing instruction, sent as one text block on every
	// turn. An empty System sends none.
	System string
	// Tools is what the model may call, in the order they are sent. The
	// order is part of the cached prefix, so it stays the same for the
	// life of a run; two tools sharing a name end the run before it
	// starts, since the loop could not tell which one the model meant.
	Tools []Tool
	// Thinking is how hard the model should think and how much of that
	// thinking comes back.
	Thinking ThinkingConfig
	// Cache decides where prompt-cache breakpoints are placed.
	Cache CachePolicy
	// MaxTokens caps the tokens generated in one turn. Zero leaves the
	// choice to the provider, which substitutes its own named default
	// where its API requires the parameter — anthropic.DefaultMaxTokens.
	MaxTokens int
	// Budget bounds the whole run: turns, tokens and wall clock. The zero
	// Budget still limits turns, to DefaultMaxTurns.
	Budget Budget
	// Hooks are the run's control points: shaping a turn before it is
	// sent, reading an answer before it is committed, and allowing,
	// refusing, changing or deferring each tool call. The zero Hooks is
	// no hooks at all.
	Hooks Hooks
	// Logger records turn boundaries, tool calls and how the run ended. A
	// nil Logger logs nothing, which is the default.
	Logger *slog.Logger
}

// Run answers the input, calling tools as the model asks for them, and yields
// everything that happens on the way: every provider event verbatim, plus the
// loop's own turn and tool-call events, ending in exactly one Done or Stopped.
//
// When input is given it is appended to conv as one user message. When it is
// empty the run continues from conv as it stands, which is how a caller who
// appended tool results itself carries on; a run that paused for approval is
// continued with [Agent.Resume], which assembles the results for you. conv is
// a value and is never modified; the conversation the run built is on the
// terminal event's Result.
//
// The stream never yields a non-nil error. Every ending — a finished answer,
// a spent budget, a canceled context, a provider failure, an agent that was
// never configured — arrives as a terminal event carrying the conversation so
// far and the usage so far (invariant 10), so a consumer has one shape to
// handle and a failed run still hands back everything that arrived. Use
// [Stream.CollectResult] to read that one value without ranging.
//
// Canceling ctx stops the run: the request is aborted, tools already running
// are waited for and keep their real results, the partial assistant message
// is appended with Partial set, and the terminal event says Canceled. A
// consumer that breaks out of the stream early gets the same unwinding
// without the terminal event, and leaves nothing running behind it.
func (a *Agent) Run(ctx context.Context, conv Conversation, input ...Block) Stream {
	return func(yield func(Event, error) bool) {
		r := &run{agent: a, yield: yield, alive: true, conv: conv}
		r.execute(ctx, input)
	}
}

// log writes to the agent's logger when it has one, which is what makes the
// Logger field optional without every call site testing it.
func (a *Agent) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if a.Logger == nil {
		return
	}
	a.Logger.Log(ctx, level, msg, args...)
}
