package goodall

import (
	"fmt"
	"strconv"
)

// Strictness is how closely a provider holds a model's arguments to a tool's
// input schema.
//
// Strict is opt-in per tool, never the default, because the providers'
// limits on it are counted across a whole request: Anthropic takes at most 20
// strict tools, 24 optional parameters and 16 union-typed parameters across
// every strict schema in one request. A strict default would make an agent
// start failing as it grew past one of those, with no change to any one tool.
type Strictness string

const (
	// Guided is the zero value: the schema is shown to the model, which
	// nearly always follows it, and a typed tool checks what arrives and
	// tells the model what to fix.
	Guided Strictness = ""
	// Strict asks the provider to constrain the model's sampling to the
	// schema, so the arguments always match it. It is how a caller gets a
	// schema guarantee on a model that refuses a forced tool choice:
	// leave the choice at auto and mark the tool strict.
	//
	// The schema must be one the provider can compile: a concrete type at
	// every level and additionalProperties false on every object (see
	// [Schema.ValidateStrict]). The OpenAI models also require every
	// property to be listed as required. A request breaking a provider's
	// rule or limit is refused before it is sent.
	Strict Strictness = "strict"
)

// String names the strictness, calling the zero value "guided".
func (s Strictness) String() string {
	if s == Guided {
		return "guided"
	}
	return string(s)
}

// StrictTool is the optional interface through which a [Tool] declares its
// Strictness, in the house pattern of [Reporter]: a provider asks a tool
// whether it implements this, and a tool that does not is Guided. Every
// tool NewTool and NewReportingTool build implements it; a hand-written tool
// implements it only to be strict.
type StrictTool interface {
	// Strictness is how closely the provider holds the model to the
	// tool's schema.
	Strictness() Strictness
}

// StrictnessOf reads a tool's Strictness: what it declares through
// [StrictTool], or Guided when it declares nothing.
func StrictnessOf(t Tool) Strictness {
	if s, ok := t.(StrictTool); ok {
		return s.Strictness()
	}
	return Guided
}

// ToolOption adjusts a tool built by [NewTool] or [NewReportingTool].
type ToolOption func(*toolOptions)

// toolOptions is what the options can set.
type toolOptions struct {
	strictness Strictness
}

// WithStrictness sets how closely the provider holds the model to the tool's
// schema. Pass [Strict] to have it enforced:
//
//	tool, err := goodall.NewTool("pick_color", "Pick a color.", pick,
//	    goodall.WithStrictness(goodall.Strict))
func WithStrictness(s Strictness) ToolOption {
	return func(o *toolOptions) { o.strictness = s }
}

// applyToolOptions folds the options and checks what they asked for against
// the inferred schema, so a strict tool no provider could compile fails when
// it is built rather than on its first request.
func applyToolOptions(schema *Schema, opts []ToolOption) (toolOptions, error) {
	var o toolOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	switch o.strictness {
	case Guided:
	case Strict:
		if err := schema.ValidateStrict(); err != nil {
			return o, err
		}
	default:
		return o, fmt.Errorf("goodall: strictness %q is not one goodall defines", string(o.strictness))
	}
	return o, nil
}

// Strictness is the strictness the tool was built with.
func (t *toolDefinition[In]) Strictness() Strictness { return t.strictness }

// ValidateStrict reports what in the schema no provider can enforce under
// [Strict]: a schema with no type, which accepts any JSON value, and an object
// that does not forbid additional properties. Both Anthropic and OpenAI refuse
// either with a 400 (Anthropic verified 2026-10-29). An inferred schema
// forbids additional properties everywhere, so for it the check comes down to
// a jsontext.Value parameter.
//
// It checks the rules every strict provider shares; a provider's own rules
// and request-wide limits are checked when the request is translated.
func (s *Schema) ValidateStrict() error {
	return s.validateStrict("")
}

func (s *Schema) validateStrict(path string) error {
	where := "the schema"
	if path != "" {
		where = "parameter " + strconv.Quote(path)
	}
	switch s.Type {
	case SchemaUnconstrained:
		return &SchemaError{Reason: where + " accepts any JSON value, and a strict schema needs a concrete type at every level"}
	case SchemaObject:
		if s.AdditionalProperties != AdditionalPropertiesForbidden {
			return &SchemaError{Reason: where + " is an object that does not forbid additional properties, which a strict schema must"}
		}
		for i := range s.Properties {
			p := &s.Properties[i]
			if err := p.Schema.validateStrict(joinParameterPath(path, p.Name)); err != nil {
				return err
			}
		}
	case SchemaArray:
		if s.Items != nil {
			return s.Items.validateStrict(path + "[]")
		}
	}
	return nil
}
