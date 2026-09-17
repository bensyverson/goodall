package goodall_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
	"github.com/bensyverson/goodall/openrouter"
)

// Delegation: a conversational agent hands a research question to a second
// agent on another provider and reads its answer as a tool result. The child
// keeps its own model, system prompt, tools and budget, and runs on the
// context the parent's tool call was given, so one cancellation ends both.
// It compiles with the suite and is not run, because it needs keys.
func ExampleAgentTool() {
	ctx := context.Background()

	researcher := &goodall.Agent{
		Provider: openrouter.New(openrouter.WithAPIKey(os.Getenv("OPENROUTER_API_KEY"))),
		Model:    "openai/gpt-5",
		System:   "You research one question at a time and answer in a short paragraph, with dates.",
		Budget:   goodall.Budget{MaxTurns: 6},
	}

	research, err := goodall.AgentTool(researcher, "research",
		"Hand one self-contained research question to a specialist agent and read its answer. "+
			"It sees none of this conversation, so write the question in full.")
	if err != nil {
		log.Fatal(err)
	}

	assistant := &goodall.Agent{
		Provider: anthropic.New(os.Getenv("ANTHROPIC_API_KEY")),
		Model:    "claude-opus-5",
		System:   "You are a terse editor. Delegate research and cite what comes back.",
		Tools:    []goodall.Tool{research},
	}

	for ev, _ := range assistant.Run(ctx, goodall.Conversation{}, goodall.Text{Text: "When did Jane Goodall reach Gombe, and how old was she?"}) {
		switch e := ev.(type) {
		case goodall.TextDelta:
			fmt.Print(e.Text)
		case goodall.ToolCallStart:
			fmt.Printf("\n[delegating: %s]\n", e.ToolUse.Input)
		case goodall.Stopped:
			fmt.Printf("\nstopped: %s\n", e.Message)
		}
	}
}
