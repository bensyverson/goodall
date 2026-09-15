package goodall_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// toolTurns scripts n turns that each ask for the echo tool, which is the
// shape of a loop that would run forever without a budget.
func toolTurns(n int, usage goodall.Usage) []fake.Turn {
	out := make([]fake.Turn, n)
	for i := range out {
		out[i] = fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "echo", `{"text":"hi"}`)).Using(usage)
	}
	return out
}

// TestRunTurnLimit stops a run that would keep calling tools for ever, with
// the conversation intact: the last turn's answer and its tool results are
// both in it, so the caller can resume or persist without repair.
func TestRunTurnLimit(t *testing.T) {
	a, p := agentFor(toolTurns(10, goodall.Usage{}), echoTool(t))
	a.Budget = goodall.Budget{MaxTurns: 3}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseTurnLimit {
		t.Fatalf("stop cause = %q (%s), want turn_limit", stopped.Cause, stopped.Message)
	}
	if got := p.Calls(); got != 3 {
		t.Errorf("the provider was called %d times, want 3", got)
	}
	conv := stopped.Result.Conversation
	// user, then three rounds of assistant plus tool results.
	if conv.Len() != 7 {
		t.Fatalf("the conversation has %d messages, want 7", conv.Len())
	}
	last := conv.At(conv.Len() - 1)
	if last.Role != goodall.RoleUser {
		t.Errorf("the conversation ends with a %q message, want the tool results", last.Role)
	}
	results := toolResults(last)
	if len(results) != 1 || results[0].IsError {
		t.Errorf("the last tool results = %+v, want one real result", results)
	}
	if stopped.Result.Response == nil {
		t.Error("the result carries no response")
	}
}

// TestRunDefaultTurnLimit checks that a zero budget is the documented default
// rather than an unlimited run.
func TestRunDefaultTurnLimit(t *testing.T) {
	a, p := agentFor(toolTurns(goodall.DefaultMaxTurns+5, goodall.Usage{}), echoTool(t))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseTurnLimit {
		t.Fatalf("stop cause = %q, want turn_limit", stopped.Cause)
	}
	if got := p.Calls(); got != goodall.DefaultMaxTurns {
		t.Errorf("the provider was called %d times, want %d", got, goodall.DefaultMaxTurns)
	}
}

// TestRunTokenLimit stops once the run's own usage passes the budget, counted
// over every turn rather than per call.
func TestRunTokenLimit(t *testing.T) {
	a, p := agentFor(toolTurns(10, goodall.Usage{Input: 100, Output: 100}), echoTool(t))
	a.Budget = goodall.Budget{MaxTokens: 350}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	stopped := terminalStop(t, events)

	if stopped.Cause != goodall.StopCauseTokenLimit {
		t.Fatalf("stop cause = %q (%s), want token_limit", stopped.Cause, stopped.Message)
	}
	if got := p.Calls(); got != 2 {
		t.Errorf("the provider was called %d times, want 2 (400 tokens passes 350)", got)
	}
	if got, want := stopped.Result.Usage, (goodall.Usage{Input: 200, Output: 200}); got != want {
		t.Errorf("result usage = %+v, want %+v", got, want)
	}
	if stopped.Result.Conversation.Len() != 5 {
		t.Errorf("the conversation has %d messages, want 5", stopped.Result.Conversation.Len())
	}
}

// TestRunSumsTheCostOfEveryTurn checks the other half of the run's
// accounting: the money, where the provider reports any.
func TestRunSumsTheCostOfEveryTurn(t *testing.T) {
	price := func(s string) goodall.Cost {
		t.Helper()
		amount, err := goodall.ParseDecimal(s)
		if err != nil {
			t.Fatal(err)
		}
		return goodall.Cost{Amount: amount, Currency: "USD", Reported: true}
	}
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "echo", `{"text":"hi"}`)).Costing(price("0.0012")),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}).Costing(price("0.0034")),
	}, echoTool(t))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	cost := terminalDone(t, events).Result.Cost

	if !cost.Reported || cost.Currency != "USD" {
		t.Fatalf("cost = %+v, want a reported USD figure", cost)
	}
	if got, want := cost.Amount.String(), "0.0046"; got != want {
		t.Errorf("summed cost = %s, want %s", got, want)
	}
}

// TestRunTimeout is the wall-clock budget, on fake time so the test is
// instant. The context is derived inside the bubble from context.Background(),
// never from t.Context(), which would leave the fake provider parked on a
// context nothing cancels until the test returns.
func TestRunTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _ := agentFor([]fake.Turn{
			fake.Stalled(goodall.Text{Text: "thinking"}),
		}, echoTool(t))
		a.Budget = goodall.Budget{Timeout: 30 * time.Second}

		start := time.Now()
		events, err := collect(a.Run(context.Background(), goodall.Conversation{}, goodall.Text{Text: "go"}))
		if err != nil {
			t.Fatalf("the run stream yielded an error: %v", err)
		}
		stopped := terminalStop(t, events)
		if stopped.Cause != goodall.StopCauseTimeout {
			t.Fatalf("stop cause = %q (%s), want timeout", stopped.Cause, stopped.Message)
		}
		if got := time.Since(start); got != 30*time.Second {
			t.Errorf("the run took %v, want the 30s budget", got)
		}
		conv := stopped.Result.Conversation
		last := conv.At(conv.Len() - 1)
		if !last.Partial {
			t.Error("the interrupted assistant message is not marked partial")
		}
		if last.Text() != "thinking" {
			t.Errorf("the partial message says %q, want what had arrived", last.Text())
		}
	})
}

// TestRunCallerCancellationBeatsTheTimeout keeps the two clocks apart: a
// caller who cancels gets Canceled even when a timeout is also set.
func TestRunCallerCancellationBeatsTheTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _ := agentFor([]fake.Turn{fake.Stalled(goodall.Text{Text: "thinking"})})
		a.Budget = goodall.Budget{Timeout: time.Hour}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(time.Second)
			cancel()
		}()
		defer cancel()

		events, err := collect(a.Run(ctx, goodall.Conversation{}, goodall.Text{Text: "go"}))
		if err != nil {
			t.Fatalf("the run stream yielded an error: %v", err)
		}
		if got := terminalStop(t, events).Cause; got != goodall.StopCauseCanceled {
			t.Errorf("stop cause = %q, want canceled", got)
		}
	})
}
