package openrouter

// Dialect names the flavor of OpenAI's format a server speaks: which conversation
// wire it takes, and within Chat Completions which extra members. The dialect
// decides what a request may carry rather than a second client existing for each
// target. The zero value is [OpenRouter], because that is the server this package
// is named for.
//
// A value this package does not define is treated as [Generic]: the safe
// answer for an unknown server is the plain format everyone implements.
type Dialect string

const (
	// OpenRouter is openrouter.ai, which accepts every member below.
	OpenRouter Dialect = ""
	// OpenAI is api.openai.com, which speaks /responses for conversation here:
	// its Chat Completions refuses function tools with reasoning on GPT-6, and
	// there is no way to choose that wire through this dialect.
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
	// ReasoningEffortField sends the top-level "reasoning_effort" string, which
	// has no place for a display setting.
	ReasoningEffortField ReasoningStyle = "reasoning_effort"
)

// StrictToolStyle is how a dialect carries a tool marked [goodall.Strict]. The
// zero value is [StrictToolsRefused]: a server never shown to take the flag
// is not sent it, and a strict tool is refused rather than quietly sent
// without the guarantee its author asked for.
type StrictToolStyle string

const (
	// StrictToolsRefused sends no strict member, and a request carrying a
	// strict tool is refused with a *goodall.CapabilityError.
	StrictToolsRefused StrictToolStyle = ""
	// StrictToolsMember sends function.strict.
	StrictToolsMember StrictToolStyle = "member"
	// StrictToolsWithAnthropicBeta sends function.strict and the
	// [HeaderAnthropicBeta] header. OpenRouter drops the flag on its way to
	// Anthropic without that header and routes the request anyway, so the
	// tool would run unconstrained with no error: 21 strict tools on
	// anthropic/claude-sonnet-5.5 succeeded without the header and drew
	// Anthropic's "Too many strict tools" 400 with it (2026-10-29).
	StrictToolsWithAnthropicBeta StrictToolStyle = "member_anthropic_beta"
)

// DecisionWire is how a dialect answers [Client.Decide]. The zero value is
// [DecisionWireSystemOne], the one wire every local server checked serves, which
// is the safe answer for a server this package has never heard of.
type DecisionWire string

const (
	// DecisionWireSystemOne posts the System One shape.
	DecisionWireSystemOne DecisionWire = ""
	// DecisionWireDecisions posts OpenAI's own shape to /decisions. Its server
	// answers the System One shape with a 400 about a member it wants instead.
	DecisionWireDecisions DecisionWire = "decisions"
)

// ConversationWire is how a dialect carries a conversation. The zero value is
// [ConversationChat], the wire every compatible server serves.
type ConversationWire string

const (
	// ConversationChat posts to /chat/completions.
	ConversationChat ConversationWire = ""
	// ConversationResponses posts OpenAI's own shape to /responses. The members
	// are the wire's, so the Quirks that spell out Chat members do not apply.
	ConversationResponses ConversationWire = "responses"
)

// Quirks is what one dialect accepts. It is a struct rather than a map so the
// set of questions is fixed, the answers are typed, and a new question is a
// compile error in the table below rather than a silently missing key.
type Quirks struct {
	// ConversationWire is how a conversation is carried.
	ConversationWire ConversationWire
	// Reasoning is how the thinking configuration is sent on Chat Completions.
	Reasoning ReasoningStyle
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
	// StrictTools is how a strict tool is carried, or whether it is
	// refused.
	StrictTools StrictToolStyle
	// DecisionWire is how a decision request is carried.
	DecisionWire DecisionWire
	// DecisionModelRequired is whether a decision request with no model is
	// refused locally. A local server may have only one model loaded and
	// ignore the member, so whether one is needed is the server's call there.
	DecisionModelRequired bool
}

// Quirks is what this dialect accepts. The table:
//
//	                    OpenRouter  OpenAI      LMStudio    Generic
//	ConversationWire    chat        responses   chat        chat
//	Reasoning           object      n/a         effort      none
//	StreamUsage         no          n/a         yes         no
//	ReasoningDetails    yes         n/a         no          no
//	CacheControl        yes         no          no          no
//	Routing             yes         no          no          no
//	Plugins             yes         no          no          no
//	SessionID           yes         no          no          no
//	Debug               yes         no          no          no
//	Metadata            yes         yes         no          no
//	UsageCost           yes         no          no          no
//	StrictTools         + beta      member      refused     refused
//	DecisionWire        System One  /decisions  System One  System One
//	DecisionModelReq…   yes         yes         no          no
//
// The n/a rows are Chat members the Responses wire fixes for itself, so they are
// left at their zero values.
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
			StrictTools:      StrictToolsWithAnthropicBeta,

			DecisionModelRequired: true,
		}
	case OpenAI:
		return Quirks{
			ConversationWire: ConversationResponses,
			Metadata:         true,
			StrictTools:      StrictToolsMember,

			DecisionWire:          DecisionWireDecisions,
			DecisionModelRequired: true,
		}
	case LMStudio:
		return Quirks{Reasoning: ReasoningEffortField, StreamUsage: true}
	default:
		return Quirks{}
	}
}
