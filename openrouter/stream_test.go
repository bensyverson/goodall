package openrouter

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bensyverson/goodall"
)

// collectEvents drains a stream and returns its events and its terminal error.
func collectEvents(s goodall.Stream) ([]goodall.Event, error) {
	var out []goodall.Event
	for ev, err := range s {
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
	return out, nil
}

// countEvents counts the events of one type.
func countEvents(events []goodall.Event, t goodall.EventType) int {
	var n int
	for _, ev := range events {
		if ev.Type() == t {
			n++
		}
	}
	return n
}

func TestCollectOverStreamEqualsComplete(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"stream":true`) {
			serveStream(w, fixture(t, "stream-reasoning-tools.txt"))
			return
		}
		w.Write(fixture(t, "blocking-reasoning-tools.json"))
	})

	streamed, err := c.Stream(t.Context(), simpleRequest()).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	blocking, err := c.Complete(t.Context(), simpleRequest())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got, want := canonical(t, streamed), canonical(t, blocking); got != want {
		t.Errorf("the two paths disagree.\nstream:   %s\nblocking: %s", got, want)
	}
	if streamed.StopReason != goodall.StopToolUse {
		t.Errorf("StopReason = %q, want tool_use", streamed.StopReason)
	}
	if streamed.NativeStopReason != "tool_use" {
		t.Errorf("NativeStopReason = %q", streamed.NativeStopReason)
	}
	if !streamed.Cost.Reported || streamed.Cost.Amount.String() != "0.00142461" {
		t.Errorf("Cost = %+v, want the reported 0.00142461", streamed.Cost)
	}
	if streamed.Usage.Reasoning != 75 || streamed.Usage.CacheRead != 24 {
		t.Errorf("Usage = %+v", streamed.Usage)
	}

	// The reasoning round trip: the merged entry on the stream path is the
	// entry the blocking path received.
	think, ok := streamed.Message.Content[0].(goodall.Thinking)
	if !ok {
		t.Fatalf("first block is %T, want a Thinking", streamed.Message.Content[0])
	}
	if think.Text != "Two cities. Call the tool twice." {
		t.Errorf("thinking text = %q", think.Text)
	}
	if think.Signature != "sig-fixture-abc" {
		t.Errorf("thinking signature = %q", think.Signature)
	}
	wantRaw := `{"type":"reasoning.text","text":"Two cities. Call the tool twice.","format":"anthropic-claude-v1","index":0,"signature":"sig-fixture-abc"}`
	if got := compact(t, think.Raw); got != wantRaw {
		t.Errorf("thinking raw =\n%s\nwant\n%s", got, wantRaw)
	}
}

// compact renders a raw JSON value with its whitespace removed.
func compact(t *testing.T, v jsontext.Value) string {
	t.Helper()
	clone := jsontext.Value(v.Clone())
	if err := clone.Compact(); err != nil {
		t.Fatalf("compacting: %v", err)
	}
	return string(clone)
}

func TestRepeatedFinishReasonCollectsToOneTerminal(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, fixture(t, "stream-reasoning-tools.txt"))
	})
	events, err := collectEvents(c.Stream(t.Context(), simpleRequest()))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if n := countEvents(events, goodall.EventMessageStop); n != 1 {
		t.Errorf("message_stop count = %d, want exactly 1", n)
	}
	if n := countEvents(events, goodall.EventMessageStart); n != 1 {
		t.Errorf("message_start count = %d, want exactly 1", n)
	}
	// The stop reason arrives once and the usage frame adds accounting.
	var stops, withUsage int
	for _, ev := range events {
		md, ok := ev.(goodall.MessageDelta)
		if !ok {
			continue
		}
		if md.StopReason != goodall.StopNone {
			stops++
		}
		if md.Usage != (goodall.Usage{}) {
			withUsage++
		}
	}
	if stops != 1 {
		t.Errorf("message_delta events carrying a stop reason = %d, want 1", stops)
	}
	if withUsage != 1 {
		t.Errorf("message_delta events carrying usage = %d, want 1", withUsage)
	}
}

func TestStreamAssignsBlockIndexesInOrder(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, fixture(t, "stream-reasoning-tools.txt"))
	})
	events, err := collectEvents(c.Stream(t.Context(), simpleRequest()))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var starts []int
	for _, ev := range events {
		if start, ok := ev.(goodall.BlockStart); ok {
			starts = append(starts, start.Index)
		}
	}
	want := []int{0, 1, 2, 3}
	if len(starts) != len(want) {
		t.Fatalf("block_start indexes = %v, want %v", starts, want)
	}
	for i := range want {
		if starts[i] != want[i] {
			t.Fatalf("block_start indexes = %v, want %v", starts, want)
		}
	}
}

func TestStreamToolCallsKeyedByIndex(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, fixture(t, "stream-reasoning-tools.txt"))
	})
	resp, err := c.Stream(t.Context(), simpleRequest()).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var calls []goodall.ToolUse
	for _, block := range resp.Message.Content {
		if use, ok := block.(goodall.ToolUse); ok {
			calls = append(calls, use)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(calls))
	}
	if calls[0].ID != "toolu_fixture_paris" || compact(t, calls[0].Input) != `{"city":"Paris"}` {
		t.Errorf("first call = %+v", calls[0])
	}
	if calls[1].ID != "toolu_fixture_tokyo" || compact(t, calls[1].Input) != `{"city":"Tokyo"}` {
		t.Errorf("second call = %+v", calls[1])
	}
	if calls[0].Name != "get_weather" || calls[1].Name != "get_weather" {
		t.Errorf("tool names = %q, %q", calls[0].Name, calls[1].Name)
	}
}

func TestStreamSkipsCommentKeepAlives(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, fixture(t, "stream-reasoning-tools.txt"))
	})
	events, err := collectEvents(c.Stream(t.Context(), simpleRequest()))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if n := countEvents(events, goodall.EventUnknown); n != 0 {
		t.Errorf("unknown events = %d, want 0: the keep-alives should be skipped", n)
	}
}

func TestStreamSurfacesUnknownChunkShape(t *testing.T) {
	const body = "data: {\"debug\":{\"upstream_body\":{\"model\":\"x\"}}}\n\n" +
		"data: {\"id\":\"gen-1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, []byte(body))
	})
	events, err := collectEvents(c.Stream(t.Context(), simpleRequest()))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var unknown *goodall.UnknownEvent
	for _, ev := range events {
		if u, ok := ev.(goodall.UnknownEvent); ok {
			unknown = &u
		}
	}
	if unknown == nil {
		t.Fatal("the unrecognised frame was dropped, want an UnknownEvent")
	}
	if unknown.EventType == "" {
		t.Error("UnknownEvent.EventType is empty, want a descriptive tag")
	}
	if len(unknown.Raw) == 0 {
		t.Error("UnknownEvent.Raw is empty, want the chunk's bytes")
	}
}

func TestStreamErrorOnlyBodyIsAPIError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, fixture(t, "stream-error-only.txt"))
	})
	_, err := c.Stream(t.Context(), simpleRequest()).Collect()
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("stream error = %v (%T), want *goodall.APIError, never a ProtocolError", err, err)
	}
	if apiErr.Kind != goodall.KindRateLimited {
		t.Errorf("Kind = %v, want rate_limited", apiErr.Kind)
	}
	if apiErr.Status != 429 {
		t.Errorf("Status = %d, want 429 from the frame's code", apiErr.Status)
	}
	if apiErr.Type != "rate_limit_exceeded" {
		t.Errorf("Type = %q", apiErr.Type)
	}
}

func TestStreamRefusalBecomesTextAndStopRefusal(t *testing.T) {
	const body = "data: {\"id\":\"gen-1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"refusal\":\"I can't help with that.\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"gen-1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"content_filter\"}]}\n\n" +
		"data: [DONE]\n\n"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, []byte(body))
	})
	resp, err := c.Stream(t.Context(), simpleRequest()).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if resp.StopReason != goodall.StopRefusal {
		t.Errorf("StopReason = %q, want refusal", resp.StopReason)
	}
	if len(resp.Message.Content) != 1 {
		t.Fatalf("blocks = %d, want 1", len(resp.Message.Content))
	}
	text, ok := resp.Message.Content[0].(goodall.Text)
	if !ok || text.Text != "I can't help with that." {
		t.Errorf("block = %#v, want the refusal as text", resp.Message.Content[0])
	}
}

func TestStreamWithoutDoneIsProtocolError(t *testing.T) {
	const body = "data: {\"id\":\"gen-1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, []byte(body))
	})
	resp, err := c.Stream(t.Context(), simpleRequest()).Collect()
	if _, ok := errors.AsType[*goodall.ProtocolError](err); !ok {
		t.Fatalf("error = %v (%T), want a *goodall.ProtocolError", err, err)
	}
	if !resp.Message.Partial {
		t.Error("the cut-off message is not marked partial")
	}
}

func TestStreamTranslationErrorIsTheOnlyYield(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request should not have been sent")
	})
	req := simpleRequest()
	req.Extensions = foreignExtension{}
	var yields int
	var lastErr error
	for _, err := range c.Stream(t.Context(), req) {
		yields++
		lastErr = err
	}
	if yields != 1 {
		t.Errorf("yields = %d, want exactly 1", yields)
	}
	if lastErr == nil {
		t.Error("the translation error was not yielded")
	}
}

func TestStreamCancellationEndsWithContextError(t *testing.T) {
	release := make(chan struct{})
	var closed atomic.Bool
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"id\":\"gen-1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })
	c.http.HTTP = &http.Client{Transport: &closeTrackingTransport{closed: &closed, base: http.DefaultTransport}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var err error
	var seen int
	for _, e := range c.Stream(ctx, simpleRequest()) {
		if e != nil {
			err = e
			break
		}
		seen++
		if seen == 2 { // message_start then the text block_start
			cancel()
		}
	}
	if err == nil {
		t.Fatal("the cancelled stream ended without an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if !closed.Load() {
		t.Error("the cancelled stream left the response body open")
	}
}

func TestStreamEarlyBreakClosesTheBody(t *testing.T) {
	var closed atomic.Bool
	tracker := &closeTrackingTransport{closed: &closed, base: http.DefaultTransport}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, fixture(t, "stream-reasoning-tools.txt"))
	}))
	t.Cleanup(server.Close)
	c := New(WithAPIKey("k"), WithBaseURL(server.URL), WithHTTPClient(&http.Client{Transport: tracker}))

	for range c.Stream(t.Context(), simpleRequest()) {
		break
	}
	if !closed.Load() {
		t.Error("breaking out of the stream left the response body open")
	}
}

// closeTrackingTransport records whether the response body was closed.
type closeTrackingTransport struct {
	closed *atomic.Bool
	base   http.RoundTripper
}

func (t *closeTrackingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = &trackedBody{ReadCloser: resp.Body, closed: t.closed}
	return resp, nil
}

type trackedBody struct {
	io.ReadCloser
	closed *atomic.Bool
}

func (b *trackedBody) Close() error {
	b.closed.Store(true)
	return b.ReadCloser.Close()
}

func TestStreamBareErrorFinishReasonIsAPIError(t *testing.T) {
	const body = "data: {\"id\":\"gen-1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"error\",\"native_finish_reason\":\"error\"}]}\n\n" +
		"data: [DONE]\n\n"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		serveStream(w, []byte(body))
	})
	_, err := c.Stream(t.Context(), simpleRequest()).Collect()
	if _, ok := errors.AsType[*goodall.APIError](err); !ok {
		t.Fatalf("error = %v (%T), want *goodall.APIError: a turn that finished in an error did not succeed", err, err)
	}
}
