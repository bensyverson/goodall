package goodall

import (
	"context"
	"fmt"
)

// noResult is what a pending call is told when the caller resumed without
// answering it. Every tool_use needs a tool_result or the conversation is a
// 400 the next time anyone sends it (invariant 9), and the model reads this
// on its next turn, so it says plainly that the call did not happen.
const noResult = "This tool was not run: no result was provided for the call."

// Resume continues a run that paused for approval. The pending calls are the
// tool_use blocks of conv's last message, which must be a model turn that
// asked for tools — the state a [Defer] decision leaves behind, carried on the
// terminal event's Result.Conversation and Result.Pending.
//
// The results are matched to those calls by ToolUseID and assembled into one
// user message in the order the model made the calls, whatever order they are
// given in; a later result for the same call replaces an earlier one. A call
// no result names is answered with an error result saying none was provided,
// because every tool_use needs a tool_result. A result naming a call the model
// never made ends the run with Stopped{Cause: StopCauseError,
// Kind: KindInvalidRequest} rather than sending a dangling tool_result.
//
// From there the run is exactly a Run with no new input: BeforeSend sees the
// assembled results as its new turn, the loop sends, and the stream carries
// the same events and the same one terminal event as any other run.
//
// Nothing about the caller's results is checked against what the tools would
// have done: this is the seam where an approver substitutes its own answer,
// which is the point of deferring.
func (a *Agent) Resume(ctx context.Context, conv Conversation, results ...ToolResult) Stream {
	return func(yield func(Event, error) bool) {
		r := &run{agent: a, yield: yield, alive: true, conv: conv, ctx: ctx}
		turn, err := resumeTurn(conv, results)
		if err != nil {
			r.fail(StopCauseError, KindInvalidRequest, err.Error())
			return
		}
		r.newTurn = &turn
		r.execute(ctx, nil)
	}
}

// resumeTurn assembles the caller's results into the one user message that
// answers the conversation's pending calls, in the order the model made them.
func resumeTurn(conv Conversation, results []ToolResult) (Message, error) {
	last, ok := conv.Last()
	if !ok {
		return Message{}, fmt.Errorf("goodall: the conversation is empty, so there is nothing to resume")
	}
	if last.Role != RoleAssistant {
		return Message{}, fmt.Errorf("goodall: the conversation ends in a %s message, so there is nothing to resume: a resumed run answers the tool calls of a model turn", last.Role)
	}
	pending := last.ToolUses()
	if len(pending) == 0 {
		return Message{}, fmt.Errorf("goodall: the last model turn asked for no tool calls, so there is nothing to resume")
	}

	byID := make(map[string]ToolResult, len(results))
	for _, result := range results {
		known := false
		for _, use := range pending {
			if use.ID == result.ToolUseID {
				known = true
				break
			}
		}
		if !known {
			return Message{}, fmt.Errorf("goodall: the result for %q answers no pending tool call; the calls waiting are %s",
				result.ToolUseID, pendingIDs(pending))
		}
		byID[result.ToolUseID] = result
	}

	blocks := make([]Block, len(pending))
	for i, use := range pending {
		result, given := byID[use.ID]
		if !given {
			result = ErrorResult(noResult)
		}
		result.ToolUseID = use.ID
		blocks[i] = result
	}
	return UserMessage(blocks...), nil
}

// pendingIDs names the calls waiting for a result, so a caller who sent the
// wrong id can see which ones were expected.
func pendingIDs(pending []ToolUse) string {
	out := make([]byte, 0, len(pending)*8)
	for i, use := range pending {
		if i > 0 {
			out = append(out, ", "...)
		}
		out = append(out, use.ID...)
	}
	return string(out)
}
