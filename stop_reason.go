package goodall

// StopReason is why the model stopped generating. It is a string so that a
// reason goodall has never heard of reaches the consumer verbatim instead of
// being flattened into a catch-all: providers map their own wire strings onto
// the constants below and pass anything else through as StopReason(raw).
//
// The loop acts on the reason: StopToolUse runs the tools, StopPauseTurn
// resends the request unchanged, and every other reason — including one it
// does not recognize — ends the turn.
type StopReason string

const (
	// StopNone is the zero value: the turn carried no stop reason, because
	// it is still streaming or was cut short.
	StopNone StopReason = ""
	// StopEndTurn is a finished answer.
	StopEndTurn StopReason = "end_turn"
	// StopMaxTokens is the output limit. A tool call cut off here is not
	// runnable, since a truncated input can still parse.
	StopMaxTokens StopReason = "max_tokens"
	// StopSequence is one of the request's stop sequences.
	StopSequence StopReason = "stop_sequence"
	// StopToolUse is the model asking for tool results.
	StopToolUse StopReason = "tool_use"
	// StopPauseTurn is a long-running turn the provider asks to have
	// resent unchanged to continue.
	StopPauseTurn StopReason = "pause_turn"
	// StopRefusal is the model declining. The turn's tool calls, if any,
	// are never run.
	StopRefusal StopReason = "refusal"
)

// String names the stop reason, calling the zero value "none". A reason from
// the wire that goodall does not define prints as it arrived.
func (r StopReason) String() string {
	if r == StopNone {
		return "none"
	}
	return string(r)
}

// Known reports whether this is one of the stop reasons goodall defines. A
// false answer means the provider sent something new, which is worth
// surfacing rather than assuming.
func (r StopReason) Known() bool {
	switch r {
	case StopNone, StopEndTurn, StopMaxTokens, StopSequence, StopToolUse, StopPauseTurn, StopRefusal:
		return true
	}
	return false
}
