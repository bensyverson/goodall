package goodall

import (
	"cmp"
	"encoding"
	"encoding/json/jsontext"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// SchemaError reports a Go type that cannot be expressed as a tool schema,
// naming the field path that caused it so the author can find it without a
// stack trace. Inference never panics.
type SchemaError struct {
	// Path is the Go field path from the top-level type, such as
	// "Options.Mode" or "Options.Tags[]" for a slice element.
	Path string
	// Reason says what about the field cannot be expressed.
	Reason string
}

// Error implements the error interface.
func (e *SchemaError) Error() string {
	if e.Path == "" {
		return "goodall: " + e.Reason
	}
	return "goodall: field " + e.Path + ": " + e.Reason
}

// SchemaFor infers the JSON Schema for T, which must be a struct or a
// pointer to one. It is what NewTool uses to describe a tool's input, so the
// schema a model sees and the type the handler receives cannot drift apart.
func SchemaFor[T any]() (*Schema, error) {
	return InferSchema(reflect.TypeFor[T]())
}

// InferSchema infers the JSON Schema for a struct type by reflection,
// following the same rules encoding/json/v2 follows when it decodes into
// that type: fields in declaration order, json tags for names, embedded
// structs flattened, unexported and json:"-" fields skipped. A desc tag
// supplies a property's description and an enum tag its permitted values.
//
// Fields that encoding/json/v2 cannot round-trip through a schema — maps,
// interfaces, functions, channels, complex numbers, time.Duration — and any
// type that reaches itself are errors, not panics.
func InferSchema(t reflect.Type) (*Schema, error) {
	if t == nil {
		return nil, &SchemaError{Reason: "the top-level type must be a struct, got nothing"}
	}
	root := t
	for root.Kind() == reflect.Pointer {
		root = root.Elem()
	}
	name := root.Name()
	if name == "" {
		name = "input"
	}
	var in inferrer
	s, err := in.resolve(root, name)
	if err != nil {
		return nil, err
	}
	if s.Type != SchemaObject {
		return nil, &SchemaError{Reason: fmt.Sprintf("the top-level type must be a struct, got %s", t)}
	}
	return &s, nil
}

// inferrer carries the struct types on the current path so a type that
// reaches itself is reported rather than recursed into forever. It is
// path-scoped, not global: the same struct may legitimately appear on two
// sibling branches.
type inferrer struct {
	stack []reflect.Type
}

var (
	rawJSONType       = reflect.TypeFor[jsontext.Value]()
	timeType          = reflect.TypeFor[time.Time]()
	durationType      = reflect.TypeFor[time.Duration]()
	byteType          = reflect.TypeFor[byte]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// resolve maps a Go type to its schema. path names the field it came from.
func (in *inferrer) resolve(t reflect.Type, path string) (Schema, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case rawJSONType:
		// Raw JSON: whatever the model sends is handed to the tool as is.
		return Schema{}, nil
	case timeType:
		return Schema{Type: SchemaString, Format: "date-time"}, nil
	case durationType:
		// json/v2 refuses a time.Duration outright ("no default
		// representation"), so a schema promising an integer would produce
		// inputs the handler can never decode.
		return Schema{}, &SchemaError{path, "time.Duration has no representation in encoding/json/v2; use a named integer or string field instead"}
	}
	// A TextMarshaler encodes as a JSON string whatever its Go kind is, so
	// this must be decided before the kind switch would call it an object.
	if t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType) {
		return Schema{Type: SchemaString}, nil
	}
	switch t.Kind() {
	case reflect.String:
		return Schema{Type: SchemaString}, nil
	case reflect.Bool:
		return Schema{Type: SchemaBoolean}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return Schema{Type: SchemaInteger}, nil
	case reflect.Float32, reflect.Float64:
		return Schema{Type: SchemaNumber}, nil
	case reflect.Slice, reflect.Array:
		if t.Elem() == byteType {
			// json/v2 base64-encodes both []byte and [N]byte, but not a
			// slice of a *named* byte type, which stays a JSON array.
			return Schema{Type: SchemaString}, nil
		}
		item, err := in.resolve(t.Elem(), path+"[]")
		if err != nil {
			return Schema{}, err
		}
		return Schema{Type: SchemaArray, Items: &item}, nil
	case reflect.Struct:
		return in.object(t, path)
	}
	return Schema{}, &SchemaError{path, t.Kind().String() + " types are not supported"}
}

// object builds the schema for a struct type.
func (in *inferrer) object(t reflect.Type, path string) (Schema, error) {
	if slices.Contains(in.stack, t) {
		return Schema{}, &SchemaError{path, fmt.Sprintf("%s reaches itself; recursive schemas are not supported", t)}
	}
	in.stack = append(in.stack, t)
	defer func() { in.stack = in.stack[:len(in.stack)-1] }()

	fields, err := visibleFields(t, path)
	if err != nil {
		return Schema{}, err
	}
	props := make(Properties, 0, len(fields))
	var required []string
	for _, f := range fields {
		s, err := in.property(f)
		if err != nil {
			return Schema{}, err
		}
		props = append(props, Property{Name: f.name, Schema: s})
		if !f.tags.optional {
			required = append(required, f.name)
		}
	}
	return Schema{
		Type:                 SchemaObject,
		Properties:           props,
		Required:             required,
		AdditionalProperties: AdditionalPropertiesForbidden,
	}, nil
}

// property resolves one field's schema and applies its tags.
func (in *inferrer) property(f candidate) (Schema, error) {
	s, err := in.resolve(f.typ, f.path)
	if err != nil {
		return Schema{}, err
	}
	if f.tags.asString {
		// json/v2's string option quotes a number and rejects everything
		// else, so the schema must say string and inference must refuse the
		// combinations json/v2 refuses.
		if s.Type != SchemaInteger && s.Type != SchemaNumber {
			return Schema{}, &SchemaError{f.path, `the json "string" option is only valid on a numeric field`}
		}
		s = Schema{Type: SchemaString}
	}
	s.Description = f.tags.desc
	if len(f.tags.enum) > 0 {
		if s.Type != SchemaString {
			return Schema{}, &SchemaError{f.path, "an enum tag is only valid on a field that encodes as a JSON string"}
		}
		s.Enum = f.tags.enum
	}
	return s, nil
}

// candidate is one field that may become a property. depth and order carry
// the information json/v2 uses to resolve a name claimed by both an outer
// field and an embedded one.
type candidate struct {
	name  string
	path  string
	typ   reflect.Type
	tags  fieldTags
	depth int
	order int
}

// visibleFields returns the properties a struct contributes, in the order
// json/v2 writes them: depth-first through embedded structs, with a
// shallower field shadowing a deeper one of the same name and keeping its
// own position. Two fields at the same depth claiming one name is an error,
// as it is in json/v2.
func visibleFields(t reflect.Type, path string) ([]candidate, error) {
	var all []candidate
	order := 0
	if err := collectFields(t, path, 0, &order, &all); err != nil {
		return nil, err
	}
	winner := make(map[string]candidate, len(all))
	for _, c := range all {
		prev, seen := winner[c.name]
		switch {
		case !seen || c.depth < prev.depth:
			winner[c.name] = c
		case c.depth == prev.depth:
			return nil, &SchemaError{c.path, fmt.Sprintf("JSON name %q is already claimed by %s", c.name, prev.path)}
		}
	}
	out := make([]candidate, 0, len(winner))
	for _, c := range winner {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b candidate) int { return cmp.Compare(a.order, b.order) })
	return out, nil
}

// collectFields walks a struct and its embedded structs in declaration
// order, appending every candidate field it finds.
func collectFields(t reflect.Type, path string, depth int, order *int, out *[]candidate) error {
	for f := range t.Fields() {
		fieldPath := path + "." + f.Name
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		tags, err := parseFieldTags(f, fieldPath)
		if err != nil {
			return err
		}
		if f.Anonymous && tags.name == "" {
			if err := collectEmbedded(f, fieldPath, depth, order, out); err != nil {
				return err
			}
			continue
		}
		// An embedded field survives an unexported *type* name: json/v2
		// reads and writes it either way. An ordinary unexported field does
		// not exist as far as JSON is concerned.
		if !f.IsExported() && !f.Anonymous {
			continue
		}
		name := tags.name
		if name == "" {
			name = f.Name
		}
		*out = append(*out, candidate{name: name, path: fieldPath, typ: f.Type, tags: tags, depth: depth, order: *order})
		*order++
	}
	return nil
}

// collectEmbedded flattens an embedded field the way json/v2 does. Only a
// struct that encodes as a JSON object can be flattened; anything else needs
// an explicit JSON name, which json/v2 also insists on.
func collectEmbedded(f reflect.StructField, path string, depth int, order *int, out *[]candidate) error {
	et := f.Type
	for et.Kind() == reflect.Pointer {
		et = et.Elem()
	}
	if et.Kind() != reflect.Struct || et == timeType || et == rawJSONType ||
		et.Implements(textMarshalerType) || reflect.PointerTo(et).Implements(textMarshalerType) {
		return &SchemaError{path, fmt.Sprintf("embedded %s does not encode as a JSON object, so it needs an explicit json tag name", et)}
	}
	return collectFields(et, path, depth+1, order, out)
}

// fieldTags is everything the struct tags say about one field.
type fieldTags struct {
	name     string
	desc     string
	enum     []string
	optional bool
	asString bool
}

// parseFieldTags reads the json, desc and enum tags. The json tag is parsed
// the way json/v2 parses it, to the extent inference cares: the name, then
// the options that decide whether the member may be absent.
func parseFieldTags(f reflect.StructField, path string) (fieldTags, error) {
	var tags fieldTags
	tags.desc = f.Tag.Get("desc")
	if raw, ok := f.Tag.Lookup("enum"); ok {
		for v := range strings.SplitSeq(raw, ",") {
			v = strings.TrimSpace(v)
			if v == "" {
				return tags, &SchemaError{path, "the enum tag has an empty value"}
			}
			tags.enum = append(tags.enum, v)
		}
	}
	name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
	if strings.HasPrefix(name, "'") {
		return tags, &SchemaError{path, "a quoted json tag name is not supported"}
	}
	if name == "-" {
		return tags, &SchemaError{path, `a json tag name of "-" is not supported; use json:"-" alone to skip the field`}
	}
	tags.name = name
	for opt := range strings.SplitSeq(opts, ",") {
		switch opt {
		case "omitzero", "omitempty":
			tags.optional = true
		case "string":
			tags.asString = true
		}
	}
	// A pointer field is absent-able whatever its options say: json/v2
	// leaves it nil when the member is missing.
	if f.Type.Kind() == reflect.Pointer {
		tags.optional = true
	}
	return tags, nil
}
