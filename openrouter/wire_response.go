// The wire shapes that come back: a blocking completion, a streamed frame,
// the usage report and the error object. The reasoning_details entry lives
// here too, because it arrives here first and the request side only rebuilds
// one when a thinking block lost the provider's own bytes.

package openrouter

import (
	"encoding/json/jsontext"

	"github.com/bensyverson/goodall"
)

// chatResponse is the body of a blocking completion. A body carrying a
// top-level error object, or a choice carrying one, decodes here too: HTTP 200
// is not success, and the error mapping needs the fields to be present.
type chatResponse struct {
	ID       string     `json:"id,omitzero"`
	Object   string     `json:"object,omitzero"`
	Created  int64      `json:"created,omitzero"`
	Model    string     `json:"model,omitzero"`
	Provider string     `json:"provider,omitzero"`
	Choices  []choice   `json:"choices,omitzero"`
	Usage    *usage     `json:"usage,omitzero"`
	Error    *wireError `json:"error,omitzero"`
}

// choice is one completion. NativeFinishReason is the upstream model's own
// stop string, which OpenRouter reports alongside its normalised one.
type choice struct {
	Index              int         `json:"index,omitzero"`
	FinishReason       string      `json:"finish_reason,omitzero"`
	NativeFinishReason string      `json:"native_finish_reason,omitzero"`
	Message            chatMessage `json:"message,omitzero"`
	Error              *wireError  `json:"error,omitzero"`
}

// chatChunk is one streamed frame. It is the response shape with deltas in
// place of messages; the trailing usage frame repeats the finish reason, so a
// decoder must not read a finish reason as a second terminal event.
type chatChunk struct {
	ID       string         `json:"id,omitzero"`
	Object   string         `json:"object,omitzero"`
	Created  int64          `json:"created,omitzero"`
	Model    string         `json:"model,omitzero"`
	Provider string         `json:"provider,omitzero"`
	Choices  []streamChoice `json:"choices,omitzero"`
	Usage    *usage         `json:"usage,omitzero"`
	Error    *wireError     `json:"error,omitzero"`
}

// streamChoice is one completion's slice of a streamed frame.
type streamChoice struct {
	Index              int          `json:"index,omitzero"`
	FinishReason       string       `json:"finish_reason,omitzero"`
	NativeFinishReason string       `json:"native_finish_reason,omitzero"`
	Delta              messageDelta `json:"delta,omitzero"`
	Error              *wireError   `json:"error,omitzero"`
}

// messageDelta is the fragment of an assistant message one frame carries.
type messageDelta struct {
	Role             role             `json:"role,omitzero"`
	Content          string           `json:"content,omitzero"`
	Reasoning        string           `json:"reasoning,omitzero"`
	ReasoningDetails []jsontext.Value `json:"reasoning_details,omitzero"`
	ToolCalls        []toolCallDelta  `json:"tool_calls,omitzero"`
	Refusal          string           `json:"refusal,omitzero"`
}

// toolCallDelta is a fragment of one tool call, keyed by Index: the id and
// the name arrive on the first fragment and the arguments in pieces after.
type toolCallDelta struct {
	Index    int                   `json:"index"`
	ID       string                `json:"id,omitzero"`
	Type     functionType          `json:"type,omitzero"`
	Function toolCallFunctionDelta `json:"function,omitzero"`
}

// toolCallFunctionDelta is the name-and-arguments fragment of a tool call.
type toolCallFunctionDelta struct {
	Name      string `json:"name,omitzero"`
	Arguments string `json:"arguments,omitzero"`
}

// reasoningType is the discriminator of a reasoning_details entry.
type reasoningType string

const (
	// reasoningText is readable thinking, with the signature that binds it
	// to the conversation prefix that produced it.
	reasoningText reasoningType = "reasoning.text"
	// reasoningSummary is a readable summary of thinking goodall never
	// sees in full.
	reasoningSummary reasoningType = "reasoning.summary"
	// reasoningEncrypted is thinking the provider sealed.
	reasoningEncrypted reasoningType = "reasoning.encrypted"
)

// reasoningDetail is one reasoning_details entry. It is read in both
// directions: decoding picks the neutral block out of it, and a thinking
// block whose provider bytes were lost is rebuilt through it. Members the
// provider set but goodall has no use for — id, format, index — are not
// listed, because an entry that came from a provider is re-sent from its own
// bytes rather than from this struct.
type reasoningDetail struct {
	Type      reasoningType `json:"type"`
	Text      string        `json:"text,omitzero"`
	Signature string        `json:"signature,omitzero"`
	Summary   string        `json:"summary,omitzero"`
	Data      string        `json:"data,omitzero"`
}

// usage is what the call consumed and, on OpenRouter, what it cost.
type usage struct {
	PromptTokens            int                      `json:"prompt_tokens,omitzero"`
	CompletionTokens        int                      `json:"completion_tokens,omitzero"`
	TotalTokens             int                      `json:"total_tokens,omitzero"`
	PromptTokensDetails     *promptTokensDetails     `json:"prompt_tokens_details,omitzero"`
	CompletionTokensDetails *completionTokensDetails `json:"completion_tokens_details,omitzero"`
	Cost                    *goodall.Decimal         `json:"cost,omitzero"`
	IsBYOK                  bool                     `json:"is_byok,omitzero"`
}

// promptTokensDetails breaks the prompt tokens down. CacheWriteTokens comes
// back only from models with explicit caching and a cache-write price.
type promptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens,omitzero"`
	CacheWriteTokens int `json:"cache_write_tokens,omitzero"`
	AudioTokens      int `json:"audio_tokens,omitzero"`
	VideoTokens      int `json:"video_tokens,omitzero"`
}

// completionTokensDetails breaks the generated tokens down.
type completionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitzero"`
	AudioTokens     int `json:"audio_tokens,omitzero"`
}

// wireError is OpenRouter's error object, which arrives at the top level of a
// body, inside a choice, or as the only frame of a stream. Code is kept as a
// raw JSON value because it mirrors the HTTP status as a number before the
// response is committed and can be a provider's own string after.
type wireError struct {
	Code     jsontext.Value `json:"code,omitzero"`
	Message  string         `json:"message,omitzero"`
	Metadata *errorMetadata `json:"metadata,omitzero"`
}

// errorMetadata is the stable part of an error: ErrorType is the field to
// switch on, not the code.
type errorMetadata struct {
	ErrorType    string         `json:"error_type,omitzero"`
	ProviderName string         `json:"provider_name,omitzero"`
	ProviderCode jsontext.Value `json:"provider_code,omitzero"`
	Raw          jsontext.Value `json:"raw,omitzero"`
}
