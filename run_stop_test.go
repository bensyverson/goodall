package goodall_test

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// TestRunPauseTurnResends checks the one reason that continues without any
// tool result: the turn is appended and the request goes again unchanged.
func TestRunPauseTurnResends(t *testing.T) {
	a, p := agentFor([]fake.Turn{
		fake.Answer(goodall.StopPauseTurn, goodall.Text{Text: "working on it"}),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "finished"}),
	})

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	result := terminalDone(t, events).Result

	if result.Conversation.Len() != 3 {
		t.Fatalf("the conversation has %d messages, want user, paused assistant, assistant", result.Conversation.Len())
	}
	if got := result.Conversation.At(1).Role; got != goodall.RoleAssistant {
		t.Errorf("the paused turn was appended as %q, want assistant", got)
	}
	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("the provider saw %d requests, want 2", len(reqs))
	}
	if len(reqs[1].Messages) != 2 {
		t.Errorf("the resend carried %d messages, want the user turn plus the paused one", len(reqs[1].Messages))
	}
	if !reflect.DeepEqual(reqs[1].Messages[:1], reqs[0].Messages) {
		t.Error("the resend rewrote the prefix")
	}
}

// TestRunRefusalDoesNotRunTools holds the line the Anthropic docs draw: a
// refused turn's tool calls are never run, and the run ends.
func TestRunRefusalDoesNotRunTools(t *testing.T) {
	var ran atomic.Bool
	tool, err := goodall.NewTool("echo", "Echo the text back.", func(ctx context.Context, in struct {
		Text string `json:"text"`
	}) (goodall.ToolResult, error) {
		ran.Store(true)
		return goodall.TextResult("ran"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopRefusal, fake.Use("tu_1", "echo", `{"text":"hi"}`)),
	}, tool)

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	stopped := terminalStop(t, events)

	if ran.Load() {
		t.Error("the refused turn's tool ran")
	}
	if stopped.Cause != goodall.StopCauseRefusal {
		t.Errorf("stop cause = %q, want refusal", stopped.Cause)
	}
	for _, ev := range events {
		if ev.Type() == goodall.EventToolCallStart {
			t.Error("the loop announced a tool call it must not make")
		}
	}
	// The call still gets a result, so the conversation can be sent again
	// without a dangling tool_use (invariant 9).
	conv := stopped.Result.Conversation
	results := toolResults(conv.At(conv.Len() - 1))
	if len(results) != 1 || !results[0].IsError || results[0].ToolUseID != "tu_1" {
		t.Errorf("results = %+v, want one error result for tu_1", results)
	}
}

// TestRunMaxTokensDoesNotRunTools is the same for a cut-off turn, whose tool
// input may be complete JSON and still be missing what the model meant to say.
func TestRunMaxTokensDoesNotRunTools(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopMaxTokens, fake.Use("tu_1", "echo", `{"text":"hi"}`)),
	}, echoTool(t))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseMaxTokens {
		t.Errorf("stop cause = %q, want max_tokens", stopped.Cause)
	}
	for _, ev := range events {
		if ev.Type() == goodall.EventToolCallStart {
			t.Error("the loop ran a tool call from a cut-off turn")
		}
	}
	if stopped.Result.StopReason != goodall.StopMaxTokens {
		t.Errorf("result stop reason = %q, want max_tokens", stopped.Result.StopReason)
	}
}

// TestRunUnknownStopReason ends the run rather than guessing, and says what
// arrived so the gap can be closed.
func TestRunUnknownStopReason(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopReason("content_filter"), goodall.Text{Text: "half an answer"}),
	})

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseUnknownStop {
		t.Errorf("stop cause = %q, want unknown_stop", stopped.Cause)
	}
	if !strings.Contains(stopped.Message, "content_filter") {
		t.Errorf("the message does not name the reason: %q", stopped.Message)
	}
	if stopped.Result.Conversation.Len() != 2 {
		t.Errorf("the conversation has %d messages, want the user turn and the answer", stopped.Result.Conversation.Len())
	}
}

// TestRunToolUseWithNoCalls is the empty case of the tool row: a turn that
// says tool_use but asks for nothing has nothing to run, so the run is done.
func TestRunToolUseWithNoCalls(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, goodall.Text{Text: "never mind"}),
	}, echoTool(t))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	done := terminalDone(t, events)

	if done.Result.Conversation.Len() != 2 {
		t.Errorf("the conversation has %d messages, want 2", done.Result.Conversation.Len())
	}
}

// TestRunEndTurnWithToolCalls covers the contradiction a provider can still
// send: tool calls in a turn that did not stop for tool use. They are not run,
// and each one gets a result so the history stays sendable.
func TestRunEndTurnWithToolCalls(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopEndTurn,
			goodall.Text{Text: "all done"},
			fake.Use("tu_1", "echo", `{"text":"hi"}`),
		),
	}, echoTool(t))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	done := terminalDone(t, events)

	for _, ev := range events {
		if ev.Type() == goodall.EventToolCallStart {
			t.Error("a tool call ran on a turn that did not stop for tool use")
		}
	}
	conv := done.Result.Conversation
	results := toolResults(conv.At(conv.Len() - 1))
	if len(results) != 1 || !results[0].IsError {
		t.Errorf("results = %+v, want one error result", results)
	}
}
