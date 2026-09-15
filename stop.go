package goodall

import "strconv"

// turnAction is what the loop does once a turn has arrived. It is a typed
// constant rather than a pair of booleans because the four outcomes are
// mutually exclusive and the zero value must be the safe one: an action
// nobody set stops the run instead of sending again.
type turnAction int

const (
	// actionStop ends the run with a Stopped carrying the outcome's cause.
	actionStop turnAction = iota
	// actionDone ends the run with a Done.
	actionDone
	// actionRunTools runs the turn's tool calls and sends the results.
	actionRunTools
	// actionResend sends the same request again with the turn appended.
	actionResend
)

// turnOutcome is one row of the stop table: what to do next and, when that is
// to stop, why. Message is written for a person reading a log or a UI, so it
// names what the provider said rather than restating the cause.
type turnOutcome struct {
	Action  turnAction
	Cause   StopCause
	Message string
}

// classifyStop is the stop table: it maps the model's reason for stopping,
// and how many tool calls the turn asked for, onto the loop's next step.
//
// The rows, and why each is what it is:
//
//   - StopToolUse with calls runs them, which is the loop's whole purpose.
//   - StopToolUse with no calls is done: there is nothing to run, and sending
//     again would ask the same question and get the same empty answer.
//   - StopPauseTurn resends the request unchanged, which is what the provider
//     asks for when it interrupts a long turn at a safe boundary.
//   - StopEndTurn and StopSequence are a finished answer.
//   - StopNone is done too. A provider that closed the message without a stop
//     reason has told the loop nothing to act on, and the run's StopReason
//     carries the silence through to the caller; treating it as an error
//     would turn a provider's omission into a failed run, and resending would
//     loop for ever.
//   - StopMaxTokens stops: the turn was cut off, and a tool call cut off with
//     it can still hold complete JSON that is missing what the model meant to
//     say, so it must never run.
//   - StopRefusal stops: the model declined, and its tool calls are never run.
//   - Anything else stops, naming the reason rather than guessing at it, so a
//     stop reason a provider adds after this release ends the run visibly
//     instead of being treated as a finished answer.
//
// A turn that carries tool calls but did not stop for tool use — the last
// three rows — has its calls left unrun; the loop still gives each one an
// error result so that no tool_use is left dangling (invariant 9).
func classifyStop(reason StopReason, toolCalls int) turnOutcome {
	switch reason {
	case StopToolUse:
		if toolCalls == 0 {
			return turnOutcome{Action: actionDone}
		}
		return turnOutcome{Action: actionRunTools}
	case StopPauseTurn:
		return turnOutcome{Action: actionResend}
	case StopEndTurn, StopSequence, StopNone:
		return turnOutcome{Action: actionDone}
	case StopMaxTokens:
		return turnOutcome{
			Action:  actionStop,
			Cause:   StopCauseMaxTokens,
			Message: "the model hit the output limit, so its turn is cut off",
		}
	case StopRefusal:
		return turnOutcome{
			Action:  actionStop,
			Cause:   StopCauseRefusal,
			Message: "the model declined to answer",
		}
	default:
		return turnOutcome{
			Action:  actionStop,
			Cause:   StopCauseUnknownStop,
			Message: "the provider stopped the turn for a reason goodall does not know: " + strconv.Quote(string(reason)),
		}
	}
}

// unrunEnded is the result text for a tool call the run ended before running,
// which is what cancellation, a timeout and a broken stream leave behind.
const unrunEnded = "This tool was not run: the run ended before the call could start."

// unrunReason is the result text for a tool call the stop reason forbade. It
// is written for the model, which reads it on any later turn, so it says that
// the call did not happen and why rather than apologising.
func unrunReason(reason StopReason) string {
	switch reason {
	case StopMaxTokens:
		return "This tool was not run: the model's turn hit the output limit, so the call may be incomplete."
	case StopRefusal:
		return "This tool was not run: the model's turn ended in a refusal."
	default:
		return "This tool was not run: the turn ended with stop reason " + strconv.Quote(reason.String()) + "."
	}
}
