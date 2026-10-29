package goodall

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"
	"testing"
)

type strictInput struct {
	City  string  `json:"city" desc:"the city"`
	Units *string `json:"units" desc:"metric or imperial"`
}

func strictHandler(context.Context, strictInput) (ToolResult, error) { return TextResult("ok"), nil }

func strictReportingHandler(context.Context, strictInput, func(Event)) (ToolOutcome, error) {
	return ToolOutcome{Result: TextResult("ok")}, nil
}

func TestNewToolIsGuidedUnlessAskedOtherwise(t *testing.T) {
	plain, err := NewTool("plain", "A plain tool.", strictHandler)
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	if got := StrictnessOf(plain); got != Guided {
		t.Errorf("StrictnessOf(NewTool without options) = %q, want Guided", got)
	}
	reporting, err := NewReportingTool("reporting", "A reporting tool.", strictReportingHandler)
	if err != nil {
		t.Fatalf("NewReportingTool: %v", err)
	}
	if got := StrictnessOf(reporting); got != Guided {
		t.Errorf("StrictnessOf(NewReportingTool without options) = %q, want Guided", got)
	}
}

func TestWithStrictnessMarksATypedTool(t *testing.T) {
	plain, err := NewTool("plain", "A plain tool.", strictHandler, WithStrictness(Strict))
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	if got := StrictnessOf(plain); got != Strict {
		t.Errorf("StrictnessOf(NewTool with Strict) = %q, want Strict", got)
	}
	reporting, err := NewReportingTool("reporting", "A reporting tool.", strictReportingHandler, WithStrictness(Strict))
	if err != nil {
		t.Fatalf("NewReportingTool: %v", err)
	}
	if got := StrictnessOf(reporting); got != Strict {
		t.Errorf("StrictnessOf(NewReportingTool with Strict) = %q, want Strict", got)
	}
	if _, ok := reporting.(Reporter); !ok {
		t.Error("a strict reporting tool no longer implements Reporter")
	}
}

// strictStub is a hand-written tool that declares its own strictness.
type strictStub struct {
	*stubTool
	strictness Strictness
}

func (s strictStub) Strictness() Strictness { return s.strictness }

func TestStrictnessOfAHandWrittenTool(t *testing.T) {
	if got := StrictnessOf(&stubTool{name: "echo"}); got != Guided {
		t.Errorf("a tool without the interface reads as %q, want Guided", got)
	}
	if got := StrictnessOf(strictStub{stubTool: &stubTool{name: "echo"}, strictness: Strict}); got != Strict {
		t.Errorf("a tool declaring Strict reads as %q", got)
	}
	if got := StrictnessOf(nil); got != Guided {
		t.Errorf("a nil tool reads as %q, want Guided", got)
	}
}

func TestStrictnessString(t *testing.T) {
	if Guided.String() != "guided" || Strict.String() != "strict" {
		t.Errorf("String() = %q, %q; want guided, strict", Guided.String(), Strict.String())
	}
}

type looseInput struct {
	Name  string         `json:"name"`
	Extra jsontext.Value `json:"extra"`
}

func TestNewToolRefusesAStrictSchemaThatAcceptsAnyValue(t *testing.T) {
	run := func(context.Context, looseInput) (ToolResult, error) { return ToolResult{}, nil }
	if _, err := NewTool("loose", "Takes anything.", run); err != nil {
		t.Fatalf("a guided tool with an unconstrained parameter was refused: %v", err)
	}
	_, err := NewTool("loose", "Takes anything.", run, WithStrictness(Strict))
	if err == nil {
		t.Fatal("a strict tool with an unconstrained parameter was accepted; both providers refuse {} under strict")
	}
	if !strings.Contains(err.Error(), `"loose"`) || !strings.Contains(err.Error(), "extra") {
		t.Errorf("err = %q, want it to name the tool and the parameter", err)
	}
	if _, ok := errors.AsType[*SchemaError](err); !ok {
		t.Errorf("err = %v, want it to wrap a *SchemaError", err)
	}
}

func TestNewToolRefusesAnUnknownStrictness(t *testing.T) {
	if _, err := NewTool("plain", "A plain tool.", strictHandler, WithStrictness("fierce")); err == nil {
		t.Error("an undefined Strictness was accepted")
	}
}

func TestValidateStrict(t *testing.T) {
	str := Schema{Type: SchemaString}
	object := func(props ...Property) Schema {
		return Schema{Type: SchemaObject, Properties: props, AdditionalProperties: AdditionalPropertiesForbidden}
	}
	open := object(Property{Name: "a", Schema: str})
	open.AdditionalProperties = AdditionalPropertiesUnset
	cases := []struct {
		name   string
		schema Schema
		want   string // a fragment of the error, or "" for none
	}{
		{"an inferred-shape object", object(Property{Name: "a", Schema: str}), ""},
		{"an object that leaves additionalProperties unset", open, "additional properties"},
		{"a nested object that allows extra members", object(Property{Name: "o", Schema: Schema{
			Type: SchemaObject, AdditionalProperties: AdditionalPropertiesAllowed}}), `"o"`},
		{"an unconstrained array element", object(Property{Name: "list", Schema: Schema{
			Type: SchemaArray, Items: &Schema{}}}), `"list[]"`},
		{"an array with no items schema", object(Property{Name: "list", Schema: Schema{Type: SchemaArray}}), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.schema.ValidateStrict()
			switch {
			case c.want == "" && err != nil:
				t.Errorf("ValidateStrict = %v, want nil", err)
			case c.want != "" && err == nil:
				t.Errorf("ValidateStrict = nil, want an error naming %s", c.want)
			case c.want != "" && !strings.Contains(err.Error(), c.want):
				t.Errorf("ValidateStrict = %q, want it to contain %s", err, c.want)
			}
		})
	}
}
