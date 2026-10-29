package anthropic

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// strictStub is the weather tool, marked strict.
type strictStub struct{ stubTool }

func (strictStub) Strictness() goodall.Strictness { return goodall.Strict }

func strictWeatherTool() goodall.Tool { return strictStub{weatherTool().(stubTool)} }

func toolRequest(tools ...goodall.Tool) *goodall.Request {
	return &goodall.Request{
		Model:     "claude-sonnet-5-5",
		MaxTokens: 1024,
		Messages:  []goodall.Message{goodall.UserMessage(goodall.Text{Text: "Hi"})},
		Tools:     tools,
	}
}

func marshalBody(t *testing.T, body *wireRequest) string {
	t.Helper()
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	return string(out)
}

func TestAStrictToolSendsStrictTrueAndNoBeta(t *testing.T) {
	body, betas, err := translateRequest(toolRequest(strictWeatherTool()))
	if err != nil {
		t.Fatalf("translating: %v", err)
	}
	if len(betas) != 0 {
		t.Errorf("betas = %v, want none: strict tool use is generally available", betas)
	}
	got := marshalBody(t, body)
	want := `"tools":[{"name":"get_weather","description":"Look up the weather.","input_schema":{"type":"object",` +
		`"properties":{"city":{"type":"string","description":"the city"}},"required":["city"],"additionalProperties":false},` +
		`"strict":true}]`
	if !strings.Contains(got, want) {
		t.Errorf("body = %s\nwant it to contain %s", got, want)
	}
}

func TestAGuidedToolSendsNoStrictMember(t *testing.T) {
	body, _, err := translateRequest(toolRequest(weatherTool()))
	if err != nil {
		t.Fatalf("translating: %v", err)
	}
	if got := marshalBody(t, body); strings.Contains(got, `"strict"`) {
		t.Errorf("body = %s\nwant no strict member on a guided tool", got)
	}
}

func TestStrictToolsOverTheLimitAreRefusedBeforeSending(t *testing.T) {
	var tools []goodall.Tool
	for i := range 21 {
		s := weatherTool().(stubTool)
		s.name = fmt.Sprintf("tool_%d", i)
		tools = append(tools, strictStub{s})
	}
	_, _, err := translateRequest(toolRequest(tools...))
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Fatalf("err = %v, want it to wrap KindInvalidRequest", err)
	}
	if !strings.Contains(err.Error(), "at most 20 strict tools") || !strings.HasPrefix(err.Error(), ProviderName+": ") {
		t.Errorf("err = %q, want it to name the provider and the 20-tool limit", err)
	}
}

func TestAStrictToolIsRefusedWhereTheCatalogSaysNo(t *testing.T) {
	req := toolRequest(strictWeatherTool())
	req.ModelInfo = &goodall.ModelInfo{ID: req.Model, Provider: ProviderName,
		Capabilities: goodall.Capabilities{StrictTools: goodall.Unsupported}}
	_, _, err := translateRequest(req)
	if capErr, ok := errors.AsType[*goodall.CapabilityError](err); !ok || capErr.Capability != goodall.CapStrictTools {
		t.Errorf("err = %v, want a CapabilityError for %s", err, goodall.CapStrictTools)
	}
}

func TestTheRecordedStrictCallWasSentStrictAndMatchesItsSchema(t *testing.T) {
	sent := string(fixture(t, "live_strict_tool_use.request.json"))
	if !strings.Contains(sent, `"strict": true`) || strings.Contains(sent, `"tool_choice"`) {
		t.Errorf("the recorded request was not a strict tool under auto:\n%s", sent)
	}
	resp, err := collectFixture(t, "live_strict_tool_use.sse")
	if err != nil {
		t.Fatalf("collecting: %v", err)
	}
	uses := resp.Message.ToolUses()
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
}

func TestTheCatalogReadsStrictToolsFromStructuredOutputs(t *testing.T) {
	for _, c := range []struct {
		leaf *wireSupported
		want goodall.Support
	}{
		{&wireSupported{Supported: true}, goodall.Supported},
		{&wireSupported{Supported: false}, goodall.Unsupported},
		{nil, goodall.SupportUnknown},
	} {
		info := neutralModel(&wireModel{ID: "claude-x", Capabilities: &wireCapabilities{StructuredOutputs: c.leaf}})
		if got := info.Capabilities.StrictTools; got != c.want {
			t.Errorf("structured_outputs %+v: StrictTools = %v, want %v", c.leaf, got, c.want)
		}
	}
}
