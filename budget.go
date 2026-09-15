package goodall

import "time"

// DefaultMaxTurns is how many model calls a run makes before its turn budget
// stops it, when the Budget does not say. It is long enough for a tool-using
// answer and finite by construction.
const DefaultMaxTurns = 20

// Budget bounds one run. Every limit is optional except the turn limit, which
// always applies: a loop with no ceiling on model calls is one bug away from
// spending a whole account, so there is no unlimited mode. A zero or negative
// MaxTurns is DefaultMaxTurns rather than "as many as it takes".
//
// Passing a budget is how a caller decides what a run may spend; the run
// reports which limit it met as the StopCause on its terminal event, so a
// caller can tell "the model finished" from "we ran out of room".
type Budget struct {
	// MaxTurns is how many model calls the run may make. Zero is
	// DefaultMaxTurns.
	MaxTurns int
	// MaxTokens is how many tokens the whole run may spend, counted by
	// TokensSpent over every turn. Zero is no limit.
	MaxTokens int
	// Timeout is the wall clock the run has, from the first turn to the
	// terminal event. Zero is no limit, leaving the caller's context as
	// the only clock.
	Timeout time.Duration
}

// Turns is the turn limit this budget enforces, which is DefaultMaxTurns
// unless the budget set a positive one.
func (b Budget) Turns() int {
	if b.MaxTurns < 1 {
		return DefaultMaxTurns
	}
	return b.MaxTurns
}

// TokensSpent is what a run's usage counts against a token budget: the whole
// prompt — cache reads and writes are prompt tokens too — plus everything
// generated. Reasoning tokens are already part of the output count, so adding
// them would charge for them twice.
func TokensSpent(u Usage) int {
	return u.TotalInput() + u.Output
}

// OverTokens reports whether the usage has passed the budget's token limit. A
// budget with no token limit is never over it.
func (b Budget) OverTokens(u Usage) bool {
	return b.MaxTokens > 0 && TokensSpent(u) > b.MaxTokens
}
