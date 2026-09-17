package goodall_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// stepInput is the one parameter the scripted step calls carry, named so that
// the reporting handlers below read as handlers rather than as struct
// literals.
type stepInput struct {
	N int `json:"n" desc:"which step this is"`
}

// price is a reported cost in US dollars.
func price(t *testing.T, s string) goodall.Cost {
	t.Helper()
	amount, err := goodall.ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return goodall.Cost{Amount: amount, Currency: "USD", Reported: true}
}

// reportingStep builds the reporting tool the scripted step calls reach, over
// the handler a test wants to exercise.
func reportingStep(t *testing.T, run func(context.Context, stepInput, func(goodall.Event)) (goodall.ToolOutcome, error)) goodall.Tool {
	t.Helper()
	tool, err := goodall.NewReportingTool("step", "Report while it runs.", run)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

// toolEvents is every ToolEvent in a stream, in the order it arrived.
func toolEvents(events []goodall.Event) []goodall.ToolEvent {
	var out []goodall.ToolEvent
	for _, ev := range events {
		if nested, ok := ev.(goodall.ToolEvent); ok {
			out = append(out, nested)
		}
	}
	return out
}

// TestReportedEventsTravelWrappedBeforeTheCallEnds is the whole of the nested
// event contract on the parent's stream: every report arrives as a ToolEvent
// naming the call that made it, in the order it was reported, and all of them
// before that call's ToolCallEnd.
func TestReportedEventsTravelWrappedBeforeTheCallEnds(t *testing.T) {
	tool := reportingStep(t, func(ctx context.Context, in stepInput, report func(goodall.Event)) (goodall.ToolOutcome, error) {
		report(goodall.TurnStart{Turn: 1})
		report(goodall.TextDelta{Text: "working"})
		report(goodall.TextDelta{Text: " on it"})
		return goodall.ToolOutcome{Result: goodall.TextResult("step 1")}, nil
	})
	a, _ := agentFor([]fake.Turn{
		stepCalls(1),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, tool)

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	terminalDone(t, events)

	nested := toolEvents(events)
	if len(nested) != 3 {
		t.Fatalf("the parent's stream carries %d tool events, want the three the tool reported: %v", len(nested), eventTypes(events))
	}
	for i, ne := range nested {
		if ne.ToolUseID != "tu_1" || ne.Name != "step" {
			t.Errorf("tool event %d = {%q, %q}, want the step call tu_1", i, ne.ToolUseID, ne.Name)
		}
	}
	if _, ok := nested[0].Event.(goodall.TurnStart); !ok {
		t.Errorf("the first tool event carries %T, want the TurnStart reported first", nested[0].Event)
	}
	if got, ok := nested[1].Event.(goodall.TextDelta); !ok || got.Text != "working" {
		t.Errorf("the second tool event carries %#v, want the first text delta", nested[1].Event)
	}
	if got, ok := nested[2].Event.(goodall.TextDelta); !ok || got.Text != " on it" {
		t.Errorf("the third tool event carries %#v, want the second text delta", nested[2].Event)
	}

	// Position matters as much as content: a UI draws the reports under the
	// call, so every one of them precedes the call's end.
	var end, last int
	for i, ev := range events {
		switch ev.Type() {
		case goodall.EventToolCallEnd:
			end = i
		case goodall.EventToolEvent:
			last = i
		}
	}
	if end == 0 || last > end {
		t.Errorf("the last tool event is at %d and the tool call end at %d, want every report before the end: %v", last, end, eventTypes(events))
	}
}

// TestToolCallEndCarriesWhatTheCallSpent is the per-call accounting: what a
// reporting tool declares it spent travels on the end of that call, which is
// where a consumer that wants a total sums it.
func TestToolCallEndCarriesWhatTheCallSpent(t *testing.T) {
	want := goodall.Usage{Input: 120, Output: 45}
	cost := price(t, "0.0021")
	tool := reportingStep(t, func(ctx context.Context, in stepInput, report func(goodall.Event)) (goodall.ToolOutcome, error) {
		return goodall.ToolOutcome{
			Result: goodall.TextResult("step 1"),
			Usage:  want,
			Cost:   cost,
		}, nil
	})
	a, _ := agentFor([]fake.Turn{
		stepCalls(1),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, tool)

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	result := terminalDone(t, events).Result

	var ends int
	for _, ev := range events {
		end, ok := ev.(goodall.ToolCallEnd)
		if !ok {
			continue
		}
		ends++
		if end.Usage != want {
			t.Errorf("ToolCallEnd usage = %+v, want %+v", end.Usage, want)
		}
		if end.Cost != cost {
			t.Errorf("ToolCallEnd cost = %+v, want %+v", end.Cost, cost)
		}
	}
	if ends != 1 {
		t.Fatalf("the run carried %d tool call ends, want one", ends)
	}

	// The ruling: what a call spent is not rolled into the run's own
	// totals, because a delegated cost nobody reported would vanish from
	// the sum while the sum called itself a provider's figure.
	if result.Usage.Output != 0 || result.Usage.Input != 0 {
		t.Errorf("the run's usage is %+v, want only the parent model's own tokens", result.Usage)
	}
	if result.Cost != (goodall.Cost{}) {
		t.Errorf("the run's cost is %+v, want the tool's money left off the roll-up", result.Cost)
	}
}

// TestToolCallEndIsZeroForAPlainTool keeps the new fields honest for the tools
// that say nothing: a zero usage means "nothing reported", so a plain Execute
// tool must not be made to look free.
func TestToolCallEndIsZeroForAPlainTool(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "echo", `{"text":"hi"}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, echoTool(t))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	terminalDone(t, events)

	if got := len(toolEvents(events)); got != 0 {
		t.Errorf("a plain tool produced %d tool events", got)
	}
	for _, ev := range events {
		end, ok := ev.(goodall.ToolCallEnd)
		if !ok {
			continue
		}
		if end.Usage != (goodall.Usage{}) || end.Cost != (goodall.Cost{}) {
			t.Errorf("ToolCallEnd = %+v / %+v, want both zero for a plain tool", end.Usage, end.Cost)
		}
	}
}

// TestReportsFromSeveralGoroutinesAllArrive is the concurrency the interface
// promises: a tool that fans its work out reports from every goroutine, and
// the run funnels them onto one stream.
func TestReportsFromSeveralGoroutinesAllArrive(t *testing.T) {
	const fan = 4
	const each = 8
	tool := reportingStep(t, func(ctx context.Context, in stepInput, report func(goodall.Event)) (goodall.ToolOutcome, error) {
		var wg sync.WaitGroup
		for range fan {
			wg.Go(func() {
				for range each {
					report(goodall.TextDelta{Text: "tick"})
				}
			})
		}
		wg.Wait()
		return goodall.ToolOutcome{Result: goodall.TextResult("step 1")}, nil
	})
	a, _ := agentFor([]fake.Turn{
		stepCalls(1),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, tool)

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	terminalDone(t, events)

	if got, want := len(toolEvents(events)), fan*each; got != want {
		t.Errorf("%d tool events arrived, want %d", got, want)
	}
}

// TestReportsAfterTheConsumerLeavesAreDroppedAndTheRunUnwinds is the
// fail-closed half of the design. A consumer that breaks out of the stream is
// no longer reading, and a tool that keeps reporting must not be able to park
// the run's unwinding on a send nobody will take: the reports are dropped, the
// tool finishes, and the deferred wait completes.
func TestReportsAfterTheConsumerLeavesAreDroppedAndTheRunUnwinds(t *testing.T) {
	const after = 1000
	toolReturned := make(chan struct{})
	var reported atomic.Int64
	tool := reportingStep(t, func(ctx context.Context, in stepInput, report func(goodall.Event)) (goodall.ToolOutcome, error) {
		defer close(toolReturned)
		// The first report is what the consumer breaks on.
		report(goodall.TextDelta{Text: "working"})
		for range after {
			report(goodall.TextDelta{Text: "still working"})
			reported.Add(1)
		}
		return goodall.ToolOutcome{Result: goodall.TextResult("step 1")}, nil
	})
	a, p := agentFor([]fake.Turn{
		stepCalls(1),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "unreached"}),
	}, tool)

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		for ev := range a.Run(context.Background(), goodall.Conversation{}, goodall.Text{Text: "go"}) {
			if ev.Type() == goodall.EventToolEvent {
				break
			}
		}
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("breaking out of the run hung on a tool that kept reporting")
	}
	select {
	case <-toolReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("the run returned with the reporting tool still parked on a report")
	}
	if got := reported.Load(); got != after {
		t.Errorf("the tool got through %d of its %d reports, want every one to have been dropped rather than blocked", got, after)
	}
	if got, want := p.Cleanups(), p.Calls(); got != want {
		t.Errorf("%d provider streams were unwound of %d opened", got, want)
	}
}

// TestAReportAfterTheToolReturnedIsDropped is the other end of the window a
// report may travel in. A goroutine the tool left behind is reporting about
// work whose result is already on the stream, so the wrapper would name a call
// that has ended; the loop is still reading, because the turn's other call is
// running, and the late report is dropped all the same.
func TestAReportAfterTheToolReturnedIsDropped(t *testing.T) {
	late := make(chan struct{})      // closed by the consumer once the first call has ended
	attempted := make(chan struct{}) // closed once the late report has been tried
	tool := reportingStep(t, func(ctx context.Context, in stepInput, report func(goodall.Event)) (goodall.ToolOutcome, error) {
		if in.N == 1 {
			report(goodall.TextDelta{Text: "during"})
			go func() {
				<-late
				report(goodall.TextDelta{Text: "after"})
				close(attempted)
			}()
			return goodall.ToolOutcome{Result: goodall.TextResult("step 1")}, nil
		}
		// The second call holds the turn open until the late report has
		// been tried, so the results loop is still reading reports when
		// it happens.
		select {
		case <-attempted:
		case <-time.After(5 * time.Second):
		}
		return goodall.ToolOutcome{Result: goodall.TextResult("step 2")}, nil
	})
	a, _ := agentFor([]fake.Turn{
		stepCalls(2),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, tool)

	var events []goodall.Event
	var closedLate bool
	for ev, err := range a.Run(t.Context(), goodall.Conversation{}, goodall.Text{Text: "go"}) {
		if err != nil {
			t.Fatalf("the run stream yielded an error: %v", err)
		}
		events = append(events, ev)
		if end, ok := ev.(goodall.ToolCallEnd); ok && end.ToolUse.ID == "tu_1" && !closedLate {
			closedLate = true
			close(late)
		}
	}
	terminalDone(t, events)
	<-attempted

	nested := toolEvents(events)
	if len(nested) != 1 {
		t.Fatalf("%d tool events arrived, want only the one reported while the tool ran: %+v", len(nested), nested)
	}
	if got, ok := nested[0].Event.(goodall.TextDelta); !ok || got.Text != "during" {
		t.Errorf("the one tool event carries %#v, want the report made while the tool ran", nested[0].Event)
	}
}
