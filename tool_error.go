package goodall

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The text below is product surface: it is what a model reads on its next
// turn, and the only chance it has to fix a bad call. Every message says what
// went wrong in the model's own vocabulary — parameters, not Go types — and
// then lists the whole parameter set, so a model that got one member wrong
// does not have to guess at the rest.

// inputErrorText joins the problem statement and the parameter listing.
func inputErrorText(s *Schema, problem string) string {
	return problem + "\n\n" + describeParameters(s)
}

// inputProblem states, in one line, what about the input could not be used.
func inputProblem(s *Schema, err error) string {
	if serr, ok := errors.AsType[*json.SemanticError](err); ok {
		path, target := parameterAt(s, serr.JSONPointer)
		switch {
		case errors.Is(err, json.ErrUnknownName):
			return fmt.Sprintf("unknown parameter %q", path)
		case path == "":
			return fmt.Sprintf("the input must be a JSON object, but the value sent was %s", sentValue(serr))
		default:
			return fmt.Sprintf("parameter %q takes %s, but the value sent was %s", path, expectedValue(target), sentValue(serr))
		}
	}
	if syn, ok := errors.AsType[*jsontext.SyntacticError](err); ok {
		detail := syn.Error()
		if syn.Err != nil {
			detail = syn.Err.Error()
		}
		return "the input is not valid JSON: " + detail
	}
	return "the input could not be read: " + err.Error()
}

// missingProblem states which required parameters the input left out.
func missingProblem(missing []string) string {
	if len(missing) == 1 {
		return fmt.Sprintf("missing required parameter %q", missing[0])
	}
	quoted := make([]string, len(missing))
	for i, name := range missing {
		quoted[i] = strconv.Quote(name)
	}
	return "missing required parameters " + strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}

// parameterAt walks a JSON pointer from a decode error down the schema,
// returning the dotted parameter name the model would recognise and the
// schema at that name, which is nil when the pointer names something the
// schema does not have.
func parameterAt(s *Schema, ptr jsontext.Pointer) (string, *Schema) {
	path := ""
	cur := s
	for tok := range ptr.Tokens() {
		if cur != nil && cur.Type == SchemaArray {
			path += "[" + tok + "]"
			cur = cur.Items
			continue
		}
		path = joinParameterPath(path, tok)
		cur = propertySchema(cur, tok)
	}
	return path, cur
}

// propertySchema finds a named property of an object schema.
func propertySchema(s *Schema, name string) *Schema {
	if s == nil {
		return nil
	}
	for i := range s.Properties {
		if s.Properties[i].Name == name {
			return &s.Properties[i].Schema
		}
	}
	return nil
}

// expectedValue names the kind of value a parameter accepts.
func expectedValue(s *Schema) string {
	if s == nil {
		return "a different kind of value"
	}
	switch s.Type {
	case SchemaString:
		return "a string"
	case SchemaInteger:
		return "an integer"
	case SchemaNumber:
		return "a number"
	case SchemaBoolean:
		return "a boolean"
	case SchemaObject:
		return "an object"
	case SchemaArray:
		return "an array"
	default:
		return "any JSON value"
	}
}

// maxQuotedValue caps how much of the offending value is echoed back. A model
// that sent a long string does not need to be shown all of it, and the
// listing below is the part worth its attention.
const maxQuotedValue = 80

// sentValue describes the value the model actually sent, quoting it when the
// decoder kept it and naming its JSON kind when it did not.
func sentValue(serr *json.SemanticError) string {
	if len(serr.JSONValue) > 0 {
		v := string(serr.JSONValue)
		if len(v) > maxQuotedValue {
			v = v[:maxQuotedValue] + "…"
		}
		return v
	}
	switch serr.JSONKind {
	case 'n':
		return "null"
	case 't', 'f':
		return "a boolean"
	case '"':
		return "a string"
	case '0':
		return "a number"
	case '{':
		return "an object"
	case '[':
		return "an array"
	default:
		return "a value this tool cannot read"
	}
}

// describeParameters lists every parameter in schema order, one per line,
// with its type, whether it is required, its permitted values and its
// description. Nested objects appear under dotted names and array elements
// under a [] suffix, so every line is a name the model can use verbatim.
func describeParameters(s *Schema) string {
	if s == nil || s.Type != SchemaObject || len(s.Properties) == 0 {
		return "This tool takes no parameters."
	}
	var b strings.Builder
	b.WriteString("Parameters:")
	appendParameterLines(&b, s, "")
	return b.String()
}

func appendParameterLines(b *strings.Builder, s *Schema, path string) {
	required := make(map[string]bool, len(s.Required))
	for _, name := range s.Required {
		required[name] = true
	}
	for i := range s.Properties {
		p := &s.Properties[i]
		name := joinParameterPath(path, p.Name)
		b.WriteString("\n  ")
		b.WriteString(name)
		b.WriteString(" (")
		b.WriteString(parameterTypeName(&p.Schema))
		if required[p.Name] {
			b.WriteString(", required")
		} else {
			b.WriteString(", optional")
		}
		if len(p.Schema.Enum) > 0 {
			b.WriteString(", one of ")
			for j, v := range p.Schema.Enum {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(strconv.Quote(v))
			}
		}
		if p.Schema.Format != "" {
			b.WriteString(", format ")
			b.WriteString(strconv.Quote(p.Schema.Format))
		}
		b.WriteString(")")
		if p.Schema.Description != "" {
			b.WriteString(": ")
			b.WriteString(p.Schema.Description)
		}
		switch p.Schema.Type {
		case SchemaObject:
			appendParameterLines(b, &p.Schema, name)
		case SchemaArray:
			if p.Schema.Items != nil && p.Schema.Items.Type == SchemaObject {
				appendParameterLines(b, p.Schema.Items, name+"[]")
			}
		}
	}
}

// parameterTypeName is the JSON type of a parameter as the listing names it.
func parameterTypeName(s *Schema) string {
	switch s.Type {
	case SchemaArray:
		if s.Items == nil || s.Items.Type == SchemaUnconstrained {
			return "array"
		}
		return "array of " + string(s.Items.Type)
	case SchemaUnconstrained:
		return "any"
	default:
		return string(s.Type)
	}
}
