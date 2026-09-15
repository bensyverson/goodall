package anthropic

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"reflect"
	"testing"

	"github.com/bensyverson/goodall"
)

// wireJSON translates one neutral block under the given policy and returns the
// bytes it would occupy in a request body.
func wireJSON(t *testing.T, policy goodall.CachePolicy, b goodall.Block) string {
	t.Helper()
	tr := &translator{policy: policy}
	w, err := tr.block(b)
	if err != nil {
		t.Fatalf("translating %T: %v", b, err)
	}
	out, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshalling %T: %v", w, err)
	}
	return string(out)
}

func TestBlockToWire(t *testing.T) {
	cases := []struct {
		name   string
		policy goodall.CachePolicy
		block  goodall.Block
		want   string
	}{
		{
			name:  "text",
			block: goodall.Text{Text: "hello"},
			want:  `{"type":"text","text":"hello"}`,
		},
		{
			name:   "text with a manual cache marker",
			policy: goodall.CacheManual,
			block:  goodall.Text{Text: "hello", Cache: &goodall.CacheControl{}},
			want:   `{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}`,
		},
		{
			name:   "text with a one-hour marker",
			policy: goodall.CacheManual,
			block:  goodall.Text{Text: "hello", Cache: &goodall.CacheControl{TTL: goodall.CacheTTL1h}},
			want:   `{"type":"text","text":"hello","cache_control":{"type":"ephemeral","ttl":"1h"}}`,
		},
		{
			name:   "a marker is dropped when caching is off",
			policy: goodall.CacheOff,
			block:  goodall.Text{Text: "hello", Cache: &goodall.CacheControl{}},
			want:   `{"type":"text","text":"hello"}`,
		},
		{
			name:  "image from bytes",
			block: goodall.Image{Source: goodall.BytesSource("image/png", []byte{1, 2, 3})},
			want:  `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AQID"}}`,
		},
		{
			name:  "image from a url",
			block: goodall.Image{Source: goodall.URLSource("https://example.com/a.png")},
			want:  `{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}}`,
		},
		{
			name:  "image from an uploaded file",
			block: goodall.Image{Source: goodall.FileSource("file_123")},
			want:  `{"type":"image","source":{"type":"file","file_id":"file_123"}}`,
		},
		{
			name: "document with a title and context",
			block: goodall.Document{
				Source:  goodall.BytesSource("application/pdf", []byte("%PDF")),
				Title:   "Report",
				Context: "quarterly numbers",
			},
			want: `{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERg=="},"title":"Report","context":"quarterly numbers"}`,
		},
		{
			name:  "tool use",
			block: goodall.ToolUse{ID: "tu_1", Name: "get_weather", Input: jsontext.Value(`{"city":"Oslo"}`)},
			want:  `{"type":"tool_use","id":"tu_1","name":"get_weather","input":{"city":"Oslo"}}`,
		},
		{
			name:  "tool use with no input at all",
			block: goodall.ToolUse{ID: "tu_1", Name: "now"},
			want:  `{"type":"tool_use","id":"tu_1","name":"now","input":{}}`,
		},
		{
			name:  "tool result",
			block: goodall.ToolResult{ToolUseID: "tu_1", Content: goodall.Blocks{goodall.Text{Text: "12C"}}},
			want:  `{"type":"tool_result","tool_use_id":"tu_1","content":[{"type":"text","text":"12C"}]}`,
		},
		{
			name:  "error tool result",
			block: goodall.ToolResult{ToolUseID: "tu_1", IsError: true, Content: goodall.Blocks{goodall.Text{Text: "no such city"}}},
			want:  `{"type":"tool_result","tool_use_id":"tu_1","is_error":true,"content":[{"type":"text","text":"no such city"}]}`,
		},
		{
			name:  "empty tool result content is a list, never a string",
			block: goodall.ToolResult{ToolUseID: "tu_1"},
			want:  `{"type":"tool_result","tool_use_id":"tu_1","content":[]}`,
		},
		{
			name: "thinking is rebuilt from the neutral fields and ignores raw",
			block: goodall.Thinking{
				Text:      "step one",
				Signature: "sig",
				Raw:       jsontext.Value(`{"type":"thinking","thinking":"stale","signature":"stale"}`),
			},
			want: `{"type":"thinking","thinking":"step one","signature":"sig"}`,
		},
		{
			name:  "thinking with no text still carries its signature",
			block: goodall.Thinking{Signature: "sig"},
			want:  `{"type":"thinking","thinking":"","signature":"sig"}`,
		},
		{
			name:  "redacted thinking",
			block: goodall.RedactedThinking{Data: "encrypted"},
			want:  `{"type":"redacted_thinking","data":"encrypted"}`,
		},
		{
			name:  "an unknown block re-emits its raw bytes verbatim",
			block: goodall.Unknown{Type: "server_tool_use", Raw: jsontext.Value(`{"type":"server_tool_use","id":"srv_1","name":"web_search","input":{"query":"go"}}`)},
			want:  `{"type":"server_tool_use","id":"srv_1","name":"web_search","input":{"query":"go"}}`,
		},
		{
			name:  "an unknown block with no raw bytes keeps its tag",
			block: goodall.Unknown{Type: "container_upload"},
			want:  `{"type":"container_upload"}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wireJSON(t, c.policy, c.block); got != c.want {
				t.Errorf("got  %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestWireToBlock(t *testing.T) {
	cases := []struct {
		name string
		wire string
		want goodall.Block
	}{
		{
			name: "text",
			wire: `{"type":"text","text":"hello"}`,
			want: goodall.Text{Text: "hello"},
		},
		{
			name: "image from bytes",
			wire: `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AQID"}}`,
			want: goodall.Image{Source: goodall.BytesSource("image/png", []byte{1, 2, 3})},
		},
		{
			name: "image from a url",
			wire: `{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}}`,
			want: goodall.Image{Source: goodall.URLSource("https://example.com/a.png")},
		},
		{
			name: "document",
			wire: `{"type":"document","source":{"type":"file","file_id":"file_1"},"title":"Report","context":"why"}`,
			want: goodall.Document{Source: goodall.FileSource("file_1"), Title: "Report", Context: "why"},
		},
		{
			name: "tool use",
			wire: `{"type":"tool_use","id":"tu_1","name":"get_weather","input":{"city":"Oslo"}}`,
			want: goodall.ToolUse{ID: "tu_1", Name: "get_weather", Input: jsontext.Value(`{"city":"Oslo"}`)},
		},
		{
			name: "tool result",
			wire: `{"type":"tool_result","tool_use_id":"tu_1","is_error":true,"content":[{"type":"text","text":"boom"}]}`,
			want: goodall.ToolResult{ToolUseID: "tu_1", IsError: true, Content: goodall.Blocks{goodall.Text{Text: "boom"}}},
		},
		{
			name: "thinking decodes with an empty raw",
			wire: `{"type":"thinking","thinking":"step one","signature":"sig"}`,
			want: goodall.Thinking{Text: "step one", Signature: "sig"},
		},
		{
			name: "redacted thinking",
			wire: `{"type":"redacted_thinking","data":"encrypted"}`,
			want: goodall.RedactedThinking{Data: "encrypted"},
		},
		{
			name: "an unrecognised block keeps its bytes",
			wire: `{"type":"web_search_tool_result","tool_use_id":"srv_1","content":[]}`,
			want: goodall.Unknown{Type: "web_search_tool_result", Raw: jsontext.Value(`{"type":"web_search_tool_result","tool_use_id":"srv_1","content":[]}`)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var blocks wireBlocks
			if err := json.Unmarshal([]byte("["+c.wire+"]"), &blocks); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			got := neutralBlocks(blocks)
			if len(got) != 1 {
				t.Fatalf("got %d blocks, want 1", len(got))
			}
			if !reflect.DeepEqual(got[0], c.want) {
				t.Errorf("got  %#v\nwant %#v", got[0], c.want)
			}
		})
	}
}

// TestUnknownBlockSurvivesARoundTrip is the invariant-6 guard at this seam: a
// block goodall does not understand goes back to Anthropic byte-identical.
func TestUnknownBlockSurvivesARoundTrip(t *testing.T) {
	const raw = `{"type":"server_tool_use","id":"srv_1","name":"web_search","input":{"query":"go 1.27"}}`
	var blocks wireBlocks
	if err := json.Unmarshal([]byte("["+raw+"]"), &blocks); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	neutral := neutralBlocks(blocks)
	if got := wireJSON(t, goodall.CacheAuto, neutral[0]); got != raw {
		t.Errorf("got  %s\nwant %s", got, raw)
	}
}

func TestTranslateResponse(t *testing.T) {
	const body = `{
		"id": "msg_01",
		"type": "message",
		"role": "assistant",
		"model": "claude-opus-4-6",
		"content": [
			{"type": "thinking", "thinking": "hmm", "signature": "sig"},
			{"type": "text", "text": "Oslo is cold."},
			{"type": "tool_use", "id": "tu_1", "name": "get_weather", "input": {"city": "Oslo"}}
		],
		"stop_reason": "tool_use",
		"stop_sequence": null,
		"usage": {
			"input_tokens": 10,
			"output_tokens": 20,
			"cache_creation_input_tokens": 30,
			"cache_read_input_tokens": 40
		}
	}`
	var w wireResponse
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	got := translateResponse(&w)

	if got.ID != "msg_01" || got.Model != "claude-opus-4-6" {
		t.Errorf("id/model = %q/%q", got.ID, got.Model)
	}
	if got.Message.Role != goodall.RoleAssistant {
		t.Errorf("role = %q, want %q", got.Message.Role, goodall.RoleAssistant)
	}
	if got.StopReason != goodall.StopToolUse {
		t.Errorf("stop reason = %q", got.StopReason)
	}
	if got.StopSequence != "" {
		t.Errorf("stop sequence = %q, want empty", got.StopSequence)
	}
	if got.NativeStopReason != "" {
		t.Errorf("native stop reason = %q, want empty on Anthropic", got.NativeStopReason)
	}
	if got.Cost != (goodall.Cost{}) {
		t.Errorf("cost = %#v, want the zero value: Anthropic reports no money", got.Cost)
	}
	want := goodall.Usage{Input: 10, Output: 20, CacheWrite: 30, CacheRead: 40}
	if got.Usage != want {
		t.Errorf("usage = %#v, want %#v", got.Usage, want)
	}
	if len(got.Message.Content) != 3 {
		t.Fatalf("got %d blocks, want 3", len(got.Message.Content))
	}
	if think, ok := got.Message.Content[0].(goodall.Thinking); !ok {
		t.Errorf("first block is %T, want goodall.Thinking", got.Message.Content[0])
	} else if think.Text != "hmm" || think.Signature != "sig" || len(think.Raw) != 0 {
		t.Errorf("thinking = %#v, want text and signature with no raw", think)
	}
}

// TestTranslateResponseKeepsAnUnknownStopReason holds the pass-through rule:
// Anthropic's stop strings are goodall's constants, so a new one arrives
// verbatim rather than being flattened.
func TestTranslateResponseKeepsAnUnknownStopReason(t *testing.T) {
	var w wireResponse
	if err := json.Unmarshal([]byte(`{"id":"msg_1","content":[],"stop_reason":"model_context_window_exceeded"}`), &w); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	got := translateResponse(&w)
	if got.StopReason != goodall.StopReason("model_context_window_exceeded") {
		t.Errorf("stop reason = %q", got.StopReason)
	}
	if got.StopReason.Known() {
		t.Error("the stop reason reports itself as known")
	}
}

// TestStreamEventPayloads decodes one of each SSE payload. The decoder itself
// belongs to the streaming leaf; these structs are the wire shapes it reads.
func TestStreamEventPayloads(t *testing.T) {
	var start wireMessageStartEvent
	if err := json.Unmarshal([]byte(`{"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"m","content":[],"usage":{"input_tokens":5}}}`), &start); err != nil {
		t.Fatalf("message_start: %v", err)
	}
	if start.Message.ID != "msg_1" || start.Message.Usage.InputTokens != 5 {
		t.Errorf("message_start = %#v", start)
	}

	var blockStart wireContentBlockStartEvent
	if err := json.Unmarshal([]byte(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_1","name":"go","input":{}}}`), &blockStart); err != nil {
		t.Fatalf("content_block_start: %v", err)
	}
	use, ok := blockStart.ContentBlock.Block.(wireToolUse)
	if !ok {
		t.Fatalf("content_block = %T, want wireToolUse", blockStart.ContentBlock.Block)
	}
	if use.ID != "tu_1" || blockStart.Index != 1 {
		t.Errorf("content_block_start = %#v", blockStart)
	}

	for _, c := range []struct {
		body string
		want wireDelta
	}{
		{`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`, wireDelta{Type: deltaText, Text: "hi"}},
		{`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`, wireDelta{Type: deltaInputJSON, PartialJSON: `{"a":`}},
		{`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hm"}}`, wireDelta{Type: deltaThinking, Thinking: "hm"}},
		{`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`, wireDelta{Type: deltaSignature, Signature: "sig"}},
	} {
		var ev wireContentBlockDeltaEvent
		if err := json.Unmarshal([]byte(c.body), &ev); err != nil {
			t.Fatalf("content_block_delta: %v", err)
		}
		if ev.Delta != c.want {
			t.Errorf("delta = %#v, want %#v", ev.Delta, c.want)
		}
	}

	var msgDelta wireMessageDeltaEvent
	if err := json.Unmarshal([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":42}}`), &msgDelta); err != nil {
		t.Fatalf("message_delta: %v", err)
	}
	if msgDelta.Delta.StopReason != goodall.StopEndTurn || msgDelta.Usage.OutputTokens != 42 {
		t.Errorf("message_delta = %#v", msgDelta)
	}

	var errEvent wireErrorEvent
	if err := json.Unmarshal([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`), &errEvent); err != nil {
		t.Fatalf("error: %v", err)
	}
	if errEvent.Error.Type != "overloaded_error" || errEvent.Error.Message != "Overloaded" {
		t.Errorf("error = %#v", errEvent)
	}
}
