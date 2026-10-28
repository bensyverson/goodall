package claudename

import "testing"

func TestVersion(t *testing.T) {
	cases := []struct {
		model        string
		major, minor int
		ok           bool
	}{
		{"claude-opus-5-5", 5, 5, true},
		{"anthropic/claude-opus-5.5", 5, 5, true},
		{"claude-opus-5-20260214", 5, 0, true},
		{"claude-3-7-sonnet-20250219", 3, 7, true},
		{"us.anthropic.claude-sonnet-4-5-v1:0", 4, 5, true},
		{"anthropic/claude-haiku-4.5", 4, 5, true},
		{"claude-opus-latest", 0, 0, false},
	}
	for _, c := range cases {
		major, minor, ok := Version(c.model)
		if major != c.major || minor != c.minor || ok != c.ok {
			t.Errorf("Version(%q) = %d, %d, %v; want %d, %d, %v", c.model, major, minor, ok, c.major, c.minor, c.ok)
		}
	}
}

// TestRejectsForcedToolChoice follows the live probes of 2026-10-28:
// Opus 5.5 and Sonnet 5.5 answered tool_choice "any" and "tool" with a 400 on
// both providers, while Opus 5 and Haiku 5.5 accepted both.
func TestRejectsForcedToolChoice(t *testing.T) {
	cases := map[string]bool{
		"claude-opus-5-5":              true,
		"claude-sonnet-5-5":            true,
		"claude-sonnet-5-5-20260928":   true,
		"us.anthropic.claude-opus-5-5": true,
		"anthropic/claude-opus-5.5":    true,
		"anthropic/claude-sonnet-5.5":  true,
		"claude-opus-6":                true,
		"claude-opus-latest":           true,
		"claude-fable-5-1":             true,
		"claude-mythos-5-1":            true,
		"claude-haiku-5-5":             false,
		"anthropic/claude-haiku-5.5":   false,
		"claude-opus-5":                false,
		"anthropic/claude-opus-5":      false,
		"claude-sonnet-5":              false,
		"claude-fable-5":               false,
		"claude-opus-4-6":              false,
		"claude-3-7-sonnet-20250219":   false,
		"openai/gpt-6-astra":           false,
	}
	for model, want := range cases {
		if got := RejectsForcedToolChoice(model); got != want {
			t.Errorf("RejectsForcedToolChoice(%q) = %v, want %v", model, got, want)
		}
	}
}
