package goodall

import (
	"encoding/json/v2"
	"testing"
)

func TestStopReasonString(t *testing.T) {
	cases := []struct {
		in   StopReason
		want string
	}{
		{StopNone, "none"},
		{StopReason(""), "none"},
		{StopEndTurn, "end_turn"},
		{StopMaxTokens, "max_tokens"},
		{StopSequence, "stop_sequence"},
		{StopToolUse, "tool_use"},
		{StopPauseTurn, "pause_turn"},
		{StopRefusal, "refusal"},
		{StopReason("banana"), "banana"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("StopReason(%q).String() = %q, want %q", string(c.in), got, c.want)
		}
	}
}

func TestStopReasonKnown(t *testing.T) {
	known := []StopReason{StopNone, StopEndTurn, StopMaxTokens, StopSequence, StopToolUse, StopPauseTurn, StopRefusal}
	for _, r := range known {
		if !r.Known() {
			t.Errorf("StopReason(%q).Known() = false, want true", string(r))
		}
	}
	for _, r := range []StopReason{"banana", "END_TURN", "tool_use "} {
		if r.Known() {
			t.Errorf("StopReason(%q).Known() = true, want false", string(r))
		}
	}
}

// TestStopReasonUnknownSurvivesJSON is invariant 6: a stop reason goodall does
// not recognise reaches the consumer verbatim rather than being dropped or
// folded into a catch-all.
func TestStopReasonUnknownSurvivesJSON(t *testing.T) {
	type envelope struct {
		Stop StopReason `json:"stop_reason"`
	}
	const wire = `{"stop_reason":"banana"}`
	var got envelope
	if err := json.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatal(err)
	}
	if got.Stop != StopReason("banana") {
		t.Fatalf("decoded stop reason = %q, want %q", string(got.Stop), "banana")
	}
	if got.Stop.Known() {
		t.Error("an unrecognised stop reason reports Known() = true")
	}
	back, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != wire {
		t.Fatalf("re-encoded = %s, want %s", back, wire)
	}
}
