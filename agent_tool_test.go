package goodall_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// delegateTurn scripts a parent turn that hands the prompt to the research
// tool, which is how every test here starts the delegation.
func delegateTurn(prompt string) fake.Turn {
	return fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "research", `{"prompt":"`+prompt+`"}`))
}

// researchTool is the child wrapped as a tool, under the name the scripted
// parent turns call.
func researchTool(t *testing.T, child *goodall.Agent) goodall.Tool {
	t.Helper()
	tool, err := goodall.AgentTool(child, "research", "Hand a research question to a specialist agent and read its answer.")
	if err != nil {
		t.Fatalf("building the agent tool: %v", err)
	}
	return tool
}

// delegatedResult is the one tool result of the parent's second request, which
// is what the parent model actually reads.
func delegatedResult(t *testing.T, p *fake.Provider) goodall.ToolResult {
	t.Helper()
	reqs := p.Requests()
	if len(reqs) < 2 {
		t.Fatalf("the parent made %d requests, want a second one carrying the tool result", len(reqs))
	}
	var results []goodall.ToolResult
	for _, msg := range reqs[len(reqs)-1].Messages {
		results = append(results, toolResults(msg)...)
	}
	if len(results) != 1 {
		t.Fatalf("the parent's last request carried %d tool results, want exactly one", len(results))
	}
	return results[0]
}

// TestAgentToolReturnsTheChildsAnswer is the delegation itself: the parent
// calls the tool, the child runs a conversation of its own, and its final text
// comes back as the tool result the parent's next turn reads.
func TestAgentToolReturnsTheChildsAnswer(t *testing.T) {
	child, childProvider := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn,
			goodall.Text{Text: "Jane Goodall arrived at Gombe in 1960."},
			goodall.Text{Text: "She was 26."},
		),
	})
	parent, parentProvider := agentFor([]fake.Turn{
		delegateTurn("When did Jane Goodall reach Gombe?"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "1960, at 26."}),
	}, researchTool(t, child))

	events := runEvents(t, parent, goodall.Conversation{}, goodall.Text{Text: "ask the specialist"})
	terminalDone(t, events)

	result := delegatedResult(t, parentProvider)
	if result.IsError {
		t.Errorf("the tool result is an error: %q", result.Text())
	}
	if got, want := result.Text(), "Jane Goodall arrived at Gombe in 1960.\n\nShe was 26."; got != want {
		t.Errorf("the tool result reads %q, want the child's text blocks joined: %q", got, want)
	}
	if got := childProvider.Calls(); got != 1 {
		t.Errorf("the child provider was called %d times, want once", got)
	}
	if got, want := childProvider.Requests()[0].Messages[0].Text(), "When did Jane Goodall reach Gombe?"; got != want {
		t.Errorf("the child was asked %q, want the prompt the parent wrote: %q", got, want)
	}
}

// TestAgentToolAnswerWithNoTextIsAnEmptyResult keeps a silent child from
// reading as a failure: the run finished, so the result is a plain one, and the
// parent model decides what an empty answer means.
func TestAgentToolAnswerWithNoTextIsAnEmptyResult(t *testing.T) {
	child, _ := agentFor([]fake.Turn{fake.Answer(goodall.StopEndTurn)})
	parent, parentProvider := agentFor([]fake.Turn{
		delegateTurn("say nothing"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "it said nothing"}),
	}, researchTool(t, child))

	terminalDone(t, runEvents(t, parent, goodall.Conversation{}, goodall.Text{Text: "go"}))

	result := delegatedResult(t, parentProvider)
	if result.IsError || result.Text() != "" {
		t.Errorf("the tool result is %+v, want a plain empty result", result)
	}
}

// TestAgentToolCancelingTheParentEndsTheChild is why Execute runs the child on
// the context it was given: the parent's cancellation reaches a child parked
// mid-stream, and the parent still ends in one terminal event saying Canceled.
func TestAgentToolCancelingTheParentEndsTheChild(t *testing.T) {
	child, childProvider := agentFor([]fake.Turn{fake.Stalled(goodall.Text{Text: "still thinking"})})
	parent, _ := agentFor([]fake.Turn{delegateTurn("wait for me")}, researchTool(t, child))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := runUntil(t, parent.Run(ctx, goodall.Conversation{}, goodall.Text{Text: "go"}), func(ev goodall.Event) bool {
		if ev.Type() != goodall.EventToolCallStart {
			return false
		}
		// The tool has not started yet — the loop emits every start
		// before it launches anything — so the cancel waits for the
		// child's provider on a goroutine of its own.
		go func() {
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) && childProvider.Calls() == 0 {
				time.Sleep(time.Millisecond)
			}
			cancel()
		}()
		return true
	})

	stopped := terminalStop(t, events)
	if stopped.Cause != goodall.StopCauseCanceled {
		t.Errorf("the parent stopped with %q (%s), want canceled", stopped.Cause, stopped.Message)
	}
	if got := childProvider.Calls(); got != 1 {
		t.Fatalf("the child provider was called %d times, want once", got)
	}
	if got, want := childProvider.Cleanups(), childProvider.Calls(); got != want {
		t.Errorf("%d of the child's %d provider streams were unwound; the child run outlived the parent", got, want)
	}
}

// TestAgentToolReportsAStoppedChildAsAnError is the contract that keeps a
// failed delegation in the model's hands: a child that ended early is an error
// result naming the cause, never a Go error and never the end of the parent.
func TestAgentToolReportsAStoppedChildAsAnError(t *testing.T) {
	child, _ := agentFor([]fake.Turn{fake.Broken(errors.New("the upstream connection reset"))})
	parent, parentProvider := agentFor([]fake.Turn{
		delegateTurn("research this"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "the specialist broke; here is what I know"}),
	}, researchTool(t, child))

	terminalDone(t, runEvents(t, parent, goodall.Conversation{}, goodall.Text{Text: "go"}))

	result := delegatedResult(t, parentProvider)
	if !result.IsError {
		t.Errorf("the tool result is not an error: %q", result.Text())
	}
	text := result.Text()
	if !strings.Contains(text, string(goodall.StopCauseError)) {
		t.Errorf("the error text %q does not name the cause %q", text, goodall.StopCauseError)
	}
	if !strings.Contains(text, "the upstream connection reset") {
		t.Errorf("the error text %q does not carry the child's message", text)
	}
}

// TestAgentToolReportsADeferredChildAsAnError is the gap this leaf documents
// rather than closes: a hook inside the child asked for approval, and a nested
// pause has no path to the parent's caller, so the model is told plainly.
func TestAgentToolReportsADeferredChildAsAnError(t *testing.T) {
	child, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_c1", "echo", `{"text":"hi"}`)),
	}, echoTool(t))
	child.Hooks = goodall.Hooks{
		BeforeToolCall: func(ctx context.Context, use goodall.ToolUse) (goodall.Decision, error) {
			return goodall.Defer(), nil
		},
	}
	parent, parentProvider := agentFor([]fake.Turn{
		delegateTurn("research this"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "it needed a human"}),
	}, researchTool(t, child))

	terminalDone(t, runEvents(t, parent, goodall.Conversation{}, goodall.Text{Text: "go"}))

	result := delegatedResult(t, parentProvider)
	if !result.IsError {
		t.Errorf("the tool result is not an error: %q", result.Text())
	}
	text := result.Text()
	if !strings.Contains(text, "Approval inside a delegated run is not supported yet") {
		t.Errorf("the error text %q does not say that approval inside a delegated run is unsupported", text)
	}
	if !strings.Contains(text, string(goodall.StopCauseDeferred)) {
		t.Errorf("the error text %q does not name the cause %q", text, goodall.StopCauseDeferred)
	}
}

// TestAgentToolForwardsTheChildsEventsWrapped is what a UI watching the parent
// now sees: every event of the delegated run, wrapped in a ToolEvent naming the
// call that started it, in the child's own order and all of them before the
// call ends. The parent's own event counts are unchanged, because a nested
// event travels under its own tag rather than as a second turn of the parent.
func TestAgentToolForwardsTheChildsEventsWrapped(t *testing.T) {
	child, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_c1", "echo", `{"text":"hi"}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "the child's answer"}),
	}, echoTool(t))
	parent, _ := agentFor([]fake.Turn{
		delegateTurn("research this"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, researchTool(t, child))

	events := runEvents(t, parent, goodall.Conversation{}, goodall.Text{Text: "go"})
	terminalDone(t, events)

	if got := countEvents(events, goodall.EventToolCallStart); got != 1 {
		t.Errorf("the parent's stream carries %d tool call starts, want only the delegation", got)
	}
	if got := countEvents(events, goodall.EventTurnStart); got != 2 {
		t.Errorf("the parent's stream carries %d turn starts, want its own two", got)
	}
	if got := countEvents(events, goodall.EventDone); got != 1 {
		t.Errorf("the parent's stream carries %d done events, want only its own", got)
	}

	var nested []goodall.Event
	for _, ev := range events {
		wrapped, ok := ev.(goodall.ToolEvent)
		if !ok {
			continue
		}
		if wrapped.ToolUseID != "tu_1" || wrapped.Name != "research" {
			t.Errorf("a nested event is addressed to {%q, %q}, want the research call tu_1", wrapped.ToolUseID, wrapped.Name)
		}
		nested = append(nested, wrapped.Event)
	}
	if len(nested) == 0 {
		t.Fatalf("the parent's stream carries no nested events: %v", eventTypes(events))
	}
	// The child ran two turns with a tool call in between, so its own
	// shape must be recognizable inside the wrappers.
	for _, want := range []goodall.EventType{
		goodall.EventTurnStart, goodall.EventToolCallStart, goodall.EventToolCallEnd, goodall.EventDone,
	} {
		if countEvents(nested, want) == 0 {
			t.Errorf("no nested %s arrived; the child's stream is not being forwarded whole: %v", want, eventTypes(nested))
		}
	}
	if got := countEvents(nested, goodall.EventTurnStart); got != 2 {
		t.Errorf("%d nested turn starts arrived, want the child's two", got)
	}
	if got, want := nested[len(nested)-1].Type(), goodall.EventDone; got != want {
		t.Errorf("the last nested event is %s, want the child's %s", got, want)
	}

	// Every report belongs under the call that produced it.
	var lastNested, callEnd int
	for i, ev := range events {
		switch ev.Type() {
		case goodall.EventToolEvent:
			lastNested = i
		case goodall.EventToolCallEnd:
			callEnd = i
		}
	}
	if callEnd == 0 || lastNested > callEnd {
		t.Errorf("the last nested event is at %d and the call ends at %d, want the child's events first: %v",
			lastNested, callEnd, eventTypes(events))
	}
}

// TestAgentToolReportsTheChildsUsageOnTheCall is the per-call accounting: what
// the delegation spent is the child's own total, and it travels on the
// ToolCallEnd rather than being rolled into the parent's ledger, which counts
// the parent model's tokens and nothing else.
func TestAgentToolReportsTheChildsUsageOnTheCall(t *testing.T) {
	childCost := price(t, "0.0075")
	child, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "the child's answer"}).
			Using(goodall.Usage{Input: 400, Output: 60}).
			Costing(childCost),
	})
	parent, _ := agentFor([]fake.Turn{
		delegateTurn("research this"),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, researchTool(t, child))

	events := runEvents(t, parent, goodall.Conversation{}, goodall.Text{Text: "go"})
	result := terminalDone(t, events).Result

	var ends int
	for _, ev := range events {
		end, ok := ev.(goodall.ToolCallEnd)
		if !ok {
			continue
		}
		ends++
		if want := (goodall.Usage{Input: 400, Output: 60}); end.Usage != want {
			t.Errorf("the delegation's ToolCallEnd usage = %+v, want the child's own %+v", end.Usage, want)
		}
		if end.Cost != childCost {
			t.Errorf("the delegation's ToolCallEnd cost = %+v, want the child's own %+v", end.Cost, childCost)
		}
	}
	if ends != 1 {
		t.Fatalf("the parent carried %d tool call ends, want the one delegation", ends)
	}
	if result.Usage != (goodall.Usage{}) {
		t.Errorf("the parent's usage is %+v, want only its own tokens, which the fake reports as none", result.Usage)
	}
	if result.Cost != (goodall.Cost{}) {
		t.Errorf("the parent's cost is %+v, want the child's money left off the roll-up", result.Cost)
	}
}

// TestAgentToolRefusesANilChild keeps a registration mistake at construction,
// where the caller can see it, rather than inside a run.
func TestAgentToolRefusesANilChild(t *testing.T) {
	tool, err := goodall.AgentTool(nil, "research", "Hand a question to a specialist agent.")
	if err == nil {
		t.Fatalf("AgentTool accepted a nil child and returned %v", tool)
	}
	if !strings.Contains(err.Error(), "research") {
		t.Errorf("the error %q does not name the tool", err)
	}
}

// TestAgentToolRefusesAnInvalidName holds the tool to the same naming rules
// NewTool applies, since it is the same registration.
func TestAgentToolRefusesAnInvalidName(t *testing.T) {
	child, _ := agentFor(nil)
	for _, name := range []string{"", "deep research", strings.Repeat("a", goodall.MaxToolNameLength+1)} {
		if tool, err := goodall.AgentTool(child, name, "Hand a question to a specialist agent."); err == nil {
			t.Errorf("AgentTool accepted the name %q and returned %v", name, tool)
		}
	}
}
