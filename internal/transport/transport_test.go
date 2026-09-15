package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
)

func TestKindForStatus(t *testing.T) {
	cases := map[int]goodall.ErrorKind{
		400: goodall.KindInvalidRequest,
		413: goodall.KindInvalidRequest,
		422: goodall.KindInvalidRequest,
		401: goodall.KindUnauthorized,
		402: goodall.KindUnauthorized,
		403: goodall.KindUnauthorized,
		404: goodall.KindNotFound,
		429: goodall.KindRateLimited,
		503: goodall.KindOverloaded,
		529: goodall.KindOverloaded,
		500: goodall.KindServer,
		502: goodall.KindServer,
		504: goodall.KindServer,
		599: goodall.KindServer,
		405: goodall.KindUnknown,
		418: goodall.KindUnknown,
		304: goodall.KindUnknown,
		200: goodall.KindUnknown,
	}
	for status, want := range cases {
		if got := KindForStatus(status); got != want {
			t.Errorf("KindForStatus(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestKindForStatusRetryability(t *testing.T) {
	for _, status := range []int{429, 500, 502, 503, 529} {
		if !KindForStatus(status).Retryable() {
			t.Errorf("KindForStatus(%d) = %v, want a retryable kind", status, KindForStatus(status))
		}
	}
	for _, status := range []int{400, 401, 403, 404, 413, 422} {
		if KindForStatus(status).Retryable() {
			t.Errorf("KindForStatus(%d) = %v, want a terminal kind", status, KindForStatus(status))
		}
	}
}

func TestDoReturnsSuccessBodyOpen(t *testing.T) {
	body := newCountingBody("event: ping\n\n")
	s := &scripted{steps: []reply{{status: 200, stream: body}}}
	resp, err := testClient(s).Do(t.Context(), req())
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "event: ping\n\n" {
		t.Errorf("body = %q", got)
	}
	if s.count() != 1 {
		t.Errorf("attempts = %d, want 1", s.count())
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if n := body.closeCount(); n != 1 {
		t.Errorf("underlying closes = %d, want 1 (Close must be idempotent)", n)
	}
}

func TestDoBuildsRequest(t *testing.T) {
	s := &scripted{steps: []reply{{status: 500, body: "boom"}, {status: 200, body: "ok"}}}
	c := testClient(s)
	c.BaseDelay = time.Nanosecond
	r := req()
	r.Header = http.Header{"X-Api-Key": []string{"secret"}}
	resp, err := c.Do(t.Context(), r)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if s.count() != 2 {
		t.Fatalf("attempts = %d, want 2", s.count())
	}
	for i := range 2 {
		got := s.attempt(i)
		if got.method != http.MethodPost {
			t.Errorf("attempt %d method = %q", i, got.method)
		}
		if string(got.body) != `{"hi":1}` {
			t.Errorf("attempt %d body = %q, want the request body resent verbatim", i, got.body)
		}
		if got.contentLength != int64(len(r.Body)) {
			t.Errorf("attempt %d ContentLength = %d, want %d", i, got.contentLength, len(r.Body))
		}
		if ct := got.header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("attempt %d Content-Type = %q, want application/json", i, ct)
		}
		if k := got.header.Get("X-Api-Key"); k != "secret" {
			t.Errorf("attempt %d lost the caller's header: X-Api-Key = %q", i, k)
		}
	}
}

func TestDoKeepsCallerContentType(t *testing.T) {
	s := &scripted{steps: []reply{{status: 200, body: "ok"}}}
	r := req()
	r.Header = http.Header{"Content-Type": []string{"application/x-ndjson"}}
	resp, err := testClient(s).Do(t.Context(), r)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if ct := s.attempt(0).header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want the caller's own", ct)
	}
}

func TestDoNoBodyNoContentType(t *testing.T) {
	s := &scripted{steps: []reply{{status: 200, body: "ok"}}}
	resp, err := testClient(s).Do(t.Context(), Request{Method: http.MethodGet, URL: "https://example.test/v1/models"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if ct := s.attempt(0).header.Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type = %q, want none for a bodyless request", ct)
	}
}

func TestDoDecoderNilFallsBackToStatus(t *testing.T) {
	s := &scripted{steps: []reply{{status: 400, body: `{"error":{"message":"bad tool"}}`}}}
	c := testClient(s)
	c.Decode = func(int, http.Header, []byte) *goodall.APIError { return nil }
	_, err := c.Do(t.Context(), req())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want *goodall.APIError", err, err)
	}
	if apiErr.Kind != goodall.KindInvalidRequest {
		t.Errorf("Kind = %v, want %v", apiErr.Kind, goodall.KindInvalidRequest)
	}
	if apiErr.Status != 400 {
		t.Errorf("Status = %d, want 400", apiErr.Status)
	}
	if apiErr.Provider != "testprov" {
		t.Errorf("Provider = %q, want testprov", apiErr.Provider)
	}
	if apiErr.Message != `{"error":{"message":"bad tool"}}` {
		t.Errorf("Message = %q, want the body text", apiErr.Message)
	}
	if string(apiErr.Raw) != `{"error":{"message":"bad tool"}}` {
		t.Errorf("Raw = %q, want the JSON body", apiErr.Raw)
	}
	if s.count() != 1 {
		t.Errorf("attempts = %d, want 1: an invalid request is never retried", s.count())
	}
}

func TestDoFallbackNonJSONBody(t *testing.T) {
	s := &scripted{steps: []reply{{status: 404, body: "  not found  "}}}
	_, err := testClient(s).Do(t.Context(), req())
	apiErr, _ := errors.AsType[*goodall.APIError](err)
	if apiErr == nil {
		t.Fatalf("err = %v, want *goodall.APIError", err)
	}
	if apiErr.Message != "not found" {
		t.Errorf("Message = %q, want the trimmed body", apiErr.Message)
	}
	if apiErr.Raw != nil {
		t.Errorf("Raw = %q, want nil for a body that is not JSON", apiErr.Raw)
	}
}

func TestDoFallbackEmptyBodyUsesStatusText(t *testing.T) {
	s := &scripted{steps: []reply{{status: 404, body: ""}}}
	_, err := testClient(s).Do(t.Context(), req())
	apiErr, _ := errors.AsType[*goodall.APIError](err)
	if apiErr == nil {
		t.Fatalf("err = %v, want *goodall.APIError", err)
	}
	if apiErr.Message != "Not Found" {
		t.Errorf("Message = %q, want the status text", apiErr.Message)
	}
}

func TestDoFallbackTruncatesAHugeBody(t *testing.T) {
	huge := make([]byte, 4096)
	for i := range huge {
		huge[i] = 'x'
	}
	s := &scripted{steps: []reply{{status: 502, body: string(huge)}}}
	c := testClient(s)
	c.MaxRetries = -1
	_, err := c.Do(t.Context(), req())
	apiErr, _ := errors.AsType[*goodall.APIError](err)
	if apiErr == nil {
		t.Fatalf("err = %v, want *goodall.APIError", err)
	}
	if len(apiErr.Message) > maxErrorMessage+len("…") {
		t.Errorf("Message is %d bytes, want it bounded at %d", len(apiErr.Message), maxErrorMessage)
	}
}

func TestDoStampsProviderOnDecodedError(t *testing.T) {
	s := &scripted{steps: []reply{{status: 403, body: "nope"}}}
	c := testClient(s)
	c.Decode = func(int, http.Header, []byte) *goodall.APIError {
		return &goodall.APIError{Type: "permission_error", Message: "no"}
	}
	_, err := c.Do(t.Context(), req())
	apiErr, _ := errors.AsType[*goodall.APIError](err)
	if apiErr == nil {
		t.Fatalf("err = %v, want *goodall.APIError", err)
	}
	if apiErr.Provider != "testprov" {
		t.Errorf("Provider = %q, want testprov", apiErr.Provider)
	}
	if apiErr.Status != 403 {
		t.Errorf("Status = %d, want the response status filled in", apiErr.Status)
	}
	if apiErr.Kind != goodall.KindUnauthorized {
		t.Errorf("Kind = %v, want the status fallback %v", apiErr.Kind, goodall.KindUnauthorized)
	}
	if apiErr.Type != "permission_error" {
		t.Errorf("Type = %q, want the decoder's own", apiErr.Type)
	}
}

func TestDoRequestIDFromHeader(t *testing.T) {
	cases := map[string]http.Header{
		"req_from_request_id":   {"Request-Id": []string{"req_from_request_id"}},
		"req_from_x_request_id": {"X-Request-Id": []string{"req_from_x_request_id"}},
	}
	for want, header := range cases {
		s := &scripted{steps: []reply{{status: 400, header: header, body: "bad"}}}
		_, err := testClient(s).Do(t.Context(), req())
		apiErr, _ := errors.AsType[*goodall.APIError](err)
		if apiErr == nil {
			t.Fatalf("err = %v, want *goodall.APIError", err)
		}
		if apiErr.RequestID != want {
			t.Errorf("RequestID = %q, want %q", apiErr.RequestID, want)
		}
	}
}

func TestDoKeepsDecoderRequestID(t *testing.T) {
	s := &scripted{steps: []reply{{status: 400, header: http.Header{"Request-Id": []string{"from_header"}}, body: "bad"}}}
	c := testClient(s)
	c.Decode = func(int, http.Header, []byte) *goodall.APIError {
		return &goodall.APIError{Kind: goodall.KindInvalidRequest, RequestID: "from_body"}
	}
	_, err := c.Do(t.Context(), req())
	apiErr, _ := errors.AsType[*goodall.APIError](err)
	if apiErr == nil {
		t.Fatalf("err = %v, want *goodall.APIError", err)
	}
	if apiErr.RequestID != "from_body" {
		t.Errorf("RequestID = %q, want the decoder's own", apiErr.RequestID)
	}
}

func TestDoClosesErrorResponseBody(t *testing.T) {
	body := newCountingBody(`{"error":"x"}`)
	s := &scripted{steps: []reply{{status: 400, stream: body}}}
	if _, err := testClient(s).Do(t.Context(), req()); err == nil {
		t.Fatal("Do: want an error")
	}
	if n := body.closeCount(); n != 1 {
		t.Errorf("error body closes = %d, want 1", n)
	}
}

// TestDoUnbuildableRequest pairs the failure with the success it must not
// resemble: an unusable method never reaches the transport, while the same
// client sends a well-formed request straight through.
func TestDoUnbuildableRequest(t *testing.T) {
	s := &scripted{steps: []reply{{status: 200, body: "ok"}}}
	c := testClient(s)
	_, err := c.Do(t.Context(), Request{Method: "bad method", URL: "https://example.test/"})
	if err == nil {
		t.Fatal("Do: want an error for an unusable method")
	}
	if _, ok := errors.AsType[*goodall.APIError](err); ok {
		t.Errorf("err = %v, want a plain error rather than an *APIError", err)
	}
	if s.count() != 0 {
		t.Fatalf("attempts after the bad request = %d, want 0", s.count())
	}
	resp, err := c.Do(t.Context(), req())
	if err != nil {
		t.Fatalf("Do on a well-formed request: %v", err)
	}
	resp.Body.Close()
	if s.count() != 1 {
		t.Errorf("attempts after the good request = %d, want 1", s.count())
	}
}

func TestDoCancelledContextBeforeSend(t *testing.T) {
	s := &scripted{steps: []reply{{status: 200, body: "ok"}}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := testClient(s).Do(ctx, req())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if s.count() != 0 {
		t.Errorf("attempts = %d, want 0", s.count())
	}
}
