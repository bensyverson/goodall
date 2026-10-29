package strict

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// tool is a hand-written tool with a schema and a strictness of its own.
type tool struct {
	name       string
	schema     *goodall.Schema
	strictness goodall.Strictness
}

func (t tool) Name() string                   { return t.name }
func (t tool) Description() string            { return "a tool" }
func (t tool) Schema() *goodall.Schema        { return t.schema }
func (t tool) Strictness() goodall.Strictness { return t.strictness }
func (t tool) Execute(context.Context, jsontext.Value) (goodall.ToolResult, error) {
	return goodall.ToolResult{}, nil
}

// object builds an object schema with the named string properties, the
// first `required` of which are required.
func object(names []string, required int) *goodall.Schema {
	s := &goodall.Schema{Type: goodall.SchemaObject, AdditionalProperties: goodall.AdditionalPropertiesForbidden}
	for i, n := range names {
		s.Properties = append(s.Properties, goodall.Property{Name: n, Schema: goodall.Schema{Type: goodall.SchemaString}})
		if i < required {
			s.Required = append(s.Required, n)
		}
	}
	return s
}

func names(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

func strictTool(name string, s *goodall.Schema) goodall.Tool {
	return tool{name: name, schema: s, strictness: goodall.Strict}
}

func request(tools ...goodall.Tool) *goodall.Request {
	return &goodall.Request{Model: "claude-sonnet-5-5", Tools: tools}
}

// wantRefusal checks the error is an invalid request whose text carries each
// fragment.
func wantRefusal(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want a refusal naming %q", fragments)
	}
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("err = %v, want it to wrap KindInvalidRequest", err)
	}
	for _, f := range fragments {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("err = %q, want it to contain %q", err, f)
		}
	}
	if !strings.HasPrefix(err.Error(), "prov: ") {
		t.Errorf("err = %q, want it to start with the provider name", err)
	}
}

func TestClaudeToolLimit(t *testing.T) {
	var tools []goodall.Tool
	for i := range MaxClaudeTools {
		tools = append(tools, strictTool(fmt.Sprintf("t%d", i), object([]string{"x"}, 1)))
	}
	// Guided tools do not count toward the limit.
	tools = append(tools, tool{name: "guided", schema: object([]string{"x"}, 1)})
	if err := Check("prov", request(tools...), RulesClaude); err != nil {
		t.Fatalf("%d strict tools and a guided one were refused: %v", MaxClaudeTools, err)
	}
	tools = append(tools, strictTool("one_more", object([]string{"x"}, 1)))
	wantRefusal(t, Check("prov", request(tools...), RulesClaude), "21", "20 strict tools")
}

func TestClaudeOptionalParameterLimitCountsAcrossToolsAndDepth(t *testing.T) {
	// 10 optional at the top of one tool, 10 in a nested object of a second,
	// 4 in the element object of an array in a third: 24 in all.
	nested := object([]string{"o"}, 1)
	nested.Properties[0].Schema = *object(names("n", 10), 0)
	listed := object([]string{"list"}, 1)
	listed.Properties[0].Schema = goodall.Schema{Type: goodall.SchemaArray, Items: object(names("e", 4), 0)}
	tools := []goodall.Tool{
		strictTool("top", object(names("p", 12), 2)),
		strictTool("nested", nested),
		strictTool("listed", listed),
		tool{name: "guided", schema: object(names("g", 30), 0)},
	}
	if err := Check("prov", request(tools...), RulesClaude); err != nil {
		t.Fatalf("24 optional parameters were refused: %v", err)
	}
	tools = append(tools, strictTool("one_more", object([]string{"x"}, 0)))
	wantRefusal(t, Check("prov", request(tools...), RulesClaude), "25 optional parameters", "at most 24")
}

func TestClaudeRefusesAnUnsupportedFormat(t *testing.T) {
	s := object([]string{"when"}, 1)
	s.Properties[0].Schema.Format = "date-time"
	if err := Check("prov", request(strictTool("ok", s)), RulesClaude); err != nil {
		t.Fatalf("date-time was refused: %v", err)
	}
	s.Properties[0].Schema.Format = "color"
	wantRefusal(t, Check("prov", request(strictTool("paint", s)), RulesClaude), `"paint"`, `"when"`, `"color"`)
	// A guided tool's format is the model's business, not a strict grammar's.
	if err := Check("prov", request(tool{name: "guided", schema: s}), RulesClaude); err != nil {
		t.Errorf("a guided tool's format was checked: %v", err)
	}
}

func TestEveryRuleSetRefusesAnUnenforceableSchema(t *testing.T) {
	loose := object([]string{"x"}, 1)
	loose.Properties[0].Schema = goodall.Schema{}
	for _, rules := range []Rules{RulesClaude, RulesOpenAI} {
		wantRefusal(t, Check("prov", request(strictTool("loose", loose)), rules), `"loose"`, `"x"`)
	}
}

func TestOpenAIRequiresEveryProperty(t *testing.T) {
	if err := Check("prov", request(strictTool("all", object(names("p", 3), 3))), RulesOpenAI); err != nil {
		t.Fatalf("a schema requiring every property was refused: %v", err)
	}
	nested := object([]string{"o"}, 1)
	nested.Properties[0].Schema = *object([]string{"a", "b"}, 1)
	wantRefusal(t, Check("prov", request(strictTool("deep", nested)), RulesOpenAI), `"deep"`, `"o.b"`, "required")
	// OpenAI has no 20-tool limit to borrow.
	var many []goodall.Tool
	for i := range MaxClaudeTools + 1 {
		many = append(many, strictTool(fmt.Sprintf("t%d", i), object([]string{"x"}, 1)))
	}
	if err := Check("prov", request(many...), RulesOpenAI); err != nil {
		t.Errorf("Claude's tool limit was applied to OpenAI: %v", err)
	}
}

func TestUncheckedRulesLetEverythingThrough(t *testing.T) {
	loose := object([]string{"x"}, 0)
	loose.Properties[0].Schema = goodall.Schema{}
	if err := Check("prov", request(strictTool("loose", loose)), RulesUnchecked); err != nil {
		t.Errorf("an upstream with unknown rules was refused: %v", err)
	}
}

func TestARequestWithNoStrictToolIsNeverChecked(t *testing.T) {
	loose := object(names("p", 40), 0)
	loose.Properties[0].Schema = goodall.Schema{}
	if err := Check("prov", request(tool{name: "guided", schema: loose}), RulesClaude); err != nil {
		t.Errorf("a guided request was refused: %v", err)
	}
}

func TestACatalogRefusalNamesTheCapability(t *testing.T) {
	req := request(strictTool("t", object([]string{"x"}, 1)))
	req.ModelInfo = &goodall.ModelInfo{Capabilities: goodall.Capabilities{StrictTools: goodall.Unsupported}}
	err := Check("prov", req, RulesClaude)
	capErr, ok := errors.AsType[*goodall.CapabilityError](err)
	if !ok {
		t.Fatalf("err = %v, want a *goodall.CapabilityError", err)
	}
	if capErr.Capability != goodall.CapStrictTools || capErr.Provider != "prov" || capErr.Model != req.Model {
		t.Errorf("err = %+v, want strict_tools refused for %q on prov", capErr, req.Model)
	}
}

func TestRulesFor(t *testing.T) {
	for model, want := range map[string]Rules{
		"claude-sonnet-5-5":          RulesClaude,
		"anthropic/claude-opus-5.5":  RulesClaude,
		"openai/gpt-6-astra":         RulesOpenAI,
		"google/gemini-3.8-flash":    RulesUnchecked,
		"meta-llama/llama-5-70b":     RulesUnchecked,
		"anthropic/claude-haiku-4.5": RulesClaude,
	} {
		if got := RulesFor(model); got != want {
			t.Errorf("RulesFor(%q) = %v, want %v", model, got, want)
		}
	}
}
