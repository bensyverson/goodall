package liveprobe

import (
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

func TestARunThatCallsTheToolAndFinishesPasses(t *testing.T) {
	provider := &fake.Provider{Script: []fake.Turn{
		fake.Answer(goodall.StopToolUse, goodall.ToolUse{ID: "t1", Name: "get_weather", Input: []byte(`{"city":"Paris"}`)}),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "Sunny."}),
	}}
	got := AgentLoop(t.Context(), provider, "m", goodall.EffortLow)
	if !got.Passed() {
		t.Fatalf("got %s", got)
	}
	if got.Turns != 2 || got.ToolCalls != 1 {
		t.Errorf("turns %d, tool calls %d", got.Turns, got.ToolCalls)
	}
}

// A model answering from memory finishes cleanly and proves nothing about taking a
// tool result back on a second turn, which is the point of the probe.
func TestARunThatNeverCallsTheToolDoesNotPass(t *testing.T) {
	provider := &fake.Provider{Script: []fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "Sunny."})}}
	got := AgentLoop(t.Context(), provider, "m", goodall.EffortLow)
	if got.Passed() {
		t.Fatalf("a run with no tool call passed: %s", got)
	}
	if !strings.Contains(got.String(), "no tool call") {
		t.Errorf("the row does not say why: %s", got)
	}
}

func TestAStoppedRunDoesNotPassAndCarriesTheMessage(t *testing.T) {
	provider := &fake.Provider{Script: []fake.Turn{{Err: &goodall.APIError{Status: 400, Kind: goodall.KindInvalidRequest, Message: "use /v1/responses"}}}}
	got := AgentLoop(t.Context(), provider, "m", goodall.EffortLow)
	if got.Passed() {
		t.Fatalf("a refused run passed: %s", got)
	}
	if !strings.Contains(got.String(), "use /v1/responses") {
		t.Errorf("the row lost the refusal: %s", got)
	}
}

// Only reasoning on a turn before the last gets replayed, so a total cannot say
// whether a run's second turn carried any.
func TestARowSaysWhichTurnReasoned(t *testing.T) {
	got := Result{Finished: true, Turns: 2, ToolCalls: 1,
		Usage:         goodall.Usage{Input: 1, Output: 2, Reasoning: 45},
		TurnReasoning: []int{45, 0}}
	if !strings.Contains(got.String(), "reasoning=45 by_turn=[45 0]") {
		t.Errorf("row = %s", got)
	}
}
