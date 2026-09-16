package chat

import "github.com/bensyverson/goodall"

// Redact is [NewThreadView]'s rule applied to a live stream: the same
// information the view withholds from a stored thread is taken out of the
// events as they pass, so a front end watching a run is trusted with no more
// than a front end reading the history.
//
// What it takes out is every tool call's input — whole on the call, and in the
// fragments that streamed it in — every tool result's content, the assistant
// message the turn and terminal events carry, and the conversation a terminal
// event carries. What survives is what a UI renders and bills on: which tool
// ran under which id, whether it failed, the tokens, the money, the stop
// reason, and the model's own text, which is the answer being watched.
// Thinking text follows the agent's own display setting, exactly as the view
// does: under [goodall.DisplayOmitted] the block still opens, streams and
// closes, carrying no text, because the fact that the model thought is not
// itself a secret.
//
// It is a pure mapping. Every event is one of goodall's own event types with
// fields zeroed, never a new shape, so a front end's switch is unchanged and a
// redacted stream is written by [WriteSSE] and [WriteNDJSON] like any other.
// The events it is given are left alone — a run's events are shared with every
// other subscriber — and a stream error passes through untouched, since an
// error frame carries nothing the back end owns.
func Redact(events goodall.Stream, opts ViewOptions) goodall.Stream {
	return func(yield func(goodall.Event, error) bool) {
		for ev, err := range events {
			if err != nil {
				if !yield(ev, err) {
					return
				}
				continue
			}
			redacted, keep := redactEvent(ev, opts)
			if !keep {
				continue
			}
			if !yield(redacted, nil) {
				return
			}
		}
	}
}

// redactEvent is the mapping one event at a time. The second return is whether
// the event travels at all: a tool input delta is nothing but the input, so it
// has no redacted form and is dropped.
//
// The default arm passes an event through whole, which is what keeps the
// redaction narrow. The events that reach it — the message and block frames,
// the deltas, a turn opening, a provider event goodall does not model — carry
// the model's own output and the shape of the stream, both of which the front
// end is watching for.
func redactEvent(ev goodall.Event, opts ViewOptions) (goodall.Event, bool) {
	switch e := ev.(type) {
	case goodall.ToolInputDelta:
		return nil, false
	case goodall.ToolCallStart:
		return goodall.ToolCallStart{ToolUse: redactToolUse(e.ToolUse)}, true
	case goodall.ToolCallEnd:
		return goodall.ToolCallEnd{
			ToolUse: redactToolUse(e.ToolUse),
			// IsError is the whole of what a front end is told about
			// how the call turned out, as [ToolResultStatus] is in the
			// view: the model saw the content, the front end sees that
			// there was a failure.
			Result: goodall.ToolResult{ToolUseID: e.Result.ToolUseID, IsError: e.Result.IsError},
		}, true
	case goodall.TurnEnd:
		e.Response = redactResponse(e.Response)
		return e, true
	case goodall.Done:
		e.Result = redactResult(e.Result)
		return e, true
	case goodall.Stopped:
		// Cause, Message and Kind stay: they are the loop's own prose
		// about why the run ended, not content it read or produced.
		e.Result = redactResult(e.Result)
		return e, true
	case goodall.ThinkingDelta:
		if opts.ThinkingDisplay == goodall.DisplayOmitted {
			e.Text = ""
		}
		return e, true
	default:
		return ev, true
	}
}

// redactToolUse keeps a call's identity and drops everything it passed. The
// cache marker goes with the input: it shapes a request to the provider and
// means nothing on the way out.
func redactToolUse(use goodall.ToolUse) goodall.ToolUse {
	return goodall.ToolUse{ID: use.ID, Name: use.Name}
}

// redactResponse drops the assistant message and keeps the rest of the
// ledger, which is what a front end that has been streaming the text already
// has and needs the totals for.
func redactResponse(response goodall.Response) goodall.Response {
	response.Message = goodall.Message{}
	return response
}

// redactResult drops the history from a terminal event: the conversation the
// run produced, the message on its last response, and the input of every tool
// call a hook deferred, which is the same input a ToolCallStart withholds.
// The pointer and the slice are replaced rather than written through, since
// the result belongs to the run and to every other subscriber reading it.
func redactResult(result goodall.Result) goodall.Result {
	result.Conversation = goodall.Conversation{}
	if result.Response != nil {
		response := redactResponse(*result.Response)
		result.Response = &response
	}
	if len(result.Pending) > 0 {
		pending := make([]goodall.ToolUse, len(result.Pending))
		for i, use := range result.Pending {
			pending[i] = redactToolUse(use)
		}
		result.Pending = pending
	}
	return result
}
