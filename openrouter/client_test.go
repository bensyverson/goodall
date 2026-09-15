package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
)

// fixture reads one file from testdata.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return body
}

// serveStream writes body as an SSE response, flushing so a reader sees the
// frames before the handler returns.
func serveStream(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// testClient starts a server around handler and returns a client pointed at
// it, with retries off and the backoff collapsed so a failing test fails fast.
func testClient(t *testing.T, handler http.HandlerFunc, opts ...Option) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	all := append([]Option{WithAPIKey("test-key"), WithBaseURL(server.URL)}, opts...)
	c := New(all...)
	c.http.BaseDelay = time.Millisecond
	c.http.MaxDelay = time.Millisecond
	return c
}

// simpleRequest is the smallest request that translates.
func simpleRequest() *goodall.Request {
	return &goodall.Request{
		Model:    "anthropic/claude-haiku-4.5",
		Messages: []goodall.Message{{Role: goodall.RoleUser, Content: goodall.Blocks{goodall.Text{Text: "hi"}}}},
	}
}

// canonical renders a value as compact JSON, which is how two responses are
// compared: the raw JSON members they carry (a reasoning entry, a tool call's
// input) differ only in the whitespace the two paths happened to preserve.
func canonical(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling for comparison: %v", err)
	}
	value := jsontext.Value(body)
	if err := value.Compact(); err != nil {
		t.Fatalf("compacting for comparison: %v", err)
	}
	return string(value)
}

func TestNewWithoutKeySendsNoAuthorization(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		serveStream(w, fixture(t, "stream-reasoning-tools.txt"))
	}))
	t.Cleanup(server.Close)
	// A local server has no key, which is the case New's options shape is
	// chosen for: no key argument to pass an empty string to.
	c := New(WithBaseURL(server.URL))
	if _, err := c.Stream(t.Context(), simpleRequest()).Collect(); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v := got.Get("Authorization"); v != "" {
		t.Errorf("Authorization = %q, want it absent for a keyless client", v)
	}
	if v := got.Get("Content-Type"); v != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", v)
	}
}

func TestClientSendsKeyAndAttribution(t *testing.T) {
	var got http.Header
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		serveStream(w, fixture(t, "stream-reasoning-tools.txt"))
	}, WithAttribution(Attribution{Referer: "https://example.test", Title: "Example"}))
	if _, err := c.Stream(t.Context(), simpleRequest()).Collect(); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v := got.Get("Authorization"); v != "Bearer test-key" {
		t.Errorf("Authorization = %q", v)
	}
	if v := got.Get(HeaderReferer); v != "https://example.test" {
		t.Errorf("%s = %q", HeaderReferer, v)
	}
	if v := got.Get(HeaderTitle); v != "Example" {
		t.Errorf("%s = %q", HeaderTitle, v)
	}
}

func TestClientPostsToChatCompletions(t *testing.T) {
	var path string
	var body []byte
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "blocking-reasoning-tools.json"))
	})
	if _, err := c.Complete(t.Context(), simpleRequest()); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.HasSuffix(path, "/chat/completions") {
		t.Errorf("path = %q, want it to end in /chat/completions", path)
	}
	if !strings.Contains(string(body), `"stream":false`) {
		t.Errorf("blocking body did not set stream false: %s", body)
	}
}

func TestGenericDialectSendsNoOpenRouterMembers(t *testing.T) {
	var body []byte
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Write(fixture(t, "blocking-reasoning-tools.json"))
	}, WithDialect(Generic))
	req := simpleRequest()
	req.Thinking = goodall.ThinkingConfig{Effort: goodall.EffortHigh}
	req.Cache = goodall.CacheAuto
	req.Metadata = map[string]string{"user_id": "u1"}
	if _, err := c.Complete(t.Context(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for _, member := range []string{"reasoning", "reasoning_effort", "cache_control", "provider", "plugins", "session_id", "debug", "metadata", "user"} {
		if strings.Contains(string(body), `"`+member+`":`) {
			t.Errorf("the Generic dialect sent %q: %s", member, body)
		}
	}
}

func TestCompleteErrorOnlyBodyIsAPIError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"error":{"code":429,"message":"Rate limit exceeded","metadata":{"error_type":"rate_limit_exceeded"}}}`))
	})
	_, err := c.Complete(t.Context(), simpleRequest())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("Complete error = %v (%T), want *goodall.APIError", err, err)
	}
	if apiErr.Kind != goodall.KindRateLimited {
		t.Errorf("Kind = %v, want rate_limited", apiErr.Kind)
	}
	if apiErr.Status != 429 {
		t.Errorf("Status = %d, want 429 from the body's code", apiErr.Status)
	}
	if apiErr.Provider != ProviderName {
		t.Errorf("Provider = %q", apiErr.Provider)
	}
}

func TestCompleteChoiceErrorIsAPIError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"gen-1","choices":[{"index":0,"finish_reason":"error","error":{"code":"provider_error","message":"upstream fell over","metadata":{"error_type":"provider_unavailable"}}}]}`))
	})
	_, err := c.Complete(t.Context(), simpleRequest())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("Complete error = %v (%T), want *goodall.APIError", err, err)
	}
	if apiErr.Kind != goodall.KindServer {
		t.Errorf("Kind = %v, want server", apiErr.Kind)
	}
}

func TestRetriesRateLimitThenSucceeds(t *testing.T) {
	var attempts int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.Header().Set("Retry-After", "0")
			w.Header().Set("X-Generation-Id", "gen-throttled")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"code":429,"message":"slow down","metadata":{"error_type":"rate_limit_exceeded"}}}`))
			return
		}
		w.Write(fixture(t, "blocking-reasoning-tools.json"))
	}, WithMaxRetries(2))
	if _, err := c.Complete(t.Context(), simpleRequest()); err != nil {
		t.Fatalf("Complete after retries: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestRateLimitSurfacesWithTheRetryAfterItAskedFor(t *testing.T) {
	// Retries are off so the test spends no time waiting: what it checks is
	// that the wait the provider asked for reaches the caller.
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.Header().Set("X-Generation-Id", "gen-throttled")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"code":429,"message":"slow down","metadata":{"error_type":"rate_limit_exceeded"}}}`))
	}, WithMaxRetries(-1))
	_, err := c.Complete(t.Context(), simpleRequest())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("Complete error = %v (%T), want *goodall.APIError", err, err)
	}
	if apiErr.Kind != goodall.KindRateLimited {
		t.Errorf("Kind = %v, want rate_limited", apiErr.Kind)
	}
	if !apiErr.Retryable() {
		t.Error("a rate limit reported itself as not retryable")
	}
	if apiErr.RetryAfter != 2*time.Second {
		t.Errorf("RetryAfter = %v, want 2s", apiErr.RetryAfter)
	}
	if apiErr.RequestID != "gen-throttled" {
		t.Errorf("RequestID = %q, want the X-Generation-Id header", apiErr.RequestID)
	}
}

func TestCompleteRejectsAnotherProvidersExtensions(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request should not have been sent")
	})
	req := simpleRequest()
	req.Extensions = foreignExtension{}
	if _, err := c.Complete(t.Context(), req); err == nil {
		t.Fatal("Complete accepted another provider's extensions")
	}
}

func TestCompleteBareErrorFinishReasonIsAPIError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"gen-1","model":"m","choices":[{"index":0,"finish_reason":"error","message":{"role":"assistant","content":"partial"}}]}`))
	})
	_, err := c.Complete(t.Context(), simpleRequest())
	if _, ok := errors.AsType[*goodall.APIError](err); !ok {
		t.Fatalf("error = %v (%T), want *goodall.APIError", err, err)
	}
}
