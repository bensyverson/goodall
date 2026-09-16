package goodall_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// committedTurns is every TurnCommitted event in a run, which is what these
// tests read instead of the whole sequence.
func committedTurns(events []goodall.Event) []goodall.TurnCommitted {
	var out []goodall.TurnCommitted
	for _, ev := range events {
		if committed, ok := ev.(goodall.TurnCommitted); ok {
			out = append(out, committed)
		}
	}
	return out
}

// TestTurnCommittedCarriesTheCallersInput is the criterion: the turn the run
// appends is an event, so a subscriber that missed the call can still render
// the question. It arrives before the first provider event, which is what
// "at the moment of commit" means on the wire.
func TestTurnCommittedCarriesTheCallersInput(t *testing.T) {
	a, _ := agentFor([]fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "hi"})})

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})

	committed := committedTurns(events)
	if len(committed) != 1 {
		t.Fatalf("the run emitted %d turn_committed events, want one: %v", len(committed), eventTypes(events))
	}
	want := goodall.TurnCommitted{Turn: 1, Message: goodall.UserMessage(goodall.Text{Text: "say hi"})}
	if !reflect.DeepEqual(committed[0], want) {
		t.Errorf("the committed turn is\n\t%#v\nwant\n\t%#v", committed[0], want)
	}

	kinds := eventTypes(events)
	at := slices.Index(kinds, goodall.EventTurnCommitted)
	first := slices.Index(kinds, goodall.EventMessageStart)
	if at < 0 || first < 0 || at > first {
		t.Errorf("turn_committed is at %d and the first provider event at %d, want the commit first: %v", at, first, kinds)
	}
	if start := slices.Index(kinds, goodall.EventTurnStart); start > at {
		t.Errorf("turn_committed arrived before turn_start: %v", kinds)
	}
}

// TestTurnCommittedCarriesTheHooksEdit is what "committed" buys over "sent":
// the event carries the turn as BeforeSend left it, which is the message that
// really entered the conversation.
func TestTurnCommittedCarriesTheHooksEdit(t *testing.T) {
	a, _ := agentFor([]fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "done"})})
	a.Hooks.BeforeSend = func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message) error {
		newTurn.Content = goodall.Blocks{goodall.Text{Text: "shaped by the hook"}}
		return nil
	}

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "raw"})

	committed := committedTurns(events)
	if len(committed) != 1 {
		t.Fatalf("the run emitted %d turn_committed events, want one", len(committed))
	}
	if got := committed[0].Message.Text(); got != "shaped by the hook" {
		t.Errorf("the committed turn says %q, want the hook's edit", got)
	}
}

// TestTurnCommittedOnEveryToolResultsTurn covers the loop's own auto-run: the
// results it assembles are a user turn like any other, so they are committed
// and announced under the turn that sent them.
func TestTurnCommittedOnEveryToolResultsTurn(t *testing.T) {
	a, _ := agentFor([]fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_1", "echo", `{"text":"hi"}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "It said hi."}),
	}, echoTool(t))

	events := runEvents(t, a, goodall.Conversation{}, goodall.Text{Text: "say hi"})

	committed := committedTurns(events)
	if len(committed) != 2 {
		t.Fatalf("the run emitted %d turn_committed events, want one per user turn", len(committed))
	}
	if committed[0].Turn != 1 || committed[1].Turn != 2 {
		t.Errorf("the committed turns are numbered %d and %d, want 1 and 2", committed[0].Turn, committed[1].Turn)
	}
	second := committed[1].Message
	if second.Role != goodall.RoleUser {
		t.Errorf("the tool results turn is from %q, want the user", second.Role)
	}
	results := toolResults(second)
	if len(results) != 1 || results[0].ToolUseID != "tu_1" || results[0].Text() != "echo: hi" {
		t.Errorf("the committed results are %+v, want the echo tool's one result", results)
	}
}

// TestResumeEmitsTheCommittedResultsTurn is the approval path: the results the
// caller handed back are appended by the same commit, so they are announced the
// same way.
func TestResumeEmitsTheCommittedResultsTurn(t *testing.T) {
	a, _ := agentFor([]fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "thanks"})})
	conv := goodall.Conversation{}.Append(
		goodall.UserMessage(goodall.Text{Text: "say hi"}),
		goodall.AssistantMessage(fake.Use("tu_1", "echo", `{"text":"hi"}`)),
	)

	events := resumeEvents(t, a, conv, goodall.ToolResult{
		ToolUseID: "tu_1",
		Content:   goodall.Blocks{goodall.Text{Text: "the human said yes"}},
	})

	committed := committedTurns(events)
	if len(committed) != 1 {
		t.Fatalf("the resumed run emitted %d turn_committed events, want one", len(committed))
	}
	want := goodall.TurnCommitted{Turn: 1, Message: goodall.UserMessage(goodall.ToolResult{
		ToolUseID: "tu_1",
		Content:   goodall.Blocks{goodall.Text{Text: "the human said yes"}},
	})}
	if !reflect.DeepEqual(committed[0], want) {
		t.Errorf("the committed turn is\n\t%#v\nwant\n\t%#v", committed[0], want)
	}
}

// TestNoTurnCommittedWithoutANewTurn is the other half of the rule: a run that
// carries on from the conversation as it stands appends nothing, so it
// announces nothing.
func TestNoTurnCommittedWithoutANewTurn(t *testing.T) {
	a, _ := agentFor([]fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "sure"})})
	conv := goodall.Conversation{}.Append(
		goodall.UserMessage(goodall.Text{Text: "hello"}),
		goodall.AssistantMessage(goodall.Text{Text: "hi"}),
		goodall.UserMessage(goodall.Text{Text: "again"}),
	)

	events := runEvents(t, a, conv)

	if committed := committedTurns(events); len(committed) != 0 {
		t.Errorf("a continuation emitted %d turn_committed events, want none: %#v", len(committed), committed)
	}
}
