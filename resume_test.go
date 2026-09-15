package goodall_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// resumeEvents drives a resumed run to its end on the same terms as
// runEvents: the stream must never yield an error, and it must say something.
func resumeEvents(t *testing.T, a *goodall.Agent, conv goodall.Conversation, results ...goodall.ToolResult) []goodall.Event {
	t.Helper()
	events, err := collect(a.Resume(t.Context(), conv, results...))
	if err != nil {
		t.Fatalf("the resumed stream yielded an error, which it must never do: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("the resumed run emitted no events at all")
	}
	return events
}

// TestDeferThenResumeReachesDone is the criterion, end to end: a hook defers
// the turn, the caller approves it by handing back results, and the same
// conversation carries on to a finished answer.
func TestDeferThenResumeReachesDone(t *testing.T) {
	rec := &recorder{}
	tool := rec.tool(t)
	a, p := agentFor([]fake.Turn{
		toolTurn("tu_1"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "thanks"}),
	}, tool)
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		return goodall.Defer(), nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	stopped := terminalStop(t, events)
	if stopped.Cause != goodall.StopCauseDeferred || len(stopped.Result.Pending) != 1 {
		t.Fatalf("the run stopped %q with %d pending, want one deferred call", stopped.Cause, len(stopped.Result.Pending))
	}

	// The approver hands the results back on an agent with no hook, which
	// is the shape of a UI that approved the turn out of band.
	approved := &goodall.Agent{Provider: p, Model: "test-model", Tools: []goodall.Tool{tool}}
	var newTurns []goodall.Message
	approved.Hooks.BeforeSend = func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message) error {
		if newTurn != nil {
			newTurns = append(newTurns, *newTurn)
		}
		return nil
	}
	result := goodall.ToolResult{
		ToolUseID: "tu_1",
		Content:   goodall.Blocks{goodall.Text{Text: "the human said yes"}},
	}

	resumed := resumeEvents(t, approved, stopped.Result.Conversation, result)
	done := terminalDone(t, resumed).Result

	if len(done.Pending) != 0 {
		t.Errorf("the resumed run reports %d pending calls, want none", len(done.Pending))
	}
	conv := done.Conversation
	if conv.Len() != 4 {
		t.Fatalf("the conversation is %d messages, want input, turn, results, answer", conv.Len())
	}
	results := toolResults(conv.At(2))
	if len(results) != 1 || results[0].ToolUseID != "tu_1" || results[0].Text() != "the human said yes" {
		t.Errorf("the results message carries %+v, want the caller's result", results)
	}
	if got := conv.At(3).Text(); got != "thanks" {
		t.Errorf("the last answer says %q, want the second scripted turn", got)
	}
	if len(newTurns) != 1 || len(newTurns[0].Content) != 1 {
		t.Fatalf("BeforeSend saw %d new turns, want the one results message", len(newTurns))
	}
	if newTurns[0].Role != goodall.RoleUser {
		t.Errorf("the resumed new turn is from %q, want the user", newTurns[0].Role)
	}
	if got := rec.seen(); len(got) != 0 {
		t.Errorf("the tool ran %d times, want never: the caller answered for it", len(got))
	}
	if reqs := p.Requests(); len(reqs) != 2 || len(reqs[1].Messages) != 3 {
		t.Errorf("the resumed request carried %d messages, want the three of the history", len(p.Requests()[1].Messages))
	}
}

// TestResumeFillsInAMissingResult: a call the caller said nothing about is
// still answered, because a conversation with a dangling tool_use is a 400 the
// next time anyone sends it (invariant 9).
func TestResumeFillsInAMissingResult(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "understood"}),
	})
	conv := goodall.Conversation{}.Append(
		goodall.UserMessage(goodall.Text{Text: "say hi"}),
		goodall.AssistantMessage(fake.Use("tu_1", "echo", `{"text":"a"}`), fake.Use("tu_2", "echo", `{"text":"b"}`)),
	)

	events := resumeEvents(t, a, conv, goodall.ToolResult{ToolUseID: "tu_2", Content: goodall.Blocks{goodall.Text{Text: "b done"}}})
	result := terminalDone(t, events).Result

	results := toolResults(result.Conversation.At(2))
	if len(results) != 2 {
		t.Fatalf("the results message carries %d results, want one per call", len(results))
	}
	if results[0].ToolUseID != "tu_1" || !results[0].IsError {
		t.Errorf("result 0 = %+v, want an error result for the call nobody answered", results[0])
	}
	if results[1].ToolUseID != "tu_2" || results[1].IsError || results[1].Text() != "b done" {
		t.Errorf("result 1 = %+v, want the caller's result in the model's order", results[1])
	}
}

// TestResumeRejectsAnUnknownCall: a result naming a call the model never made
// is the caller's mistake, and sending it would put a tool_result with no
// tool_use into the history.
func TestResumeRejectsAnUnknownCall(t *testing.T) {
	a, p := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "never sent"}),
	})
	conv := goodall.Conversation{}.Append(
		goodall.UserMessage(goodall.Text{Text: "say hi"}),
		goodall.AssistantMessage(fake.Use("tu_1", "echo", `{"text":"a"}`)),
	)

	events := resumeEvents(t, a, conv, goodall.ToolResult{ToolUseID: "tu_9"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseError || stopped.Kind != goodall.KindInvalidRequest {
		t.Errorf("the run stopped %q/%q, want an invalid-request error", stopped.Cause, stopped.Kind)
	}
	if !strings.Contains(stopped.Message, "tu_9") {
		t.Errorf("the message is %q, want it to name the unknown call", stopped.Message)
	}
	if p.Calls() != 0 {
		t.Errorf("the provider was called %d times, want none", p.Calls())
	}
}

// TestResumeNeedsAnAssistantTurnWithCalls: there is nothing to resume unless
// the conversation ends in a model turn that asked for tools.
func TestResumeNeedsAnAssistantTurnWithCalls(t *testing.T) {
	cases := map[string]goodall.Conversation{
		"empty": {},
		"ends in a user message": goodall.Conversation{}.Append(
			goodall.UserMessage(goodall.Text{Text: "say hi"}),
		),
		"ends in an answer with no calls": goodall.Conversation{}.Append(
			goodall.UserMessage(goodall.Text{Text: "say hi"}),
			goodall.AssistantMessage(goodall.Text{Text: "hi"}),
		),
	}
	for name, conv := range cases {
		t.Run(name, func(t *testing.T) {
			a, p := agentFor([]fake.Turn{
				fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "never sent"}),
			})
			events := resumeEvents(t, a, conv)
			stopped := terminalStop(t, events)
			if stopped.Cause != goodall.StopCauseError || stopped.Kind != goodall.KindInvalidRequest {
				t.Errorf("the run stopped %q/%q, want an invalid-request error", stopped.Cause, stopped.Kind)
			}
			if p.Calls() != 0 {
				t.Errorf("the provider was called %d times, want none", p.Calls())
			}
			if stopped.Result.Conversation.Len() != conv.Len() {
				t.Errorf("the conversation came back %d messages, want the caller's %d untouched",
					stopped.Result.Conversation.Len(), conv.Len())
			}
		})
	}
}

// TestResumeReportsAMisconfiguredAgent keeps Resume on the same contract as
// Run: every ending is a terminal event, never a panic or a yielded error.
func TestResumeReportsAMisconfiguredAgent(t *testing.T) {
	conv := goodall.Conversation{}.Append(
		goodall.UserMessage(goodall.Text{Text: "say hi"}),
		goodall.AssistantMessage(fake.Use("tu_1", "echo", `{"text":"a"}`)),
	)
	a := &goodall.Agent{Model: "test-model"}

	events := resumeEvents(t, a, conv, goodall.ToolResult{ToolUseID: "tu_1"})
	stopped := terminalStop(t, events)
	if stopped.Cause != goodall.StopCauseError || stopped.Kind != goodall.KindInvalidRequest {
		t.Errorf("the run stopped %q/%q, want an invalid-request error", stopped.Cause, stopped.Kind)
	}
}
