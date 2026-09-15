package openrouter

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// marshalWire renders a translated request, failing the test if it cannot.
func marshalWire(t *testing.T, req *goodall.Request, d Dialect, stream bool) []byte {
	t.Helper()
	wire, err := translateRequest(req, d, stream)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return body
}

// messagesJSON renders just the translated messages array, which is what most
// of the block assertions are about.
func messagesJSON(t *testing.T, req *goodall.Request, d Dialect) string {
	t.Helper()
	wire, err := translateRequest(req, d, false)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}
	body, err := json.Marshal(wire.Messages)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(body)
}

func TestTranslateSystemBecomesOneLeadingMessage(t *testing.T) {
	req := &goodall.Request{
		Model:    "m",
		System:   []goodall.Text{{Text: "one"}, {Text: "two"}},
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
		Cache:    goodall.CacheOff,
	}
	want := `[{"role":"system","content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]},` +
		`{"role":"user","content":[{"type":"text","text":"hi"}]}]`
	if got := messagesJSON(t, req, OpenRouter); got != want {
		t.Errorf("messages =\n%s\nwant\n%s", got, want)
	}
}

func TestTranslateMidConversationSystemMessage(t *testing.T) {
	req := &goodall.Request{
		Model: "m",
		Messages: []goodall.Message{
			goodall.UserMessage(goodall.Text{Text: "hi"}),
			goodall.SystemMessage(goodall.Text{Text: "be terse"}),
		},
		Cache: goodall.CacheOff,
	}
	got := messagesJSON(t, req, OpenRouter)
	if !strings.Contains(got, `{"role":"system","content":[{"type":"text","text":"be terse"}]}`) {
		t.Errorf("mid-conversation system message not translated: %s", got)
	}
}

func TestTranslateUserBlocks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block goodall.Block
		want  string
	}{
		{
			name:  "text",
			block: goodall.Text{Text: "hello"},
			want:  `{"type":"text","text":"hello"}`,
		},
		{
			name:  "image bytes",
			block: goodall.Image{Source: goodall.BytesSource("image/png", []byte("PNG"))},
			want:  `{"type":"image_url","image_url":{"url":"data:image/png;base64,UE5H"}}`,
		},
		{
			name:  "image url",
			block: goodall.Image{Source: goodall.URLSource("https://example.com/a.png")},
			want:  `{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}`,
		},
		{
			name:  "document bytes",
			block: goodall.Document{Source: goodall.BytesSource("application/pdf", []byte("PDF")), Title: "report.pdf"},
			want:  `{"type":"file","file":{"filename":"report.pdf","file_data":"data:application/pdf;base64,UERG"}}`,
		},
		{
			name:  "document url",
			block: goodall.Document{Source: goodall.URLSource("https://example.com/b.pdf")},
			want:  `{"type":"file","file":{"filename":"document","file_data":"https://example.com/b.pdf"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &goodall.Request{
				Model:    "m",
				Messages: []goodall.Message{goodall.UserMessage(tc.block)},
				Cache:    goodall.CacheOff,
			}
			if got := messagesJSON(t, req, OpenRouter); !strings.Contains(got, tc.want) {
				t.Errorf("messages = %s\nwant a part %s", got, tc.want)
			}
		})
	}
}

func TestTranslateFileSourceIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block goodall.Block
		want  string
	}{
		{"image", goodall.Image{Source: goodall.FileSource("file_123")}, "image"},
		{"document", goodall.Document{Source: goodall.FileSource("file_123")}, "document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &goodall.Request{
				Model:    "m",
				Messages: []goodall.Message{goodall.UserMessage(tc.block)},
			}
			_, err := translateRequest(req, OpenRouter, false)
			if err == nil {
				t.Fatalf("translateRequest accepted a provider file id")
			}
			if !errors.Is(err, goodall.KindInvalidRequest) {
				t.Errorf("error %v does not wrap KindInvalidRequest", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v does not name the block kind %q", err, tc.want)
			}
		})
	}
}

func TestTranslateToolResultsBecomeToolMessagesFirst(t *testing.T) {
	req := &goodall.Request{
		Model: "m",
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.Text{Text: "and also"},
			goodall.ToolResult{ToolUseID: "a", Content: goodall.Blocks{goodall.Text{Text: "alpha"}}},
			goodall.ToolResult{ToolUseID: "b", IsError: true, Content: goodall.Blocks{goodall.Text{Text: "boom"}}},
			goodall.ToolResult{ToolUseID: "c", Content: goodall.Blocks{
				goodall.Text{Text: "see "},
				goodall.Image{Source: goodall.URLSource("https://example.com/c.png")},
			}},
		)},
		Cache: goodall.CacheOff,
	}
	want := `[{"role":"tool","content":"alpha","tool_call_id":"a"},` +
		`{"role":"tool","content":"Error: boom","tool_call_id":"b"},` +
		`{"role":"tool","content":"see [image]","tool_call_id":"c"},` +
		`{"role":"user","content":[{"type":"text","text":"and also"}]}]`
	if got := messagesJSON(t, req, OpenRouter); got != want {
		t.Errorf("messages =\n%s\nwant\n%s", got, want)
	}
}

func TestTranslateToolResultOnlyMessageEmitsNoUserMessage(t *testing.T) {
	req := &goodall.Request{
		Model: "m",
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.ToolResult{ToolUseID: "a", Content: goodall.Blocks{goodall.Text{Text: "alpha"}}},
		)},
		Cache: goodall.CacheOff,
	}
	want := `[{"role":"tool","content":"alpha","tool_call_id":"a"}]`
	if got := messagesJSON(t, req, OpenRouter); got != want {
		t.Errorf("messages =\n%s\nwant\n%s", got, want)
	}
}

func TestTranslateAssistantMessage(t *testing.T) {
	req := &goodall.Request{
		Model: "m",
		Messages: []goodall.Message{goodall.AssistantMessage(
			goodall.Text{Text: "on it"},
			goodall.ToolUse{ID: "call_1", Name: "lookup", Input: []byte(`{"query":"x"}`)},
		)},
		Cache: goodall.CacheOff,
	}
	want := `[{"role":"assistant","content":"on it","tool_calls":` +
		`[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"x\"}"}}]}]`
	if got := messagesJSON(t, req, OpenRouter); got != want {
		t.Errorf("messages =\n%s\nwant\n%s", got, want)
	}
}

func TestTranslateAssistantWithNoTextSendsNullContent(t *testing.T) {
	req := &goodall.Request{
		Model: "m",
		Messages: []goodall.Message{goodall.AssistantMessage(
			goodall.ToolUse{ID: "call_1", Name: "lookup", Input: []byte(`{}`)},
		)},
		Cache: goodall.CacheOff,
	}
	if got := messagesJSON(t, req, OpenRouter); !strings.Contains(got, `"content":null`) {
		t.Errorf("messages = %s, want a null content", got)
	}
}

func TestTranslateAssistantDropsUnknownBlocks(t *testing.T) {
	req := &goodall.Request{
		Model: "m",
		Messages: []goodall.Message{goodall.AssistantMessage(
			goodall.Unknown{Type: "server_tool_use", Raw: []byte(`{"type":"server_tool_use"}`)},
			goodall.Text{Text: "done"},
		)},
		Cache: goodall.CacheOff,
	}
	want := `[{"role":"assistant","content":"done"}]`
	if got := messagesJSON(t, req, OpenRouter); got != want {
		t.Errorf("messages =\n%s\nwant\n%s", got, want)
	}
}

func TestTranslateReasoningDetailsAreReEmittedVerbatimInOrder(t *testing.T) {
	first := []byte(`{"type":"reasoning.text","id":"rd_1","format":"anthropic-claude-v1","index":0,"text":"step one","signature":"sig1"}`)
	second := []byte(`{"type":"reasoning.encrypted","id":"rd_2","index":1,"data":"ZW5j"}`)
	third := []byte(`{"type":"reasoning.server_tool_call","tool_name":"openrouter:fusion","arguments":"{}","result":"{}"}`)
	req := &goodall.Request{
		Model: "m",
		Messages: []goodall.Message{goodall.AssistantMessage(
			goodall.Thinking{Text: "step one", Signature: "sig1", Raw: first},
			goodall.RedactedThinking{Data: "ZW5j", Raw: second},
			goodall.Thinking{Raw: third},
			goodall.Text{Text: "done"},
		)},
		Cache: goodall.CacheOff,
	}
	want := `"reasoning_details":[` + string(first) + `,` + string(second) + `,` + string(third) + `]`
	if got := messagesJSON(t, req, OpenRouter); !strings.Contains(got, want) {
		t.Errorf("messages =\n%s\nwant to contain\n%s", got, want)
	}
}

func TestTranslateThinkingWithoutRawIsRebuilt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block goodall.Block
		want  string
	}{
		{
			name:  "text with signature",
			block: goodall.Thinking{Text: "hmm", Signature: "sig"},
			want:  `{"type":"reasoning.text","text":"hmm","signature":"sig"}`,
		},
		{
			name:  "text without signature",
			block: goodall.Thinking{Text: "hmm"},
			want:  `{"type":"reasoning.text","text":"hmm"}`,
		},
		{
			name:  "encrypted",
			block: goodall.RedactedThinking{Data: "ZW5j"},
			want:  `{"type":"reasoning.encrypted","data":"ZW5j"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &goodall.Request{
				Model:    "m",
				Messages: []goodall.Message{goodall.AssistantMessage(tc.block)},
				Cache:    goodall.CacheOff,
			}
			if got := messagesJSON(t, req, OpenRouter); !strings.Contains(got, tc.want) {
				t.Errorf("messages = %s\nwant to contain %s", got, tc.want)
			}
		})
	}
}

func TestTranslateDropsReasoningOnDialectsThatDoNotCarryIt(t *testing.T) {
	req := &goodall.Request{
		Model: "m",
		Messages: []goodall.Message{goodall.AssistantMessage(
			goodall.Thinking{Text: "hmm", Raw: []byte(`{"type":"reasoning.text","text":"hmm"}`)},
			goodall.Text{Text: "done"},
		)},
		Cache: goodall.CacheOff,
	}
	if got := messagesJSON(t, req, OpenAI); strings.Contains(got, "reasoning") {
		t.Errorf("OpenAI dialect carried reasoning_details: %s", got)
	}
}

func TestTranslateToolChoice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		choice goodall.ToolChoice
		want   string
		absent string
	}{
		{name: "auto omits", choice: goodall.ChooseAuto(), absent: `"tool_choice"`},
		{name: "none", choice: goodall.ChooseNone(), want: `"tool_choice":"none"`},
		{name: "any", choice: goodall.ChooseAny(), want: `"tool_choice":"required"`},
		{
			name:   "named",
			choice: goodall.ChooseTool("lookup"),
			want:   `"tool_choice":{"type":"function","function":{"name":"lookup"}}`,
		},
		{
			name:   "serial",
			choice: goodall.ChooseAuto().Serial(),
			want:   `"parallel_tool_calls":false`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &goodall.Request{
				Model:      "m",
				Messages:   []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
				ToolChoice: tc.choice,
				Cache:      goodall.CacheOff,
			}
			body := marshalWire(t, req, OpenRouter, false)
			if tc.want != "" && !bytes.Contains(body, []byte(tc.want)) {
				t.Errorf("body = %s\nwant to contain %s", body, tc.want)
			}
			if tc.absent != "" && bytes.Contains(body, []byte(tc.absent)) {
				t.Errorf("body = %s\nwant no %s", body, tc.absent)
			}
		})
	}
}

func TestTranslateInvalidToolChoiceIsRejected(t *testing.T) {
	req := &goodall.Request{
		Model:      "m",
		Messages:   []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
		ToolChoice: goodall.ToolChoice{Mode: goodall.ToolChoiceNamed},
	}
	_, err := translateRequest(req, OpenRouter, false)
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Fatalf("translateRequest error = %v, want KindInvalidRequest", err)
	}
}

func TestTranslateReasoningConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dialect Dialect
		config  goodall.ThinkingConfig
		want    string
		absent  string
	}{
		{name: "default sends nothing", dialect: OpenRouter, config: goodall.ThinkingConfig{}, absent: `"reasoning"`},
		{
			name:    "effort",
			dialect: OpenRouter,
			config:  goodall.ThinkingConfig{Effort: goodall.EffortHigh},
			want:    `"reasoning":{"effort":"high"}`,
		},
		{
			name:    "off is none",
			dialect: OpenRouter,
			config:  goodall.ThinkingConfig{Effort: goodall.EffortOff},
			want:    `"reasoning":{"effort":"none"}`,
		},
		{
			name:    "omitted display excludes",
			dialect: OpenRouter,
			config:  goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplayOmitted},
			want:    `"reasoning":{"effort":"low","exclude":true}`,
		},
		{
			name:    "summarized display sends only the effort",
			dialect: OpenRouter,
			config:  goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized},
			want:    `"reasoning":{"effort":"low"}`,
		},
		{
			name:    "unknown rung passes through",
			dialect: OpenRouter,
			config:  goodall.ThinkingConfig{Effort: goodall.Effort("colossal")},
			want:    `"reasoning":{"effort":"colossal"}`,
		},
		{
			name:    "openai uses the top-level field",
			dialect: OpenAI,
			config:  goodall.ThinkingConfig{Effort: goodall.EffortMedium},
			want:    `"reasoning_effort":"medium"`,
		},
		{
			name:    "openai off is none",
			dialect: OpenAI,
			config:  goodall.ThinkingConfig{Effort: goodall.EffortOff},
			want:    `"reasoning_effort":"none"`,
		},
		{
			name:    "openai ignores display",
			dialect: OpenAI,
			config:  goodall.ThinkingConfig{Display: goodall.DisplayOmitted},
			absent:  `"reasoning`,
		},
		{
			name:    "generic sends nothing",
			dialect: Generic,
			config:  goodall.ThinkingConfig{Effort: goodall.EffortHigh, Display: goodall.DisplayOmitted},
			absent:  `"reasoning`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &goodall.Request{
				Model:    "m",
				Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
				Thinking: tc.config,
				Cache:    goodall.CacheOff,
			}
			body := marshalWire(t, req, tc.dialect, false)
			if tc.want != "" && !bytes.Contains(body, []byte(tc.want)) {
				t.Errorf("body = %s\nwant to contain %s", body, tc.want)
			}
			if tc.absent != "" && bytes.Contains(body, []byte(tc.absent)) {
				t.Errorf("body = %s\nwant no %s", body, tc.absent)
			}
		})
	}
}

func TestTranslateCachePolicy(t *testing.T) {
	system := []goodall.Text{{Text: "static"}, {Text: "also static", Cache: &goodall.CacheControl{TTL: goodall.CacheTTL1h}}}
	user := goodall.UserMessage(goodall.Text{Text: "hi", Cache: &goodall.CacheControl{}})

	for _, tc := range []struct {
		name    string
		dialect Dialect
		policy  goodall.CachePolicy
		want    []string
		absent  []string
	}{
		{
			name:    "auto marks the last system part and sends the top-level directive",
			dialect: OpenRouter,
			policy:  goodall.CacheAuto,
			want: []string{
				`{"type":"text","text":"also static","cache_control":{"type":"ephemeral"}}`,
				`"stream":false,"cache_control":{"type":"ephemeral"}`,
			},
			absent: []string{`"ttl"`, `{"type":"text","text":"hi","cache_control"`},
		},
		{
			name:    "manual sends exactly the caller's markers",
			dialect: OpenRouter,
			policy:  goodall.CacheManual,
			want: []string{
				`{"type":"text","text":"also static","cache_control":{"type":"ephemeral","ttl":"1h"}}`,
				`{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}`,
			},
			absent: []string{`"stream":false,"cache_control"`},
		},
		{
			name:    "off sends none",
			dialect: OpenRouter,
			policy:  goodall.CacheOff,
			absent:  []string{`"cache_control"`},
		},
		{
			name:    "a dialect without cache control sends none whatever the policy",
			dialect: Generic,
			policy:  goodall.CacheAuto,
			absent:  []string{`"cache_control"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &goodall.Request{
				Model:    "m",
				System:   system,
				Messages: []goodall.Message{user},
				Cache:    tc.policy,
			}
			body := marshalWire(t, req, tc.dialect, false)
			for _, want := range tc.want {
				if !bytes.Contains(body, []byte(want)) {
					t.Errorf("body = %s\nwant to contain %s", body, want)
				}
			}
			for _, absent := range tc.absent {
				if bytes.Contains(body, []byte(absent)) {
					t.Errorf("body = %s\nwant no %s", body, absent)
				}
			}
		})
	}
}

func TestTranslateManualCacheRejectsMoreThanFourMarkers(t *testing.T) {
	marker := func() *goodall.CacheControl { return &goodall.CacheControl{} }
	req := &goodall.Request{
		Model: "m",
		System: []goodall.Text{
			{Text: "a", Cache: marker()},
			{Text: "b", Cache: marker()},
			{Text: "c", Cache: marker()},
		},
		Messages: []goodall.Message{goodall.UserMessage(
			goodall.Text{Text: "d", Cache: marker()},
			goodall.Text{Text: "e", Cache: marker()},
		)},
		Cache: goodall.CacheManual,
	}
	_, err := translateRequest(req, OpenRouter, false)
	if err == nil {
		t.Fatalf("translateRequest accepted five cache breakpoints")
	}
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("error %v does not wrap KindInvalidRequest", err)
	}
	if !strings.Contains(err.Error(), "4") {
		t.Errorf("error %v does not name the limit", err)
	}
}

// foreignExtension stands in for another provider's options struct.
type foreignExtension struct{}

func (foreignExtension) Provider() string { return "anthropic" }

func TestTranslateRejectsAForeignExtension(t *testing.T) {
	req := &goodall.Request{
		Model:      "m",
		Messages:   []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
		Extensions: foreignExtension{},
	}
	_, err := translateRequest(req, OpenRouter, false)
	if err == nil {
		t.Fatalf("translateRequest accepted another provider's extension")
	}
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("error %v does not wrap KindInvalidRequest", err)
	}
	if !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("error %v does not name the foreign provider", err)
	}
}

func TestTranslateMetadataAndUser(t *testing.T) {
	req := &goodall.Request{
		Model:    "m",
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
		Metadata: map[string]string{"user_id": "u-1", "tenant": "t-1", "app": "demo"},
		Cache:    goodall.CacheOff,
	}
	body := marshalWire(t, req, OpenRouter, false)
	if !bytes.Contains(body, []byte(`"user":"u-1"`)) {
		t.Errorf("body = %s\nwant the end-user id lifted to \"user\"", body)
	}
	if !bytes.Contains(body, []byte(`"metadata":{"app":"demo","tenant":"t-1"}`)) {
		t.Errorf("body = %s\nwant the rest of the metadata sorted by key", body)
	}
}

func TestTranslateExtensions(t *testing.T) {
	req := &goodall.Request{
		Model:    "m",
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
		Cache:    goodall.CacheOff,
		Extensions: Extensions{
			Routing: &ProviderRouting{
				Order:          []string{"anthropic", "google"},
				AllowFallbacks: falseFlag(),
				Sort:           SortPrice,
				DataCollection: DataCollectionDeny,
				MaxPrice:       &MaxPrice{Prompt: mustDecimal(t, "1.5")},
			},
			Models:    []string{"other/model"},
			Plugins:   []Plugin{{ID: PluginFileParser, PDF: &PDFOptions{Engine: PDFEngineNative}}},
			SessionID: "sess-1",
			Debug:     Debug{EchoUpstreamBody: true},
		},
	}
	body := marshalWire(t, req, OpenRouter, false)
	for _, want := range []string{
		`"provider":{"order":["anthropic","google"],"allow_fallbacks":false,"sort":"price","data_collection":"deny","max_price":{"prompt":"1.5"}}`,
		`"models":["other/model"]`,
		`"plugins":[{"id":"file-parser","pdf":{"engine":"native"}}]`,
		`"session_id":"sess-1"`,
		`"debug":{"echo_upstream_body":true}`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("body = %s\nwant to contain %s", body, want)
		}
	}
}

// falseFlag is a pointer to false, for the tri-state routing flags.
func falseFlag() *bool { b := false; return &b }

func mustDecimal(t *testing.T, s string) goodall.Decimal {
	t.Helper()
	d, err := goodall.ParseDecimal(s)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", s, err)
	}
	return d
}

func TestTranslateMaxTokensAndStop(t *testing.T) {
	req := &goodall.Request{
		Model:         "m",
		Messages:      []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
		StopSequences: []string{"END"},
		Cache:         goodall.CacheOff,
	}
	body := marshalWire(t, req, OpenRouter, false)
	if bytes.Contains(body, []byte(`"max_tokens"`)) {
		t.Errorf("body = %s\nwant no max_tokens when it is zero", body)
	}
	if !bytes.Contains(body, []byte(`"stop":["END"]`)) {
		t.Errorf("body = %s\nwant the stop sequences", body)
	}
	req.MaxTokens = 64
	body = marshalWire(t, req, OpenRouter, false)
	if !bytes.Contains(body, []byte(`"max_tokens":64`)) {
		t.Errorf("body = %s\nwant max_tokens", body)
	}
}

func TestTranslateStreamIsTheCallersChoice(t *testing.T) {
	req := &goodall.Request{
		Model:    "m",
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
		Cache:    goodall.CacheOff,
	}
	if body := marshalWire(t, req, OpenRouter, true); !bytes.Contains(body, []byte(`"stream":true`)) {
		t.Errorf("body = %s\nwant stream true", body)
	}
	if body := marshalWire(t, req, OpenRouter, false); !bytes.Contains(body, []byte(`"stream":false`)) {
		t.Errorf("body = %s\nwant stream false", body)
	}
}

func TestTranslateToolsCarryTheOrderedSchema(t *testing.T) {
	tool := everyFeatureRequest(t).Tools[0]
	req := &goodall.Request{
		Model:    "m",
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
		Tools:    []goodall.Tool{tool},
		Cache:    goodall.CacheOff,
	}
	body := marshalWire(t, req, OpenRouter, false)
	want := `"tools":[{"type":"function","function":{"name":"lookup","description":"Look something up",` +
		`"parameters":{"type":"object","properties":{"query":{"type":"string","description":"what to look up"}},` +
		`"required":["query"],"additionalProperties":false}}}]`
	if !bytes.Contains(body, []byte(want)) {
		t.Errorf("body = %s\nwant to contain %s", body, want)
	}
}

func TestTranslateRequestPrefixIsDeterministic(t *testing.T) {
	base := everyFeatureRequest(t)
	shorter := *base
	longer := *base
	longer.Messages = append(append([]goodall.Message(nil), base.Messages...),
		goodall.AssistantMessage(goodall.Text{Text: "and one more"}))

	shortWire, err := translateRequest(&shorter, OpenRouter, true)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}
	longWire, err := translateRequest(&longer, OpenRouter, true)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}
	shortBody, err := json.Marshal(shortWire)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	longBody, err := json.Marshal(longWire)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	messages, err := json.Marshal(shortWire.Messages)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Everything up to the end of the last shared message must match, which
	// is the whole messages array of the shorter request minus its "]".
	prefix := append([]byte(`{"model":"some/model","messages":`), messages[:len(messages)-1]...)
	if !bytes.HasPrefix(shortBody, prefix) {
		t.Fatalf("shorter body = %s\nwant prefix %s", shortBody, prefix)
	}
	if !bytes.HasPrefix(longBody, prefix) {
		t.Errorf("longer body = %s\nwant prefix %s", longBody, prefix)
	}
}
