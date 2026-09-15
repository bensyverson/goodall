package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
)

// TestDoSuccessNeverRetriedOnBrokenStream is the "no retry once a body byte
// has been handed over" guarantee against a real connection: the server
// aborts mid-body, and the failure surfaces to the reader rather than
// triggering a second request.
func TestDoSuccessNeverRetriedOnBrokenStream(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: message_start\n"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // drop the connection mid-body
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), Provider: "testprov", BaseDelay: time.Millisecond}
	resp, err := c.Do(t.Context(), Request{Method: http.MethodPost, URL: srv.URL, Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Error("ReadAll: want an error from the aborted stream")
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Errorf("server saw %d requests, want exactly 1", hits)
	}
}

// TestDoCancelMidBodyOverRealConnection proves the promise end to end: a
// cancel while the caller is reading surfaces context.Canceled and tears the
// connection down, which the handler observes on its own request context.
func TestDoCancelMidBodyOverRealConnection(t *testing.T) {
	handlerDone := make(chan error, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("event: ping\n\n"))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			handlerDone <- r.Context().Err()
		case <-release:
			handlerDone <- errors.New("handler released without a cancelled request context")
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := &Client{HTTP: srv.Client(), Provider: "testprov"}
	resp, err := c.Do(ctx, Request{Method: http.MethodPost, URL: srv.URL, Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("first read: %v", err)
	}
	cancel()
	if _, err := resp.Body.Read(buf); !errors.Is(err, context.Canceled) {
		t.Errorf("read after cancel: err = %v, want context.Canceled", err)
	}
	select {
	case err := <-handlerDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("handler saw %v, want the request context cancelled", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("handler never saw its request context cancelled")
	}
}

// TestDoRetriesOverRealConnection checks the retry loop over a real
// connection too: a 529 with a Retry-After the server actually sends is
// retried and the second attempt succeeds, so the synctest timings above are
// measuring the same code path a provider will use.
func TestDoRetriesOverRealConnection(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Request-Id", "req_real")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(529)
			io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error"}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "done")
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), Provider: "testprov", MaxRetryAfter: 2 * time.Second}
	begin := time.Now()
	resp, err := c.Do(t.Context(), Request{Method: http.MethodPost, URL: srv.URL, Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != "done" {
		t.Errorf("body = %q, want the second attempt's", body)
	}
	// The lower bound is the assertion; the upper bound is left loose
	// because a loaded suite inflates wall clock several-fold.
	if d := time.Since(begin); d < time.Second {
		t.Errorf("retry took %v, want at least the 1s the server asked for", d)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 2 {
		t.Errorf("server saw %d requests, want 2", hits)
	}
}

// TestDoRealConnectionRefused is the pre-response transport failure over a
// real dialler: nothing is listening, every attempt fails the same way, and
// the error comes back as itself rather than as an *APIError.
func TestDoRealConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // the port is now closed; dialling it fails

	c := &Client{HTTP: &http.Client{}, Provider: "testprov", MaxRetries: 1, BaseDelay: time.Millisecond}
	_, err := c.Do(t.Context(), Request{Method: http.MethodPost, URL: url, Body: []byte(`{}`)})
	if err == nil {
		t.Fatal("Do: want a dial error")
	}
	if _, ok := errors.AsType[*goodall.APIError](err); ok {
		t.Errorf("err = %v, want a plain transport error", err)
	}
}
