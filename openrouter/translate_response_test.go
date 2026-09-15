package openrouter

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// decodeResponse decodes a recorded body into the wire response.
func decodeResponse(t *testing.T, body string) *chatResponse {
	t.Helper()
	var wire chatResponse
	if err := json.Unmarshal([]byte(body), &wire); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return &wire
}

func TestTranslateResponseBlockOrder(t *testing.T) {
	body := `{
	  "id": "gen-1",
	  "model": "anthropic/claude-sonnet-4",
	  "choices": [{
	    "index": 0,
	    "finish_reason": "tool_calls",
	    "native_finish_reason": "tool_use",
	    "message": {
	      "role": "assistant",
	      "content": "Checking now.",
	      "reasoning_details": [
	        {"type":"reasoning.text","text":"think","signature":"sig","index":0},
	        {"type":"reasoning.summary","summary":"a summary","index":1},
	        {"type":"reasoning.encrypted","data":"ZW5j","index":2},
	        {"type":"reasoning.server_tool_call","tool_name":"openrouter:fusion","arguments":"{}","result":"{}"}
	      ],
	      "tool_calls": [
	        {"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"query\":\"x\"}"}}
	      ]
	    }
	  }]
	}`
	resp, err := translateResponse(decodeResponse(t, body))
	if err != nil {
		t.Fatalf("translateResponse: %v", err)
	}
	if resp.ID != "gen-1" || resp.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("ID/Model = %q/%q", resp.ID, resp.Model)
	}
	if resp.StopReason != goodall.StopToolUse {
		t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
	}
	if resp.NativeStopReason != "tool_use" {
		t.Errorf("NativeStopReason = %q", resp.NativeStopReason)
	}
	if resp.Message.Role != goodall.RoleAssistant {
		t.Errorf("Role = %q", resp.Message.Role)
	}

	blocks := resp.Message.Content
	if len(blocks) != 6 {
		t.Fatalf("got %d blocks, want 6: %#v", len(blocks), blocks)
	}
	first, ok := blocks[0].(goodall.Thinking)
	if !ok || first.Text != "think" || first.Signature != "sig" {
		t.Errorf("blocks[0] = %#v, want a Thinking with text and signature", blocks[0])
	}
	if want := `{"type":"reasoning.text","text":"think","signature":"sig","index":0}`; string(first.Raw) != want {
		t.Errorf("blocks[0].Raw = %s, want %s", first.Raw, want)
	}
	if second, ok := blocks[1].(goodall.Thinking); !ok || second.Text != "a summary" {
		t.Errorf("blocks[1] = %#v, want a Thinking carrying the summary", blocks[1])
	}
	if third, ok := blocks[2].(goodall.RedactedThinking); !ok || third.Data != "ZW5j" {
		t.Errorf("blocks[2] = %#v, want a RedactedThinking", blocks[2])
	}
	fourth, ok := blocks[3].(goodall.Thinking)
	if !ok || fourth.Text != "" || fourth.Signature != "" {
		t.Errorf("blocks[3] = %#v, want an opaque Thinking", blocks[3])
	}
	if !strings.Contains(string(fourth.Raw), "reasoning.server_tool_call") {
		t.Errorf("blocks[3].Raw = %s, want the entry verbatim", fourth.Raw)
	}
	if text, ok := blocks[4].(goodall.Text); !ok || text.Text != "Checking now." {
		t.Errorf("blocks[4] = %#v, want the assistant text", blocks[4])
	}
	use, ok := blocks[5].(goodall.ToolUse)
	if !ok {
		t.Fatalf("blocks[5] = %#v, want a ToolUse", blocks[5])
	}
	if use.ID != "call_1" || use.Name != "lookup" {
		t.Errorf("tool use = %+v", use)
	}
	if string(use.Input) != `{"query":"x"}` {
		t.Errorf("tool use input = %s, want the arguments as a jsontext.Value", use.Input)
	}
}

func TestTranslateResponseContentParts(t *testing.T) {
	body := `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant",
	  "content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]}}]}`
	resp, err := translateResponse(decodeResponse(t, body))
	if err != nil {
		t.Fatalf("translateResponse: %v", err)
	}
	if len(resp.Message.Content) != 2 {
		t.Fatalf("got %d blocks, want 2: %#v", len(resp.Message.Content), resp.Message.Content)
	}
	if resp.Message.Text() != "onetwo" {
		t.Errorf("text = %q", resp.Message.Text())
	}
	if resp.StopReason != goodall.StopEndTurn {
		t.Errorf("StopReason = %q, want end_turn", resp.StopReason)
	}
}

func TestTranslateResponseRefusal(t *testing.T) {
	body := `{"choices":[{"index":0,"finish_reason":"content_filter","message":{"role":"assistant",
	  "content":null,"refusal":"I can't help with that."}}]}`
	resp, err := translateResponse(decodeResponse(t, body))
	if err != nil {
		t.Fatalf("translateResponse: %v", err)
	}
	if resp.StopReason != goodall.StopRefusal {
		t.Errorf("StopReason = %q, want refusal", resp.StopReason)
	}
	if len(resp.Message.Content) != 1 {
		t.Fatalf("got %d blocks, want 1: %#v", len(resp.Message.Content), resp.Message.Content)
	}
	if text, ok := resp.Message.Content[0].(goodall.Text); !ok || text.Text != "I can't help with that." {
		t.Errorf("blocks[0] = %#v, want the refusal as text", resp.Message.Content[0])
	}
}

func TestTranslateResponseRefusalWithoutAContentFilterFinishReason(t *testing.T) {
	body := `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant",
	  "content":null,"refusal":"No."}}]}`
	resp, err := translateResponse(decodeResponse(t, body))
	if err != nil {
		t.Fatalf("translateResponse: %v", err)
	}
	if resp.StopReason != goodall.StopRefusal {
		t.Errorf("StopReason = %q, want a refusal to win over the finish reason", resp.StopReason)
	}
}

func TestTranslateResponseStopReasons(t *testing.T) {
	for _, tc := range []struct {
		finish string
		want   goodall.StopReason
	}{
		{"stop", goodall.StopEndTurn},
		{"length", goodall.StopMaxTokens},
		{"tool_calls", goodall.StopToolUse},
		{"content_filter", goodall.StopRefusal},
		{"error", goodall.StopReason("error")},
		{"something_new", goodall.StopReason("something_new")},
		{"", goodall.StopNone},
	} {
		if got := stopReason(tc.finish); got != tc.want {
			t.Errorf("stopReason(%q) = %q, want %q", tc.finish, got, tc.want)
		}
	}
}

func TestTranslateResponseRejectsInvalidToolArguments(t *testing.T) {
	body := `{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant",
	  "tool_calls":[{"id":"call_9","type":"function","function":{"name":"lookup","arguments":"{oops"}}]}}]}`
	_, err := translateResponse(decodeResponse(t, body))
	if err == nil {
		t.Fatalf("translateResponse accepted invalid tool-call arguments")
	}
	if !strings.Contains(err.Error(), "call_9") || !strings.Contains(err.Error(), "lookup") {
		t.Errorf("error %v does not name the tool call", err)
	}
}

func TestTranslateResponseEmptyToolArgumentsBecomeAnEmptyObject(t *testing.T) {
	body := `{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant",
	  "tool_calls":[{"id":"call_9","type":"function","function":{"name":"ping","arguments":""}}]}}]}`
	resp, err := translateResponse(decodeResponse(t, body))
	if err != nil {
		t.Fatalf("translateResponse: %v", err)
	}
	use, ok := resp.Message.Content[0].(goodall.ToolUse)
	if !ok {
		t.Fatalf("blocks[0] = %#v, want a ToolUse", resp.Message.Content[0])
	}
	if string(use.Input) != "{}" {
		t.Errorf("input = %s, want {}", use.Input)
	}
}

func TestTranslateResponseWithNoChoicesIsAnError(t *testing.T) {
	body := `{"error":{"code":429,"message":"rate limited","metadata":{"error_type":"rate_limit_exceeded"}}}`
	wire := decodeResponse(t, body)
	if wire.Error == nil {
		t.Fatalf("the wire struct did not recognise the top-level error object")
	}
	if wire.Error.Message != "rate limited" {
		t.Errorf("error message = %q", wire.Error.Message)
	}
	if wire.Error.Metadata == nil || wire.Error.Metadata.ErrorType != "rate_limit_exceeded" {
		t.Errorf("error metadata = %+v", wire.Error.Metadata)
	}
	if _, err := translateResponse(wire); err == nil {
		t.Errorf("translateResponse accepted a body with no choices")
	}
}

func TestTranslateResponseRecognisesAPerChoiceError(t *testing.T) {
	body := `{"choices":[{"index":0,"finish_reason":"error","message":{"role":"assistant"},
	  "error":{"code":502,"message":"upstream died","metadata":{"provider_name":"acme"}}}]}`
	wire := decodeResponse(t, body)
	if len(wire.Choices) != 1 || wire.Choices[0].Error == nil {
		t.Fatalf("the wire struct did not recognise the per-choice error object")
	}
	if wire.Choices[0].Error.Metadata.ProviderName != "acme" {
		t.Errorf("provider name = %q", wire.Choices[0].Error.Metadata.ProviderName)
	}
}

func TestTranslateUsageAndCost(t *testing.T) {
	body := `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}],
	  "usage":{"prompt_tokens":1000,"completion_tokens":40,"total_tokens":1040,
	    "prompt_tokens_details":{"cached_tokens":600,"cache_write_tokens":150},
	    "completion_tokens_details":{"reasoning_tokens":12},
	    "cost":0.0000001234567890123}}`
	resp, err := translateResponse(decodeResponse(t, body))
	if err != nil {
		t.Fatalf("translateResponse: %v", err)
	}
	want := goodall.Usage{Input: 250, Output: 40, CacheRead: 600, CacheWrite: 150, Reasoning: 12}
	if resp.Usage != want {
		t.Errorf("Usage = %+v, want %+v", resp.Usage, want)
	}
	if resp.Usage.TotalInput() != 1000 {
		t.Errorf("TotalInput = %d, want the prompt token count", resp.Usage.TotalInput())
	}
	if !resp.Cost.Reported || resp.Cost.Currency != "USD" {
		t.Errorf("Cost = %+v, want a reported USD cost", resp.Cost)
	}
	if got := resp.Cost.Amount.String(); got != "0.0000001234567890123" {
		t.Errorf("Cost.Amount = %s, want the exact decimal", got)
	}
}

func TestTranslateUsageWithoutCost(t *testing.T) {
	body := `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}],
	  "usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`
	resp, err := translateResponse(decodeResponse(t, body))
	if err != nil {
		t.Fatalf("translateResponse: %v", err)
	}
	if resp.Cost != (goodall.Cost{}) {
		t.Errorf("Cost = %+v, want the zero Cost when the provider reported none", resp.Cost)
	}
	if resp.Usage.Input != 10 {
		t.Errorf("Input = %d, want 10", resp.Usage.Input)
	}
}

func TestTranslateUsageIsCallableOnATrailingChunk(t *testing.T) {
	var u usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":5,"completion_tokens":1,"cost":"0.5"}`), &u); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got, cost := translateUsage(&u)
	if got.Input != 5 || got.Output != 1 {
		t.Errorf("usage = %+v", got)
	}
	if cost.Amount.String() != "0.5" || !cost.Reported {
		t.Errorf("cost = %+v", cost)
	}
	if zero, zeroCost := translateUsage(nil); zero != (goodall.Usage{}) || zeroCost != (goodall.Cost{}) {
		t.Errorf("translateUsage(nil) = %+v %+v, want zero values", zero, zeroCost)
	}
}

func TestStreamChunkStructsAdmitTheWireShapes(t *testing.T) {
	body := `{"id":"gen-2","model":"m","choices":[{"index":0,"finish_reason":null,
	  "native_finish_reason":null,"delta":{"role":"assistant","content":"He",
	  "reasoning":"th","reasoning_details":[{"type":"reasoning.text","text":"th","index":0}],
	  "tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q"}}]}}],
	  "usage":null}`
	var chunk chatChunk
	if err := json.Unmarshal([]byte(body), &chunk); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(chunk.Choices) != 1 {
		t.Fatalf("got %d choices", len(chunk.Choices))
	}
	delta := chunk.Choices[0].Delta
	if delta.Content != "He" || delta.Reasoning != "th" {
		t.Errorf("delta = %+v", delta)
	}
	if len(delta.ReasoningDetails) != 1 {
		t.Errorf("reasoning details = %v", delta.ReasoningDetails)
	}
	if len(delta.ToolCalls) != 1 || delta.ToolCalls[0].Index != 0 || delta.ToolCalls[0].Function.Arguments != `{"q` {
		t.Errorf("tool call fragments = %+v", delta.ToolCalls)
	}

	trailing := `{"id":"gen-2","choices":[{"index":0,"finish_reason":"stop","delta":{}}],
	  "usage":{"prompt_tokens":3,"completion_tokens":4,"cost":0.25}}`
	var last chatChunk
	if err := json.Unmarshal([]byte(trailing), &last); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if last.Usage == nil || last.Usage.Cost == nil {
		t.Fatalf("trailing usage chunk not decoded: %+v", last.Usage)
	}
	if last.Choices[0].FinishReason != "stop" {
		t.Errorf("the trailing chunk must still carry its repeated finish reason")
	}

	errBody := `{"error":{"code":500,"message":"boom","metadata":{"error_type":"server"}}}`
	var errChunk chatChunk
	if err := json.Unmarshal([]byte(errBody), &errChunk); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if errChunk.Error == nil || errChunk.Error.Message != "boom" {
		t.Errorf("a mid-stream error chunk was not recognised: %+v", errChunk.Error)
	}
}

func TestAttributionHeaders(t *testing.T) {
	a := Attribution{Referer: "https://example.com", Title: "Demo"}
	got := a.Headers()
	want := []Header{{HeaderReferer, "https://example.com"}, {HeaderTitle, "Demo"}}
	if len(got) != len(want) {
		t.Fatalf("Headers() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Headers()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if n := len(Attribution{}.Headers()); n != 0 {
		t.Errorf("an empty Attribution yielded %d headers", n)
	}
}

func TestExtensionsNamesItsProvider(t *testing.T) {
	if got := (Extensions{}).Provider(); got != ProviderName {
		t.Errorf("Provider() = %q, want %q", got, ProviderName)
	}
}
