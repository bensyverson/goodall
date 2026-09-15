package openrouter

// Dialect names the flavour of the OpenAI Chat Completions format a server
// speaks. The format is shared; the extra members are not, so the dialect
// decides what a request may carry rather than a second client existing for
// each target. The zero value is [OpenRouter], because that is the server
// this package is named for.
//
// A value this package does not define is treated as [Generic]: the safe
// answer for an unknown server is the plain format everyone implements.
type Dialect string

const (
	// OpenRouter is openrouter.ai, which accepts every member below.
	OpenRouter Dialect = ""
	// OpenAI is api.openai.com, which takes reasoning effort as a
	// top-level string and understands metadata and end-user ids.
	OpenAI Dialect = "openai"
	// LMStudio is a local LM Studio server, which speaks the OpenAI format
	// including reasoning effort but keeps no account-level metadata.
	LMStudio Dialect = "lmstudio"
	// Generic is any other OpenAI-compatible server: only the members the
	// original Chat Completions format defines are sent.
	Generic Dialect = "generic"
)

// String names the dialect, calling the zero value "openrouter".
func (d Dialect) String() string {
	if d == OpenRouter {
		return "openrouter"
	}
	return string(d)
}

// ReasoningStyle is how a dialect is told how hard to think. The zero value
// is [ReasoningNone]: a server that has never been asked stays unasked.
type ReasoningStyle string

const (
	// ReasoningNone sends no thinking configuration at all.
	ReasoningNone ReasoningStyle = ""
	// ReasoningObject sends OpenRouter's "reasoning" object, which also
	// carries the exclude flag that a display setting maps onto.
	ReasoningObject ReasoningStyle = "object"
	// ReasoningEffortField sends OpenAI's top-level "reasoning_effort"
	// string, which has no place for a display setting.
	ReasoningEffortField ReasoningStyle = "reasoning_effort"
)

// OutputCap is the member a dialect reads the output-token cap from. The zero
// value is [CapMaxTokens], the name the original Chat Completions format
// defined and the one every compatible server still understands.
type OutputCap string

const (
	// CapMaxTokens is "max_tokens", the original member.
	CapMaxTokens OutputCap = ""
	// CapMaxCompletionTokens is "max_completion_tokens", which OpenAI's
	// reasoning models take instead: api.openai.com refuses max_tokens on
	// them outright (HTTP 400, "Unsupported parameter: 'max_tokens' is not
	// supported with this model", observed 2026-09-15) rather than
	// ignoring it, so the name is not interchangeable.
	CapMaxCompletionTokens OutputCap = "max_completion_tokens"
)

// Quirks is what one dialect accepts. It is a struct rather than a map so the
// set of questions is fixed, the answers are typed, and a new question is a
// compile error in the table below rather than a silently missing key.
type Quirks struct {
	// Reasoning is how the thinking configuration is sent.
	Reasoning ReasoningStyle
	// OutputCap is the member the output-token cap travels in.
	OutputCap OutputCap
	// StreamUsage is whether a streamed request must ask for the usage
	// report through stream_options.include_usage. A server that needs
	// asking and is not asked streams no usage chunk at all, so the answer
	// arrives with every token count at zero.
	StreamUsage bool
	// ReasoningDetails is whether reasoning_details entries come back and
	// are re-sent verbatim on the next turn. They are the model-agnostic
	// carrier of Anthropic signatures and OpenAI encrypted reasoning, so a
	// server that does not carry them cannot continue a thinking turn.
	ReasoningDetails bool
	// CacheControl is whether prompt-cache breakpoints are sent, at the
	// top level and on content parts.
	CacheControl bool
	// Routing is whether the provider-routing object and the model
	// fallback list are sent.
	Routing bool
	// Plugins is whether the plugins list is sent.
	Plugins bool
	// SessionID is whether the sticky-routing session id is sent.
	SessionID bool
	// Debug is whether the debug options, such as echoing the upstream
	// body, are sent.
	Debug bool
	// Metadata is whether per-request metadata and an end-user identifier
	// are sent.
	Metadata bool
	// UsageCost is whether the usage report carries a cost the provider
	// charged. Where it does not, goodall reports no cost rather than
	// inventing one from a price list.
	UsageCost bool
}

// Quirks is what this dialect accepts. The table:
//
//	                    OpenRouter  OpenAI            LMStudio    Generic
//	Reasoning           object      effort            effort      none
//	OutputCap           max_tokens  max_completion_…  max_tokens  max_tokens
//	StreamUsage         no          yes               yes         no
//	ReasoningDetails    yes         no                no          no
//	CacheControl        yes         no                no          no
//	Routing             yes         no                no          no
//	Plugins             yes         no                no          no
//	SessionID           yes         no                no          no
//	Debug               yes         no                no          no
//	Metadata            yes         yes               no          no
//	UsageCost           yes         no                no          no
func (d Dialect) Quirks() Quirks {
	switch d {
	case OpenRouter:
		return Quirks{
			Reasoning:        ReasoningObject,
			ReasoningDetails: true,
			CacheControl:     true,
			Routing:          true,
			Plugins:          true,
			SessionID:        true,
			Debug:            true,
			Metadata:         true,
			UsageCost:        true,
		}
	case OpenAI:
		return Quirks{
			Reasoning:   ReasoningEffortField,
			OutputCap:   CapMaxCompletionTokens,
			StreamUsage: true,
			Metadata:    true,
		}
	case LMStudio:
		return Quirks{Reasoning: ReasoningEffortField, StreamUsage: true}
	default:
		return Quirks{}
	}
}
