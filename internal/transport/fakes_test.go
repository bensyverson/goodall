package transport

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// reply is one scripted answer: either a transport-level failure (err) or a
// response assembled from status, header and body.
type reply struct {
	status int
	header http.Header
	body   string
	stream io.ReadCloser // used instead of body when set
	err    error
}

// recorded is what the scripted transport saw on one attempt.
type recorded struct {
	method        string
	url           string
	header        http.Header
	body          []byte
	contentLength int64
	at            time.Time
}

// scripted is an http.RoundTripper that answers attempts from a script,
// repeating the last entry once the script runs out, and records every
// request it was handed. It exists so a test can assert how many attempts
// were made and, under synctest, exactly when each one happened.
type scripted struct {
	mu       sync.Mutex
	steps    []reply
	requests []recorded
}

func (s *scripted) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		r.Body.Close()
	}
	s.mu.Lock()
	s.requests = append(s.requests, recorded{
		method:        r.Method,
		url:           r.URL.String(),
		header:        r.Header.Clone(),
		body:          body,
		contentLength: r.ContentLength,
		at:            time.Now(),
	})
	step := s.steps[min(len(s.requests)-1, len(s.steps)-1)]
	s.mu.Unlock()

	if step.err != nil {
		return nil, step.err
	}
	header := step.header
	if header == nil {
		header = http.Header{}
	}
	rc := step.stream
	if rc == nil {
		rc = io.NopCloser(strings.NewReader(step.body))
	}
	return &http.Response{
		Status:     http.StatusText(step.status),
		StatusCode: step.status,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     header,
		Body:       rc,
		Request:    r,
	}, nil
}

func (s *scripted) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *scripted) attempt(i int) recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[i]
}

// countingBody serves fixed bytes and counts Close calls. Close may arrive
// from the cancellation watcher while the caller is in Read, so every field
// is guarded.
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

// testClient builds a Client wired to a scripted transport, with the retry
// knobs left at their defaults unless a test overrides them.
func testClient(s *scripted) *Client {
	return &Client{HTTP: &http.Client{Transport: s}, Provider: "testprov"}
}

// req is the request every test sends unless it needs a different shape.
func req() Request {
	return Request{Method: http.MethodPost, URL: "https://example.test/v1/messages", Body: []byte(`{"hi":1}`)}
}
