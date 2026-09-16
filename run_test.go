package goodall_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// TestRunTwoTurnsWithATool is the shape of the whole loop: the model asks for
// a tool, the loop runs it, appends the exchange as one unit and sends again,
// and the second answer ends the run.
func TestRunTwoTurnsWithATool(t *testing.T) {
	a, p := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse,
			goodall.Text{Text: "Let me check."},
			fake.Use("tu_1", "echo", `{"text":"hi"}`),
		).Using(goodall.Usage{Input: 10, Output: 4}),
		fake.Answer(goodall.StopEndTurn,
			goodall.Text{Text: "It said hi."},
		).Using(goodall.Usage{Input: 20, Output: 6}),
	}, echoTool(t))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})

	wantShape := []goodall.EventType{
		goodall.EventTurnStart, goodall.EventTurnCommitted,
		goodall.EventMessageStart,
		goodall.EventBlockStart, goodall.EventTextDelta, goodall.EventBlockStop,
		goodall.EventBlockStart, goodall.EventToolInputDelta, goodall.EventBlockStop,
		goodall.EventMessageDelta, goodall.EventMessageStop,
		goodall.EventTurnEnd,
		goodall.EventToolCallStart, goodall.EventToolCallEnd,
		goodall.EventTurnStart, goodall.EventTurnCommitted,
		goodall.EventMessageStart,
		goodall.EventBlockStart, goodall.EventTextDelta, goodall.EventBlockStop,
		goodall.EventMessageDelta, goodall.EventMessageStop,
		goodall.EventTurnEnd,
		goodall.EventDone,
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, wantShape) {
		t.Errorf("event sequence =\n\t%v\nwant\n\t%v", got, wantShape)
	}

	result := terminalDone(t, events).Result
	conv := result.Conversation
	if conv.Len() != 4 {
		t.Fatalf("the conversation has %d messages, want 4", conv.Len())
	}
	wantRoles := []goodall.Role{goodall.RoleUser, goodall.RoleAssistant, goodall.RoleUser, goodall.RoleAssistant}
	for i, want := range wantRoles {
		if got := conv.At(i).Role; got != want {
			t.Errorf("message %d is from %q, want %q", i, got, want)
		}
	}
	if uses := conv.At(1).ToolUses(); len(uses) != 1 || uses[0].ID != "tu_1" {
		t.Errorf("assistant turn tool uses = %+v, want one call tu_1", uses)
	}
	results := toolResults(conv.At(2))
	if len(results) != 1 {
		t.Fatalf("the tool turn carries %d results, want 1", len(results))
	}
	if results[0].ToolUseID != "tu_1" || results[0].IsError || results[0].Text() != "echo: hi" {
		t.Errorf("tool result = %+v, want tu_1 with %q and no error", results[0], "echo: hi")
	}
	if got, want := result.Usage, (goodall.Usage{Input: 30, Output: 10}); got != want {
		t.Errorf("summed usage = %+v, want %+v", got, want)
	}
	if result.StopReason != goodall.StopEndTurn {
		t.Errorf("result stop reason = %q, want end_turn", result.StopReason)
	}
	if result.Response == nil || result.Response.Message.Text() != "It said hi." {
		t.Errorf("result response = %+v, want the last turn's", result.Response)
	}
	if len(result.Pending) != 0 {
		t.Errorf("result pending = %+v, want none", result.Pending)
	}

	// The second request carries the first request's messages unchanged,
	// followed by the new ones: history is append-only (invariant 1) and
	// the cached prefix is stable (invariant 8).
	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("the provider saw %d requests, want 2", len(reqs))
	}
	first, second := reqs[0].Messages, reqs[1].Messages
	if len(second) != len(first)+2 {
		t.Fatalf("the second request has %d messages, want %d", len(second), len(first)+2)
	}
	if !reflect.DeepEqual(second[:len(first)], first) {
		t.Error("the second request rewrote the prefix of the first")
	}
	if reqs[0].Model != "test-model" {
		t.Errorf("request model = %q, want test-model", reqs[0].Model)
	}
	if len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name() != "echo" {
		t.Errorf("request tools = %+v, want the echo tool", reqs[0].Tools)
	}
}

// TestRunSendsTheSystemPromptAndCaps checks the request fields the agent owns,
// including that an empty system prompt sends no system block at all.
func TestRunSendsTheSystemPromptAndCaps(t *testing.T) {
	a, p := agentFor([]fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"})})
	a.System = "Be terse."
	a.MaxTokens = 512
	a.Thinking = goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized}
	a.Cache = goodall.CacheOff

	runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "hello"})

	req := p.Requests()[0]
	if want := []goodall.Text{{Text: "Be terse."}}; !reflect.DeepEqual(req.System, want) {
		t.Errorf("request system = %+v, want %+v", req.System, want)
	}
	if req.MaxTokens != 512 {
		t.Errorf("request max tokens = %d, want 512", req.MaxTokens)
	}
	if req.Thinking != a.Thinking {
		t.Errorf("request thinking = %+v, want %+v", req.Thinking, a.Thinking)
	}
	if req.Cache != goodall.CacheOff {
		t.Errorf("request cache policy = %q, want off", req.Cache)
	}

	b, p2 := agentFor([]fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"})})
	runEvents(t, b, goodall.Conversation{}, goodall.Text{Text: "hello"})
	if got := p2.Requests()[0].System; got != nil {
		t.Errorf("an agent with no system prompt sent %+v, want none", got)
	}
}

// TestRunWithoutInputContinuesTheConversation is how a caller who appended
// tool results itself carries on: no input means no new user message.
func TestRunWithoutInputContinuesTheConversation(t *testing.T) {
	conv := goodall.Conversation{}.Append(
		goodall.UserMessage(goodall.Text{Text: "hello"}),
		goodall.AssistantMessage(goodall.Text{Text: "hi"}),
		goodall.UserMessage(goodall.Text{Text: "again"}),
	)
	a, p := agentFor([]fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "sure"})})

	events := runEvents(t, a, conv)
	result := terminalDone(t, events).Result

	if got := len(p.Requests()[0].Messages); got != 3 {
		t.Errorf("the request carried %d messages, want the 3 it was given", got)
	}
	if result.Conversation.Len() != 4 {
		t.Errorf("the result conversation has %d messages, want 4", result.Conversation.Len())
	}
	if conv.Len() != 3 {
		t.Errorf("the caller's conversation grew to %d messages; it must not be touched", conv.Len())
	}
}

// TestRunParseFailureContinues is the Operator bug this loop must not repeat:
// a tool call whose arguments do not fit the schema comes back as an error
// result the model can correct, not as a dangling tool_use or a dead run.
func TestRunParseFailureContinues(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "echo", `{"text":42}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "sorry about that"}),
	}, echoTool(t))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	result := terminalDone(t, events).Result

	results := toolResults(result.Conversation.At(2))
	if len(results) != 1 {
		t.Fatalf("the tool turn carries %d results, want 1", len(results))
	}
	if !results[0].IsError {
		t.Error("a parse failure produced a result that is not an error")
	}
	if !strings.Contains(results[0].Text(), "text") {
		t.Errorf("the error result does not name the bad parameter: %q", results[0].Text())
	}
	if result.Conversation.Len() != 4 {
		t.Errorf("the run stopped at %d messages; it should have continued", result.Conversation.Len())
	}
}

// TestRunUnknownToolName checks that a name the agent does not have still gets
// a result, and that the result lists what the model could have called.
func TestRunUnknownToolName(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "teleport", `{}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "understood"}),
	}, echoTool(t))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	result := terminalDone(t, events).Result

	results := toolResults(result.Conversation.At(2))
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("results = %+v, want one error result", results)
	}
	text := results[0].Text()
	if !strings.Contains(text, "teleport") || !strings.Contains(text, "echo") {
		t.Errorf("the error result should name the missing tool and the available ones: %q", text)
	}
}

// TestRunToolHandlerError turns a tool's own failure into a result the model
// reads, because a failing tool is not a failing run.
func TestRunToolHandlerError(t *testing.T) {
	boom, err := goodall.NewTool("boom", "Always fails.", func(ctx context.Context, in struct{}) (goodall.ToolResult, error) {
		return goodall.ToolResult{}, errors.New("the database is on fire")
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "boom", `{}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "noted"}),
	}, boom)

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	result := terminalDone(t, events).Result

	results := toolResults(result.Conversation.At(2))
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("results = %+v, want one error result", results)
	}
	if !strings.Contains(results[0].Text(), "the database is on fire") {
		t.Errorf("the error result does not carry the handler's message: %q", results[0].Text())
	}
}

// TestRunToolPanic keeps one tool author's bug from taking the run down with
// it: the panic is recovered into an error result.
func TestRunToolPanic(t *testing.T) {
	panicky, err := goodall.NewTool("panicky", "Always panics.", func(ctx context.Context, in struct{}) (goodall.ToolResult, error) {
		panic("nil map write")
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "panicky", `{}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "noted"}),
	}, panicky)

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	result := terminalDone(t, events).Result

	results := toolResults(result.Conversation.At(2))
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("results = %+v, want one error result", results)
	}
	if !strings.Contains(results[0].Text(), "nil map write") {
		t.Errorf("the error result does not carry the panic value: %q", results[0].Text())
	}
}

// TestRunCollectResult checks the blocking path over a run stream: one value
// out, read off the terminal event.
func TestRunCollectResult(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "echo", `{"text":"hi"}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "It said hi."}),
	}, echoTool(t))

	result, err := a.Run(t.Context(), goodall.Conversation{}, goodall.Text{Text: "go"}).CollectResult()
	if err != nil {
		t.Fatalf("CollectResult: %v", err)
	}
	if result.Response == nil || result.Response.Message.Text() != "It said hi." {
		t.Errorf("result response = %+v, want the last turn's", result.Response)
	}
	if result.Conversation.Len() != 4 {
		t.Errorf("the conversation has %d messages, want 4", result.Conversation.Len())
	}
}

// TestRunWithoutAProvider reports the misconfiguration as a terminal event
// rather than a panic or a yielded error, so one contract covers every exit.
func TestRunWithoutAProvider(t *testing.T) {
	a := &goodall.Agent{Model: "test-model"}
	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	stopped := terminalStop(t, events)
	if stopped.Cause != goodall.StopCauseError || stopped.Kind != goodall.KindInvalidRequest {
		t.Errorf("stopped = %+v, want error/invalid_request", stopped)
	}
}

// TestRunWithoutAModel is the same for the other required field.
func TestRunWithoutAModel(t *testing.T) {
	a := &goodall.Agent{Provider: &fake.Provider{}}
	events := runEvents(t, a, goodall.Conversation{})
	stopped := terminalStop(t, events)
	if stopped.Cause != goodall.StopCauseError || stopped.Kind != goodall.KindInvalidRequest {
		t.Errorf("stopped = %+v, want error/invalid_request", stopped)
	}
}

// TestRunWithDuplicateToolNames refuses a tool set where a name is ambiguous,
// since the loop would otherwise silently pick one of them.
func TestRunWithDuplicateToolNames(t *testing.T) {
	a, _ := agentFor([]fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "hi"})}, echoTool(t), echoTool(t))
	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	stopped := terminalStop(t, events)
	if stopped.Cause != goodall.StopCauseError || !strings.Contains(stopped.Message, "echo") {
		t.Errorf("stopped = %+v, want an error naming the duplicated tool", stopped)
	}
}
