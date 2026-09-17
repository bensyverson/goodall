package goodall

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
)

// call is one tool call of a turn once a hook has ruled on it: the model's
// own tool_use, which is what the conversation keeps, and the call as it will
// actually run, which is what the events report.
type call struct {
	use    ToolUse    // the model's call, verbatim
	run    ToolUse    // the call as it runs, differing only when a hook modified the input
	denied bool       // true when a hook refused it, in which case it never starts
	result ToolResult // a denied call's error result, ready before anything runs
}

// runTools runs every call of one turn, concurrently, and appends the turn
// and its results to the conversation as one unit. It reports whether the run
// should continue, which is false when the consumer stopped reading and when
// a hook ended the run, in which case the terminal event has already gone out.
//
// Results are emitted as they arrive, in whatever order the tools finish, and
// appended in the order the model asked for them: a model reading its own
// transcript must see its calls answered in the order it made them.
//
// A tool that implements [Reporter] also reports events while it runs. The
// run's yield is not safe for concurrent use and only this goroutine may call
// it, so the reports are funneled here: every tool sends onto one unbuffered
// channel this loop selects on beside the finished ones, which is what puts a
// report on the stream before the result of the call that made it. A report
// the loop will never read is dropped rather than waited on — see report in
// runTool — so a reporting tool cannot park the run's unwinding.
func (r *run) runTools(ctx context.Context, msg Message) bool {
	calls, ok := r.decide(ctx, msg)
	if !ok {
		return false
	}
	for _, c := range calls {
		if c.denied {
			continue
		}
		if !r.emit(ToolCallStart{ToolUse: c.run}) {
			return false
		}
		r.agent.log(ctx, slog.LevelDebug, "goodall: tool call", "turn", r.turn, "tool", c.run.Name, "id", c.run.ID)
	}

	type finished struct {
		index   int
		use     ToolUse
		outcome ToolOutcome
	}
	// Buffered for every call, so no tool can block on sending its result
	// even when this loop has stopped reading: waiting below then waits
	// only for work already begun, which is what keeps a canceled run from
	// leaving tools behind.
	done := make(chan finished, len(calls))
	// Unbuffered on purpose: a report is handed over only when this loop
	// takes it, so the event is on the stream before the reporting tool can
	// go on to finish, and the order a tool reported in is the order a
	// consumer sees.
	reports := make(chan ToolEvent)
	// Closed on the way out, which is what turns every report still in
	// flight into a dropped one.
	stopped := make(chan struct{})
	var wg sync.WaitGroup
	// Deferred before the close so that it runs after it: a tool parked on
	// a report nobody will read must be released before this waits for it.
	defer wg.Wait()
	defer close(stopped)
	for i, c := range calls {
		if c.denied {
			// A refused call still produces its result, so the
			// bookkeeping below has one shape for every call.
			done <- finished{index: i, use: c.use, outcome: ToolOutcome{Result: c.result}}
			continue
		}
		wg.Go(func() {
			done <- finished{index: i, use: c.run, outcome: r.runTool(ctx, c.run, reports, stopped)}
		})
	}

	results := make([]Block, len(calls))
	var hookErr error
	for remaining := len(calls); remaining > 0; {
		var out finished
		select {
		case nested := <-reports:
			if hookErr != nil {
				// The run is ending and the work these events
				// describe is being discarded with it.
				continue
			}
			if !r.emit(nested) {
				r.cancel()
				return false
			}
			continue
		case out = <-done:
			remaining--
		}
		if hookErr != nil {
			// The run is ending; the remaining tools are drained so
			// they finish, and their results are dropped rather
			// than committed unseen by the hook that shapes them.
			continue
		}
		if err := r.afterToolCall(ctx, out.use, &out.outcome.Result); err != nil {
			hookErr = err
			continue
		}
		results[out.index] = out.outcome.Result
		end := ToolCallEnd{
			ToolUse: out.use,
			Result:  out.outcome.Result,
			Usage:   out.outcome.Usage,
			Cost:    out.outcome.Cost,
		}
		if !r.emit(end) {
			// The consumer left with tools still running. Cancel them
			// before the deferred wait, or a tool that only ends with
			// its context would hold the consumer's break for ever.
			r.cancel()
			return false
		}
	}
	if hookErr != nil {
		for i, c := range calls {
			if results[i] == nil {
				results[i] = errorResultFor(c.use, unrunDropped)
			}
		}
		r.conv = r.conv.Append(msg, UserMessage(results...))
		r.stop(StopCauseHook, hookErr.Error())
		return false
	}

	// The turn is committed now; its results are held uncommitted until
	// BeforeSend has seen them, and committed before anything terminal.
	r.conv = r.conv.Append(msg)
	turn := UserMessage(results...)
	r.newTurn = &turn
	return true
}

// decide asks the BeforeToolCall hook about every call of the turn before any
// of them runs, and reports whether the run continues. It ends the run itself
// on a hook error and on a deferral, because both are decisions about the
// whole turn rather than about one call.
func (r *run) decide(ctx context.Context, msg Message) ([]call, bool) {
	uses := msg.ToolUses()
	calls := make([]call, 0, len(uses))
	deferred := false
	for _, use := range uses {
		decision, err := r.beforeToolCall(ctx, use)
		if err != nil {
			// Nothing has run, and nothing will: the turn is
			// committed with an error result for every call so the
			// conversation can still be sent (invariant 9).
			r.commit(msg, unrunHook)
			r.stop(StopCauseHook, err.Error())
			return nil, false
		}
		c := call{use: use, run: use}
		switch decision.kind {
		case decisionDeny:
			c.denied = true
			// The model reads this next turn and is expected to act
			// on it, so a refusal that said nothing would leave it
			// retrying the same call.
			reason := decision.reason
			if reason == "" {
				reason = deniedText
			}
			c.result = errorResultFor(use, reason)
		case decisionModify:
			c.run.Input = decision.input
		case decisionDefer:
			deferred = true
		}
		calls = append(calls, c)
	}
	if deferred {
		// One deferred call holds the turn: a person approves the turn
		// they were shown, and a tool that had already run could not be
		// un-run if they then said no. The turn is committed without a
		// results message, which is what Resume completes.
		r.conv = r.conv.Append(msg)
		r.deferred = uses
		r.agent.log(ctx, slog.LevelInfo, "goodall: run deferred", "turn", r.turn, "calls", len(uses))
		r.stop(StopCauseDeferred, deferredText(len(uses)))
		return nil, false
	}
	return calls, true
}

// runTool runs one call and always produces a result, because every tool_use
// gets exactly one tool_result (invariant 9). A name the agent does not have,
// a handler that returned an error and a handler that panicked all come back
// as an error result the model can read and correct; a panic is recovered
// here so that one tool author's bug cannot take the run, or the process,
// down with it.
//
// A tool that implements [Reporter] runs through that interface instead, and
// is handed a report function that sends onto reports, which the results loop
// in runTools is reading. Everything else about the call is the same.
func (r *run) runTool(ctx context.Context, use ToolUse, reports chan<- ToolEvent, stopped <-chan struct{}) (outcome ToolOutcome) {
	defer func() {
		if v := recover(); v != nil {
			outcome.Result = ErrorResult(fmt.Sprintf("The tool %q failed: %v", use.Name, v))
			outcome.Result.ToolUseID = use.ID
		}
	}()

	tool, ok := r.tools[use.Name]
	if !ok {
		outcome.Result = ErrorResult(unknownToolText(use.Name, r.agent.Tools))
		outcome.Result.ToolUseID = use.ID
		return outcome
	}

	var err error
	if reporter, reporting := tool.(Reporter); reporting {
		// returned closes as this call ends, which is what makes a
		// report from a goroutine the tool left behind a dropped one:
		// the wrapper would name a call whose result is already out.
		returned := make(chan struct{})
		defer close(returned)
		report := func(ev Event) {
			if ev == nil {
				return
			}
			// Asked first, so that a report made after the call
			// ended is dropped even while the loop is still reading
			// the turn's other calls.
			select {
			case <-returned:
				return
			case <-stopped:
				return
			default:
			}
			select {
			case reports <- ToolEvent{ToolUseID: use.ID, Name: use.Name, Event: ev}:
			case <-returned:
			case <-stopped:
			}
		}
		outcome, err = reporter.ExecuteReporting(ctx, use.Input, report)
	} else {
		// Execute turns a bad argument object into an error result of
		// its own, naming the parameter and listing the rest, so the
		// run continues. A plain tool reports nothing spent, which is
		// what leaves ToolCallEnd's usage and cost zero.
		outcome.Result, err = tool.Execute(ctx, use.Input)
	}
	if err != nil {
		outcome.Result = ErrorResult(err.Error())
	}
	outcome.Result.ToolUseID = use.ID
	return outcome
}

// errorResultFor is one error result addressed to a call.
func errorResultFor(use ToolUse, text string) ToolResult {
	result := ErrorResult(text)
	result.ToolUseID = use.ID
	return result
}

// unknownToolText tells the model what it called and what it could have
// called, which is the only thing that lets it correct itself.
func unknownToolText(name string, tools []Tool) string {
	var b strings.Builder
	b.WriteString("There is no tool named ")
	b.WriteString(strconv.Quote(name))
	b.WriteString(".")
	if len(tools) == 0 {
		b.WriteString(" This agent has no tools at all.")
		return b.String()
	}
	b.WriteString(" The tools available are: ")
	for i, tool := range tools {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(tool.Name())
	}
	b.WriteString(".")
	return b.String()
}
