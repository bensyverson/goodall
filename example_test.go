package goodall_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
)

// The customiser's quick start: an agent with a tool, run to completion while
// its events are printed as they arrive. It is the README's first example and
// compiles with the suite; it is not run, because it needs a key.
func ExampleAgent_Run() {
	ctx := context.Background()

	weather, err := goodall.NewTool("get_weather", "Look up the current weather in a city.",
		func(ctx context.Context, in struct {
			City string `json:"city" desc:"the city to look up, such as Paris"`
		}) (goodall.ToolResult, error) {
			return goodall.TextResult(`{"temperature_c":18,"conditions":"light rain"}`), nil
		})
	if err != nil {
		log.Fatal(err)
	}

	agent := &goodall.Agent{
		Provider: anthropic.New(os.Getenv("ANTHROPIC_API_KEY")),
		Model:    "claude-sonnet-5",
		System:   "You are a terse weather assistant.",
		Tools:    []goodall.Tool{weather},
		Thinking: goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized},
	}

	var conv goodall.Conversation
	for ev, _ := range agent.Run(ctx, conv, goodall.Text{Text: "Will it rain in Paris this afternoon?"}) {
		switch e := ev.(type) {
		case goodall.TextDelta:
			fmt.Print(e.Text)
		case goodall.ToolCallStart:
			fmt.Printf("\n[calling %s with %s]\n", e.ToolUse.Name, e.ToolUse.Input)
		case goodall.Done:
			conv = e.Result.Conversation // continue from here on the next Run
			fmt.Printf("\n(%d tokens)\n", goodall.TokensSpent(e.Result.Usage))
		case goodall.Stopped:
			fmt.Printf("\nstopped: %s\n", e.Message)
		}
	}
	_ = conv
}
