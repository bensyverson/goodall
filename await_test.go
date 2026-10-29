package goodall_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"slices"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// TestAwaitHoldsOnlyItsCall is the criterion: an awaited call waits for the
// caller, and the turn's other calls run as they would without it, with their
// events, and their results reach the caller on the pause.
func TestAwaitHoldsOnlyItsCall(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1", "tu_2"),
	}, rec.tool(t))
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		if use.ID == "tu_2" {
			return goodall.Await(), nil
		}
		return goodall.Allow(), nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseDeferred {
		t.Errorf("the run stopped because %q, want %q", stopped.Cause, goodall.StopCauseDeferred)
	}
	if got := rec.seen(); !slices.Equal(got, []string{"tu_1"}) {
		t.Errorf("the tool ran with %q, want only the call that was not awaited", got)
	}
	pending := stopped.Result.Pending
	if len(pending) != 1 || pending[0].ID != "tu_2" {
		t.Fatalf("pending = %+v, want only the awaited call", pending)
	}
	finished := stopped.Result.Finished
	if len(finished) != 1 || finished[0].ToolUseID != "tu_1" || finished[0].Text() != "echo: tu_1" {
		t.Fatalf("finished = %+v, want the result of the call that ran", finished)
	}
	conv := stopped.Result.Conversation
	if conv.Len() != 2 {
		t.Fatalf("the conversation is %d messages, want the input and the assistant turn with no results yet", conv.Len())
	}
	if len(conv.At(1).ToolUses()) != 2 {
		t.Errorf("the committed turn carries %d calls, want both: history is what the model said", len(conv.At(1).ToolUses()))
	}
	starts := countEvents(events, goodall.EventToolCallStart)
	ends := countEvents(events, goodall.EventToolCallEnd)
	if starts != 1 || ends != 1 {
		t.Errorf("%d starts and %d ends, want one of each for the call that ran and none for the awaited one", starts, ends)
	}
}

// TestAwaitFinishesDeniedAndModifiedCalls: the other decisions of an awaiting
// turn keep their meaning, and every finished result is on the pause, in the
// order the model made the calls.
func TestAwaitFinishesDeniedAndModifiedCalls(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1", "tu_2", "tu_3"),
	}, rec.tool(t))
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		switch use.ID {
		case "tu_1":
			return goodall.Await(), nil
		case "tu_2":
			return goodall.Deny("not now"), nil
		default:
			return goodall.Modify(jsontext.Value(`{"text":"sanitized"}`)), nil
		}
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	stopped := terminalStop(t, events)

	if got := rec.seen(); !slices.Equal(got, []string{"sanitized"}) {
		t.Errorf("the tool ran with %q, want only the modified call", got)
	}
	finished := stopped.Result.Finished
	if len(finished) != 2 {
		t.Fatalf("finished = %+v, want the denied and the modified call", finished)
	}
	if finished[0].ToolUseID != "tu_2" || !finished[0].IsError || finished[0].Text() != "not now" {
		t.Errorf("the first finished result is %+v, want the denial", finished[0])
	}
	if finished[1].ToolUseID != "tu_3" || finished[1].Text() != "echo: sanitized" {
		t.Errorf("the second finished result is %+v, want the modified call's answer", finished[1])
	}
	if pending := stopped.Result.Pending; len(pending) != 1 || pending[0].ID != "tu_1" {
		t.Errorf("pending = %+v, want the awaited call", pending)
	}
	if n := countEvents(events, goodall.EventToolCallEnd); n != 2 {
		t.Errorf("%d tool call ends, want one each for the denied and the modified call", n)
	}
}

// TestDeferInAnAwaitingTurnHoldsTheWholeTurn: approval wins. A person approves
// the turn they are shown, so one Defer anywhere holds every call, awaited or
// not, exactly as it does on its own.
func TestDeferInAnAwaitingTurnHoldsTheWholeTurn(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1", "tu_2", "tu_3"),
	}, rec.tool(t))
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		switch use.ID {
		case "tu_1":
			return goodall.Await(), nil
		case "tu_3":
			return goodall.Defer(), nil
		default:
			return goodall.Allow(), nil
		}
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	stopped := terminalStop(t, events)

	if got := rec.seen(); len(got) != 0 {
		t.Errorf("the tool ran %d times, want never: a deferred call holds the turn", len(got))
	}
	if n := len(stopped.Result.Pending); n != 3 {
		t.Errorf("%d pending, want every call of the turn", n)
	}
	if n := len(stopped.Result.Finished); n != 0 {
		t.Errorf("%d finished, want none", n)
	}
	if n := countEvents(events, goodall.EventToolCallStart) + countEvents(events, goodall.EventToolCallEnd); n != 0 {
		t.Errorf("%d tool call events, want none", n)
	}
}

// TestAwaitThenResumeAnswersEveryCall: the caller hands the finished results
// back with its own answer, and the resumed turn answers every call of the
// turn in one results message, in the model's order, without running the
// finished calls again.
func TestAwaitThenResumeAnswersEveryCall(t *testing.T) {
	rec := &recorder{}
	tool := rec.tool(t)
	a, p := agentFor([]fake.Turn{
		toolTurn("tu_1", "tu_2"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "thanks"}),
	}, tool)
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		if use.ID == "tu_1" {
			return goodall.Await(), nil
		}
		return goodall.Allow(), nil
	}

	stopped := terminalStop(t, runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"}))
	answer := goodall.TextResult("the person chose B")
	answer.ToolUseID = "tu_1"

	resumed := resumeEvents(t, a, stopped.Result.Conversation, append(stopped.Result.Finished, answer)...)
	done := terminalDone(t, resumed).Result

	results := toolResults(done.Conversation.At(2))
	if len(results) != 2 {
		t.Fatalf("the results message carries %d results, want one per call", len(results))
	}
	if results[0].ToolUseID != "tu_1" || results[0].Text() != "the person chose B" {
		t.Errorf("the first result is %+v, want the caller's answer", results[0])
	}
	if results[1].ToolUseID != "tu_2" || results[1].Text() != "echo: tu_2" {
		t.Errorf("the second result is %+v, want the finished call's own result", results[1])
	}
	if got := rec.seen(); !slices.Equal(got, []string{"tu_2"}) {
		t.Errorf("the tool ran with %q, want the finished call once and never again", got)
	}
	if len(done.Finished) != 0 || len(done.Pending) != 0 {
		t.Errorf("the finished run reports %d finished and %d pending, want none", len(done.Finished), len(done.Pending))
	}
	if n := len(p.Requests()); n != 2 {
		t.Errorf("%d requests, want two", n)
	}
}

// TestAwaitEveryCallRunsNothing: a turn whose calls are all awaited pauses with
// nothing finished.
func TestAwaitEveryCallRunsNothing(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1"),
	}, rec.tool(t))
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		return goodall.Await(), nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseDeferred || len(stopped.Result.Pending) != 1 {
		t.Fatalf("the run stopped %q with %d pending, want one awaited call", stopped.Cause, len(stopped.Result.Pending))
	}
	if len(stopped.Result.Finished) != 0 || len(rec.seen()) != 0 {
		t.Errorf("%d finished and %d runs, want nothing", len(stopped.Result.Finished), len(rec.seen()))
	}
}

// TestAfterToolCallErrorInAnAwaitingTurnAnswersEveryCall: a hook failing on a
// finished call ends the run as it would in any turn, and the awaited call is
// answered too, so the conversation can still be sent (invariant 9).
func TestAfterToolCallErrorInAnAwaitingTurnAnswersEveryCall(t *testing.T) {
	rec := &recorder{}
	a, _ := agentFor([]fake.Turn{
		toolTurn("tu_1", "tu_2"),
	}, rec.tool(t))
	a.Hooks.BeforeToolCall = func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
		if use.ID == "tu_1" {
			return goodall.Await(), nil
		}
		return goodall.Allow(), nil
	}
	a.Hooks.AfterToolCall = func(ctx context.Context, use goodall.ToolUse, result *goodall.ToolResult) error {
		return errors.New("the result store is down")
	}

	stopped := terminalStop(t, runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"}))

	if stopped.Cause != goodall.StopCauseHook {
		t.Errorf("the run stopped because %q, want the hook's error", stopped.Cause)
	}
	if len(stopped.Result.Pending) != 0 || len(stopped.Result.Finished) != 0 {
		t.Errorf("%d pending and %d finished, want none: the run did not pause", len(stopped.Result.Pending), len(stopped.Result.Finished))
	}
	conv := stopped.Result.Conversation
	if conv.Len() != 3 || len(toolResults(conv.At(2))) != 2 {
		t.Fatalf("the conversation is %d messages, want the turn answered with one result per call", conv.Len())
	}
}
