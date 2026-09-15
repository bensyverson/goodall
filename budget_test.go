package goodall

import (
	"testing"
	"time"
)

// TestBudgetTurns pins the defaulting rule: the zero value is
// DefaultMaxTurns, and there is no unlimited mode, so a nonsense value gets
// the default rather than a run that never ends.
func TestBudgetTurns(t *testing.T) {
	cases := map[int]int{
		0:   DefaultMaxTurns,
		1:   1,
		7:   7,
		-1:  DefaultMaxTurns,
		-99: DefaultMaxTurns,
	}
	for set, want := range cases {
		if got := (Budget{MaxTurns: set}).Turns(); got != want {
			t.Errorf("Budget{MaxTurns: %d}.Turns() = %d, want %d", set, got, want)
		}
	}
}

// TestTokensSpent checks the accounting the token budget is measured in: the
// whole prompt, cache reads and writes included, plus what was generated.
func TestTokensSpent(t *testing.T) {
	u := Usage{Input: 10, Output: 5, CacheRead: 100, CacheWrite: 20, Reasoning: 3}
	if got, want := TokensSpent(u), 135; got != want {
		t.Errorf("TokensSpent(%+v) = %d, want %d", u, got, want)
	}
}

// TestBudgetOverTokens checks that a zero MaxTokens is no limit at all, and
// that the limit is exceeded only once it is passed.
func TestBudgetOverTokens(t *testing.T) {
	spent := Usage{Input: 60, Output: 40}
	cases := []struct {
		max  int
		want bool
	}{
		{max: 0, want: false},
		{max: -1, want: false},
		{max: 101, want: false},
		{max: 100, want: false},
		{max: 99, want: true},
	}
	for _, c := range cases {
		if got := (Budget{MaxTokens: c.max}).OverTokens(spent); got != c.want {
			t.Errorf("Budget{MaxTokens: %d}.OverTokens(100 spent) = %v, want %v", c.max, got, c.want)
		}
	}
}

// TestBudgetZeroTimeout confirms the zero Timeout is no deadline, which is
// what lets a caller hold the only clock.
func TestBudgetZeroTimeout(t *testing.T) {
	if (Budget{}).Timeout != 0 {
		t.Error("the zero Budget has a timeout")
	}
	if got := (Budget{Timeout: time.Second}).Timeout; got != time.Second {
		t.Errorf("Timeout = %v, want 1s", got)
	}
}
