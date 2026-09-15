package goodall_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// barrierTool answers only once every call of the turn has arrived, so a loop
// that ran its tools one after another would never get past the first and the
// test fails on the timeout rather than hanging.
func barrierTool(t *testing.T, width int) goodall.Tool {
	t.Helper()
	var arrived atomic.Int32
	all := make(chan struct{})
	tool, err := goodall.NewTool("step", "Wait for the other calls of this turn.", func(ctx context.Context, in struct {
		N int `json:"n" desc:"which step this is"`
	}) (goodall.ToolResult, error) {
		if int(arrived.Add(1)) == width {
			close(all)
		}
		select {
		case <-all:
			return goodall.TextResult("step " + strconv.Itoa(in.N)), nil
		case <-time.After(2 * time.Second):
			return goodall.ToolResult{}, errors.New("the calls of this turn did not run concurrently")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

// stepCalls scripts one turn asking for n calls of the step tool.
func stepCalls(n int) fake.Turn {
	blocks := make([]goodall.Block, n)
	for i := range blocks {
		blocks[i] = fake.Use(fmt.Sprintf("tu_%d", i+1), "step", fmt.Sprintf(`{"n":%d}`, i+1))
	}
	return fake.Answer(goodall.StopToolUse, blocks...)
}

// TestRunRunsATurnsToolsConcurrently is the reason the loop uses a wait group:
// three calls in one turn run at once, and their results still come back in
// the order the model asked for them.
func TestRunRunsATurnsToolsConcurrently(t *testing.T) {
	const width = 3
	a, _ := agentFor([]fake.Turn{
		stepCalls(width),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "all three"}),
	}, barrierTool(t, width))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})
	result := terminalDone(t, events).Result

	var starts, ends int
	for _, ev := range events {
		switch ev.Type() {
		case goodall.EventToolCallStart:
			starts++
		case goodall.EventToolCallEnd:
			ends++
		}
	}
	if starts != width || ends != width {
		t.Errorf("%d tool call starts and %d ends, want %d of each", starts, ends, width)
	}

	results := toolResults(result.Conversation.At(2))
	if len(results) != width {
		t.Fatalf("the tool turn carries %d results, want %d", len(results), width)
	}
	for i, r := range results {
		wantID := fmt.Sprintf("tu_%d", i+1)
		wantText := fmt.Sprintf("step %d", i+1)
		if r.ToolUseID != wantID || r.Text() != wantText || r.IsError {
			t.Errorf("result %d = %+v, want %s saying %q", i, r, wantID, wantText)
		}
	}
}

// TestRunToolCallEventsCarryTheCall checks the two loop events a UI draws a
// tool call from.
func TestRunToolCallEventsCarryTheCall(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "echo", `{"text":"hi"}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"}),
	}, echoTool(t))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "go"})

	var sawStart, sawEnd bool
	for _, ev := range events {
		switch e := ev.(type) {
		case goodall.ToolCallStart:
			sawStart = true
			if e.ToolUse.ID != "tu_1" || e.ToolUse.Name != "echo" {
				t.Errorf("ToolCallStart = %+v, want the echo call", e.ToolUse)
			}
			if string(e.ToolUse.Input) != `{"text":"hi"}` {
				t.Errorf("ToolCallStart input = %s, want the model's arguments", e.ToolUse.Input)
			}
		case goodall.ToolCallEnd:
			sawEnd = true
			if e.ToolUse.ID != "tu_1" || e.Result.ToolUseID != "tu_1" {
				t.Errorf("ToolCallEnd = %+v / %+v, want tu_1 on both", e.ToolUse, e.Result)
			}
			if e.Result.Text() != "echo: hi" {
				t.Errorf("ToolCallEnd result = %q, want the tool's answer", e.Result.Text())
			}
		}
	}
	if !sawStart || !sawEnd {
		t.Errorf("start seen: %v, end seen: %v; want both", sawStart, sawEnd)
	}
}

// TestRunEarlyBreakLeavesNothingRunning is the contract a consumer relies on
// when it stops reading: the provider stream is unwound, every tool the loop
// started has finished, and nothing is emitted afterwards.
func TestRunEarlyBreakLeavesNothingRunning(t *testing.T) {
	const width = 3
	var started, finished atomic.Int32
	var mu sync.Mutex
	tool, err := goodall.NewTool("step", "Take a moment.", func(ctx context.Context, in struct {
		N int `json:"n"`
	}) (goodall.ToolResult, error) {
		started.Add(1)
		defer finished.Add(1)
		mu.Lock()
		defer mu.Unlock()
		time.Sleep(time.Millisecond)
		return goodall.TextResult("step"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, p := agentFor([]fake.Turn{
		stepCalls(width),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "unreached"}),
	}, tool)

	var after int
	stop := false
	for ev, err := range a.Run(t.Context(), goodall.Conversation{}, goodall.Text{Text: "go"}) {
		if err != nil {
			t.Errorf("the run stream yielded an error: %v", err)
			break
		}
		if stop {
			after++
			continue
		}
		if ev.Type() == goodall.EventToolCallEnd {
			stop = true
			break
		}
	}
	if after != 0 {
		t.Errorf("%d events arrived after the break", after)
	}
	if got, want := finished.Load(), started.Load(); got != want {
		t.Errorf("%d tools finished of %d started; the loop returned with tools still running", got, want)
	}
	if started.Load() != width {
		t.Errorf("%d tools started, want %d", started.Load(), width)
	}
	if got, want := p.Cleanups(), p.Calls(); got != want {
		t.Errorf("%d provider streams were unwound of %d opened", got, want)
	}
}

// TestRunEarlyBreakCancelsRunningTools is the case the test above cannot
// reach: a consumer breaks out while a tool that honours its context is still
// running. The break must cancel the run's context before waiting for the
// tool, or the consumer's break hangs on a tool that only ends when the
// context does.
func TestRunEarlyBreakCancelsRunningTools(t *testing.T) {
	tool, err := goodall.NewTool("step", "Wait for the context.", func(ctx context.Context, in struct {
		N int `json:"n"`
	}) (goodall.ToolResult, error) {
		if in.N == 1 {
			return goodall.TextResult("quick"), nil
		}
		<-ctx.Done()
		return goodall.TextResult("cancelled"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := agentFor([]fake.Turn{stepCalls(2)}, tool)

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		for ev, _ := range a.Run(context.Background(), goodall.Conversation{}, goodall.Text{Text: "go"}) {
			if ev.Type() == goodall.EventToolCallEnd {
				break
			}
		}
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("breaking out of the run hung on a tool that waits for its context")
	}
}
