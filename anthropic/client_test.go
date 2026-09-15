package anthropic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bensyverson/goodall"
)

// capture records the requests a test server was sent, so a test can assert
// on headers, paths and bodies after the call has finished.
type capture struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
}

// record stores one request and its body. It runs before the handler, so the
// body is still intact.
func (c *capture) record(r *http.Request) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, r.Clone(context.Background()))
	c.bodies = append(c.bodies, body)
}

// count is how many requests arrived.
func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

// last is the most recent request and its body.
func (c *capture) last(t *testing.T) (*http.Request, []byte) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("no request reached the server")
	}
	return c.requests[len(c.requests)-1], c.bodies[len(c.bodies)-1]
}

// fixture reads one testdata file.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return data
}

// serveFixture answers every request with the named fixture and returns a
// client pointed at the server together with what the server saw.
func serveFixture(t *testing.T, name, contentType string, opts ...Option) (*Client, *capture) {
	t.Helper()
	body := fixture(t, name)
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}, opts...)
}

// serve starts a test server running h and returns a client pointed at it.
func serve(t *testing.T, h http.HandlerFunc, opts ...Option) (*Client, *capture) {
	t.Helper()
	seen := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	opts = append([]Option{WithBaseURL(srv.URL)}, opts...)
	return New("sk-ant-test", opts...), seen
}

// simpleRequest is the smallest request that translates.
func simpleRequest() *goodall.Request {
	return &goodall.Request{
		Model:    "claude-opus-4-5-20251101",
		Messages: []goodall.Message{{Role: goodall.RoleUser, Content: goodall.Blocks{goodall.Text{Text: "hi"}}}},
	}
}

func TestClientSatisfiesTheProviderSeams(t *testing.T) {
	var c any = New("sk-ant-test")
	if _, ok := c.(goodall.Provider); !ok {
		t.Error("*Client does not satisfy goodall.Provider")
	}
	if _, ok := c.(goodall.Completer); !ok {
		t.Error("*Client does not satisfy goodall.Completer")
	}
	if _, ok := c.(goodall.ModelLister); !ok {
		t.Error("*Client does not satisfy goodall.ModelLister")
	}
}

func TestDefaultBaseURLIsAnthropic(t *testing.T) {
	c := New("sk-ant-test")
	if got := c.endpoint(messagesPath); got != "https://api.anthropic.com/v1/messages" {
		t.Errorf("endpoint = %q, want the documented Messages URL", got)
	}
}

func TestBaseURLTrailingSlashDoesNotDoubleUp(t *testing.T) {
	c := New("sk-ant-test", WithBaseURL("https://proxy.test/anthropic/"))
	if got := c.endpoint(messagesPath); got != "https://proxy.test/anthropic/v1/messages" {
		t.Errorf("endpoint = %q, want one slash between the base and the path", got)
	}
}

func TestCompleteSendsTheDocumentedHeaders(t *testing.T) {
	c, seen := serveFixture(t, "thinking_tool_use.json", "application/json")
	if _, err := c.Complete(t.Context(), simpleRequest()); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	req, _ := seen.last(t)
	for header, want := range map[string]string{
		"X-Api-Key":         "sk-ant-test",
		"Anthropic-Version": apiVersion,
		"Content-Type":      "application/json",
	} {
		if got := req.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if got := req.Header.Get("Anthropic-Beta"); got != "" {
		t.Errorf("Anthropic-Beta = %q, want none when no beta was asked for", got)
	}
	if req.URL.Path != messagesPath {
		t.Errorf("path = %q, want %q", req.URL.Path, messagesPath)
	}
}

func TestBetasMergeClientAndRequestInOrderWithoutDuplicates(t *testing.T) {
	c, seen := serveFixture(t, "thinking_tool_use.json", "application/json",
		WithBetas(BetaThinkingBindingControls, BetaStructuredOutputs))
	req := simpleRequest()
	req.Extensions = Extensions{Betas: []Beta{BetaStructuredOutputs, "context-management-2025-06-27"}}
	if _, err := c.Complete(t.Context(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	sent, _ := seen.last(t)
	want := string(BetaThinkingBindingControls) + "," + string(BetaStructuredOutputs) + ",context-management-2025-06-27"
	if got := sent.Header.Get("Anthropic-Beta"); got != want {
		t.Errorf("Anthropic-Beta = %q, want %q", got, want)
	}
}

func TestStreamSetsStreamTrueAndCompleteLeavesItOff(t *testing.T) {
	c, seen := serveFixture(t, "thinking_tool_use.sse", "text/event-stream")
	for range c.Stream(t.Context(), simpleRequest()) {
	}
	_, body := seen.last(t)
	if !strings.Contains(string(body), `"stream":true`) {
		t.Errorf("streaming body = %s, want stream true", body)
	}

	blocking, seenBlocking := serveFixture(t, "thinking_tool_use.json", "application/json")
	if _, err := blocking.Complete(t.Context(), simpleRequest()); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	_, body = seenBlocking.last(t)
	if strings.Contains(string(body), `"stream"`) {
		t.Errorf("blocking body = %s, want no stream member", body)
	}
}

func TestWithHTTPClientIsUsed(t *testing.T) {
	used := false
	c := New("sk-ant-test", WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return cannedResponse(http.StatusOK, string(fixture(t, "thinking_tool_use.json"))), nil
	})}))
	if _, err := c.Complete(t.Context(), simpleRequest()); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !used {
		t.Error("the injected http.Client was not used")
	}
}

func TestATranslationErrorNeverReachesTheNetwork(t *testing.T) {
	c, seen := serve(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request was sent although translation failed")
	})
	req := simpleRequest()
	req.Metadata = map[string]string{"tenant": "acme"}
	if _, err := c.Complete(t.Context(), req); err == nil {
		t.Fatal("Complete accepted metadata Anthropic does not define")
	}
	if seen.count() != 0 {
		t.Errorf("requests = %d, want 0", seen.count())
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// cannedResponse builds a response over a body that counts its closes.
func cannedResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       newCountingBody(body),
	}
}
