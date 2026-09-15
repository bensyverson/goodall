package goodall

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Tool is something the model can call. NewTool builds one from a typed Go
// handler, inferring the schema from the handler's input type; a consumer
// with unusual needs implements the interface directly.
//
// Execute receives the model's argument object exactly as produced. It
// returns a ToolResult on every path the model should hear about, including
// a bad input, with IsError set so the model can correct itself; the error
// return is for failures of the tool itself, which the loop also turns into
// an error result so that every tool_use still gets a tool_result. The loop
// fills in the result's ToolUseID.
type Tool interface {
	Name() string
	Description() string
	Schema() *Schema
	Execute(ctx context.Context, input jsontext.Value) (ToolResult, error)
}

// MaxToolNameLength is the longest name a tool may have. It is the shorter of
// the two providers' limits, so a tool that goodall accepts is one both
// providers accept.
const MaxToolNameLength = 64

// NewTool builds a Tool from a typed handler. The schema the model sees is
// inferred from In, so the description of the input and the type the handler
// receives cannot drift apart; see SchemaFor for the tags that shape it.
//
// It returns an error, never a panic, for a definition no provider would
// accept: an empty name, a name over MaxToolNameLength or carrying anything
// but letters, digits, underscore and hyphen, an empty description, a nil
// handler, or an In that cannot be expressed as a schema.
//
//	tool, err := goodall.NewTool("get_weather", "Look up the weather in a city.",
//	    func(ctx context.Context, in struct {
//	        City string `json:"city" desc:"the city to look up"`
//	    }) (goodall.ToolResult, error) {
//	        return goodall.TextResult(lookUp(in.City)), nil
//	    })
func NewTool[In any](name, description string, run func(context.Context, In) (ToolResult, error)) (Tool, error) {
	if err := validateToolName(name); err != nil {
		return nil, err
	}
	if description == "" {
		return nil, fmt.Errorf("goodall: tool %q has no description; the description is what tells the model when to call it", name)
	}
	if run == nil {
		return nil, fmt.Errorf("goodall: tool %q has no handler function", name)
	}
	schema, err := SchemaFor[In]()
	if err != nil {
		return nil, &toolDefinitionError{name: name, err: err}
	}
	if err := schema.Validate(); err != nil {
		return nil, &toolDefinitionError{name: name, err: err}
	}
	return &typedTool[In]{name: name, description: description, schema: schema, run: run}, nil
}

// validateToolName applies the intersection of the two providers' rules.
func validateToolName(name string) error {
	if name == "" {
		return errors.New("goodall: a tool needs a name")
	}
	if n := utf8.RuneCountInString(name); n > MaxToolNameLength {
		return fmt.Errorf("goodall: tool name %q is %d characters; the limit is %d", name, n, MaxToolNameLength)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return fmt.Errorf("goodall: tool name %q contains %q; a name may use letters, digits, underscore and hyphen", name, string(r))
		}
	}
	return nil
}

// toolDefinitionError names the tool a schema problem came from while keeping
// the underlying *SchemaError reachable with errors.AsType. The inner
// message's own package prefix is dropped so the text reads as one sentence.
type toolDefinitionError struct {
	name string
	err  error
}

func (e *toolDefinitionError) Error() string {
	return "goodall: tool " + strconv.Quote(e.name) + ": " + strings.TrimPrefix(e.err.Error(), "goodall: ")
}

func (e *toolDefinitionError) Unwrap() error { return e.err }

// typedTool is the Tool NewTool returns: a handler over a Go type, plus the
// schema inferred from that type once, at construction.
type typedTool[In any] struct {
	name        string
	description string
	schema      *Schema
	run         func(context.Context, In) (ToolResult, error)
}

// Name is the name the model calls the tool by.
func (t *typedTool[In]) Name() string { return t.name }

// Description is the prose the model decides with.
func (t *typedTool[In]) Description() string { return t.description }

// Schema is the inferred input schema. It is the same pointer on every call:
// a provider marshals it into every request, so it must not be rebuilt per
// turn.
func (t *typedTool[In]) Schema() *Schema { return t.schema }

// Execute decodes the model's arguments into In and runs the handler.
//
// An absent input — nil, empty, or JSON null — is read as an empty object,
// because a provider may send nothing at all for a tool with no parameters.
// Unknown members are refused, since the schema promised
// additionalProperties: false, and a required parameter the model left out is
// refused too, since encoding/json/v2 would otherwise hand the handler a zero
// value the model never sent.
//
// A bad input is the model's to fix, so it comes back as a result with
// IsError set and a nil error, carrying a message shaped like:
//
//	parameter "…" takes an integer, but the value sent was "…"
//
//	Parameters:
//	  … (integer, required): …
//
// The handler's own result and error pass through untouched; the loop assigns
// the ToolUseID and turns an error into an error result.
func (t *typedTool[In]) Execute(ctx context.Context, input jsontext.Value) (ToolResult, error) {
	value := toolInputObject(input)
	var in In
	if err := json.Unmarshal(value, &in, json.RejectUnknownMembers(true)); err != nil {
		return ErrorResult(inputErrorText(t.schema, inputProblem(t.schema, err))), nil
	}
	if missing := missingRequired(t.schema, value); len(missing) > 0 {
		return ErrorResult(inputErrorText(t.schema, missingProblem(missing))), nil
	}
	return t.run(ctx, in)
}

// emptyToolInput is what an absent input decodes as.
var emptyToolInput = jsontext.Value(`{}`)

// toolInputObject reads an absent input as an empty object. Providers differ
// on what a no-argument call carries: nothing, an empty string, or null.
func toolInputObject(input jsontext.Value) jsontext.Value {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return emptyToolInput
	}
	return input
}

// missingRequired names every required parameter the input left out, in
// schema order, with dotted paths for nested objects and indices for array
// elements. encoding/json/v2 does not enforce required members — it leaves
// the Go field at its zero value — so the check happens here, against the
// schema the model was given.
func missingRequired(s *Schema, v jsontext.Value) []string {
	var out []string
	collectMissing(s, v, "", &out)
	return out
}

func collectMissing(s *Schema, v jsontext.Value, path string, out *[]string) {
	switch s.Type {
	case SchemaObject:
		var members map[string]jsontext.Value
		if err := json.Unmarshal(v, &members); err != nil {
			return
		}
		required := make(map[string]bool, len(s.Required))
		for _, name := range s.Required {
			required[name] = true
		}
		for i := range s.Properties {
			p := &s.Properties[i]
			name := joinParameterPath(path, p.Name)
			child, present := members[p.Name]
			if !present {
				if required[p.Name] {
					*out = append(*out, name)
				}
				continue
			}
			collectMissing(&p.Schema, child, name, out)
		}
	case SchemaArray:
		if s.Items == nil {
			return
		}
		var elems []jsontext.Value
		if err := json.Unmarshal(v, &elems); err != nil {
			return
		}
		for i, elem := range elems {
			collectMissing(s.Items, elem, path+"["+strconv.Itoa(i)+"]", out)
		}
	}
}

// joinParameterPath builds the dotted name a model reads in an error message.
func joinParameterPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}
