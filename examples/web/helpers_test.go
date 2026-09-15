package main

import (
	"bufio"
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/internal/fake"
)

// readTimeout bounds every blocking read in these tests, so a handler that
// never answers fails with a message rather than hanging until the package
// times out.
const readTimeout = 20 * time.Second

// serve is the example's handler over a scripted provider, on a real HTTP
// server: the SSE route is only meaningful over a connection the test can
// read while the run is still producing, which a ResponseRecorder cannot do.
//
// The service is shut down before the server is closed, so the runs end and
// release their handlers before the server waits for them.
func serve(t *testing.T, agent *goodall.Agent) (*httptest.Server, *chat.Service) {
	t.Helper()
	svc := chat.NewService(agent, chat.NewMemoryStore())
	srv := httptest.NewServer(newHandler(svc))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
		defer cancel()
		if err := svc.Shutdown(ctx); err != nil {
			t.Errorf("shutting the service down at the end of the test: %v", err)
		}
		srv.Close()
	})
	return srv, svc
}

// agentFor is an agent over a scripted provider, with the given tools.
func agentFor(script []fake.Turn, tools ...goodall.Tool) (*goodall.Agent, *fake.Provider) {
	p := &fake.Provider{Script: script}
	return &goodall.Agent{Provider: p, Model: "test-model", Tools: tools}, p
}

// client is an HTTP client that gives up rather than hanging. Redirects are
// left alone; nothing here issues one.
func client() *http.Client { return &http.Client{Timeout: readTimeout} }

// do sends one request and reads the whole body, which is every route except
// the event stream.
func do(t *testing.T, method, url, body string) (*http.Response, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, reader)
	if err != nil {
		t.Fatalf("building a %s %s: %v", method, url, err)
	}
	resp, err := client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body of %s %s: %v", method, url, err)
	}
	return resp, string(out)
}

// createThread is a thread created through the route the page uses, and the
// view the route answered.
func createThread(t *testing.T, srv *httptest.Server) chat.ThreadView {
	t.Helper()
	resp, body := do(t, http.MethodPost, srv.URL+"/threads", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /threads answered %d, want %d: %s", resp.StatusCode, http.StatusCreated, body)
	}
	return decodeView(t, body)
}

// decodeView parses a ThreadView from a response body.
func decodeView(t *testing.T, body string) chat.ThreadView {
	t.Helper()
	var view chat.ThreadView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("decoding a thread view from %q: %v", body, err)
	}
	return view
}

// sendText posts one line to a thread and returns the response and its body,
// so a test can assert on a refusal as well as on a run id.
func sendText(t *testing.T, srv *httptest.Server, threadID, text string) (*http.Response, string) {
	t.Helper()
	payload, err := json.Marshal(struct {
		Text string `json:"text"`
	}{text})
	if err != nil {
		t.Fatal(err)
	}
	return do(t, http.MethodPost, srv.URL+"/threads/"+threadID+"/send", string(payload))
}

// sseFrame is one Server-Sent Event as a browser's EventSource sees it: the
// name the frame was written under and its data, rejoined.
type sseFrame struct {
	Name string
	Data string
}

// frameBody is a frame's JSON decoded far enough to assert on. The members are
// the ones this example's page reads; everything else in an event is ignored,
// as it is by the page.
type frameBody struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Index   int    `json:"index"`
	ToolUse struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"tool_use"`
}

// body decodes the frame's data.
func (f sseFrame) body(t *testing.T) frameBody {
	t.Helper()
	var out frameBody
	if err := json.Unmarshal([]byte(f.Data), &out); err != nil {
		t.Fatalf("decoding a %q frame from %q: %v", f.Name, f.Data, err)
	}
	return out
}

// eventStream is an open SSE connection, read one frame at a time so a test
// can assert on what has already arrived while the run is still producing.
type eventStream struct {
	t    *testing.T
	resp *http.Response
	br   *bufio.Reader
}

// openEvents opens the events route for a thread. The response headers have
// arrived by the time it returns, so its Content-Type can be asserted on
// before a single frame exists.
func openEvents(t *testing.T, srv *httptest.Server, threadID string) *eventStream {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/threads/"+threadID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client().Do(req)
	if err != nil {
		t.Fatalf("opening the event stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &eventStream{t: t, resp: resp, br: bufio.NewReader(resp.Body)}
}

// next is the next frame, or false once the stream has ended. A stream ends
// when the run does, which is what closes the page's EventSource.
func (s *eventStream) next() (sseFrame, bool) {
	s.t.Helper()
	var frame sseFrame
	var data []string
	for {
		line, err := s.br.ReadString('\n')
		if line == "" && err != nil {
			if err != io.EOF {
				s.t.Fatalf("reading the event stream: %v", err)
			}
			return sseFrame{}, false
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case line == "":
			if frame.Name == "" && len(data) == 0 {
				continue // a keep-alive or a stray blank line
			}
			frame.Data = strings.Join(data, "\n")
			return frame, true
		case strings.HasPrefix(line, "event: "):
			frame.Name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = append(data, strings.TrimPrefix(line, "data: "))
		}
	}
}

// until reads frames until one is named kind, and returns everything read,
// including it. It fails rather than blocking for ever if the stream ends
// first.
func (s *eventStream) until(kind string) []sseFrame {
	s.t.Helper()
	var seen []sseFrame
	for {
		frame, ok := s.next()
		if !ok {
			s.t.Fatalf("the event stream ended before a %q frame, after %s", kind, names(seen))
		}
		seen = append(seen, frame)
		if frame.Name == kind {
			return seen
		}
	}
}

// rest reads the stream to its end.
func (s *eventStream) rest() []sseFrame {
	s.t.Helper()
	var seen []sseFrame
	for {
		frame, ok := s.next()
		if !ok {
			return seen
		}
		seen = append(seen, frame)
	}
}

// names lists the frames by name, which is what a failure needs to read.
func names(frames []sseFrame) string {
	out := make([]string, len(frames))
	for i, f := range frames {
		out[i] = f.Name
	}
	return "[" + strings.Join(out, " ") + "]"
}

// gateTool is a tool that parks until the returned func is called, which is
// how these tests hold a run open at a known point without a clock: started
// is closed when the run reaches the tool, and release lets it go.
func gateTool(t *testing.T) (tool goodall.Tool, started <-chan struct{}, release func()) {
	t.Helper()
	gate := make(chan struct{})
	reached := make(chan struct{})
	var open, arrive sync.Once
	tool, err := goodall.NewTool("gate", "Wait until the test lets go.", func(ctx context.Context, in struct {
		Note string `json:"note" desc:"ignored"`
	}) (goodall.ToolResult, error) {
		arrive.Do(func() { close(reached) })
		select {
		case <-gate:
			return goodall.TextResult("released"), nil
		case <-ctx.Done():
			return goodall.ToolResult{}, ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	release = func() { open.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return tool, reached, release
}

// gatedScript is a first turn that says something and then calls the gate
// tool, and a second that answers once the gate is released.
func gatedScript(first, answer string) []fake.Turn {
	return []fake.Turn{
		fake.Answer(goodall.StopToolUse, goodall.Text{Text: first}, fake.Use("tu_1", "gate", `{"note":"x"}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: answer}),
	}
}

// waitFor blocks until c is closed, failing the test rather than the package
// if it never is.
func waitFor(t *testing.T, c <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(readTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}
