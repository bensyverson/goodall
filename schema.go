package goodall

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
)

// SchemaType is the JSON Schema "type" keyword. goodall emits the draft
// 2020-12 subset the model providers accept, so only the types a tool
// parameter can take are named here.
type SchemaType string

const (
	// SchemaUnconstrained is the zero value: a schema that says nothing,
	// emitted as {}, which accepts any JSON value. It is what a
	// jsontext.Value parameter infers to.
	SchemaUnconstrained SchemaType = ""
	// SchemaObject is a JSON object with named properties.
	SchemaObject SchemaType = "object"
	// SchemaString is a JSON string.
	SchemaString SchemaType = "string"
	// SchemaInteger is a JSON number with no fractional part.
	SchemaInteger SchemaType = "integer"
	// SchemaNumber is any JSON number.
	SchemaNumber SchemaType = "number"
	// SchemaBoolean is a JSON boolean.
	SchemaBoolean SchemaType = "boolean"
	// SchemaArray is a JSON array, described by Items.
	SchemaArray SchemaType = "array"
)

// AdditionalProperties says whether an object schema accepts members its
// Properties do not name. It is a named string rather than a bool so the
// unset state is distinguishable from "allowed" and so the zero value means
// "not an object schema, emit nothing"; a bare bool would make the absent
// case and the false case the same value.
type AdditionalProperties string

const (
	// AdditionalPropertiesUnset emits no additionalProperties member. It is
	// the zero value and the only valid setting on a non-object schema.
	AdditionalPropertiesUnset AdditionalProperties = ""
	// AdditionalPropertiesForbidden emits false. Inference always chooses
	// it, because a model that invents a parameter should be told so.
	AdditionalPropertiesForbidden AdditionalProperties = "forbidden"
	// AdditionalPropertiesAllowed emits true.
	AdditionalPropertiesAllowed AdditionalProperties = "allowed"
)

// MarshalJSONTo writes the setting as the JSON boolean JSON Schema expects.
// The unset value has no JSON form: it is omitted by the omitzero option on
// Schema, and marshalling it on its own is an error rather than a silently
// wrong "false".
func (a AdditionalProperties) MarshalJSONTo(enc *jsontext.Encoder) error {
	switch a {
	case AdditionalPropertiesForbidden:
		return enc.WriteToken(jsontext.False)
	case AdditionalPropertiesAllowed:
		return enc.WriteToken(jsontext.True)
	default:
		return fmt.Errorf("goodall: additionalProperties %q has no JSON form", string(a))
	}
}

// UnmarshalJSONFrom reads the JSON boolean back into the typed setting.
func (a *AdditionalProperties) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	switch tok.Kind() {
	case 't':
		*a = AdditionalPropertiesAllowed
	case 'f':
		*a = AdditionalPropertiesForbidden
	default:
		return fmt.Errorf("goodall: additionalProperties must be a JSON boolean, got %v", tok.Kind())
	}
	return nil
}

// Property is one named member of an object schema. Properties hold their
// name because they live in a slice: a map would sort them, and the order
// properties are presented in measurably changes how a model fills them in.
type Property struct {
	// Name is the JSON member name, which is the name encoding/json/v2
	// matches when it decodes the tool's input.
	Name string
	// Schema describes the member's value.
	Schema Schema
}

// Properties is the ordered member list of an object schema. It marshals as
// a JSON object in slice order and unmarshals back in document order.
type Properties []Property

// MarshalJSONTo writes the properties as a JSON object, in slice order.
func (p Properties) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for i := range p {
		if err := enc.WriteToken(jsontext.String(p[i].Name)); err != nil {
			return err
		}
		if err := json.MarshalEncode(enc, &p[i].Schema); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// UnmarshalJSONFrom reads a JSON object into the slice, keeping the order the
// members were written in. Duplicate member names are rejected by the
// decoder itself.
func (p *Properties) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != '{' {
		return fmt.Errorf("goodall: properties must be a JSON object, got %v", tok.Kind())
	}
	out := make(Properties, 0, len(*p))
	for dec.PeekKind() != '}' {
		nameTok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		// The token is voided by the next read, so the name is copied out
		// before the nested value is decoded.
		name := nameTok.String()
		var s Schema
		if err := json.UnmarshalDecode(dec, &s); err != nil {
			return err
		}
		out = append(out, Property{Name: name, Schema: s})
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	*p = out
	return nil
}

// Schema is a JSON Schema for a tool's input, in the draft 2020-12 subset
// both providers accept. Members are emitted in the fixed order below, and
// unset members are omitted, so the bytes are deterministic and a request
// prefix stays byte-identical between turns.
//
// A Schema with no members set is the unconstrained schema {}: it accepts
// any JSON value.
type Schema struct {
	// Type is the JSON type this schema accepts; the zero value accepts any.
	Type SchemaType `json:"type,omitzero"`
	// Description is prose for the model. It is the single most effective
	// place to say what a parameter means.
	Description string `json:"description,omitzero"`
	// Enum lists the permitted values of a string schema.
	Enum []string `json:"enum,omitzero"`
	// Properties are an object schema's members, in the order they are sent.
	// A nil slice emits no properties member at all; an empty non-nil slice
	// emits "properties": {}, which is how a tool with no parameters is
	// spelled.
	Properties Properties `json:"properties,omitzero"`
	// Required names the properties the model must supply.
	Required []string `json:"required,omitzero"`
	// AdditionalProperties says whether unnamed members are accepted. It is
	// meaningful only on an object schema.
	AdditionalProperties AdditionalProperties `json:"additionalProperties,omitzero"`
	// Items describes the elements of an array schema.
	Items *Schema `json:"items,omitzero"`
	// Format is a JSON Schema format annotation, such as "date-time".
	Format string `json:"format,omitzero"`
}

// Validate reports the structural mistakes a hand-built or decoded schema can
// carry: a required name with no matching property, a keyword on a type it
// does not apply to, or two properties claiming one name. It does not check
// a schema against a document; it checks that the schema itself is one a
// provider will accept.
func (s *Schema) Validate() error {
	return s.validate("")
}

func (s *Schema) validate(path string) error {
	fail := func(format string, args ...any) error {
		msg := fmt.Sprintf(format, args...)
		if path == "" {
			return errors.New("goodall: schema: " + msg)
		}
		return errors.New("goodall: schema at " + path + ": " + msg)
	}
	if s.Type != SchemaObject {
		if len(s.Properties) > 0 {
			return fail("properties are only valid on an object schema")
		}
		if len(s.Required) > 0 {
			return fail("required is only valid on an object schema")
		}
		if s.AdditionalProperties != AdditionalPropertiesUnset {
			return fail("additionalProperties is only valid on an object schema")
		}
	}
	if len(s.Enum) > 0 && s.Type != SchemaString {
		return fail("enum is only valid on a string schema")
	}
	if s.Items != nil && s.Type != SchemaArray {
		return fail("items is only valid on an array schema")
	}
	named := make(map[string]bool, len(s.Properties))
	for _, p := range s.Properties {
		if p.Name == "" {
			return fail("a property has an empty name")
		}
		if named[p.Name] {
			return fail("duplicate property %q", p.Name)
		}
		named[p.Name] = true
	}
	for _, name := range s.Required {
		if !named[name] {
			return fail("required names %q, which is not a property", name)
		}
	}
	for i := range s.Properties {
		if err := s.Properties[i].Schema.validate(schemaPath(path, s.Properties[i].Name)); err != nil {
			return err
		}
	}
	if s.Items != nil {
		if err := s.Items.validate(path + "[]"); err != nil {
			return err
		}
	}
	return nil
}

// schemaPath joins a parent location and a property name for error messages.
func schemaPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}
