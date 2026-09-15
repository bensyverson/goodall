package goodall

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
)

// runTools runs every call of one turn, concurrently, and appends the turn
// and its results to the conversation as one unit. It reports whether the run
// should continue, which is false only when the consumer stopped reading.
//
// Results are emitted as they arrive, in whatever order the tools finish, and
// appended in the order the model asked for them: a model reading its own
// transcript must see its calls answered in the order it made them.
func (r *run) runTools(ctx context.Context, msg Message) bool {
	calls := msg.ToolUses()
	for _, use := range calls {
		// HOOK INSERTION POINT (mi7b09): BeforeToolCall(ctx, use) runs
		// here, before the call is announced and before it starts.
		if !r.emit(ToolCallStart{ToolUse: use}) {
			return false
		}
		r.agent.log(ctx, slog.LevelDebug, "goodall: tool call", "turn", r.turn, "tool", use.Name, "id", use.ID)
	}

	type finished struct {
		index  int
		use    ToolUse
		result ToolResult
	}
	done := make(chan finished, len(calls))
	var wg sync.WaitGroup
	for i, use := range calls {
		wg.Go(func() {
			done <- finished{index: i, use: use, result: r.runTool(ctx, use)}
		})
	}
	// The channel is buffered for every call, so no tool can block on
	// sending its result; waiting here only waits for work already begun,
	// which is what keeps a cancelled run from leaving tools behind.
	defer wg.Wait()

	results := make([]Block, len(calls))
	for range calls {
		out := <-done
		results[out.index] = out.result
		// HOOK INSERTION POINT (mi7b09): AfterToolCall(ctx, out.use,
		// out.result) runs here, before the result is announced.
		if !r.emit(ToolCallEnd{ToolUse: out.use, Result: out.result}) {
			// The consumer left with tools still running. Cancel them
			// before the deferred wait, or a tool that only ends with
			// its context would hold the consumer's break for ever.
			r.cancel()
			return false
		}
	}
	r.conv = r.conv.Append(msg, UserMessage(results...))
	return true
}

// runTool runs one call and always produces a result, because every tool_use
// gets exactly one tool_result (invariant 9). A name the agent does not have,
// a handler that returned an error and a handler that panicked all come back
// as an error result the model can read and correct; a panic is recovered
// here so that one tool author's bug cannot take the run, or the process,
// down with it.
func (r *run) runTool(ctx context.Context, use ToolUse) (result ToolResult) {
	defer func() {
		if v := recover(); v != nil {
			result = ErrorResult(fmt.Sprintf("The tool %q failed: %v", use.Name, v))
			result.ToolUseID = use.ID
		}
	}()

	tool, ok := r.tools[use.Name]
	if !ok {
		result = ErrorResult(unknownToolText(use.Name, r.agent.Tools))
		result.ToolUseID = use.ID
		return result
	}
	// Execute turns a bad argument object into an error result of its own,
	// naming the parameter and listing the rest, so the run continues.
	out, err := tool.Execute(ctx, use.Input)
	if err != nil {
		out = ErrorResult(err.Error())
	}
	out.ToolUseID = use.ID
	return out
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
