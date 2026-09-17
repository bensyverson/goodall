package chat

import "github.com/bensyverson/goodall"

// Redact is [NewThreadView]'s rule applied to a live stream: the same
// information the view withholds from a stored thread is taken out of the
// events as they pass, so a front end watching a run is trusted with no more
// than a front end reading the history.
//
// What it takes out is every tool call's input — whole on the call, and in the
// fragments that streamed it in — every tool result's content, wherever one
// travels, the assistant message the turn and terminal events carry, the
// conversation a terminal event carries, and the bytes of any media on the turn
// a run commits. What survives is what a UI renders and bills on: which tool
// ran under which id, whether it failed, the tokens, the money, the stop
// reason, the model's own text, which is the answer being watched, and the
// person's own words on the turn that prompted it.
//
// An event a running tool reported — a delegated run's own stream, wrapped in a
// [goodall.ToolEvent] — is redacted by the rule its own type earns, at every
// depth, keeping the id and the name of the call it belongs to. So a front end
// watching a redacted stream sees a child's progress as it sees the parent's,
// and nothing the parent withholds travels through a child instead.
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
			// The tokens and the money a call spent are what a front
			// end bills on, exactly as a turn's ledger is, so they
			// travel where the content does not.
			Usage: e.Usage,
			Cost:  e.Cost,
		}, true
	case goodall.ToolEvent:
		// A nested event earns the rule its own type earns, at every
		// depth: the wrapper keeps the call it belongs to, which is
		// what a front end draws the nesting from, and the event
		// inside goes through this same mapping. An inner event with no
		// redacted form takes its wrapper with it, or a front end would
		// be handed a nesting with nothing in it.
		if e.Event == nil {
			return nil, false
		}
		inner, keep := redactEvent(e.Event, opts)
		if !keep {
			return nil, false
		}
		e.Event = inner
		return e, true
	case goodall.TurnCommitted:
		e.Message = redactMessage(e.Message)
		return e, true
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

// redactMessage redacts a whole turn block by block. It is the new turn's
// rule, and only the new turn's: the assistant message a response carries is
// dropped whole by redactResponse, because a front end has been streaming it
// and has no use for a second copy, while the turn the run just appended is the
// question a front end has nothing else to render from.
//
// An empty turn is returned as it stands, so the events a stream carries are
// unchanged rather than merely equal.
func redactMessage(msg goodall.Message) goodall.Message {
	if len(msg.Content) == 0 {
		return msg
	}
	content := make(goodall.Blocks, len(msg.Content))
	for i, blk := range msg.Content {
		content[i] = redactBlock(blk)
	}
	msg.Content = content
	return msg
}

// redactBlock redacts one block of a new turn, following [blockView]'s rules on
// [goodall.Block] itself: [goodall.Block] is sealed, so there is no chat-side
// placeholder block to mint and no position in a stored history to derive a
// placeholder id from. A block that withholds something keeps its shape and
// loses its content instead.
//
// Text passes through: it is the question a front end is rendering. Media keeps
// its type and its media type and loses its bytes, which the front end sent and
// already has; a source it can fetch for itself is nothing to withhold. A
// document's title is the sender's own label and stays, while its context is
// prose about the content, which the view never carries either. A tool result
// keeps its id and whether it failed, exactly as [goodall.ToolCallEnd]'s result
// does, since a results turn reaching a front end whole would be a hole in that
// same fence.
//
// The cache marker goes with the content wherever one is dropped, for
// redactToolUse's reason: it shapes a request to the provider and means nothing
// on the way out.
//
// Everything else — a tool call, thinking, a block goodall does not model —
// passes through. A new turn does not carry them in practice, but a caller's
// Send takes any block, and passing one on is honest about not having reasoned
// about it, where dropping it would hide that.
func redactBlock(blk goodall.Block) goodall.Block {
	switch b := blk.(type) {
	case goodall.Image:
		return goodall.Image{Source: redactSource(b.Source)}
	case goodall.Document:
		return goodall.Document{Source: redactSource(b.Source), Title: b.Title}
	case goodall.ToolResult:
		return goodall.ToolResult{ToolUseID: b.ToolUseID, IsError: b.IsError}
	default:
		return blk
	}
}

// redactSource replaces inline bytes with the shape they arrived in. A URL or
// an uploaded file is fetchable by the front end, so it passes through.
func redactSource(src goodall.Source) goodall.Source {
	if src.Type != goodall.SourceBytes {
		return src
	}
	return goodall.Source{Type: goodall.SourceBytes, MediaType: src.MediaType}
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
