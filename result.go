package goodall

// Response is one model call's answer, whether it arrived as a stream that
// was collected or through a Completer. It carries the stop reason and the
// usage alongside the message: a turn that returned only the message would
// lose why the model stopped, and the loop's whole job is to act on that.
type Response struct {
	// ID is the provider's identifier for the call, for its logs.
	ID string `json:"id,omitzero"`
	// Model is the model that actually answered, which a router may
	// choose differently from the one requested.
	Model string `json:"model,omitzero"`
	// Message is the assistant turn. Partial is set when the answer was
	// cut short, so a partial response is still usable.
	Message Message `json:"message"`
	// StopReason is why the model stopped generating.
	StopReason StopReason `json:"stop_reason,omitzero"`
	// StopSequence is the stop sequence that ended the turn, empty
	// otherwise.
	StopSequence string `json:"stop_sequence,omitzero"`
	// NativeStopReason is the upstream model's own stop string when a
	// router normalised it into StopReason, for diagnostics; empty when
	// StopReason is already the wire string.
	NativeStopReason string `json:"native_stop_reason,omitzero"`
	// Usage counts the tokens this call consumed.
	Usage Usage `json:"usage,omitzero"`
	// Cost is what the call cost, when the provider says so.
	Cost Cost `json:"cost,omitzero"`
}

// Result is what a whole agent run yields: the last response, the
// conversation the run produced, and the totals over every turn. It is what
// the terminal event of a run stream carries, so a consumer that only wants
// the answer reads one value and a consumer that wants to continue reads the
// conversation out of the same one.
type Result struct {
	// Response is the last model call of the run, nil when the run ended
	// before any call completed.
	Response *Response `json:"response,omitzero"`
	// Conversation is the history including everything the run appended,
	// which is what a caller passes back in to continue.
	Conversation Conversation `json:"conversation,omitzero"`
	// Usage is the sum over every turn of the run, not just the last.
	Usage Usage `json:"usage,omitzero"`
	// Cost is the sum over every turn, reported only when every turn's
	// cost was.
	Cost Cost `json:"cost,omitzero"`
	// StopReason is the model's reason for stopping on the last turn.
	StopReason StopReason `json:"stop_reason,omitzero"`
	// Pending is the tool calls a hook deferred, waiting for the caller to
	// approve them and resume. It is empty on every other exit.
	Pending []ToolUse `json:"pending,omitzero"`
}
