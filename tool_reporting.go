package goodall

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
)

// Reporter is the optional interface a [Tool] implements when it has something
// to say while it runs, or tokens and money of its own to declare. It is the
// house pattern of [Completer] and [ModelLister]: the loop asks a tool whether
// it implements this, and runs the ones that do through ExecuteReporting
// instead of [Tool.Execute].
//
// The name is the tool's part of the bargain — it reports — and the method is
// ExecuteReporting rather than a second Execute because an implementation is a
// Tool as well, and two methods of one name cannot live on one type. The three
// values a call produces travel in a [ToolOutcome] rather than as three returns
// so that a later fact about a call can be added without changing every
// implementation's signature.
//
// It is how an [AgentTool] puts a delegated run's events on the parent's stream
// and how the typesafe judgment tools declare what a judgment cost; a tool that
// only computes something has no use for it and implements [Tool] alone.
type Reporter interface {
	// ExecuteReporting runs the tool, reporting events as it goes, and
	// returns what the call produced and what it spent.
	//
	// report may be called any number of times, from any goroutine, for as
	// long as the call is running: each event reaches the run's stream
	// wrapped in a [ToolEvent] naming this call, in the order it was
	// reported and before this call's [ToolCallEnd]. It never blocks for
	// long and never panics — a report the run cannot deliver, because the
	// consumer stopped reading or because the tool has already returned, is
	// dropped — so a tool may report freely without reasoning about the
	// consumer at all. A nil event is ignored.
	//
	// The result, the error and the ToolUseID work exactly as [Tool.Execute]
	// describes them: a result with IsError for what the model should
	// correct, an error for a failure of the tool itself, and the loop fills
	// in the id.
	ExecuteReporting(ctx context.Context, input jsontext.Value, report func(Event)) (ToolOutcome, error)
}

// ToolOutcome is what one call of a [Reporter] produced: the result the model
// reads, and what the call itself spent. Usage and Cost travel to the consumer
// on the call's [ToolCallEnd] and are not rolled into the run's own totals; a
// zero of either means "nothing reported", never "free".
type ToolOutcome struct {
	// Result is the result the model reads, as [Tool.Execute] returns it.
	Result ToolResult
	// Usage is the tokens this call spent, zero when the tool spends none
	// or knows none.
	Usage Usage
	// Cost is the money this call spent, unreported when the tool's own
	// provider reports no figure.
	Cost Cost
}

// NewReportingTool builds a [Reporter] from a typed handler, with the schema
// inference, the input checking and the error messages [NewTool] gives — the
// same constructor, for a tool that reports while it runs.
//
// The handler is handed the report function the interface describes; a handler
// that reports nothing and only declares what it spent passes it by. The Tool
// it returns implements Reporter, so the loop runs it through ExecuteReporting;
// a direct caller who reaches for [Tool.Execute] instead gets the result with
// the reports dropped and the accounting left on the floor, because Execute has
// nowhere to put either.
//
// It returns an error on the same definitions [NewTool] refuses:
//
//	tool, err := goodall.NewReportingTool("research", "Hand a question to a specialist.",
//	    func(ctx context.Context, in struct {
//	        Prompt string `json:"prompt" desc:"the question to research"`
//	    }, report func(goodall.Event)) (goodall.ToolOutcome, error) {
//	        …
//	    })
func NewReportingTool[In any](name, description string, run func(context.Context, In, func(Event)) (ToolOutcome, error)) (Tool, error) {
	if err := validateToolName(name); err != nil {
		return nil, err
	}
	if description == "" {
		return nil, fmt.Errorf("goodall: tool %q has no description; the description is what tells the model when to call it", name)
	}
	if run == nil {
		return nil, fmt.Errorf("goodall: tool %q has no handler function", name)
	}
	def, err := newToolDefinition[In](name, description)
	if err != nil {
		return nil, err
	}
	return &reportingTool[In]{toolDefinition: def, run: run}, nil
}

// reportingTool is the Tool NewReportingTool returns. It shares every part of
// a typed tool but the handler, so a reporting tool and a plain one are
// described to the model identically.
type reportingTool[In any] struct {
	toolDefinition[In]
	run func(context.Context, In, func(Event)) (ToolOutcome, error)
}

// ExecuteReporting decodes the model's arguments into In and runs the handler
// with the report function. A bad input comes back as an error result with
// nothing spent, exactly as [typedTool.Execute] describes it.
func (t *reportingTool[In]) ExecuteReporting(ctx context.Context, input jsontext.Value, report func(Event)) (ToolOutcome, error) {
	in, bad := t.decode(input)
	if bad != nil {
		return ToolOutcome{Result: *bad}, nil
	}
	return t.run(ctx, in, report)
}

// Execute is the plain path, for a caller that runs the tool itself rather
// than through a run: the handler's reports are discarded and so is what it
// says it spent, because a ToolResult has nowhere to carry either.
func (t *reportingTool[In]) Execute(ctx context.Context, input jsontext.Value) (ToolResult, error) {
	outcome, err := t.ExecuteReporting(ctx, input, func(Event) {})
	return outcome.Result, err
}
