package goodall

import "testing"

// TestClassifyStop is the stop table: every reason the loop can meet, and what
// it does next. It is an in-package test because the table is internal to the
// loop; the behavior it decides is asserted end to end in run_stop_test.go.
func TestClassifyStop(t *testing.T) {
	cases := []struct {
		name      string
		reason    StopReason
		toolCalls int
		want      turnOutcome
	}{{
		name:      "tool use with calls runs them",
		reason:    StopToolUse,
		toolCalls: 2,
		want:      turnOutcome{Action: actionRunTools},
	}, {
		name:      "tool use with no calls is done",
		reason:    StopToolUse,
		toolCalls: 0,
		want:      turnOutcome{Action: actionDone},
	}, {
		name:   "pause turn resends",
		reason: StopPauseTurn,
		want:   turnOutcome{Action: actionResend},
	}, {
		name:   "end turn is done",
		reason: StopEndTurn,
		want:   turnOutcome{Action: actionDone},
	}, {
		name:   "stop sequence is done",
		reason: StopSequence,
		want:   turnOutcome{Action: actionDone},
	}, {
		name:   "no stop reason is done",
		reason: StopNone,
		want:   turnOutcome{Action: actionDone},
	}, {
		name:      "max tokens stops without running the tools",
		reason:    StopMaxTokens,
		toolCalls: 1,
		want: turnOutcome{
			Action:  actionStop,
			Cause:   StopCauseMaxTokens,
			Message: "the model hit the output limit, so its turn is cut off",
		},
	}, {
		name:      "refusal stops without running the tools",
		reason:    StopRefusal,
		toolCalls: 1,
		want: turnOutcome{
			Action:  actionStop,
			Cause:   StopCauseRefusal,
			Message: "the model declined to answer",
		},
	}, {
		name:   "an unrecognized reason stops the run",
		reason: StopReason("content_filter"),
		want: turnOutcome{
			Action:  actionStop,
			Cause:   StopCauseUnknownStop,
			Message: `the provider stopped the turn for a reason goodall does not know: "content_filter"`,
		},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyStop(c.reason, c.toolCalls); got != c.want {
				t.Errorf("classifyStop(%q, %d) = %+v, want %+v", c.reason, c.toolCalls, got, c.want)
			}
		})
	}
}

// TestClassifyStopCoversEveryKnownReason fails when a stop reason is added to
// the vocabulary and not to the table: an unhandled reason would fall into the
// unknown row and silently end runs that should have continued.
func TestClassifyStopCoversEveryKnownReason(t *testing.T) {
	known := []StopReason{StopNone, StopEndTurn, StopMaxTokens, StopSequence, StopToolUse, StopPauseTurn, StopRefusal}
	for _, r := range known {
		got := classifyStop(r, 1)
		if got.Cause == StopCauseUnknownStop {
			t.Errorf("classifyStop(%q, 1) fell through to the unknown row", r)
		}
	}
}

// TestUnrunReason checks that every path that leaves a tool call unrun has
// text written for the model, since that text becomes the tool_result the
// model reads on its next turn.
func TestUnrunReason(t *testing.T) {
	for _, r := range []StopReason{StopNone, StopEndTurn, StopMaxTokens, StopSequence, StopToolUse, StopPauseTurn, StopRefusal, StopReason("weird")} {
		if got := unrunReason(r); got == "" {
			t.Errorf("unrunReason(%q) is empty", r)
		}
	}
	if unrunEnded == "" {
		t.Error("unrunEnded is empty")
	}
}
