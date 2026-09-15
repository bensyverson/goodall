package openrouter

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"strings"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/sse"
	"github.com/bensyverson/goodall/internal/transport"
)

// doneSentinel is the payload of the frame that ends an OpenRouter stream. It
// is the only data line that is not JSON.
const doneSentinel = "[DONE]"

// Event tags for the frames goodall does not recognize. They travel on an
// [goodall.UnknownEvent] rather than being dropped, because OpenRouter adds
// frame shapes without notice — the debug echo of the upstream body is one —
// and a consumer that can see them can work out what arrived.
const (
	// eventUnrecognizedChunk is a frame that decoded as a chunk but
	// carried no choices, no usage and no error.
	eventUnrecognizedChunk goodall.EventType = "openrouter.unrecognized_chunk"
	// eventMalformedChunk is a data line that is not a chunk at all.
	eventMalformedChunk goodall.EventType = "openrouter.malformed_chunk"
)

// Stream sends the request and yields the events it produces, neutralized into
// goodall's event vocabulary.
//
// The stream owns the HTTP response: ranging to the end, or breaking out
// early, closes it, and canceling ctx ends the stream with ctx's error. A
// request that cannot be translated is the stream's first and only yield, so
// a caller never has to check for an error before ranging.
func (c *Client) Stream(ctx context.Context, req *goodall.Request) goodall.Stream {
	return func(yield func(goodall.Event, error) bool) {
		body, err := c.requestBody(req, true)
		if err != nil {
			yield(nil, err)
			return
		}
		resp, err := c.http.Do(ctx, transport.Request{
			Method: http.MethodPost,
			URL:    c.baseURL + pathChatCompletions,
			Header: c.streamHeader(),
			Body:   body,
		})
		if err != nil {
			yield(nil, err)
			return
		}
		defer resp.Body.Close()

		n := &neutralizer{header: resp.Header}
		for frame, err := range sse.Events(resp.Body) {
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					err = ctxErr
				}
				yield(nil, err)
				return
			}
			events, apiErr, done := n.frame(frame.Data)
			for _, ev := range events {
				if !yield(ev, nil) {
					return
				}
			}
			if apiErr != nil {
				yield(nil, apiErr)
				return
			}
			if done {
				return
			}
		}
		if err := ctx.Err(); err != nil {
			yield(nil, err)
		}
		// A stream that simply stopped yields nothing more: Collect
		// reports the missing message_stop as a *ProtocolError, which
		// is the one place that judgment belongs.
	}
}

// streamHeader is the per-call header set plus the Accept a server needs to
// choose the event-stream representation.
func (c *Client) streamHeader() http.Header {
	h := c.header()
	h.Set("Accept", "text/event-stream")
	return h
}

// neutralizer turns OpenRouter's frames into goodall events. It owns the block
// indexes — OpenRouter numbers nothing — and the open-block bookkeeping the
// root Accumulator checks, so one frame in yields the events that fold cleanly
// into one message.
//
// It is not safe for concurrent use; one lives inside one stream.
type neutralizer struct {
	header  http.Header
	blocks  blockState
	started bool
	// sawFinish is set once a finish_reason has produced a terminal
	// MessageDelta, so the usage frame — which repeats the finish reason —
	// contributes accounting rather than a second terminal.
	sawFinish bool
	// refused is set by a refusal delta, which makes the stop reason a
	// refusal whatever the finish reason said.
	refused bool
}

// frame turns one SSE data payload into events. It reports the error that ends
// the stream, and whether this frame was the terminal [DONE].
func (n *neutralizer) frame(data string) (events []goodall.Event, apiErr *goodall.APIError, done bool) {
	payload := strings.TrimSpace(data)
	if payload == "" {
		return nil, nil, false
	}
	if payload == doneSentinel {
		events = append(events, n.blocks.closeAll()...)
		return append(events, goodall.MessageStop{}), nil, true
	}

	raw := []byte(payload)
	var chunk chatChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return []goodall.Event{unknownEvent(eventMalformedChunk, raw)}, nil, false
	}
	if chunk.Error != nil {
		return nil, inBodyError(chunk.Error, n.header, raw), true
	}
	if len(chunk.Choices) == 0 && chunk.Usage == nil {
		return []goodall.Event{unknownEvent(eventUnrecognizedChunk, raw)}, nil, false
	}

	if !n.started {
		n.started = true
		events = append(events, goodall.MessageStart{ID: chunk.ID, Model: chunk.Model})
	}
	if len(chunk.Choices) == 0 {
		return append(events, n.accounting(&chunk)...), nil, false
	}

	first := chunk.Choices[0]
	if first.Error != nil {
		return events, inBodyError(first.Error, n.header, raw), true
	}
	if first.FinishReason == finishError {
		events = append(events, n.blocks.closeAll()...)
		return events, bareFinishError(n.header, raw), true
	}
	events = append(events, n.delta(&first.Delta)...)
	events = append(events, n.terminal(&chunk, &first)...)
	return events, nil, false
}

// delta turns one chunk's message fragment into block events. The order
// matches the order a blocking response lists its blocks in — reasoning, then
// content, then tool calls — so Collect over a stream and Complete over the
// same exchange produce the same message.
func (n *neutralizer) delta(d *messageDelta) []goodall.Event {
	var events []goodall.Event
	switch {
	case len(d.ReasoningDetails) > 0:
		for _, entry := range d.ReasoningDetails {
			events = append(events, n.blocks.reasoningFragment(entry)...)
		}
	case d.Reasoning != "":
		// A server that sends the flat reasoning string and no
		// reasoning_details — a Generic or LM Studio endpoint — still
		// has its thinking carried, just without a raw entry to
		// re-send.
		events = append(events, n.blocks.flatReasoning(d.Reasoning)...)
	}
	// An empty content string is the wire's filler: almost every chunk of a
	// reasoning or tool-call turn carries one, and opening a text block for
	// it would give the message an empty block the blocking path has not.
	if d.Content != "" {
		events = append(events, n.blocks.textFragment(d.Content)...)
	}
	if d.Refusal != "" {
		n.refused = true
		events = append(events, n.blocks.textFragment(d.Refusal)...)
	}
	for _, call := range d.ToolCalls {
		events = append(events, n.blocks.toolFragment(call)...)
	}
	return events
}

// terminal handles the two frames that end a message: the one carrying a
// finish reason, and the trailing usage frame that repeats it.
func (n *neutralizer) terminal(chunk *chatChunk, first *streamChoice) []goodall.Event {
	newFinish := first.FinishReason != "" && !n.sawFinish
	if !newFinish && chunk.Usage == nil {
		return nil
	}

	events := n.blocks.closeAll()
	delta := goodall.MessageDelta{}
	if newFinish {
		n.sawFinish = true
		delta.StopReason = stopReason(first.FinishReason)
		if n.refused {
			delta.StopReason = goodall.StopRefusal
		}
		delta.NativeStopReason = first.NativeFinishReason
	}
	if chunk.Usage != nil {
		delta.Usage, delta.Cost = translateUsage(chunk.Usage)
	}
	return append(events, delta)
}

// accounting is the trailing usage frame in the shape some servers send it,
// with the usage and no choices at all.
func (n *neutralizer) accounting(chunk *chatChunk) []goodall.Event {
	events := n.blocks.closeAll()
	usage, cost := translateUsage(chunk.Usage)
	return append(events, goodall.MessageDelta{Usage: usage, Cost: cost})
}

// unknownEvent wraps a frame goodall does not recognize.
func unknownEvent(tag goodall.EventType, raw []byte) goodall.Event {
	return goodall.UnknownEvent{EventType: tag, Raw: jsontext.Value(raw)}
}
