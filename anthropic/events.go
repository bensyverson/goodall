package anthropic

import "github.com/bensyverson/goodall"

// The shapes of the Messages API server-sent events. The decoder that reads
// them belongs to the streaming path; the shapes live here with the rest of
// the wire, because a payload is a wire struct like any other and Anthropic
// changes both together.

// streamEventType is the "type" tag of an SSE event.
type streamEventType string

const (
	streamMessageStart      streamEventType = "message_start"
	streamContentBlockStart streamEventType = "content_block_start"
	streamContentBlockDelta streamEventType = "content_block_delta"
	streamContentBlockStop  streamEventType = "content_block_stop"
	streamMessageDelta      streamEventType = "message_delta"
	streamMessageStop       streamEventType = "message_stop"
	streamPing              streamEventType = "ping"
	streamError             streamEventType = "error"
)

// deltaType is the "type" tag of a content_block_delta's delta.
type deltaType string

const (
	deltaText      deltaType = "text_delta"
	deltaInputJSON deltaType = "input_json_delta"
	deltaThinking  deltaType = "thinking_delta"
	deltaSignature deltaType = "signature_delta"
	deltaCitations deltaType = "citations_delta"
)

// wireMessageStartEvent opens an assistant message.
type wireMessageStartEvent struct {
	Type    streamEventType `json:"type"`
	Message wireResponse    `json:"message"`
}

// wireContentBlockStartEvent opens a content block at Index.
type wireContentBlockStartEvent struct {
	Type         streamEventType `json:"type"`
	Index        int             `json:"index"`
	ContentBlock wireBlockValue  `json:"content_block"`
}

// wireDelta is one increment of an open block. Anthropic's delta variants
// share a tag and differ only in which member they carry, so one struct with
// the tag reads them all and the decoder switches on Type.
type wireDelta struct {
	Type        deltaType `json:"type"`
	Text        string    `json:"text,omitzero"`
	PartialJSON string    `json:"partial_json,omitzero"`
	Thinking    string    `json:"thinking,omitzero"`
	Signature   string    `json:"signature,omitzero"`
}

// wireContentBlockDeltaEvent adds to the open block at Index.
type wireContentBlockDeltaEvent struct {
	Type  streamEventType `json:"type"`
	Index int             `json:"index"`
	Delta wireDelta       `json:"delta"`
}

// wireContentBlockStopEvent closes the block at Index.
type wireContentBlockStopEvent struct {
	Type  streamEventType `json:"type"`
	Index int             `json:"index"`
}

// wireMessageDeltaBody is the stop information of a finished message.
type wireMessageDeltaBody struct {
	StopReason   goodall.StopReason `json:"stop_reason,omitzero"`
	StopSequence string             `json:"stop_sequence,omitzero"`
}

// wireMessageDeltaEvent carries the stop reason and the message's cumulative
// usage, which is the only place the output tokens are reported on a stream.
type wireMessageDeltaEvent struct {
	Type  streamEventType      `json:"type"`
	Delta wireMessageDeltaBody `json:"delta"`
	Usage wireUsage            `json:"usage,omitzero"`
}

// wireMessageStopEvent closes the assistant message.
type wireMessageStopEvent struct {
	Type streamEventType `json:"type"`
}

// wireErrorBody is Anthropic's error object.
type wireErrorBody struct {
	Type    string `json:"type,omitzero"`
	Message string `json:"message,omitzero"`
}

// wireErrorEvent is an error delivered in the body of a 200 response, which
// is how an overload mid-stream arrives: HTTP 200 is not success.
type wireErrorEvent struct {
	Type      streamEventType `json:"type"`
	Error     wireErrorBody   `json:"error"`
	RequestID string          `json:"request_id,omitzero"`
}
