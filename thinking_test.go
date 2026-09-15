package goodall

import (
	"encoding/json/v2"
	"testing"
)

func TestEffortString(t *testing.T) {
	cases := []struct {
		in   Effort
		want string
	}{
		{EffortDefault, "default"},
		{Effort(""), "default"},
		{EffortOff, "off"},
		{EffortLow, "low"},
		{EffortMedium, "medium"},
		{EffortHigh, "high"},
		{EffortXHigh, "xhigh"},
		{EffortMax, "max"},
		{Effort("ludicrous"), "ludicrous"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("Effort(%q).String() = %q, want %q", string(c.in), got, c.want)
		}
	}
}

func TestEffortKnown(t *testing.T) {
	for _, e := range []Effort{EffortDefault, EffortOff, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax} {
		if !e.Known() {
			t.Errorf("Effort(%q).Known() = false, want true", string(e))
		}
	}
	for _, e := range []Effort{"ludicrous", "HIGH", "none"} {
		if e.Known() {
			t.Errorf("Effort(%q).Known() = true, want false", string(e))
		}
	}
}

func TestThinkingDisplayString(t *testing.T) {
	cases := []struct {
		in   ThinkingDisplay
		want string
	}{
		{DisplayDefault, "default"},
		{DisplaySummarized, "summarized"},
		{DisplayOmitted, "omitted"},
		{ThinkingDisplay("verbose"), "verbose"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("ThinkingDisplay(%q).String() = %q, want %q", string(c.in), got, c.want)
		}
	}
}

func TestThinkingDisplayKnown(t *testing.T) {
	for _, d := range []ThinkingDisplay{DisplayDefault, DisplaySummarized, DisplayOmitted} {
		if !d.Known() {
			t.Errorf("ThinkingDisplay(%q).Known() = false, want true", string(d))
		}
	}
	if ThinkingDisplay("verbose").Known() {
		t.Error(`ThinkingDisplay("verbose").Known() = true, want false`)
	}
}

func TestThinkingJSON(t *testing.T) {
	cases := []struct {
		name string
		in   ThinkingConfig
		want string
	}{
		{"zero", ThinkingConfig{}, `{}`},
		{"effort only", ThinkingConfig{Effort: EffortHigh}, `{"effort":"high"}`},
		{"display only", ThinkingConfig{Display: DisplaySummarized}, `{"display":"summarized"}`},
		{"both", ThinkingConfig{Effort: EffortMax, Display: DisplayOmitted}, `{"effort":"max","display":"omitted"}`},
		{"off", ThinkingConfig{Effort: EffortOff}, `{"effort":"off"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("marshal = %s, want %s", got, c.want)
			}
			var back ThinkingConfig
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
			if back != c.in {
				t.Fatalf("round trip = %+v, want %+v", back, c.in)
			}
		})
	}
}

// TestThinkingUnknownEffortSurvivesJSON keeps an effort ladder rung goodall has
// not heard of readable rather than silently rewriting it to the default.
func TestThinkingUnknownEffortSurvivesJSON(t *testing.T) {
	const wire = `{"effort":"ludicrous"}`
	var got ThinkingConfig
	if err := json.Unmarshal([]byte(wire), &got); err != nil {
		t.Fatal(err)
	}
	if got.Effort != Effort("ludicrous") {
		t.Fatalf("decoded effort = %q, want %q", string(got.Effort), "ludicrous")
	}
	if got.Effort.Known() {
		t.Error("an unrecognized effort reports Known() = true")
	}
	back, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != wire {
		t.Fatalf("re-encoded = %s, want %s", back, wire)
	}
}
