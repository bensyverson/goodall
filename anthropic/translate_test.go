package anthropic

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// stubTool is a tool definition with no behaviour: translation only reads the
// name, the description and the schema.
type stubTool struct {
	name        string
	description string
	schema      *goodall.Schema
}

func (t stubTool) Name() string            { return t.name }
func (t stubTool) Description() string     { return t.description }
func (t stubTool) Schema() *goodall.Schema { return t.schema }
func (t stubTool) Execute(context.Context, jsontext.Value) (goodall.ToolResult, error) {
	return goodall.ToolResult{}, nil
}

// weatherTool is the tool used across the request tests.
func weatherTool() goodall.Tool {
	return stubTool{
		name:        "get_weather",
		description: "Look up the weather.",
		schema: &goodall.Schema{
			Type: goodall.SchemaObject,
			Properties: goodall.Properties{
				{Name: "city", Schema: goodall.Schema{Type: goodall.SchemaString, Description: "the city"}},
			},
			Required:             []string{"city"},
			AdditionalProperties: goodall.AdditionalPropertiesForbidden,
		},
	}
}

// bodyJSON translates a request and returns the marshalled body.
func bodyJSON(t *testing.T, req *goodall.Request) string {
	t.Helper()
	body, _, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translating: %v", err)
	}
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	return string(out)
}

func TestTranslateRequest(t *testing.T) {
	req := &goodall.Request{
		Model:         "claude-opus-4-6",
		System:        []goodall.Text{{Text: "You are helpful."}},
		Messages:      []goodall.Message{goodall.UserMessage(goodall.Text{Text: "Hi"})},
		Tools:         []goodall.Tool{weatherTool()},
		MaxTokens:     1024,
		Cache:         goodall.CacheOff,
		StopSequences: []string{"END"},
		Metadata:      map[string]string{"user_id": "u-1"},
	}
	const want = `{"model":"claude-opus-4-6","max_tokens":1024,` +
		`"system":[{"type":"text","text":"You are helpful."}],` +
		`"tools":[{"name":"get_weather","description":"Look up the weather.","input_schema":{"type":"object","properties":{"city":{"type":"string","description":"the city"}},"required":["city"],"additionalProperties":false}}],` +
		`"stop_sequences":["END"],"metadata":{"user_id":"u-1"},` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"Hi"}]}]}`
	if got := bodyJSON(t, req); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// TestTranslateRequestSubstitutesMaxTokens holds the documented default:
// Anthropic requires max_tokens, and a zero on the neutral request is not a
// number goodall may send.
func TestTranslateRequestSubstitutesMaxTokens(t *testing.T) {
	req := &goodall.Request{Model: "m", Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "Hi"})}}
	body, _, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translating: %v", err)
	}
	if body.MaxTokens != DefaultMaxTokens {
		t.Errorf("max_tokens = %d, want %d", body.MaxTokens, DefaultMaxTokens)
	}
}

// TestTranslateRequestPassesASystemRole holds the mid-conversation system
// message through as its own role rather than folding it into the prompt.
func TestTranslateRequestPassesASystemRole(t *testing.T) {
	req := &goodall.Request{
		Model:     "m",
		MaxTokens: 16,
		Messages: []goodall.Message{
			goodall.UserMessage(goodall.Text{Text: "Hi"}),
			goodall.SystemMessage(goodall.Text{Text: "Be brief."}),
		},
	}
	got := bodyJSON(t, req)
	const want = `"messages":[{"role":"user","content":[{"type":"text","text":"Hi"}]},{"role":"system","content":[{"type":"text","text":"Be brief."}]}]`
	if !strings.Contains(got, want) {
		t.Errorf("got %s\nwant it to contain %s", got, want)
	}
}

func TestTranslateToolChoice(t *testing.T) {
	cases := []struct {
		name   string
		choice goodall.ToolChoice
		want   string
	}{
		{"auto sends nothing", goodall.ChooseAuto(), ``},
		{"any", goodall.ChooseAny(), `"tool_choice":{"type":"any"},`},
		{"named", goodall.ChooseTool("get_weather"), `"tool_choice":{"type":"tool","name":"get_weather"},`},
		{"none", goodall.ChooseNone(), `"tool_choice":{"type":"none"},`},
		{"serial auto", goodall.ChooseAuto().Serial(), `"tool_choice":{"type":"auto","disable_parallel_tool_use":true},`},
		{"serial any", goodall.ChooseAny().Serial(), `"tool_choice":{"type":"any","disable_parallel_tool_use":true},`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &goodall.Request{
				Model:      "m",
				MaxTokens:  16,
				Messages:   []goodall.Message{goodall.UserMessage(goodall.Text{Text: "Hi"})},
				Tools:      []goodall.Tool{weatherTool()},
				ToolChoice: c.choice,
			}
			got := bodyJSON(t, req)
			if c.want == "" {
				if strings.Contains(got, "tool_choice") {
					t.Errorf("got %s, want no tool_choice", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("got %s\nwant it to contain %s", got, c.want)
			}
		})
	}
}

// TestTranslateRejectsAnInvalidToolChoice defers to ToolChoice.Validate, so a
// named choice with no name is refused before the network call.
func TestTranslateRejectsAnInvalidToolChoice(t *testing.T) {
	req := &goodall.Request{
		Model:      "m",
		MaxTokens:  16,
		ToolChoice: goodall.ToolChoice{Mode: goodall.ToolChoiceNamed},
	}
	_, _, err := translateRequest(req)
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Fatalf("err = %v, want it to wrap KindInvalidRequest", err)
	}
}

func TestTranslateRejectsABadToolDefinition(t *testing.T) {
	cases := map[string]goodall.Tool{
		"no name":   stubTool{description: "d", schema: &goodall.Schema{Type: goodall.SchemaObject}},
		"no schema": stubTool{name: "t", description: "d"},
	}
	for name, tool := range cases {
		t.Run(name, func(t *testing.T) {
			req := &goodall.Request{Model: "m", MaxTokens: 16, Tools: []goodall.Tool{tool}}
			_, _, err := translateRequest(req)
			if !errors.Is(err, goodall.KindInvalidRequest) {
				t.Fatalf("err = %v, want it to wrap KindInvalidRequest", err)
			}
		})
	}
}

func TestTranslateMetadata(t *testing.T) {
	t.Run("the user_id key becomes metadata.user_id", func(t *testing.T) {
		req := &goodall.Request{Model: "m", MaxTokens: 16, Metadata: map[string]string{"user_id": "u-1"}}
		if got := bodyJSON(t, req); !strings.Contains(got, `"metadata":{"user_id":"u-1"}`) {
			t.Errorf("got %s", got)
		}
	})
	t.Run("no metadata sends none", func(t *testing.T) {
		req := &goodall.Request{Model: "m", MaxTokens: 16}
		if got := bodyJSON(t, req); strings.Contains(got, "metadata") {
			t.Errorf("got %s, want no metadata", got)
		}
	})
	t.Run("a key Anthropic does not take is refused", func(t *testing.T) {
		req := &goodall.Request{Model: "m", MaxTokens: 16, Metadata: map[string]string{"session_id": "s-1"}}
		_, _, err := translateRequest(req)
		if !errors.Is(err, goodall.KindInvalidRequest) {
			t.Fatalf("err = %v, want it to wrap KindInvalidRequest", err)
		}
		if !strings.Contains(err.Error(), "session_id") {
			t.Errorf("err = %v, want it to name the key", err)
		}
	})
}

func TestTranslateCachePolicy(t *testing.T) {
	system := []goodall.Text{
		{Text: "static"},
		{Text: "also static", Cache: &goodall.CacheControl{TTL: goodall.CacheTTL1h}},
	}
	messages := []goodall.Message{goodall.UserMessage(goodall.Text{Text: "Hi", Cache: &goodall.CacheControl{}})}

	t.Run("auto places one marker on the last system block and one at the top", func(t *testing.T) {
		req := &goodall.Request{Model: "m", MaxTokens: 16, System: system, Messages: messages, Cache: goodall.CacheAuto}
		got := bodyJSON(t, req)
		const wantSystem = `"system":[{"type":"text","text":"static"},{"type":"text","text":"also static","cache_control":{"type":"ephemeral"}}]`
		if !strings.Contains(got, wantSystem) {
			t.Errorf("got %s\nwant it to contain %s", got, wantSystem)
		}
		if !strings.Contains(got, `"cache_control":{"type":"ephemeral"},"messages"`) {
			t.Errorf("got %s\nwant a top-level cache_control", got)
		}
		if strings.Contains(got, `"text":"Hi","cache_control"`) {
			t.Errorf("got %s\nwant the caller's message marker ignored", got)
		}
		if strings.Contains(got, `"ttl"`) {
			t.Errorf("got %s\nwant the caller's ttl ignored under the auto policy", got)
		}
	})

	t.Run("auto with no system blocks still sends the top-level marker", func(t *testing.T) {
		req := &goodall.Request{Model: "m", MaxTokens: 16, Messages: messages, Cache: goodall.CacheAuto}
		got := bodyJSON(t, req)
		if !strings.Contains(got, `"cache_control":{"type":"ephemeral"}`) {
			t.Errorf("got %s\nwant a top-level cache_control", got)
		}
	})

	t.Run("manual sends exactly the caller's markers", func(t *testing.T) {
		req := &goodall.Request{Model: "m", MaxTokens: 16, System: system, Messages: messages, Cache: goodall.CacheManual}
		got := bodyJSON(t, req)
		const wantSystem = `"system":[{"type":"text","text":"static"},{"type":"text","text":"also static","cache_control":{"type":"ephemeral","ttl":"1h"}}]`
		if !strings.Contains(got, wantSystem) {
			t.Errorf("got %s\nwant it to contain %s", got, wantSystem)
		}
		if !strings.Contains(got, `{"type":"text","text":"Hi","cache_control":{"type":"ephemeral"}}`) {
			t.Errorf("got %s\nwant the caller's message marker kept", got)
		}
		if strings.Contains(got, `"cache_control":{"type":"ephemeral"},"messages"`) {
			t.Errorf("got %s\nwant no top-level cache_control under the manual policy", got)
		}
	})

	t.Run("off sends nothing", func(t *testing.T) {
		req := &goodall.Request{Model: "m", MaxTokens: 16, System: system, Messages: messages, Cache: goodall.CacheOff}
		if got := bodyJSON(t, req); strings.Contains(got, "cache_control") {
			t.Errorf("got %s, want no cache_control anywhere", got)
		}
	})
}

// TestManualCacheRejectsFiveMarkers is the four-breakpoint limit, checked
// before the request leaves rather than as a 400 from Anthropic. Markers
// nested inside a tool result count too.
func TestManualCacheRejectsFiveMarkers(t *testing.T) {
	marker := func() *goodall.CacheControl { return &goodall.CacheControl{} }
	req := &goodall.Request{
		Model:     "m",
		MaxTokens: 16,
		Cache:     goodall.CacheManual,
		System:    []goodall.Text{{Text: "one", Cache: marker()}},
		Messages: []goodall.Message{
			goodall.UserMessage(goodall.Text{Text: "two", Cache: marker()}),
			goodall.AssistantMessage(goodall.Text{Text: "three", Cache: marker()}),
			goodall.UserMessage(goodall.ToolResult{
				ToolUseID: "tu_1",
				Cache:     marker(),
				Content:   goodall.Blocks{goodall.Text{Text: "five", Cache: marker()}},
			}),
		},
	}
	_, _, err := translateRequest(req)
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Fatalf("err = %v, want it to wrap KindInvalidRequest", err)
	}
	if !strings.Contains(err.Error(), "4") {
		t.Errorf("err = %v, want it to name the limit", err)
	}

	// Four is still accepted, so the limit is off-by-one proof.
	req.System = nil
	if _, _, err := translateRequest(req); err != nil {
		t.Fatalf("four markers: %v", err)
	}
}

// TestRequestPrefixIsStable is invariant 8 at this seam: appending a message
// must leave every earlier byte of the body untouched, because any change
// invalidates the prompt cache and every later thinking signature.
func TestRequestPrefixIsStable(t *testing.T) {
	base := &goodall.Request{
		Model:     "claude-opus-4-6",
		MaxTokens: 1024,
		System:    []goodall.Text{{Text: "You are helpful."}},
		Tools:     []goodall.Tool{weatherTool()},
		Thinking:  goodall.ThinkingConfig{Effort: goodall.EffortHigh, Display: goodall.DisplaySummarized},
		Messages: []goodall.Message{
			goodall.UserMessage(goodall.Text{Text: "Hi"}),
			goodall.AssistantMessage(goodall.Text{Text: "Hello."}),
		},
	}
	first := bodyJSON(t, base)

	next := *base
	next.Messages = append(append([]goodall.Message(nil), base.Messages...), goodall.UserMessage(goodall.Text{Text: "Again"}))
	second := bodyJSON(t, &next)

	// The body ends with the messages array, so everything before the last
	// message is the first body minus its closing "]}".
	prefix := strings.TrimSuffix(first, "]}") + ","
	if !strings.HasSuffix(first, "]}") {
		t.Fatalf("the body does not end with the messages array: %s", first)
	}
	if !strings.HasPrefix(second, prefix) {
		t.Fatalf("the prefix changed\nfirst  %s\nsecond %s", first, second)
	}
	if first == second {
		t.Fatal("the second body is identical; the message was not appended")
	}
}

func TestTranslateExtensions(t *testing.T) {
	t.Run("betas travel beside the body", func(t *testing.T) {
		req := &goodall.Request{
			Model:      "m",
			MaxTokens:  16,
			Extensions: Extensions{Betas: []Beta{BetaStructuredOutputs}, ServiceTier: ServiceTierStandardOnly},
		}
		body, betas, err := translateRequest(req)
		if err != nil {
			t.Fatalf("translating: %v", err)
		}
		if len(betas) != 1 || betas[0] != BetaStructuredOutputs {
			t.Errorf("betas = %v", betas)
		}
		if body.ServiceTier != ServiceTierStandardOnly {
			t.Errorf("service_tier = %q", body.ServiceTier)
		}
	})

	t.Run("a pointer extension works too", func(t *testing.T) {
		req := &goodall.Request{Model: "m", MaxTokens: 16, Extensions: &Extensions{Betas: []Beta{BetaThinkingBindingControls}}}
		_, betas, err := translateRequest(req)
		if err != nil {
			t.Fatalf("translating: %v", err)
		}
		if len(betas) != 1 || betas[0] != BetaThinkingBindingControls {
			t.Errorf("betas = %v", betas)
		}
	})

	t.Run("another provider's extension is refused by name", func(t *testing.T) {
		req := &goodall.Request{Model: "m", MaxTokens: 16, Extensions: foreignExtension{}}
		_, _, err := translateRequest(req)
		if !errors.Is(err, goodall.KindInvalidRequest) {
			t.Fatalf("err = %v, want it to wrap KindInvalidRequest", err)
		}
		if !strings.Contains(err.Error(), "openrouter") {
			t.Errorf("err = %v, want it to name the foreign provider", err)
		}
	})
}

// foreignExtension stands in for another provider's options struct.
type foreignExtension struct{}

func (foreignExtension) Provider() string { return "openrouter" }
