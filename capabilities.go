package goodall

// Support is a tri-state fact about a model: goodall either knows a model
// takes something, knows it does not, or has not been told. The zero value is
// SupportUnknown, and unknown means try — a capability check only refuses a
// request the model is known to reject, so a catalogue goodall cannot read
// never blocks a call that would have worked.
type Support int

const (
	// SupportUnknown is the zero value: nobody has said.
	SupportUnknown Support = iota
	// Supported means the model takes it.
	Supported
	// Unsupported means the model is known to reject it.
	Unsupported
)

// String names the support state. Any value outside the three constants reads
// as "unknown", which is the safe reading.
func (s Support) String() string {
	switch s {
	case Supported:
		return "supported"
	case Unsupported:
		return "unsupported"
	default:
		return "unknown"
	}
}

// MarshalText writes the state as its name, so a serialised ModelInfo is
// readable rather than a column of integers.
func (s Support) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText reads a state name. Text goodall does not recognise decodes to
// SupportUnknown without an error, because a catalogue that grows a new word
// should leave the model usable.
func (s *Support) UnmarshalText(text []byte) error {
	switch string(text) {
	case "supported":
		*s = Supported
	case "unsupported":
		*s = Unsupported
	default:
		*s = SupportUnknown
	}
	return nil
}

// Capability names a fact a request can require of a model, so that a refusal
// can say which one was missing.
type Capability string

const (
	// CapImageInput is image blocks in a message.
	CapImageInput Capability = "image_input"
	// CapPDFInput is PDF documents in a message.
	CapPDFInput Capability = "pdf_input"
	// CapAudioInput is audio in a message.
	CapAudioInput Capability = "audio_input"
	// CapTools is tool definitions and tool calls.
	CapTools Capability = "tools"
	// CapThinking is extended thinking.
	CapThinking Capability = "thinking"
	// CapCacheControl is explicit prompt-cache breakpoints.
	CapCacheControl Capability = "cache_control"
	// CapStructuredOutput is a schema-constrained response.
	CapStructuredOutput Capability = "structured_output"
)

// String names the capability.
func (c Capability) String() string { return string(c) }

// Known reports whether this is one of the capabilities goodall defines.
func (c Capability) Known() bool {
	switch c {
	case CapImageInput, CapPDFInput, CapAudioInput, CapTools, CapThinking, CapCacheControl, CapStructuredOutput:
		return true
	}
	return false
}

// Capabilities is what a model accepts, as far as the provider has said. Every
// field is tri-state or zero-means-unknown, so a partially filled set is
// honest rather than pessimistic.
type Capabilities struct {
	// ImageInput is whether the model reads images.
	ImageInput Support `json:"image_input,omitzero"`
	// PDFInput is whether the model reads PDF documents. Anthropic reports
	// it separately from image input, and a model can have one without the
	// other.
	PDFInput Support `json:"pdf_input,omitzero"`
	// AudioInput is whether the model reads audio.
	AudioInput Support `json:"audio_input,omitzero"`
	// Tools is whether the model takes tool definitions.
	Tools Support `json:"tools,omitzero"`
	// Thinking is whether the model thinks before answering.
	Thinking Support `json:"thinking,omitzero"`
	// CacheControl is whether the model takes explicit prompt-cache
	// breakpoints.
	CacheControl Support `json:"cache_control,omitzero"`
	// StructuredOutput is whether the model constrains its answer to a
	// schema.
	StructuredOutput Support `json:"structured_output,omitzero"`
	// ThinkingStyle is which form of thinking the model takes, where the
	// provider distinguishes them; the zero value means it did not say,
	// which leaves a provider's own heuristic in charge.
	ThinkingStyle ThinkingStyle `json:"thinking_style,omitzero"`
	// ThinkingEfforts are the rungs of the effort ladder this model
	// accepts; empty means the provider did not say.
	ThinkingEfforts []Effort `json:"thinking_efforts,omitzero"`
	// ContextWindow is the largest prompt the model takes, in tokens; zero
	// means unknown.
	ContextWindow int `json:"context_window,omitzero"`
	// MaxOutput is the largest completion the model produces, in tokens;
	// zero means unknown.
	MaxOutput int `json:"max_output,omitzero"`
}

// ThinkingStyle is which form of extended thinking a model takes: the
// effort-driven adaptive form or the older explicit token budget. It is a
// fact from the catalogue, kept separate from Thinking (whether the model
// thinks at all) because a provider translates the same ThinkingConfig
// differently for each form.
type ThinkingStyle string

const (
	// ThinkingStyleUnknown is the zero value: the provider did not say.
	ThinkingStyleUnknown ThinkingStyle = ""
	// ThinkingAdaptive takes an effort rung and decides its own budget.
	ThinkingAdaptive ThinkingStyle = "adaptive"
	// ThinkingBudget takes an explicit token budget.
	ThinkingBudget ThinkingStyle = "budget"
)

// String names the style, calling the zero value "unknown".
func (s ThinkingStyle) String() string {
	if s == ThinkingStyleUnknown {
		return "unknown"
	}
	return string(s)
}

// Get reads one capability by name, so a check can be written once over
// whatever a request happens to carry. A capability goodall does not define
// reads as SupportUnknown.
func (c Capabilities) Get(name Capability) Support {
	switch name {
	case CapImageInput:
		return c.ImageInput
	case CapPDFInput:
		return c.PDFInput
	case CapAudioInput:
		return c.AudioInput
	case CapTools:
		return c.Tools
	case CapThinking:
		return c.Thinking
	case CapCacheControl:
		return c.CacheControl
	case CapStructuredOutput:
		return c.StructuredOutput
	}
	return SupportUnknown
}

// ModelInfo is what a provider knows about one model. A provider fills it
// from its catalogue endpoint and memoises it; the agent reads it once at the
// start of a run, puts it on every Request as ModelInfo, and uses it to
// refuse — before the network call — a request carrying an input the model
// rejects.
type ModelInfo struct {
	// ID is the model identifier as the provider spells it.
	ID string `json:"id,omitzero"`
	// Provider names the provider the facts came from.
	Provider string `json:"provider,omitzero"`
	// DisplayName is the provider's human-readable name for the model,
	// empty when it publishes none.
	DisplayName string `json:"display_name,omitzero"`
	// Capabilities is what the model accepts.
	Capabilities Capabilities `json:"capabilities"`
	// Pricing is the per-token price, or nil when the provider publishes
	// none — which is not the same as a price of zero.
	Pricing *Pricing `json:"pricing,omitzero"`
}

// Pricing is a model's price per token, held exactly as the provider published
// it. Per token, not per million: OpenRouter quotes it that way and rescaling
// on the way in would be a rounding decision nobody asked for.
type Pricing struct {
	// Input is the price of one uncached prompt token.
	Input Decimal `json:"input,omitzero"`
	// Output is the price of one generated token.
	Output Decimal `json:"output,omitzero"`
	// CacheRead is the price of one prompt token served from the cache.
	CacheRead Decimal `json:"cache_read,omitzero"`
	// CacheWrite is the price of one prompt token written to the cache.
	CacheWrite Decimal `json:"cache_write,omitzero"`
	// Currency is the ISO code the prices are in.
	Currency string `json:"currency,omitzero"`
}
