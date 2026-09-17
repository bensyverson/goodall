package goodall

import "encoding/json/jsontext"

// EventType is the tag that identifies an event on the wire. Providers
// neutralize their own event vocabularies onto these, and the agent loop adds
// the eight that describe a run; anything a provider sends that does not map
// onto one arrives as an UnknownEvent rather than being dropped.
type EventType string

const (
	// EventMessageStart opens an assistant message.
	EventMessageStart EventType = "message_start"
	// EventBlockStart opens a content block.
	EventBlockStart EventType = "block_start"
	// EventTextDelta adds text to an open text block.
	EventTextDelta EventType = "text_delta"
	// EventThinkingDelta adds text to an open thinking block.
	EventThinkingDelta EventType = "thinking_delta"
	// EventSignatureDelta adds the signature of an open thinking block.
	EventSignatureDelta EventType = "signature_delta"
	// EventToolInputDelta adds a fragment of an open tool call's input.
	EventToolInputDelta EventType = "tool_input_delta"
	// EventBlockStop closes a content block.
	EventBlockStop EventType = "block_stop"
	// EventMessageDelta carries the stop reason and the message's usage.
	EventMessageDelta EventType = "message_delta"
	// EventMessageStop closes the assistant message.
	EventMessageStop EventType = "message_stop"
	// EventUnknown is a provider event goodall does not recognize.
	EventUnknown EventType = "unknown"

	// EventTurnStart opens one model call of a run.
	EventTurnStart EventType = "turn_start"
	// EventTurnCommitted carries the user turn a run just appended.
	EventTurnCommitted EventType = "turn_committed"
	// EventToolCallStart reports that a tool is about to run.
	EventToolCallStart EventType = "tool_call_start"
	// EventToolCallEnd reports a tool's result.
	EventToolCallEnd EventType = "tool_call_end"
	// EventToolEvent carries an event a running tool reported.
	EventToolEvent EventType = "tool_event"
	// EventTurnEnd closes one model call of a run.
	EventTurnEnd EventType = "turn_end"
	// EventDone is the terminal event of a run that finished.
	EventDone EventType = "done"
	// EventStopped is the terminal event of a run that ended early.
	EventStopped EventType = "stopped"
)

// String names the event type as it appears on the wire.
func (t EventType) String() string { return string(t) }

// Known reports whether this is one of the event types goodall defines.
func (t EventType) Known() bool {
	switch t {
	case EventMessageStart, EventBlockStart, EventTextDelta, EventThinkingDelta,
		EventSignatureDelta, EventToolInputDelta, EventBlockStop, EventMessageDelta,
		EventMessageStop, EventUnknown:
		return true
	}
	return t.FromLoop()
}

// FromLoop reports whether the agent loop emits this event rather than a
// provider. A run stream carries both kinds, and an accumulator over the
// model's message ignores the loop's.
func (t EventType) FromLoop() bool {
	switch t {
	case EventTurnStart, EventTurnCommitted, EventToolCallStart, EventToolCallEnd,
		EventToolEvent, EventTurnEnd, EventDone, EventStopped:
		return true
	}
	return false
}

// Event is one thing that happened while a model answered or while a run
// progressed. The interface is sealed: only the types in this package
// implement it, so a type switch over an Event is exhaustive once it handles
// UnknownEvent. Events are values, so a Stream yields MessageStart{…} rather
// than &MessageStart{…}, and every one is JSON-serializable (invariant 4).
type Event interface {
	// Type is the event's wire tag, so a consumer can log or route an
	// event without a full type switch.
	Type() EventType
	isEvent()
}

// MessageStart opens an assistant message. Usage is what the provider knows
// at the start, which on Anthropic is the input side of the ledger; the
// output counts arrive on MessageDelta.
type MessageStart struct {
	// ID is the provider's identifier for the message.
	ID string `json:"id,omitzero"`
	// Model is the model that is answering.
	Model string `json:"model,omitzero"`
	// Usage is what the provider knows at the start, the input side of
	// the ledger on Anthropic; the output counts arrive on MessageDelta.
	Usage Usage `json:"usage,omitzero"`
}

// BlockStart opens a content block at Index. Block is the block as far as the
// provider knows it at the opening: a Text or Thinking with no content yet, a
// ToolUse with its id and name but no input, a RedactedThinking that is
// already complete, or an Unknown carrying the provider's bytes.
type BlockStart struct {
	// Index is the block's position in the message.
	Index int `json:"index,omitzero"`
	// Block is the block as far as the provider knows it at the opening.
	Block Block `json:"block,omitzero"`
}

// TextDelta adds text to the open text block at Index.
type TextDelta struct {
	// Index is the block index this delta belongs to.
	Index int `json:"index,omitzero"`
	// Text is the fragment appended to the block.
	Text string `json:"text,omitzero"`
}

// ThinkingDelta adds text to the open thinking block at Index. It arrives
// empty when the request asked for thinking to be omitted, which still opens
// and closes a block, because the block itself must be replayed.
type ThinkingDelta struct {
	// Index is the block index this delta belongs to.
	Index int `json:"index,omitzero"`
	// Text is the fragment appended to the block.
	Text string `json:"text,omitzero"`
}

// SignatureDelta carries the signature of the open thinking block at Index.
// The signature binds the block to the conversation prefix that produced it,
// so it is never regenerated, only replayed.
type SignatureDelta struct {
	// Index is the block index this delta belongs to.
	Index int `json:"index,omitzero"`
	// Signature is the fragment appended to the block's signature.
	Signature string `json:"signature,omitzero"`
}

// ToolInputDelta adds a fragment of the open tool call's input at Index. The
// fragments are pieces of a JSON document, not JSON values in their own
// right, so they are concatenated and parsed only once the block closes.
type ToolInputDelta struct {
	// Index is the block index this delta belongs to.
	Index int `json:"index,omitzero"`
	// PartialJSON is the fragment appended to the tool call's input.
	PartialJSON string `json:"partial_json,omitzero"`
}

// BlockStop closes the block at Index. Raw is the provider's finished block,
// set only when the neutral fields cannot reproduce it — an OpenRouter
// reasoning_details entry, whose id, format and index have to go back
// verbatim. Anthropic rebuilds a thinking block from its text and signature
// and leaves Raw empty.
type BlockStop struct {
	// Index is the block being closed.
	Index int `json:"index,omitzero"`
	// Raw is the provider's finished block, set only when the neutral
	// fields cannot rebuild it.
	Raw jsontext.Value `json:"raw,omitzero"`
}

// MessageDelta carries the stop reason and the message's usage. Usage is
// cumulative for the message, not an increment, so a later MessageDelta
// replaces an earlier one rather than adding to it. NativeStopReason is the
// upstream model's own stop string when a router normalized it into
// StopReason (OpenRouter's native_finish_reason); a provider whose wire
// string is the StopReason leaves it empty.
type MessageDelta struct {
	// StopReason is why the model stopped generating.
	StopReason StopReason `json:"stop_reason,omitzero"`
	// StopSequence is the stop sequence that ended the turn, empty
	// otherwise.
	StopSequence string `json:"stop_sequence,omitzero"`
	// NativeStopReason is the upstream model's own stop string when a
	// router normalized it into StopReason; empty when the provider's
	// wire string is already StopReason.
	NativeStopReason string `json:"native_stop_reason,omitzero"`
	// Usage is the message's cumulative usage so far, replacing rather
	// than adding to any earlier MessageDelta.
	Usage Usage `json:"usage,omitzero"`
	// Cost is what the call has cost so far, when the provider says so.
	Cost Cost `json:"cost,omitzero"`
}

// MessageStop closes the assistant message. It is the only event that makes a
// collected message complete rather than partial.
type MessageStop struct{}

// UnknownEvent is a provider event this version of goodall does not
// recognize. It keeps the provider's own tag and bytes so a consumer can see
// what arrived, because both providers add event types without notice and the
// Anthropic docs require unknown ones to be tolerated.
//
// Unlike an Unknown block, it is written under goodall's own "unknown" tag
// with the provider's tag beside it: an event travels to a front end, never
// back to a provider, so the front end's switch never meets a surprise tag.
type UnknownEvent struct {
	// EventType is the provider's own tag for the event.
	EventType EventType `json:"event_type,omitzero"`
	// Raw is the provider's bytes for the event.
	Raw jsontext.Value `json:"raw,omitzero"`
}

// TurnStart opens one model call of a run. Turns are numbered from one.
type TurnStart struct {
	// Turn is the turn number, counted from one.
	Turn int `json:"turn,omitzero"`
}

// TurnCommitted carries the user turn the run has just appended to the
// conversation: the caller's input on the first turn of a Run, the results a
// Resume was handed, or the results the loop's own tool calls produced. It
// fires at the moment the turn is appended — after BeforeSend has shaped it and
// before the request goes out — not when the caller made the call, so what it
// carries is the message that really entered the history rather than the one
// that was asked for.
//
// It is what lets a subscriber who was not there for the call render the whole
// exchange: a client attaching mid-answer replays it out of the run's backlog
// and has the question above the answer.
//
// Message.Role is always [RoleUser]. A turn carrying tool results is a user
// turn in the conversation itself, as it is on Anthropic's wire, so the loop
// does not invent a third role for it.
type TurnCommitted struct {
	// Turn is the turn number this commit belongs to, counted from one.
	Turn int `json:"turn,omitzero"`
	// Message is the turn as it was appended.
	Message Message `json:"message"`
}

// ToolCallStart reports that the loop is about to run a tool, after any hook
// has allowed it.
type ToolCallStart struct {
	// ToolUse is the call the loop is about to make.
	ToolUse ToolUse `json:"tool_use"`
}

// ToolCallEnd reports a tool's result, including the error results that a
// failed or refused call produces: every tool_use gets a tool_result.
//
// Usage and Cost are what this one call spent, which only a tool that
// implements [Reporter] can say: a tool that calls a model of its own — an
// [AgentTool]'s child run, a judgment service — declares its tokens and its
// money here, and a plain [Tool.Execute] leaves both zero. A zero Usage means
// "nothing reported", exactly as an unreported Cost does, never "free".
//
// What a call spent is deliberately not rolled into [Result]: a delegated call
// whose provider reports no money would vanish from the sum while the sum
// called itself a provider's figure, and marking the whole run unreported
// because one call was would erase a real figure instead. A consumer that
// wants a total sums the calls it chose to count, knowing which ones reported.
// [Budget] is unchanged for the same reason: it bounds the parent model's own
// tokens. See the "Ruled 2026-09-17, nested events and per-call accounting"
// block quote in project/2026-09-14-architecture-plan.md.
type ToolCallEnd struct {
	// ToolUse is the call this result answers.
	ToolUse ToolUse `json:"tool_use"`
	// Result is the tool's result, including an error result for a call
	// the loop declined to run.
	Result ToolResult `json:"result"`
	// Usage is the tokens this one call spent, reported only by a tool
	// that implements [Reporter]; zero means nothing was reported.
	Usage Usage `json:"usage,omitzero"`
	// Cost is the money this one call spent, reported only by a tool that
	// implements [Reporter] and only where its provider says so.
	Cost Cost `json:"cost,omitzero"`
}

// ToolEvent is one event a running tool reported, wrapped for the stream of
// the run that called it. It is how a delegation's progress reaches a UI
// watching the parent: an [AgentTool] forwards every event of its child run,
// and the loop emits each one under the call that produced it, in the order it
// was reported and before that call's [ToolCallEnd].
//
// The inner Event is any event at all, a ToolEvent included, so a delegate's
// own delegate nests a level deeper; the wrapper carries the id and the name of
// the call at *this* level, which is what lets a consumer draw a tree without
// tracking the stack itself. [EventType.FromLoop] reports true, so an
// [Accumulator] over the parent's message ignores it, exactly as it ignores
// every other loop event: a child's tokens are not the parent's message.
type ToolEvent struct {
	// ToolUseID is the id of the call whose tool reported this event.
	ToolUseID string `json:"tool_use_id,omitzero"`
	// Name is the name of the tool that reported it, so a consumer can
	// label the nesting without holding the call it came from.
	Name string `json:"name,omitzero"`
	// Event is what the tool reported.
	Event Event `json:"event,omitzero"`
}

// TurnEnd closes one model call of a run and carries the whole response, so a
// consumer that ignores the deltas can still render turn by turn.
type TurnEnd struct {
	// Turn is the turn number, counted from one.
	Turn int `json:"turn,omitzero"`
	// Response is the whole model response for the turn.
	Response Response `json:"response"`
}

// Done is the terminal event of a run that reached a natural end.
type Done struct {
	// Result is what the run produced.
	Result Result `json:"result"`
}

// StopCause is why a run ended before a natural end. It is separate from
// StopReason, which is why the *model* stopped generating: a run can end for
// reasons the model knows nothing about.
type StopCause string

const (
	// StopCauseCanceled is the caller's context being canceled.
	StopCauseCanceled StopCause = "canceled"
	// StopCauseTurnLimit is the run's turn budget being spent.
	StopCauseTurnLimit StopCause = "turn_limit"
	// StopCauseTokenLimit is the run's token budget being spent.
	StopCauseTokenLimit StopCause = "token_limit"
	// StopCauseTimeout is the run's wall-clock budget being spent.
	StopCauseTimeout StopCause = "timeout"
	// StopCauseHook is a hook short-circuiting the run.
	StopCauseHook StopCause = "hook"
	// StopCauseDeferred is a hook deferring a tool call for approval; the
	// calls are in the result's Pending.
	StopCauseDeferred StopCause = "deferred"
	// StopCauseRefusal is the model declining. Its tool calls are not run.
	StopCauseRefusal StopCause = "refusal"
	// StopCauseMaxTokens is the model hitting the output limit, whose
	// truncated tool input must never be run.
	StopCauseMaxTokens StopCause = "max_tokens"
	// StopCauseError is a failure: the message and the kind say which.
	StopCauseError StopCause = "error"
	// StopCauseUnknownStop is a stop reason goodall does not recognize,
	// which ends the run rather than being guessed at.
	StopCauseUnknownStop StopCause = "unknown_stop"
)

// String names the cause.
func (c StopCause) String() string { return string(c) }

// Known reports whether this is one of the causes goodall defines.
func (c StopCause) Known() bool {
	switch c {
	case StopCauseCanceled, StopCauseTurnLimit, StopCauseTokenLimit, StopCauseTimeout,
		StopCauseHook, StopCauseDeferred, StopCauseRefusal, StopCauseMaxTokens,
		StopCauseError, StopCauseUnknownStop:
		return true
	}
	return false
}

// Stopped is the terminal event of a run that ended early. It is not itself an
// error: a failure travels as Message plus Kind (invariant 4), so the event
// stays JSON-serializable and a front end renders the same shape whether the
// run was canceled, budgeted out or broken.
type Stopped struct {
	// Cause is why the run ended early.
	Cause StopCause `json:"cause,omitzero"`
	// Message says in prose why the run ended, on every early ending: a
	// budget, a hook, a refusal, a cancellation or a failure.
	Message string `json:"message,omitzero"`
	// Kind classifies a failure or a cancellation, so a caller can decide
	// whether trying again is worth anything; it is empty for an ending
	// the loop chose, such as a budget, a hook or a refusal.
	Kind ErrorKind `json:"kind,omitzero"`
	// Result is what the run produced before it stopped.
	Result Result `json:"result"`
}

// Type reports the event's wire tag.
func (MessageStart) Type() EventType { return EventMessageStart }

// Type reports the event's wire tag.
func (BlockStart) Type() EventType { return EventBlockStart }

// Type reports the event's wire tag.
func (TextDelta) Type() EventType { return EventTextDelta }

// Type reports the event's wire tag.
func (ThinkingDelta) Type() EventType { return EventThinkingDelta }

// Type reports the event's wire tag.
func (SignatureDelta) Type() EventType { return EventSignatureDelta }

// Type reports the event's wire tag.
func (ToolInputDelta) Type() EventType { return EventToolInputDelta }

// Type reports the event's wire tag.
func (BlockStop) Type() EventType { return EventBlockStop }

// Type reports the event's wire tag.
func (MessageDelta) Type() EventType { return EventMessageDelta }

// Type reports the event's wire tag.
func (MessageStop) Type() EventType { return EventMessageStop }

// Type reports the event's wire tag.
func (UnknownEvent) Type() EventType { return EventUnknown }

// Type reports the event's wire tag.
func (TurnStart) Type() EventType { return EventTurnStart }

// Type reports the event's wire tag.
func (TurnCommitted) Type() EventType { return EventTurnCommitted }

// Type reports the event's wire tag.
func (ToolCallStart) Type() EventType { return EventToolCallStart }

// Type reports the event's wire tag.
func (ToolCallEnd) Type() EventType { return EventToolCallEnd }

// Type reports the event's wire tag.
func (ToolEvent) Type() EventType { return EventToolEvent }

// Type reports the event's wire tag.
func (TurnEnd) Type() EventType { return EventTurnEnd }

// Type reports the event's wire tag.
func (Done) Type() EventType { return EventDone }

// Type reports the event's wire tag.
func (Stopped) Type() EventType { return EventStopped }

func (MessageStart) isEvent()   {}
func (BlockStart) isEvent()     {}
func (TextDelta) isEvent()      {}
func (ThinkingDelta) isEvent()  {}
func (SignatureDelta) isEvent() {}
func (ToolInputDelta) isEvent() {}
func (BlockStop) isEvent()      {}
func (MessageDelta) isEvent()   {}
func (MessageStop) isEvent()    {}
func (UnknownEvent) isEvent()   {}
func (TurnStart) isEvent()      {}
func (TurnCommitted) isEvent()  {}
func (ToolCallStart) isEvent()  {}
func (ToolCallEnd) isEvent()    {}
func (ToolEvent) isEvent()      {}
func (TurnEnd) isEvent()        {}
func (Done) isEvent()           {}
func (Stopped) isEvent()        {}
