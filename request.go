package goodall

import "fmt"

// Request is one call to a provider: the model, the conversation and every
// per-call parameter. Providers translate it into their own wire format, so
// goodall never marshals a Request itself and it carries no JSON tags.
//
// A Request is read-only once handed to a provider. Nothing in the library
// writes through it, so one Request may be shared between goroutines and one
// provider may hold it for the life of a stream; a caller who wants to change
// a parameter builds a new value.
type Request struct {
	// Model is the provider's model identifier, verbatim.
	Model string
	// System is the standing instruction, as text blocks rather than a
	// string so a cache breakpoint can sit on the static part of it.
	System []Text
	// Messages is the conversation history, oldest first. The loop copies
	// it out of a Conversation, so the provider sees a plain slice.
	Messages []Message
	// Tools is what the model may call, in the order they are sent. The
	// order is part of the cached prefix, so it must not vary between
	// turns of a run.
	Tools []Tool
	// ToolChoice constrains which of those tools the model may call.
	ToolChoice ToolChoice
	// MaxTokens caps the tokens generated in the turn. Anthropic requires
	// the member and publishes no default, so a provider that needs one
	// substitutes its own named default — anthropic.DefaultMaxTokens —
	// when this is zero.
	MaxTokens int
	// Thinking is how hard the model should think and how much of that
	// thinking comes back.
	Thinking ThinkingConfig
	// Cache decides where prompt-cache breakpoints are placed.
	Cache CachePolicy
	// StopSequences end the turn when the model generates one of them.
	StopSequences []string
	// Metadata is the provider's per-request metadata, such as an end-user
	// identifier for abuse monitoring.
	Metadata map[string]string
	// ModelInfo is what the provider's catalogue says about Model, which
	// the loop fetches once per run and puts here so a translation can
	// read a fact rather than guess it from the model's name. A direct
	// consumer may fill it in by hand; nil means nobody consulted the
	// catalogue, never "the model can do nothing".
	ModelInfo *ModelInfo
	// Extensions carries provider-specific options. A provider accepts
	// only its own extension type and rejects another provider's before
	// sending, so a request built for one provider fails loudly on
	// another rather than losing the options silently.
	Extensions Extension
}

// Extension is a provider-specific options struct. Each provider defines one,
// names itself in Provider, and documents what it carries; the interface
// exists so the core can hold the value and the provider can check it belongs
// to them.
type Extension interface {
	// Provider is the name of the provider that understands this value,
	// matching the name that provider puts on its errors.
	Provider() string
}

// ToolChoiceMode is how the model is allowed to use the tools on a request.
// It is a string so a mode a provider adds later survives a round trip.
type ToolChoiceMode string

const (
	// ToolChoiceAuto is the zero value: the model decides whether to call
	// a tool.
	ToolChoiceAuto ToolChoiceMode = ""
	// ToolChoiceAny requires the model to call one of the tools.
	ToolChoiceAny ToolChoiceMode = "any"
	// ToolChoiceNone forbids tool calls for this turn.
	ToolChoiceNone ToolChoiceMode = "none"
	// ToolChoiceNamed requires the model to call one named tool.
	ToolChoiceNamed ToolChoiceMode = "tool"
)

// String names the mode, calling the zero value "auto".
func (m ToolChoiceMode) String() string {
	if m == ToolChoiceAuto {
		return "auto"
	}
	return string(m)
}

// Known reports whether this is one of the modes goodall defines.
func (m ToolChoiceMode) Known() bool {
	switch m {
	case ToolChoiceAuto, ToolChoiceAny, ToolChoiceNone, ToolChoiceNamed:
		return true
	}
	return false
}

// ToolChoice constrains the model's tool use for one request. The zero value
// is ToolChoiceAuto with parallel calls allowed, which is what an agent loop
// wants, so a caller who never thinks about tool choice sends nothing.
type ToolChoice struct {
	// Mode is the constraint.
	Mode ToolChoiceMode
	// Name is the tool the model must call, required by ToolChoiceNamed
	// and meaningless otherwise.
	Name string
	// NoParallel asks for at most one tool call per turn. Both providers
	// default to allowing several.
	NoParallel bool
}

// ChooseAuto lets the model decide whether to call a tool.
func ChooseAuto() ToolChoice { return ToolChoice{Mode: ToolChoiceAuto} }

// ChooseAny requires the model to call one of the request's tools.
func ChooseAny() ToolChoice { return ToolChoice{Mode: ToolChoiceAny} }

// ChooseNone forbids tool calls on this turn, which is how a loop asks for a
// final answer after the tools have run.
func ChooseNone() ToolChoice { return ToolChoice{Mode: ToolChoiceNone} }

// ChooseTool requires the model to call the named tool.
func ChooseTool(name string) ToolChoice {
	return ToolChoice{Mode: ToolChoiceNamed, Name: name}
}

// Serial returns the choice with parallel tool calls turned off, so the four
// constructors compose with the one modifier rather than needing eight.
func (c ToolChoice) Serial() ToolChoice {
	c.NoParallel = true
	return c
}

// Validate reports a tool choice a provider cannot send: a named choice with
// no name, or a name given for a mode that does not use one. Providers call
// it before translating, so the caller hears about it rather than the model
// silently choosing for itself. The error wraps KindInvalidRequest, so
// errors.Is places it without reaching for a concrete type.
func (c ToolChoice) Validate() error {
	switch {
	case c.Mode == ToolChoiceNamed && c.Name == "":
		return fmt.Errorf("%w: a named tool choice needs a tool name", KindInvalidRequest)
	case c.Mode != ToolChoiceNamed && c.Name != "":
		return fmt.Errorf("%w: a tool name is only meaningful with %s", KindInvalidRequest, ToolChoiceNamed)
	}
	return nil
}
