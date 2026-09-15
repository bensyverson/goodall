package goodall

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

// orderedFixture is the schema most of these tests start from: three
// properties whose declaration order is not alphabetical, so any map-backed
// implementation would visibly reorder them.
func orderedFixture() *Schema {
	return &Schema{
		Type: SchemaObject,
		Properties: Properties{
			{Name: "zebra", Schema: Schema{Type: SchemaString, Description: "last alphabetically, first in the struct"}},
			{Name: "alpha", Schema: Schema{Type: SchemaInteger}},
			{Name: "middle", Schema: Schema{Type: SchemaBoolean}},
		},
		Required:             []string{"zebra", "alpha"},
		AdditionalProperties: AdditionalPropertiesForbidden,
	}
}

const orderedFixtureJSON = `{"type":"object","properties":{"zebra":{"type":"string","description":"last alphabetically, first in the struct"},"alpha":{"type":"integer"},"middle":{"type":"boolean"}},"required":["zebra","alpha"],"additionalProperties":false}`

func TestSchemaMarshalsPropertiesInSliceOrder(t *testing.T) {
	got, err := json.Marshal(orderedFixture())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != orderedFixtureJSON {
		t.Errorf("Marshal =\n%s\nwant\n%s", got, orderedFixtureJSON)
	}
}

// TestSchemaMemberOrderIsFixed pins the emitted member order. The schema is
// deliberately not a legal one (Validate rejects it) because only a schema
// that sets every member can prove the order of every member.
func TestSchemaMemberOrderIsFixed(t *testing.T) {
	s := &Schema{
		Type:                 SchemaObject,
		Description:          "d",
		Enum:                 []string{"a"},
		Properties:           Properties{{Name: "p", Schema: Schema{}}},
		Required:             []string{"p"},
		AdditionalProperties: AdditionalPropertiesForbidden,
		Items:                &Schema{Type: SchemaString},
		Format:               "f",
	}
	const want = `{"type":"object","description":"d","enum":["a"],"properties":{"p":{}},"required":["p"],"additionalProperties":false,"items":{"type":"string"},"format":"f"}`
	got, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != want {
		t.Errorf("Marshal =\n%s\nwant\n%s", got, want)
	}
}

func TestSchemaUnconstrainedMarshalsAsEmptyObject(t *testing.T) {
	got, err := json.Marshal(&Schema{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != `{}` {
		t.Errorf("Marshal = %s, want {}", got)
	}
}

// TestSchemaEmptyPropertiesMarshal proves the difference between "no
// properties member" (nil) and "a tool that takes no parameters" (a non-nil
// empty slice), which providers want spelled out as "properties": {}.
func TestSchemaEmptyPropertiesMarshal(t *testing.T) {
	nilProps, err := json.Marshal(&Schema{Type: SchemaObject})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(nilProps) != `{"type":"object"}` {
		t.Errorf("nil Properties = %s, want {\"type\":\"object\"}", nilProps)
	}
	emptyProps, err := json.Marshal(&Schema{Type: SchemaObject, Properties: Properties{}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(emptyProps) != `{"type":"object","properties":{}}` {
		t.Errorf("empty Properties = %s, want {\"type\":\"object\",\"properties\":{}}", emptyProps)
	}
}

func TestSchemaRoundTripPreservesPropertyOrder(t *testing.T) {
	var got Schema
	if err := json.Unmarshal([]byte(orderedFixtureJSON), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	wantNames := []string{"zebra", "alpha", "middle"}
	if len(got.Properties) != len(wantNames) {
		t.Fatalf("got %d properties, want %d", len(got.Properties), len(wantNames))
	}
	for i, name := range wantNames {
		if got.Properties[i].Name != name {
			t.Errorf("property %d = %q, want %q", i, got.Properties[i].Name, name)
		}
	}
	again, err := json.Marshal(&got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(again) != orderedFixtureJSON {
		t.Errorf("round trip =\n%s\nwant\n%s", again, orderedFixtureJSON)
	}
}

func TestPropertiesUnmarshalRejectsNonObject(t *testing.T) {
	var p Properties
	if err := json.Unmarshal([]byte(`["a"]`), &p); err == nil {
		t.Fatal("Unmarshal of a JSON array into Properties succeeded, want an error")
	}
}

// TestSchemaInsideProviderStruct is how both providers will emit it: the
// schema is a member of a larger request object and must keep its order
// there too.
func TestSchemaInsideProviderStruct(t *testing.T) {
	type wireTool struct {
		Name        string  `json:"name"`
		InputSchema *Schema `json:"input_schema"`
	}
	got, err := json.Marshal(wireTool{Name: "lookup", InputSchema: orderedFixture()})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"name":"lookup","input_schema":` + orderedFixtureJSON + `}`
	if string(got) != want {
		t.Errorf("Marshal =\n%s\nwant\n%s", got, want)
	}
}

func TestAdditionalPropertiesJSON(t *testing.T) {
	type holder struct {
		A AdditionalProperties `json:"a"`
	}
	marshal := []struct {
		value   AdditionalProperties
		want    string
		wantErr bool
	}{
		{AdditionalPropertiesForbidden, `{"a":false}`, false},
		{AdditionalPropertiesAllowed, `{"a":true}`, false},
		{AdditionalPropertiesUnset, "", true},
	}
	for _, c := range marshal {
		got, err := json.Marshal(holder{c.value})
		if c.wantErr {
			if err == nil {
				t.Errorf("Marshal(%q) = %s, want an error", string(c.value), got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Marshal(%q): %v", string(c.value), err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("Marshal(%q) = %s, want %s", string(c.value), got, c.want)
		}
	}

	unmarshal := []struct {
		doc     string
		want    AdditionalProperties
		wantErr bool
	}{
		{`{"a":false}`, AdditionalPropertiesForbidden, false},
		{`{"a":true}`, AdditionalPropertiesAllowed, false},
		{`{"a":"false"}`, AdditionalPropertiesUnset, true},
		{`{"a":null}`, AdditionalPropertiesUnset, true},
	}
	for _, c := range unmarshal {
		var h holder
		err := json.Unmarshal([]byte(c.doc), &h)
		if c.wantErr {
			if err == nil {
				t.Errorf("Unmarshal(%s) = %q, want an error", c.doc, string(h.A))
			}
			continue
		}
		if err != nil {
			t.Errorf("Unmarshal(%s): %v", c.doc, err)
			continue
		}
		if h.A != c.want {
			t.Errorf("Unmarshal(%s) = %q, want %q", c.doc, string(h.A), string(c.want))
		}
	}
}

func TestSchemaValidateAccepts(t *testing.T) {
	cases := map[string]*Schema{
		"object":         orderedFixture(),
		"no properties":  {Type: SchemaObject, Properties: Properties{}, AdditionalProperties: AdditionalPropertiesForbidden},
		"unconstrained":  {},
		"string enum":    {Type: SchemaString, Enum: []string{"a", "b"}},
		"array of items": {Type: SchemaArray, Items: &Schema{Type: SchemaString}},
		"nested": {
			Type: SchemaObject,
			Properties: Properties{
				{Name: "list", Schema: Schema{Type: SchemaArray, Items: &Schema{Type: SchemaObject, Properties: Properties{{Name: "x", Schema: Schema{Type: SchemaInteger}}}, Required: []string{"x"}, AdditionalProperties: AdditionalPropertiesForbidden}}},
			},
			AdditionalProperties: AdditionalPropertiesForbidden,
		},
	}
	for name, s := range cases {
		if err := s.Validate(); err != nil {
			t.Errorf("%s: Validate() = %v, want nil", name, err)
		}
	}
}

func TestSchemaValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		schema *Schema
		want   string // a substring the message must carry
	}{
		{
			"required names a missing property",
			&Schema{Type: SchemaObject, Properties: Properties{{Name: "a", Schema: Schema{Type: SchemaString}}}, Required: []string{"b"}, AdditionalProperties: AdditionalPropertiesForbidden},
			`"b"`,
		},
		{
			"enum on a non-string",
			&Schema{Type: SchemaInteger, Enum: []string{"1"}},
			"enum",
		},
		{
			"items on a non-array",
			&Schema{Type: SchemaString, Items: &Schema{Type: SchemaString}},
			"items",
		},
		{
			"properties on a non-object",
			&Schema{Type: SchemaString, Properties: Properties{{Name: "a", Schema: Schema{Type: SchemaString}}}},
			"properties",
		},
		{
			"required on a non-object",
			&Schema{Type: SchemaArray, Items: &Schema{Type: SchemaString}, Required: []string{"a"}},
			"required",
		},
		{
			"additionalProperties on a non-object",
			&Schema{Type: SchemaString, AdditionalProperties: AdditionalPropertiesForbidden},
			"additionalProperties",
		},
		{
			"duplicate property names",
			&Schema{Type: SchemaObject, Properties: Properties{
				{Name: "a", Schema: Schema{Type: SchemaString}},
				{Name: "a", Schema: Schema{Type: SchemaInteger}},
			}, AdditionalProperties: AdditionalPropertiesForbidden},
			`"a"`,
		},
		{
			"empty property name",
			&Schema{Type: SchemaObject, Properties: Properties{{Name: "", Schema: Schema{Type: SchemaString}}}, AdditionalProperties: AdditionalPropertiesForbidden},
			"empty",
		},
		{
			"invalid nested property",
			&Schema{Type: SchemaObject, Properties: Properties{
				{Name: "inner", Schema: Schema{Type: SchemaInteger, Enum: []string{"1"}}},
			}, AdditionalProperties: AdditionalPropertiesForbidden},
			"inner",
		},
		{
			"invalid items",
			&Schema{Type: SchemaArray, Items: &Schema{Type: SchemaInteger, Enum: []string{"1"}}},
			"[]",
		},
	}
	for _, c := range cases {
		err := c.schema.Validate()
		if err == nil {
			t.Errorf("%s: Validate() = nil, want an error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Validate() = %q, want it to mention %q", c.name, err, c.want)
		}
	}
}
