package openrouter

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// lookupInput is the strict tool's input; every parameter is required, which
// is the one shape every dialect that carries strict accepts.
type lookupInput struct {
	Query string `json:"query" desc:"what to look up"`
}

// optionalInput has an optional parameter, which OpenAI's strict mode refuses.
type optionalInput struct {
	Query string `json:"query" desc:"what to look up"`
	Limit *int   `json:"limit" desc:"how many results"`
}

func strictLookup[In any](t *testing.T) goodall.Tool {
	t.Helper()
	tool, err := goodall.NewTool("lookup", "Look something up",
		func(context.Context, In) (goodall.ToolResult, error) { return goodall.ToolResult{}, nil },
		goodall.WithStrictness(goodall.Strict))
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	return tool
}

func strictRequest(model string, tool goodall.Tool) *goodall.Request {
	return &goodall.Request{
		Model:    model,
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
		Tools:    []goodall.Tool{tool},
		Cache:    goodall.CacheOff,
	}
}

const strictFunction = `"function":{"name":"lookup","description":"Look something up",` +
	`"parameters":{"type":"object","properties":{"query":{"type":"string","description":"what to look up"}},` +
	`"required":["query"],"additionalProperties":false},"strict":true}`

func TestAStrictToolSendsFunctionStrictWhereTheDialectCarriesIt(t *testing.T) {
	for _, d := range []Dialect{OpenRouter, OpenAI} {
		t.Run(d.String(), func(t *testing.T) {
			body := marshalWire(t, strictRequest("openai/gpt-6-astra", strictLookup[lookupInput](t)), d, false)
			if !bytes.Contains(body, []byte(strictFunction)) {
				t.Errorf("body = %s\nwant to contain %s", body, strictFunction)
			}
		})
	}
}

func TestAGuidedToolSendsNoStrictMember(t *testing.T) {
	for _, d := range []Dialect{OpenRouter, OpenAI, LMStudio, Generic} {
		body := marshalWire(t, strictRequest("some/model", everyFeatureRequest(t).Tools[0]), d, false)
		if bytes.Contains(body, []byte(`"strict"`)) {
			t.Errorf("%s: body = %s\nwant no strict member on a guided tool", d, body)
		}
	}
}

func TestAStrictToolIsRefusedOnADialectWithNoStrictMember(t *testing.T) {
	for _, d := range []Dialect{LMStudio, Generic} {
		_, err := translateRequest(strictRequest("local-model", strictLookup[lookupInput](t)), d, false)
		capErr, ok := errors.AsType[*goodall.CapabilityError](err)
		if !ok {
			t.Fatalf("%s: err = %v, want a *goodall.CapabilityError", d, err)
		}
		if capErr.Capability != goodall.CapStrictTools || capErr.Provider != ProviderName {
			t.Errorf("%s: err = %+v, want %s refused on %s", d, capErr, goodall.CapStrictTools, ProviderName)
		}
	}
}

func TestAnOptionalParameterIsRefusedWhereOpenAIRulesApply(t *testing.T) {
	cases := []struct {
		dialect Dialect
		model   string
	}{
		{OpenAI, "gpt-5.6-terra"},
		{OpenRouter, "openai/gpt-6-astra"},
	}
	for _, c := range cases {
		_, err := translateRequest(strictRequest(c.model, strictLookup[optionalInput](t)), c.dialect, false)
		if !errors.Is(err, goodall.KindInvalidRequest) {
			t.Fatalf("%s %s: err = %v, want it to wrap KindInvalidRequest", c.dialect, c.model, err)
		}
		if !strings.Contains(err.Error(), `"limit"`) || !strings.Contains(err.Error(), "required") {
			t.Errorf("%s %s: err = %q, want it to name the optional parameter and the rule", c.dialect, c.model, err)
		}
	}
	// Claude takes optional parameters under strict.
	if _, err := translateRequest(strictRequest("anthropic/claude-sonnet-5.5", strictLookup[optionalInput](t)), OpenRouter, false); err != nil {
		t.Errorf("an optional parameter on a Claude model was refused: %v", err)
	}
}

func TestClaudesLimitsApplyThroughOpenRouter(t *testing.T) {
	req := strictRequest("anthropic/claude-sonnet-5.5", strictLookup[lookupInput](t))
	for len(req.Tools) <= 20 {
		req.Tools = append(req.Tools, strictLookup[lookupInput](t))
	}
	_, err := translateRequest(req, OpenRouter, false)
	if !errors.Is(err, goodall.KindInvalidRequest) || !strings.Contains(err.Error(), "at most 20 strict tools") {
		t.Errorf("err = %v, want the 20-tool limit named", err)
	}
}

func TestOpenRouterSendsTheAnthropicBetaOnlyWithAStrictTool(t *testing.T) {
	var got http.Header
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		serveStream(w, fixture(t, "stream-reasoning-tools.txt"))
	})
	if _, err := c.Stream(t.Context(), strictRequest("anthropic/claude-sonnet-5.5", strictLookup[lookupInput](t))).Collect(); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v := got.Get(HeaderAnthropicBeta); v != anthropicStructuredOutputsBeta {
		t.Errorf("%s = %q on a strict request, want %q", HeaderAnthropicBeta, v, anthropicStructuredOutputsBeta)
	}
	if _, err := c.Stream(t.Context(), simpleRequest()).Collect(); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v := got.Get(HeaderAnthropicBeta); v != "" {
		t.Errorf("%s = %q on a request with no strict tool, want none", HeaderAnthropicBeta, v)
	}
}

func TestTheOpenAIDialectSendsNoAnthropicBeta(t *testing.T) {
	var got http.Header
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		serveStream(w, fixture(t, "stream-reasoning-tools.txt"))
	}, WithDialect(OpenAI))
	c.Stream(t.Context(), strictRequest("gpt-5.6-terra", strictLookup[lookupInput](t))).Collect()
	if v := got.Get(HeaderAnthropicBeta); v != "" {
		t.Errorf("%s = %q under the OpenAI dialect, want none", HeaderAnthropicBeta, v)
	}
}

func TestTheRecordedStrictCallsWereSentStrictAndMatchTheirSchema(t *testing.T) {
	for _, name := range []string{"live_strict_tool_use", "live_openai_strict_tool_use"} {
		t.Run(name, func(t *testing.T) {
			sent := string(fixture(t, name+".request.json"))
			if !strings.Contains(sent, `"strict": true`) || strings.Contains(sent, `"tool_choice"`) {
				t.Errorf("the recorded request was not a strict tool under auto:\n%s", sent)
			}
			uses := collectRecording(t, name+".sse").Message.ToolUses()
			if len(uses) != 1 || uses[0].Name != "get_weather" {
				t.Fatalf("tool calls = %+v, want one get_weather", uses)
			}
			var input struct {
				City string `json:"city"`
				Unit string `json:"unit"`
			}
			if err := json.Unmarshal(uses[0].Input, &input, json.RejectUnknownMembers(true)); err != nil {
				t.Fatalf("the strict input does not match its schema: %v", err)
			}
			if input.City != recordedToolCity || input.Unit != "celsius" {
				t.Errorf("input = %+v, want %s in celsius", input, recordedToolCity)
			}
		})
	}
}

func TestTheCatalogReadsStrictToolsFromStructuredOutputs(t *testing.T) {
	with := modelInfo(&catalogModel{ID: "anthropic/claude-opus-4.5", SupportedParameters: []parameter{paramTools, paramStructuredOutputs}})
	if with.Capabilities.StrictTools != goodall.Supported {
		t.Errorf("StrictTools = %v with structured_outputs listed, want supported", with.Capabilities.StrictTools)
	}
	without := modelInfo(&catalogModel{ID: "anthropic/claude-opus-4.1", SupportedParameters: []parameter{paramTools}})
	if without.Capabilities.StrictTools != goodall.Unsupported {
		t.Errorf("StrictTools = %v with structured_outputs absent, want unsupported", without.Capabilities.StrictTools)
	}
}
