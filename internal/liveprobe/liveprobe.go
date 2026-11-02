// Package liveprobe runs goodall's real agent loop against a model and reports
// whether it can do what an agent needs: think, call a tool, take the result back
// on a second turn and finish.
//
// The second turn is the point. Models that carry signed reasoning refuse a
// follow-up whose history lost it, so a single-turn check passes while every real
// agent run fails. It goes through [goodall.Agent], the production path, and never
// hand-builds a request. scripts/probe-agent-loop and scripts/check-drift share it.
package liveprobe

import (
	"context"
	"fmt"
	"strings"

	"github.com/bensyverson/goodall"
)

const (
	maxTokens = 2000
	// prompt asks for arithmetic before the tool, because a bare weather question
	// drew no reasoning from GPT-6 at low effort, and a run with no reasoning passes
	// with nothing replayed on its second turn.
	prompt = "A cyclist in Paris will ride 137 km at 23.4 km/h with two 18-minute stops, starting at 13:47. " +
		"Work out to the minute when they finish, use the tool to look up the weather there, " +
		"then answer in one short sentence whether they should expect rain."
	// messageLimit keeps a provider's message on its row.
	messageLimit = 240
)

// weatherInput is the probe tool's input.
type weatherInput struct {
	City string `json:"city" desc:"the city to look up"`
}

// Result is what one run did.
type Result struct {
	// Finished is whether the run reached [goodall.Done].
	Finished bool
	// Turns counts completed model turns.
	Turns int
	// ToolCalls counts how often the tool ran.
	ToolCalls int
	// Usage is the run's token use, known only for a finished run.
	Usage goodall.Usage
	// Cost is what the run cost, known only for a finished run.
	Cost goodall.Cost
	// TurnReasoning is each completed turn's reasoning tokens, in order.
	TurnReasoning []int
	// Failure says what stopped the run, empty for one that finished.
	Failure string
}

// Passed reports whether the run called the tool and finished. A model that answers
// from memory also finishes, and proves nothing about the second turn.
func (r Result) Passed() bool { return r.Finished && r.ToolCalls > 0 }

// String renders the run as one row.
func (r Result) String() string {
	switch {
	case r.Passed():
		return fmt.Sprintf("ok turns=%d tool_calls=%d in=%d out=%d reasoning=%d by_turn=%v cost=%s reported=%v",
			r.Turns, r.ToolCalls, r.Usage.Input, r.Usage.Output, r.Usage.Reasoning, r.TurnReasoning, r.Cost.Amount, r.Cost.Reported)
	case r.Finished:
		return fmt.Sprintf("FAIL turns=%d: finished with no tool call", r.Turns)
	default:
		return fmt.Sprintf("FAIL turns=%d tool_calls=%d %s", r.Turns, r.ToolCalls, r.Failure)
	}
}

// AgentLoop drives one agent to a terminal event.
func AgentLoop(ctx context.Context, provider goodall.Provider, model string, effort goodall.Effort) Result {
	var result Result
	weather, err := goodall.NewTool("get_weather", "Get the weather for a city",
		func(_ context.Context, in weatherInput) (goodall.ToolResult, error) {
			result.ToolCalls++
			return goodall.TextResult("Sunny, 21C in " + in.City), nil
		})
	if err != nil {
		result.Failure = "tool: " + err.Error()
		return result
	}
	agent := &goodall.Agent{
		Provider:  provider,
		Model:     model,
		Tools:     []goodall.Tool{weather},
		Thinking:  goodall.ThinkingConfig{Effort: effort},
		MaxTokens: maxTokens,
	}
	var conv goodall.Conversation
	for ev, err := range agent.Run(ctx, conv, goodall.Text{Text: prompt}) {
		if err != nil {
			result.Failure = "iterator: " + clip(err.Error())
			return result
		}
		switch e := ev.(type) {
		case goodall.TurnEnd:
			result.Turns++
			result.TurnReasoning = append(result.TurnReasoning, e.Response.Usage.Reasoning)
		case goodall.Done:
			result.Finished = true
			result.Usage = e.Result.Usage
			result.Cost = e.Result.Cost
			return result
		case goodall.Stopped:
			result.Failure = fmt.Sprintf("cause=%s kind=%s: %s", e.Cause, e.Kind, clip(e.Message))
			return result
		}
	}
	result.Failure = "no terminal event"
	return result
}

// clip keeps a provider's message on its row.
func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > messageLimit {
		return s[:messageLimit] + "…"
	}
	return s
}
