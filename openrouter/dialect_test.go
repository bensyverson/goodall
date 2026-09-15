package openrouter

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/bensyverson/goodall"
)

func TestDialectString(t *testing.T) {
	for _, tc := range []struct {
		dialect Dialect
		want    string
	}{
		{OpenRouter, "openrouter"},
		{OpenAI, "openai"},
		{LMStudio, "lmstudio"},
		{Generic, "generic"},
		{Dialect("vllm"), "vllm"},
	} {
		if got := tc.dialect.String(); got != tc.want {
			t.Errorf("Dialect(%q).String() = %q, want %q", string(tc.dialect), got, tc.want)
		}
	}
}

func TestDialectQuirks(t *testing.T) {
	for _, tc := range []struct {
		dialect Dialect
		want    Quirks
	}{
		{OpenRouter, Quirks{
			Reasoning:        ReasoningObject,
			ReasoningDetails: true,
			CacheControl:     true,
			Routing:          true,
			Plugins:          true,
			SessionID:        true,
			Debug:            true,
			Metadata:         true,
			UsageCost:        true,
		}},
		{OpenAI, Quirks{Reasoning: ReasoningEffortField, Metadata: true}},
		{LMStudio, Quirks{Reasoning: ReasoningEffortField}},
		{Generic, Quirks{}},
		{Dialect("something-new"), Quirks{}},
	} {
		if got := tc.dialect.Quirks(); got != tc.want {
			t.Errorf("%s.Quirks() = %+v, want %+v", tc.dialect, got, tc.want)
		}
	}
}

// everyFeatureRequest is a request that sets every neutral feature the
// translation can express, so a dialect test can assert on the whole body.
func everyFeatureRequest(t *testing.T) *goodall.Request {
	t.Helper()
	tool, err := goodall.NewTool("lookup", "Look something up", func(_ context.Context, in struct {
		Query string `json:"query" desc:"what to look up"`
	}) (goodall.ToolResult, error) {
		_ = in
		return goodall.ToolResult{}, nil
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	return &goodall.Request{
		Model:  "some/model",
		System: []goodall.Text{{Text: "Be helpful."}, {Text: "Be brief.", Cache: &goodall.CacheControl{}}},
		Messages: []goodall.Message{
			goodall.UserMessage(
				goodall.Text{Text: "look at this", Cache: &goodall.CacheControl{TTL: goodall.CacheTTL1h}},
				goodall.Image{Source: goodall.BytesSource("image/png", []byte{1, 2, 3})},
				goodall.Document{Source: goodall.URLSource("https://example.com/paper.pdf"), Title: "paper.pdf"},
			),
			goodall.AssistantMessage(
				goodall.Thinking{Text: "pondering", Signature: "sig"},
				goodall.RedactedThinking{Data: "opaque"},
				goodall.Text{Text: "one moment"},
				goodall.ToolUse{ID: "call_1", Name: "lookup", Input: []byte(`{"query":"x"}`)},
			),
			goodall.UserMessage(
				goodall.ToolResult{ToolUseID: "call_1", Content: goodall.Blocks{goodall.Text{Text: "found it"}}},
				goodall.Text{Text: "thanks"},
			),
		},
		Tools:         []goodall.Tool{tool},
		ToolChoice:    goodall.ChooseAny().Serial(),
		MaxTokens:     256,
		Thinking:      goodall.ThinkingConfig{Effort: goodall.EffortHigh, Display: goodall.DisplayOmitted},
		Cache:         goodall.CacheManual,
		StopSequences: []string{"STOP"},
		Metadata:      map[string]string{"user_id": "u-1", "tenant": "t-1"},
		Extensions: Extensions{
			Routing:   &ProviderRouting{Order: []string{"anthropic"}, Sort: SortThroughput},
			Models:    []string{"other/model"},
			Plugins:   []Plugin{{ID: PluginFileParser}},
			SessionID: "sess-1",
			Debug:     Debug{EchoUpstreamBody: true},
		},
	}
}

// openRouterOnlyMembers are the request members no OpenAI-compatible server
// outside OpenRouter is asked to understand. Each is written with its colon,
// so a member name is not confused with the same word as a value — "user" is
// also a role.
var openRouterOnlyMembers = []string{
	`"reasoning":`,
	`"reasoning_effort":`,
	`"reasoning_details":`,
	`"cache_control":`,
	`"provider":`,
	`"models":`,
	`"plugins":`,
	`"session_id":`,
	`"debug":`,
	`"metadata":`,
	`"user":`,
}

func TestGenericDialectSendsNoOpenRouterOnlyMembers(t *testing.T) {
	req := everyFeatureRequest(t)
	wire, err := translateRequest(req, Generic, true)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, member := range openRouterOnlyMembers {
		if bytes.Contains(body, []byte(member)) {
			t.Errorf("Generic request contains %s\nbody: %s", member, body)
		}
	}
}

func TestOpenRouterDialectSendsItsOwnMembers(t *testing.T) {
	req := everyFeatureRequest(t)
	wire, err := translateRequest(req, OpenRouter, true)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, member := range []string{
		`"reasoning":`, `"reasoning_details":`, `"cache_control":`, `"provider":`,
		`"models":`, `"plugins":`, `"session_id":`, `"debug":`, `"metadata":`, `"user":`,
	} {
		if !bytes.Contains(body, []byte(member)) {
			t.Errorf("OpenRouter request is missing %s\nbody: %s", member, body)
		}
	}
	if bytes.Contains(body, []byte(`"reasoning_effort":`)) {
		t.Errorf("OpenRouter request should carry the reasoning object, not reasoning_effort\nbody: %s", body)
	}
}
