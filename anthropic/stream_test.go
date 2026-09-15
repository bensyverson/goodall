package anthropic

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/bensyverson/goodall"
)

// countingBody serves fixed bytes and counts how often it was closed, which
// is how a test proves the stream owns the response body.
type countingBody struct {
	mu     sync.Mutex
	r      *strings.Reader
	closes int
}

func newCountingBody(s string) *countingBody { return &countingBody{r: strings.NewReader(s)} }

func (b *countingBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.r.Read(p)
}

func (b *countingBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closes++
	return nil
}

func (b *countingBody) closeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closes
}

// streamClient answers one POST with the given event stream over a body whose
// closes are counted.
func streamClient(t *testing.T, events string) (*Client, *countingBody) {
	t.Helper()
	body := newCountingBody(events)
	c := New("sk-ant-test", WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       body,
		}, nil
	})}))
	return c, body
}

// drain collects every event a stream yields, with the error that ended it.
func drain(s goodall.Stream) ([]goodall.Event, error) {
	var events []goodall.Event
	for ev, err := range s {
		if err != nil {
			return events, err
		}
		events = append(events, ev)
	}
	return events, nil
}

func TestStreamMapsEveryDocumentedEvent(t *testing.T) {
	c, _ := serveFixture(t, "thinking_tool_use.sse", "text/event-stream")
	got, err := drain(c.Stream(t.Context(), simpleRequest()))
	if err != nil {
		t.Fatalf("the stream ended with %v", err)
	}
	want := []goodall.Event{
		goodall.MessageStart{
			ID:    "msg_01Fixture",
			Model: "claude-opus-4-5-20251101",
			Usage: goodall.Usage{Input: 412, CacheRead: 1024, Output: 3},
		},
		goodall.BlockStart{Index: 0, Block: goodall.Thinking{}},
		goodall.ThinkingDelta{Index: 0, Text: "The user wants the weather in Paris. "},
		goodall.ThinkingDelta{Index: 0, Text: "I should call get_weather."},
		goodall.SignatureDelta{Index: 0, Signature: "ErUBCkYIBRgCIkDfixtureSignature=="},
		goodall.BlockStop{Index: 0},
		goodall.BlockStart{Index: 1, Block: goodall.Text{}},
		goodall.TextDelta{Index: 1, Text: "Let me check"},
		goodall.TextDelta{Index: 1, Text: " the weather."},
		goodall.BlockStop{Index: 1},
		goodall.BlockStart{Index: 2, Block: goodall.ToolUse{
			ID:    "toolu_01Fixture",
			Name:  "get_weather",
			Input: jsontext.Value("{}"),
		}},
		goodall.ToolInputDelta{Index: 2},
		goodall.ToolInputDelta{Index: 2, PartialJSON: `{"location":`},
		goodall.ToolInputDelta{Index: 2, PartialJSON: `"Paris"}`},
		goodall.BlockStop{Index: 2},
		goodall.MessageDelta{StopReason: goodall.StopToolUse, Usage: goodall.Usage{Output: 187}},
		goodall.MessageStop{},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d:\n got: %#v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("event %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestPingCarriesNothingAndIsSkipped(t *testing.T) {
	c, _ := streamClient(t, "event: ping\ndata: {\"type\":\"ping\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	got, err := drain(c.Stream(t.Context(), simpleRequest()))
	if err != nil {
		t.Fatalf("the stream ended with %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want only the message_stop: %#v", len(got), got)
	}
}

// TestAnOmittedThinkingBlockStillStreamsAndSigns covers what
// display: "omitted" puts on the wire: the thinking block opens, its text
// delta arrives empty, it is signed, and it closes. The empty delta is not an
// anomaly to be filtered — the block has to be replayed to the next turn
// exactly as it arrived, signature and all.
func TestAnOmittedThinkingBlockStillStreamsAndSigns(t *testing.T) {
	c, _ := streamClient(t, strings.Join([]string{
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"ErUBsigned=="}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n"))
	got, err := drain(c.Stream(t.Context(), simpleRequest()))
	if err != nil {
		t.Fatalf("the stream ended with %v", err)
	}
	want := []goodall.Event{
		goodall.BlockStart{Index: 0, Block: goodall.Thinking{}},
		goodall.ThinkingDelta{Index: 0},
		goodall.SignatureDelta{Index: 0, Signature: "ErUBsigned=="},
		goodall.BlockStop{Index: 0},
		goodall.MessageStop{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events =\n%#v\nwant\n%#v", got, want)
	}
}

func TestRedactedThinkingArrivesCompleteAtTheBlockStart(t *testing.T) {
	c, _ := streamClient(t, strings.Join([]string{
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"EroBCkYIBRg..."}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		``,
	}, "\n"))
	got, err := drain(c.Stream(t.Context(), simpleRequest()))
	if err != nil {
		t.Fatalf("the stream ended with %v", err)
	}
	want := goodall.BlockStart{Index: 0, Block: goodall.RedactedThinking{Data: "EroBCkYIBRg..."}}
	if len(got) == 0 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("first event = %#v, want %#v", got, want)
	}
}

func TestStreamSendsTheSameHeadersAsComplete(t *testing.T) {
	c, seen := serveFixture(t, "thinking_tool_use.sse", "text/event-stream", WithBetas(BetaStructuredOutputs))
	if _, err := drain(c.Stream(t.Context(), simpleRequest())); err != nil {
		t.Fatalf("the stream ended with %v", err)
	}
	req, _ := seen.last(t)
	if got := req.Header.Get("X-Api-Key"); got != "sk-ant-test" {
		t.Errorf("X-Api-Key = %q, want the client's key", got)
	}
	if got := req.Header.Get("Anthropic-Version"); got != apiVersion {
		t.Errorf("Anthropic-Version = %q, want %q", got, apiVersion)
	}
	if got := req.Header.Get("Anthropic-Beta"); got != string(BetaStructuredOutputs) {
		t.Errorf("Anthropic-Beta = %q, want the client's beta", got)
	}
}

func TestCollectingTheStreamEqualsTheBlockingResponse(t *testing.T) {
	streaming, _ := serveFixture(t, "thinking_tool_use.sse", "text/event-stream")
	collected, err := streaming.Stream(t.Context(), simpleRequest()).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	blocking, _ := serveFixture(t, "thinking_tool_use.json", "application/json")
	complete, err := blocking.Complete(t.Context(), simpleRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !reflect.DeepEqual(collected, complete) {
		t.Errorf("the collected stream and the blocking response differ:\ncollected: %#v\nblocking:  %#v", collected, complete)
	}
	if collected.Message.Partial {
		t.Error("the collected message is marked partial although message_stop arrived")
	}
}

func TestMidStreamErrorIsAnAPIError(t *testing.T) {
	c, _ := serveFixture(t, "error_overloaded.sse", "text/event-stream")
	events, err := drain(c.Stream(t.Context(), simpleRequest()))
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("err = %#v, want a *goodall.APIError", err)
	}
	if !errors.Is(err, goodall.KindOverloaded) {
		t.Errorf("Kind = %v, want %v", apiErr.Kind, goodall.KindOverloaded)
	}
	if apiErr.Type != string(errorOverloaded) {
		t.Errorf("Type = %q, want the provider's own type", apiErr.Type)
	}
	if apiErr.Status != 0 {
		t.Errorf("Status = %d, want 0: the error arrived inside a 200", apiErr.Status)
	}
	if apiErr.Provider != ProviderName {
		t.Errorf("Provider = %q, want %q", apiErr.Provider, ProviderName)
	}
	if apiErr.RequestID != "req_011Fixture" {
		t.Errorf("RequestID = %q, want the body's request_id", apiErr.RequestID)
	}
	if len(events) != 1 {
		t.Errorf("got %d events before the error, want the message_start alone", len(events))
	}
}

func TestAStreamWhoseFirstEventIsAnErrorIsAnAPIErrorNotAProtocolError(t *testing.T) {
	c, _ := streamClient(t, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	_, err := c.Stream(t.Context(), simpleRequest()).Collect()
	if _, ok := errors.AsType[*goodall.APIError](err); !ok {
		t.Fatalf("err = %#v, want a *goodall.APIError", err)
	}
	if _, ok := errors.AsType[*goodall.ProtocolError](err); ok {
		t.Error("the error was reported as a protocol fault rather than the provider's own refusal")
	}
}

func TestAnUnknownEventTypeIsSurfacedNotDropped(t *testing.T) {
	c, _ := serveFixture(t, "unknown_events.sse", "text/event-stream")
	got, err := drain(c.Stream(t.Context(), simpleRequest()))
	if err != nil {
		t.Fatalf("the stream ended with %v", err)
	}
	var unknowns []goodall.UnknownEvent
	for _, ev := range got {
		if u, ok := ev.(goodall.UnknownEvent); ok {
			unknowns = append(unknowns, u)
		}
	}
	if len(unknowns) != 3 {
		t.Fatalf("got %d unknown events, want 3 (an event type and two delta types): %#v", len(unknowns), got)
	}
	wantTypes := []goodall.EventType{"message_metadata", "citations_delta", "widget_delta"}
	for i, want := range wantTypes {
		if unknowns[i].EventType != want {
			t.Errorf("unknown %d EventType = %q, want %q", i, unknowns[i].EventType, want)
		}
		if !strings.Contains(string(unknowns[i].Raw), string(want)) {
			t.Errorf("unknown %d Raw = %s, want the event's own bytes", i, unknowns[i].Raw)
		}
	}
	if got, want := unknowns[0].Type(), goodall.EventUnknown; got != want {
		t.Errorf("Type() = %q, want goodall's own unknown tag %q", got, want)
	}
}

func TestAnUnknownEventDoesNotDisturbTheMessage(t *testing.T) {
	c, _ := serveFixture(t, "unknown_events.sse", "text/event-stream")
	resp, err := c.Stream(t.Context(), simpleRequest()).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := goodall.Blocks{goodall.Text{Text: "Paris is the capital."}}
	if !reflect.DeepEqual(resp.Message.Content, want) {
		t.Errorf("content = %#v, want %#v", resp.Message.Content, want)
	}
}

func TestATranslationErrorIsTheStreamsFirstAndOnlyError(t *testing.T) {
	c, seen := serve(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request was sent although translation failed")
	})
	req := simpleRequest()
	req.Extensions = otherExtension{}
	events, err := drain(c.Stream(t.Context(), req))
	if err == nil {
		t.Fatal("the stream accepted another provider's extensions")
	}
	if len(events) != 0 {
		t.Errorf("got %d events before the translation error, want none", len(events))
	}
	if seen.count() != 0 {
		t.Errorf("requests = %d, want 0", seen.count())
	}
}

// otherExtension stands in for a request built for a different provider.
type otherExtension struct{}

func (otherExtension) Provider() string { return "openrouter" }

func TestANon2xxIsAnAPIErrorOnTheStream(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens is required"},"request_id":"req_bad"}`)
	})
	_, err := drain(c.Stream(t.Context(), simpleRequest()))
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("err = %#v, want a *goodall.APIError", err)
	}
	if apiErr.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", apiErr.Status)
	}
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("Kind = %v, want %v", apiErr.Kind, goodall.KindInvalidRequest)
	}
}

func TestRangingToTheEndClosesTheBody(t *testing.T) {
	c, body := streamClient(t, string(fixture(t, "thinking_tool_use.sse")))
	if _, err := drain(c.Stream(t.Context(), simpleRequest())); err != nil {
		t.Fatalf("the stream ended with %v", err)
	}
	if body.closeCount() == 0 {
		t.Error("the response body was left open after the stream ended")
	}
}

func TestBreakingOutEarlyClosesTheBody(t *testing.T) {
	c, body := streamClient(t, string(fixture(t, "thinking_tool_use.sse")))
	for ev, err := range c.Stream(t.Context(), simpleRequest()) {
		if err != nil {
			t.Fatalf("the stream ended with %v", err)
		}
		if _, ok := ev.(goodall.MessageStart); ok {
			break
		}
	}
	if body.closeCount() == 0 {
		t.Error("the response body was left open after the consumer broke out")
	}
}

func TestCancellingTheContextEndsTheStreamWithItsError(t *testing.T) {
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"m\",\"content\":[],\"usage\":{\"input_tokens\":1}}}\n\n")
		w.(http.Flusher).Flush()
		<-release
	})
	defer close(release)

	var seen int
	var streamErr error
	for _, err := range c.Stream(ctx, simpleRequest()) {
		if err != nil {
			streamErr = err
			break
		}
		seen++
		cancel()
	}
	if seen != 1 {
		t.Errorf("events before the cancellation = %d, want 1", seen)
	}
	if !errors.Is(streamErr, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", streamErr)
	}
}
