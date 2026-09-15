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

// runUntil drives a run, calling at on every event, and stops when at returns
// true. It is how these tests cancel at an exact point in the stream rather
// than after a sleep.
func runUntil(t *testing.T, s goodall.Stream, at func(goodall.Event) bool) []goodall.Event {
	t.Helper()
	var out []goodall.Event
	var failure error
	for ev, err := range s {
		if err != nil {
			failure = err
			break
		}
		out = append(out, ev)
		if at != nil && at(ev) {
			at = nil
		}
	}
	if failure != nil {
		t.Fatalf("the run stream yielded an error, which it must never do: %v", failure)
	}
	return out
}

// TestRunCancelledMidStream is invariant 10 at its hardest: the caller pulls
// the plug while the model is still writing, and the run still ends in one
// terminal event carrying everything that arrived.
func TestRunCancelledMidStream(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Stalled(goodall.Text{Text: "half an answer"}, fake.Use("tu_1", "echo", `{"text":"hi"}`)),
	}, echoTool(t))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := runUntil(t, a.Run(ctx, goodall.Conversation{}, goodall.Text{Text: "go"}), func(ev goodall.Event) bool {
		// Cancel once the tool call has fully arrived, so the partial
		// message holds a complete tool_use nobody will run.
		if ev.Type() == goodall.EventBlockStop {
			if e := ev.(goodall.BlockStop); e.Index == 1 {
				cancel()
				return true
			}
		}
		return false
	})

	stopped := terminalStop(t, events)
	if stopped.Cause != goodall.StopCauseCancelled {
		t.Fatalf("stop cause = %q (%s), want cancelled", stopped.Cause, stopped.Message)
	}
	if stopped.Kind != goodall.KindUnknown {
		t.Errorf("stop kind = %q, want unknown: a cancellation is not an API error", stopped.Kind)
	}

	conv := stopped.Result.Conversation
	if conv.Len() != 3 {
		t.Fatalf("the conversation has %d messages, want the input, the partial answer and its results", conv.Len())
	}
	partial := conv.At(1)
	if !partial.Partial {
		t.Error("the interrupted assistant message is not marked partial")
	}
	if partial.Text() != "half an answer" {
		t.Errorf("the partial message says %q, want what had arrived", partial.Text())
	}
	results := toolResults(conv.At(2))
	if len(results) != 1 || !results[0].IsError || results[0].ToolUseID != "tu_1" {
		t.Fatalf("results = %+v, want one error result for tu_1", results)
	}
	if results[0].Text() == "" {
		t.Error("the error result says nothing; the model reads this text")
	}
	if stopped.Result.Response != nil {
		t.Errorf("the result carries a response (%+v), want none: no turn completed", stopped.Result.Response)
	}
	// No tool ran, so nothing announced one.
	for _, ev := range events {
		if ev.Type() == goodall.EventToolCallStart {
			t.Error("a tool call was announced on a cancelled turn")
		}
	}
}

// TestRunCancelledWhileToolsRun waits for the work that is already under way
// and keeps its real answers rather than throwing them away.
func TestRunCancelledWhileToolsRun(t *testing.T) {
	tool, err := goodall.NewTool("slow", "Finishes whatever happens.", func(ctx context.Context, in struct{}) (goodall.ToolResult, error) {
		time.Sleep(2 * time.Millisecond)
		return goodall.TextResult("finished anyway"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "slow", `{}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "unreached"}),
	}, tool)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := runUntil(t, a.Run(ctx, goodall.Conversation{}, goodall.Text{Text: "go"}), func(ev goodall.Event) bool {
		if ev.Type() == goodall.EventToolCallStart {
			cancel()
			return true
		}
		return false
	})

	stopped := terminalStop(t, events)
	if stopped.Cause != goodall.StopCauseCancelled {
		t.Fatalf("stop cause = %q (%s), want cancelled", stopped.Cause, stopped.Message)
	}
	conv := stopped.Result.Conversation
	results := toolResults(conv.At(conv.Len() - 1))
	if len(results) != 1 {
		t.Fatalf("results = %+v, want the one call's result", results)
	}
	if results[0].IsError || results[0].Text() != "finished anyway" {
		t.Errorf("result = %+v, want the tool's real answer", results[0])
	}
}

// TestRunProviderErrorCarriesItsKind hands the caller the classification it
// would have got from the provider, without ever yielding an error from the
// run stream.
func TestRunProviderErrorCarriesItsKind(t *testing.T) {
	apiErr := &goodall.APIError{
		Provider: "fake",
		Status:   429,
		Kind:     goodall.KindRateLimited,
		Type:     "rate_limit_error",
		Message:  "slow down",
	}
	a, _ := agentFor([]fake.Turn{
		fake.Broken(apiErr, goodall.Text{Text: "as far as I got"}),
	})

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseError {
		t.Fatalf("stop cause = %q, want error", stopped.Cause)
	}
	if stopped.Kind != goodall.KindRateLimited {
		t.Errorf("stop kind = %q, want rate_limited", stopped.Kind)
	}
	if !strings.Contains(stopped.Message, "slow down") {
		t.Errorf("the message drops the provider's words: %q", stopped.Message)
	}
	conv := stopped.Result.Conversation
	if conv.Len() != 2 || !conv.At(1).Partial {
		t.Errorf("the conversation is %d messages ending partial=%v, want the partial answer kept", conv.Len(), conv.At(conv.Len()-1).Partial)
	}
}

// TestRunProtocolErrorIsUnknownKind covers the other error family: a provider
// layer that broke the event contract is a bug, not something to retry, and it
// carries no cross-provider classification.
func TestRunProtocolErrorIsUnknownKind(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		// A stream that simply ends: no message_delta, no message_stop.
		{Events: []goodall.Event{goodall.MessageStart{ID: "msg_1"}}},
	})

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseError || stopped.Kind != goodall.KindUnknown {
		t.Errorf("stopped = %+v, want error/unknown", stopped)
	}
	if !strings.Contains(stopped.Message, "message_stop") {
		t.Errorf("the message does not say what broke: %q", stopped.Message)
	}
}

// TestRunPlainErrorIsUnknownKind checks the same for an error that is neither
// an APIError nor a ProtocolError, such as a transport failure.
func TestRunPlainErrorIsUnknownKind(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Broken(errors.New("connection reset by peer")),
	})

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseError || stopped.Kind != goodall.KindUnknown {
		t.Errorf("stopped = %+v, want error/unknown", stopped)
	}
	if !strings.Contains(stopped.Message, "connection reset") {
		t.Errorf("the message drops the cause: %q", stopped.Message)
	}
	// Nothing arrived, so nothing is appended: an empty assistant turn
	// would make the conversation unsendable.
	if got := stopped.Result.Conversation.Len(); got != 1 {
		t.Errorf("the conversation has %d messages, want just the input", got)
	}
}

// TestRunCancelledBeforeTheFirstTurn still ends in a terminal event, with the
// conversation the caller handed in plus the input.
func TestRunCancelledBeforeTheFirstTurn(t *testing.T) {
	a, p := agentFor([]fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "unreached"})})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	events, err := collect(a.Run(ctx, goodall.Conversation{}, goodall.Text{Text: "go"}))
	if err != nil {
		t.Fatalf("the run stream yielded an error: %v", err)
	}
	stopped := terminalStop(t, events)
	if stopped.Cause != goodall.StopCauseCancelled {
		t.Errorf("stop cause = %q, want cancelled", stopped.Cause)
	}
	if p.Calls() != 0 {
		t.Errorf("the provider was called %d times on an already-cancelled run", p.Calls())
	}
	if stopped.Result.Conversation.Len() != 1 {
		t.Errorf("the conversation has %d messages, want the input", stopped.Result.Conversation.Len())
	}
}
