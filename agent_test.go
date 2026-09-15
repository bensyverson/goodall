package goodall_test

import (
	"context"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// collect drains a run stream, returning the events and the first error. It
// returns rather than failing so the caller does the reporting: calling
// t.Fatal inside the body of a range-over-func unwinds the iterator from the
// wrong place.
func collect(s goodall.Stream) ([]goodall.Event, error) {
	var out []goodall.Event
	for ev, err := range s {
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
	return out, nil
}

// eventTypes is the shape of a run, which is what most of these tests assert
// on rather than the events' contents.
func eventTypes(events []goodall.Event) []goodall.EventType {
	out := make([]goodall.EventType, len(events))
	for i, ev := range events {
		out[i] = ev.Type()
	}
	return out
}

// runEvents drives a run to its end and fails the test if the stream yielded
// an error, which it never should: the terminal event is the contract.
func runEvents(t *testing.T, a *goodall.Agent, conv goodall.Conversation, input ...goodall.Block) []goodall.Event {
	t.Helper()
	events, err := collect(a.Run(t.Context(), conv, input...))
	if err != nil {
		t.Fatalf("the run stream yielded an error, which it must never do: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("the run emitted no events at all")
	}
	return events
}

// terminalStop is the run's Stopped event, and fails the test when the run
// ended some other way.
func terminalStop(t *testing.T, events []goodall.Event) goodall.Stopped {
	t.Helper()
	last := events[len(events)-1]
	stopped, ok := last.(goodall.Stopped)
	if !ok {
		t.Fatalf("the run ended with %T, want a Stopped: %v", last, eventTypes(events))
	}
	return stopped
}

// terminalDone is the run's Done event, and fails the test when the run ended
// some other way.
func terminalDone(t *testing.T, events []goodall.Event) goodall.Done {
	t.Helper()
	last := events[len(events)-1]
	done, ok := last.(goodall.Done)
	if !ok {
		if stopped, isStop := last.(goodall.Stopped); isStop {
			t.Fatalf("the run stopped (%s: %s), want Done", stopped.Cause, stopped.Message)
		}
		t.Fatalf("the run ended with %T, want a Done: %v", last, eventTypes(events))
	}
	return done
}

// toolResults is every tool result in a message, which is how these tests read
// what the loop sent back to the model.
func toolResults(m goodall.Message) []goodall.ToolResult {
	var out []goodall.ToolResult
	for _, b := range m.Content {
		if r, ok := b.(goodall.ToolResult); ok {
			out = append(out, r)
		}
	}
	return out
}

// echoTool answers with the text it was given, and is the well-behaved tool
// most of these tests call.
func echoTool(t *testing.T) goodall.Tool {
	t.Helper()
	tool, err := goodall.NewTool("echo", "Echo the text back to the model.", func(ctx context.Context, in struct {
		Text string `json:"text" desc:"the text to echo"`
	}) (goodall.ToolResult, error) {
		return goodall.TextResult("echo: " + in.Text), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

// agentFor is an agent over a script, with the given tools.
func agentFor(script []fake.Turn, tools ...goodall.Tool) (*goodall.Agent, *fake.Provider) {
	p := &fake.Provider{Script: script}
	return &goodall.Agent{Provider: p, Model: "test-model", Tools: tools}, p
}
