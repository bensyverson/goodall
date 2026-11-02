package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// responsesCompleted runs Complete over a body, under the OpenAI dialect.
func responsesCompleted(t *testing.T, body []byte) (*goodall.Response, error) {
	t.Helper()
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(body)
	}, WithDialect(OpenAI))
	return c.Complete(t.Context(), simpleRequest())
}

// outputItems reads the raw items of a recorded blocking response.
func outputItems(t *testing.T, body []byte) []jsontext.Value {
	t.Helper()
	var decoded struct {
		Output []jsontext.Value `json:"output"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Output
}

// compacted is a value with its whitespace gone and every other byte kept. A
// blocking response from OpenAI is indented and a stream is not.
func compacted(t *testing.T, raw jsontext.Value) string {
	t.Helper()
	out := jsontext.Value(append([]byte(nil), raw...))
	if err := out.Compact(); err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestARecordedBlockingToolCallReadsIntoBlocks(t *testing.T) {
	body := probeRecording(t, "two_turn_block_gpt-6-astra.turn1.response.json")
	resp, err := responsesCompleted(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.ID, "resp_") || resp.Model != "gpt-6-astra" {
		t.Errorf("id %q, model %q", resp.ID, resp.Model)
	}
	if resp.Message.Role != goodall.RoleAssistant {
		t.Errorf("role = %q", resp.Message.Role)
	}
	if resp.StopReason != goodall.StopToolUse || resp.NativeStopReason != "completed" {
		t.Errorf("stop = %q, native %q", resp.StopReason, resp.NativeStopReason)
	}
	if want := (goodall.Usage{Input: 90, Output: 78, Reasoning: 58}); resp.Usage != want {
		t.Errorf("usage = %+v, want %+v", resp.Usage, want)
	}
	if resp.Cost.Reported {
		t.Errorf("cost = %+v, and OpenAI reports none", resp.Cost)
	}
	if got := blockKinds(resp.Message); got != "goodall.Thinking,goodall.ToolUse" {
		t.Fatalf("blocks = %s", got)
	}
	thinking := resp.Message.Content[0].(goodall.Thinking)
	if !strings.Contains(thinking.Text, "successful mathematical task") {
		t.Errorf("thinking text = %q, want the summary", thinking.Text)
	}
	if want := compacted(t, outputItems(t, body)[0]); string(thinking.Raw) != want {
		t.Errorf("Raw is not the whole item:\n got %s\nwant %s", thinking.Raw, want)
	}
	use := resp.Message.Content[1].(goodall.ToolUse)
	if !strings.HasPrefix(use.ID, "call_") || use.Name != "get_weather" || string(use.Input) != `{"city":"Paris"}` {
		t.Errorf("tool use = %+v, want the call_id as its ID", use)
	}
}

func TestARecordedBlockingAnswerReadsAsEndOfTurn(t *testing.T) {
	resp, err := responsesCompleted(t, probeRecording(t, "two_turn_block_gpt-6-astra.turn2.response.json"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != goodall.StopEndTurn {
		t.Errorf("stop = %q", resp.StopReason)
	}
	if !strings.Contains(resp.Message.Text(), "Paris") {
		t.Errorf("text = %q", resp.Message.Text())
	}
}

// nextTurn is the request a loop sends after answering a response's tool calls.
func nextTurn(first *goodall.Request, resp *goodall.Response) *goodall.Request {
	var results goodall.Blocks
	for _, use := range resp.Message.ToolUses() {
		results = append(results, goodall.ToolResult{ToolUseID: use.ID, Content: goodall.Blocks{goodall.Text{Text: `{"temp_c":18,"sky":"clear"}`}}})
	}
	next := *first
	next.Messages = append(append([]goodall.Message{}, first.Messages...), resp.Message, goodall.UserMessage(results...))
	return &next
}

// itemsSent reads the input items of one request body, as bytes.
func itemsSent(t *testing.T, body string) []string {
	t.Helper()
	var decoded struct {
		Input []jsontext.Value `json:"input"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(decoded.Input))
	for _, item := range decoded.Input {
		out = append(out, string(item))
	}
	return out
}

// The server answers a lost reasoning item with a 200 and no complaint, as
// strip_reasoning and strip_encrypted record, so this test is the only thing that
// would notice.
func TestARecordedTwoTurnExchangeReplaysItsReasoningByteForByte(t *testing.T) {
	body := probeRecording(t, "two_turn_block_gpt-6-astra.turn1.response.json")
	resp, err := responsesCompleted(t, body)
	if err != nil {
		t.Fatal(err)
	}
	sent := itemsSent(t, sentBody(t, nextTurn(&goodall.Request{
		Model:    "gpt-6-astra",
		Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "the prompt"})},
	}, resp)))
	if len(sent) != 4 {
		t.Fatalf("items = %d, want the prompt, the reasoning, the call and its answer:\n%v", len(sent), sent)
	}
	if want := compacted(t, outputItems(t, body)[0]); sent[1] != want {
		t.Errorf("the reasoning item changed:\n got %s\nwant %s", sent[1], want)
	}
	// And the bytes goodall sends are the ones the live turn two was sent, which
	// the API accepted.
	var recorded struct {
		Input []jsontext.Value `json:"input"`
	}
	if err := json.Unmarshal(probeRecording(t, "two_turn_block_gpt-6-astra.turn2.request.json"), &recorded); err != nil {
		t.Fatal(err)
	}
	if want := compacted(t, recorded.Input[1]); sent[1] != want {
		t.Errorf("goodall's reasoning item is not the recorded turn two's:\n got %s\nwant %s", sent[1], want)
	}
	if !strings.Contains(sent[1], `"encrypted_content":"gAAAA`) {
		t.Errorf("the replayed item has no ciphertext: %s", sent[1])
	}
}

func TestAReasoningItemWithNoSummaryIsStillABlock(t *testing.T) {
	// Recorded: with no summary member the item's summary is empty, and the item
	// still has to go back.
	body := probeRecording(t, "summary_absent.response.json")
	resp, err := responsesCompleted(t, body)
	if err != nil {
		t.Fatal(err)
	}
	thinking := thinkingOf(resp.Message)
	if len(thinking) != 1 || thinking[0].Text != "" || !strings.Contains(string(thinking[0].Raw), "encrypted_content") {
		t.Errorf("thinking = %+v, want one block with no text and the item as Raw", thinking)
	}
}

func TestACommentaryMessageBeforeACallReadsInOrder(t *testing.T) {
	resp, err := responsesCompleted(t, probeRecording(t, "phase_commentary.turn1.response.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := blockKinds(resp.Message); got != "goodall.Text,goodall.ToolUse" {
		t.Errorf("blocks = %s", got)
	}
	if resp.StopReason != goodall.StopToolUse {
		t.Errorf("stop = %q", resp.StopReason)
	}
}

// authoredResponse builds a blocking body from output items. Every body made with it
// is authored, not recorded: OpenAI's own response was never observed in that state.
func authoredResponse(status, extra, output string) []byte {
	return []byte(`{"id":"resp_1","model":"gpt-6-astra","status":"` + status + `",` + extra +
		`"output":[` + output + `],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":1},"output_tokens":6,"output_tokens_details":{"reasoning_tokens":2}}}`)
}

func TestUsageTakesTheCachedPartOutOfInput(t *testing.T) {
	resp, err := responsesCompleted(t, authoredResponse("completed", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if want := (goodall.Usage{Input: 5, Output: 6, CacheRead: 4, CacheWrite: 1, Reasoning: 2}); resp.Usage != want {
		t.Errorf("usage = %+v, want %+v", resp.Usage, want)
	}
}

func TestStopReasonsOnBlockingResponses(t *testing.T) {
	for _, tc := range []struct {
		name, status, extra, output string
		want                        goodall.StopReason
		native                      string
	}{
		{"out of tokens", "incomplete", `"incomplete_details":{"reason":"max_output_tokens"},`, ``, goodall.StopMaxTokens, "max_output_tokens"},
		{"a content filter, seen only in a docs sample", "incomplete", `"incomplete_details":{"reason":"content_filter"},`, ``, goodall.StopRefusal, "content_filter"},
		{"an unknown reason travels on", "incomplete", `"incomplete_details":{"reason":"new_reason"},`, ``, goodall.StopReason("new_reason"), "new_reason"},
		{"an unknown status travels on", "paused", ``, ``, goodall.StopReason("paused"), "paused"},
		{"a refusal part, authored from the docs", "completed", ``, `{"type":"message","content":[{"type":"refusal","refusal":"I can't."}]}`, goodall.StopRefusal, "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := responsesCompleted(t, authoredResponse(tc.status, tc.extra, tc.output))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StopReason != tc.want || resp.NativeStopReason != tc.native {
				t.Errorf("stop = %q, native %q; want %q, %q", resp.StopReason, resp.NativeStopReason, tc.want, tc.native)
			}
		})
	}
}

func TestARefusalPartBecomesText(t *testing.T) {
	resp, err := responsesCompleted(t, authoredResponse("completed", "", `{"type":"message","content":[{"type":"refusal","refusal":"I can't."}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Text() != "I can't." {
		t.Errorf("text = %q", resp.Message.Text())
	}
}

func TestSeveralSummaryPartsJoinIntoOneReadableText(t *testing.T) {
	// Authored: every recorded item had at most one summary part.
	item := `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"First."},{"type":"summary_text","text":""},{"type":"summary_text","text":"Second."}],"encrypted_content":"x"}`
	resp, err := responsesCompleted(t, authoredResponse("completed", "", item))
	if err != nil {
		t.Fatal(err)
	}
	thinking := thinkingOf(resp.Message)
	if len(thinking) != 1 || thinking[0].Text != "First.\n\nSecond." {
		t.Fatalf("thinking = %+v", thinking)
	}
	if string(thinking[0].Raw) != item {
		t.Errorf("Raw was touched:\n got %s\nwant %s", thinking[0].Raw, item)
	}
}

func TestAnUnknownOutputItemIsSurfacedAsAnUnknownBlock(t *testing.T) {
	item := `{"type":"shiny_new_call","id":"x_1"}`
	resp, err := responsesCompleted(t, authoredResponse("completed", "", item))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.Content) != 1 {
		t.Fatalf("blocks = %+v", resp.Message.Content)
	}
	unknown, ok := resp.Message.Content[0].(goodall.Unknown)
	if !ok || unknown.Type != "shiny_new_call" || string(unknown.Raw) != item {
		t.Errorf("block = %#v", resp.Message.Content[0])
	}
}

func TestToolArgumentsThatAreNotJSONAreAnError(t *testing.T) {
	_, err := responsesCompleted(t, authoredResponse("incomplete", `"incomplete_details":{"reason":"max_output_tokens"},`,
		`{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":"}`))
	if err == nil || !strings.Contains(err.Error(), "call_1") {
		t.Errorf("err = %v, want one naming the call, because a truncated call must never run", err)
	}
}

func TestAFailedStatusIsAnAPIError(t *testing.T) {
	// Authored from the documentation: response.failed was never provoked.
	_, err := responsesCompleted(t, authoredResponse("failed", `"error":{"code":"rate_limit_exceeded","message":"slow down"},`, ""))
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want *goodall.APIError", err, err)
	}
	if apiErr.Kind != goodall.KindRateLimited || apiErr.Message != "slow down" || apiErr.Type != "rate_limit_exceeded" {
		t.Errorf("error = %+v", apiErr)
	}
}

func TestAFailedStatusWithNoErrorIsStillAnError(t *testing.T) {
	_, err := responsesCompleted(t, []byte(`{"id":"resp_1","status":"failed","error":null,"output":[]}`))
	if _, ok := errors.AsType[*goodall.APIError](err); !ok {
		t.Errorf("err = %v (%T), want *goodall.APIError", err, err)
	}
}

func TestA200ThatIsNotAResponseIsAnError(t *testing.T) {
	for name, body := range map[string]string{
		"an error body": `{"error":{"message":"boom","code":null}}`,
		"no status":     `{"id":"resp_1"}`,
		"not JSON":      `<html>`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := responsesCompleted(t, []byte(body)); err == nil {
				t.Errorf("no error for %s", body)
			}
		})
	}
}

func TestARecordedRefusedRequestIsATypedError(t *testing.T) {
	body := probeRecording(t, "unknown_call_id.response.json")
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write(body)
	}, WithDialect(OpenAI))
	_, err := c.Complete(t.Context(), simpleRequest())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if apiErr.Kind != goodall.KindInvalidRequest || apiErr.Status != http.StatusBadRequest || !strings.Contains(apiErr.Message, "call_nothing") {
		t.Errorf("error = %+v", apiErr)
	}
}

func TestA503WithABodyThatIsNotJSONIsStillTyped(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, "upstream connect error")
	}, WithDialect(OpenAI), WithMaxRetries(-1))
	_, err := c.Complete(t.Context(), simpleRequest())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if apiErr.Status != http.StatusServiceUnavailable {
		t.Errorf("error = %+v", apiErr)
	}
}
