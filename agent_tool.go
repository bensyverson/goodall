package goodall

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// delegatedTask is the input schema of an [AgentTool]. The description is
// where the instruction belongs rather than in the parent's system prompt: the
// model reads it at the moment it decides what to write.
type delegatedTask struct {
	Prompt string `json:"prompt" desc:"the task or question for the other agent, written in full: it starts a fresh conversation and sees nothing of this one"`
}

// AgentTool wraps an Agent as a Tool, so a model can hand a task to another
// agent — another model, another provider, its own system prompt, tools and
// budget — and read its final answer as a tool result. The parent sees one
// tool call; the child sees one user message, the prompt the parent wrote.
//
// The child runs on the context [Tool.Execute] was given, so the parent's
// cancellation and its deadline reach the delegated run. Everything else is
// the child's own: its budget bounds it, its hooks shape it, and its
// conversation starts empty, because a child that inherited the parent's
// history would be a second copy of it rather than a delegate.
//
// A child that finishes answers with its final assistant message's text
// blocks, joined by a blank line; one that finished without writing anything
// answers with an empty result, since the delegation itself worked. A child
// that ended early — a spent budget, a cancellation, a refusal, a provider
// failure — comes back as a result with IsError set, naming the cause and
// carrying the child's own message, so the parent model can rephrase the task,
// try something else or tell the person. A run that ends in neither is the
// only Go error this tool returns, and the loop turns that into an error
// result too.
//
// The delegation is visible while it runs: the tool implements [Reporter], so
// every event of the child run reaches the parent's stream wrapped in a
// [ToolEvent] naming this call, and a UI watching the parent can draw the
// delegate's turns, text and tool calls under the call that started them. What
// the child spent travels on the call's [ToolCallEnd] as the child's own
// Result.Usage and Result.Cost — per call, not rolled into the parent's totals,
// which stay the parent model's own tokens; the reason is the
// "Ruled 2026-09-17, nested events and per-call accounting" block quote in
// project/2026-09-14-architecture-plan.md.
//
// A hook inside the child that defers a tool call for approval is reported as
// an error result too: a nested pause has no path back to the parent's caller,
// so there is nobody to approve it.
//
// It returns an error, never a panic, for a definition no provider would
// accept — the naming rules are [NewTool]'s — and for a nil child:
//
//	research, err := goodall.AgentTool(researcher, "research",
//	    "Hand one self-contained research question to a specialist agent.")
func AgentTool(child *Agent, name, description string) (Tool, error) {
	// The name is checked first so that a call with two mistakes reports
	// the same one NewTool would.
	if err := validateToolName(name); err != nil {
		return nil, err
	}
	if child == nil {
		return nil, fmt.Errorf("goodall: tool %q has no child agent to delegate to", name)
	}
	// The child is copied because an Agent holds no per-run state
	// (invariant 12): a caller that goes on editing its own struct cannot
	// change the model, the tools or the budget of a delegation already
	// under way.
	delegate := *child
	return NewReportingTool(name, description, func(ctx context.Context, in delegatedTask, report func(Event)) (ToolOutcome, error) {
		return runDelegate(ctx, &delegate, name, in.Prompt, report)
	})
}

// runDelegate runs one delegated conversation to its terminal event, forwards
// every event of it to the parent's stream, and turns the terminal event into
// the result the parent model reads and the accounting the call carries.
//
// It reads the event rather than calling [Stream.CollectResult] because the
// Result a terminal event carries does not say which event carried it: the
// cause and the prose of an early ending live on Stopped itself.
//
// Every event is reported, the terminal one included, so a consumer watching
// the parent sees the delegation end as well as begin. The usage and the cost
// are the child's own run totals, which the terminal event carries whichever
// way the run ended: a delegation that broke halfway still spent what it spent.
func runDelegate(ctx context.Context, child *Agent, name, prompt string, report func(Event)) (ToolOutcome, error) {
	var out ToolOutcome
	var terminal bool
	for ev, err := range child.Run(ctx, Conversation{}, Text{Text: prompt}) {
		if err != nil {
			return ToolOutcome{}, err
		}
		report(ev)
		switch e := ev.(type) {
		case Done:
			out, terminal = outcomeOf(TextResult(delegatedAnswer(e.Result)), e.Result), true
		case Stopped:
			out, terminal = outcomeOf(ErrorResult(delegationEndedEarly(name, e)), e.Result), true
		}
	}
	if !terminal {
		return ToolOutcome{}, &ProtocolError{
			Reason: "the delegated run of " + strconv.Quote(name) + " ended without a done or stopped event",
		}
	}
	return out, nil
}

// outcomeOf pairs the result the parent model reads with what the child run
// spent.
func outcomeOf(result ToolResult, child Result) ToolOutcome {
	return ToolOutcome{Result: result, Usage: child.Usage, Cost: child.Cost}
}

// delegatedAnswer is the child's answer as text: the text blocks of its final
// assistant message, joined by a blank line so several blocks read as
// paragraphs. Thinking is left out, because a delegate's reasoning is not part
// of its answer.
func delegatedAnswer(result Result) string {
	if result.Response == nil {
		return ""
	}
	var parts []string
	for _, blk := range result.Response.Message.Content {
		if t, ok := blk.(Text); ok && t.Text != "" {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// delegationEndedEarly is what the parent model reads when the child stopped
// short. It is written for that model, which is expected to act on it, so it
// names the cause in the vocabulary of the event and then says, in the child's
// own words, what happened.
func delegationEndedEarly(name string, e Stopped) string {
	var b strings.Builder
	b.WriteString("The agent ")
	b.WriteString(strconv.Quote(name))
	b.WriteString(" did not finish (")
	b.WriteString(e.Cause.String())
	b.WriteString(")")
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	b.WriteString(".")
	if len(e.Result.Pending) > 0 {
		b.WriteString(" Approval inside a delegated run is not supported yet, so ask it for something it can finish on its own, or raise the ")
		b.WriteString(pendingCalls(len(e.Result.Pending)))
		b.WriteString(" with the person yourself.")
	}
	return b.String()
}

// pendingCalls names how many calls were waiting, since a message that says
// "1 tool calls" reads as a bug to the model as well as to a person.
func pendingCalls(n int) string {
	if n == 1 {
		return "tool call it wanted"
	}
	return strconv.Itoa(n) + " tool calls it wanted"
}
