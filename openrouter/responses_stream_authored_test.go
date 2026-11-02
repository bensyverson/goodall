// Streams written by hand, each with a comment saying from what. response.failed,
// the stream's error event and refusal parts were never observed from the live API.

package openrouter

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// frames builds a stream from data payloads. Every stream built with it is
// authored, and each test below says from what.
func frames(payloads ...string) []byte {
	var b strings.Builder
	for _, payload := range payloads {
		b.WriteString("data: " + payload + "\n\n")
	}
	return []byte(b.String())
}

const (
	authoredCreated = `{"type":"response.created","response":{"id":"resp_1","model":"gpt-6-astra","status":"in_progress"}}`
)

// authoredCompleted is a terminal event holding the given output.
func authoredCompleted(output string) string {
	return `{"type":"response.completed","response":{"id":"resp_1","model":"gpt-6-astra","status":"completed","output":[` + output + `],"usage":{"input_tokens":3,"output_tokens":2}}}`
}

func TestDeltasInterleavedAcrossItemsAreAddressedByOutputIndex(t *testing.T) {
	// Authored: the recorded parallel turn ran its calls one after the other,
	// and nothing on the wire promises that.
	resp, err := responsesStream(t, frames(
		authoredCreated,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_a","name":"one","arguments":""}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_b","name":"two","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"b\":"}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"a\":"}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"2}"}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"1}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call_a","name":"one","arguments":"{\"a\":1}"}}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_b","name":"two","arguments":"{\"b\":2}"}}`,
		authoredCompleted(""),
	)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	uses := resp.Message.ToolUses()
	if len(uses) != 2 || string(uses[0].Input) != `{"a":1}` || string(uses[1].Input) != `{"b":2}` {
		t.Errorf("calls = %+v", uses)
	}
}

func TestACallWhoseArgumentsCameOnlyOnDoneKeepsThem(t *testing.T) {
	// Authored: a tool that ran with an empty input because a delta never came
	// would be a silent wrong answer, so the done item fills in.
	resp, err := responsesStream(t, frames(
		authoredCreated,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_a","name":"one","arguments":""}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call_a","name":"one","arguments":"{\"a\":1}"}}`,
		authoredCompleted(""),
	)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if uses := resp.Message.ToolUses(); len(uses) != 1 || string(uses[0].Input) != `{"a":1}` {
		t.Errorf("calls = %+v", uses)
	}
}

func TestSeveralSummaryPartsJoinOnTheStreamAsTheyDoOnComplete(t *testing.T) {
	// Authored: every recorded item had one summary part.
	item := `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"First."},{"type":"summary_text","text":"Second."}],"encrypted_content":"x"}`
	resp, err := responsesStream(t, frames(
		authoredCreated,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"short"}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"Fir"}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"st."}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":1,"delta":"Second."}`,
		`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
		authoredCompleted(item),
	)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	thinking := thinkingOf(resp.Message)
	if len(thinking) != 1 || thinking[0].Text != "First.\n\nSecond." {
		t.Fatalf("thinking = %+v", thinking)
	}
	if string(thinking[0].Raw) != item {
		t.Errorf("Raw was touched: %s", thinking[0].Raw)
	}
}

func TestAReasoningItemWithNoDoneTakesItsBytesFromTheTerminalEvent(t *testing.T) {
	// Authored: an item cut off by the terminal event must not lose its Raw, since
	// nothing on the server would complain about the loss.
	item := `{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"final"}`
	resp, err := responsesStream(t, frames(
		authoredCreated,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"short"}}`,
		authoredCompleted(item),
	)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if thinking := thinkingOf(resp.Message); len(thinking) != 1 || string(thinking[0].Raw) != item {
		t.Errorf("thinking = %+v", thinking)
	}
}

func TestADoneItemThatWasNeverAddedStillBecomesABlock(t *testing.T) {
	// Authored: the reasoning bytes are the part that must survive this.
	item := `{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"x"}`
	resp, err := responsesStream(t, frames(
		authoredCreated,
		`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
		authoredCompleted(item),
	)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if thinking := thinkingOf(resp.Message); len(thinking) != 1 || string(thinking[0].Raw) != item {
		t.Errorf("thinking = %+v", thinking)
	}
}

func TestAnAuthoredRefusalIsTextAndAStopReason(t *testing.T) {
	// Authored from the documentation's refusal delta events: nothing here drew a
	// refusal from a model.
	resp, err := responsesStream(t, frames(
		authoredCreated,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"refusal","refusal":""}}`,
		`{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"I can"}`,
		`{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"not."}`,
		`{"type":"response.refusal.done","output_index":0,"content_index":0,"refusal":"I cannot."}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","content":[{"type":"refusal","refusal":"I cannot."}]}}`,
		authoredCompleted(`{"type":"message","content":[{"type":"refusal","refusal":"I cannot."}]}`),
	)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != goodall.StopRefusal || resp.Message.Text() != "I cannot." {
		t.Errorf("stop %q, text %q", resp.StopReason, resp.Message.Text())
	}
}

func TestAnAuthoredFailedResponseIsATypedError(t *testing.T) {
	// Authored from the documentation: response.failed could not be provoked.
	_, err := responsesStream(t, frames(
		authoredCreated,
		`{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_error","message":"The model broke."}}}`,
	)).Collect()
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if apiErr.Kind != goodall.KindServer || apiErr.Message != "The model broke." || apiErr.Type != "server_error" {
		t.Errorf("error = %+v", apiErr)
	}
}

func TestAnAuthoredErrorEventIsATypedError(t *testing.T) {
	// Authored from the documentation, which puts code and message at the top
	// level: the stream's error event could not be provoked. The nested form is
	// the shape every other error here has, so a change of mind is still typed.
	for name, payload := range map[string]string{
		"top level": `{"type":"error","code":"rate_limit_exceeded","message":"Slow down.","param":null,"sequence_number":2}`,
		"nested":    `{"type":"error","error":{"code":"rate_limit_exceeded","message":"Slow down."}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := responsesStream(t, frames(authoredCreated, payload)).Collect()
			apiErr, ok := errors.AsType[*goodall.APIError](err)
			if !ok {
				t.Fatalf("err = %v (%T)", err, err)
			}
			if apiErr.Kind != goodall.KindRateLimited || apiErr.Message != "Slow down." {
				t.Errorf("error = %+v", apiErr)
			}
		})
	}
}

func TestAnUnknownEventIsSurfacedAndChangesNothing(t *testing.T) {
	// Authored: invariant 6 asks for an event type nobody has seen yet.
	unknown := `{"type":"response.shiny_new.delta","delta":"x"}`
	malformed := `not json at all`
	var seen []goodall.UnknownEvent
	var acc goodall.Accumulator
	for ev, err := range responsesStream(t, frames(authoredCreated, unknown, malformed, authoredCompleted(""))) {
		if err != nil {
			t.Fatal(err)
		}
		if e, ok := ev.(goodall.UnknownEvent); ok {
			seen = append(seen, e)
		}
		if err := acc.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("unknown events = %+v", seen)
	}
	if seen[0].EventType != "response.shiny_new.delta" || string(seen[0].Raw) != unknown {
		t.Errorf("first = %+v", seen[0])
	}
	if seen[1].EventType != eventMalformedChunk {
		t.Errorf("second = %+v", seen[1])
	}
	if !acc.Done() {
		t.Error("the message never finished")
	}
}

func TestAnUnknownOutputItemIsABlockOnTheStreamToo(t *testing.T) {
	// Authored: an item type nobody has seen yet, as on the blocking path.
	item := `{"type":"shiny_new_call","id":"x_1"}`
	resp, err := responsesStream(t, frames(
		authoredCreated,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"shiny_new_call","id":"x_1","status":"in_progress"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
		authoredCompleted(item),
	)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	unknown, ok := resp.Message.Content[0].(goodall.Unknown)
	if !ok || unknown.Type != "shiny_new_call" || string(unknown.Raw) != item {
		t.Errorf("block = %#v", resp.Message.Content[0])
	}
}

func TestAStreamThatStopsWithNoTerminalEventIsAProtocolError(t *testing.T) {
	_, err := responsesStream(t, frames(authoredCreated)).Collect()
	if _, ok := errors.AsType[*goodall.ProtocolError](err); !ok {
		t.Errorf("err = %v (%T), want a *goodall.ProtocolError", err, err)
	}
}

func TestAStreamedRequestPostsToResponsesAndAsksForAStream(t *testing.T) {
	var path, accept, body string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		path, accept = r.URL.Path, r.Header.Get("Accept")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		serveStream(w, probeRecording(t, "two_turn_stream_gpt-6-luna.turn2.response.sse"))
	}, WithDialect(OpenAI))
	if _, err := c.Stream(t.Context(), simpleRequest()).Collect(); err != nil {
		t.Fatal(err)
	}
	if path != "/responses" || accept != "text/event-stream" {
		t.Errorf("path %q, accept %q", path, accept)
	}
	if !strings.Contains(body, `"store":false,"stream":true,"input":[`) {
		t.Errorf("body = %s", body)
	}
}

func TestARecordedRefusalBeforeAnyStreamIsTheStreamsOnlyYield(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write(probeRecording(t, "unknown_call_id.response.json"))
	}, WithDialect(OpenAI))
	_, err := c.Stream(t.Context(), simpleRequest()).Collect()
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if apiErr.Kind != goodall.KindInvalidRequest || apiErr.Status != http.StatusBadRequest {
		t.Errorf("error = %+v", apiErr)
	}
}

func TestADeltaThatFitsNoItemIsSurfacedRatherThanDropped(t *testing.T) {
	// Authored: a delta aimed at the wrong kind of item, or at no item, says
	// something went wrong that a consumer should be able to see.
	call := `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_a","name":"one","arguments":""}}`
	stray := []string{
		`{"type":"response.output_text.delta","output_index":0,"delta":"x"}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"x"}`,
		`{"type":"response.function_call_arguments.delta","output_index":9,"delta":"x"}`,
	}
	var seen []string
	body := frames(append(append([]string{authoredCreated, call}, stray...), authoredCompleted(""))...)
	for ev, err := range responsesStream(t, body) {
		if err != nil {
			t.Fatal(err)
		}
		if unknown, ok := ev.(goodall.UnknownEvent); ok {
			seen = append(seen, string(unknown.Raw))
		}
	}
	if strings.Join(seen, "\n") != strings.Join(stray, "\n") {
		t.Errorf("surfaced = %v, want %v", seen, stray)
	}
}
