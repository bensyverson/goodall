// Package transport executes the HTTP requests both provider packages make.
// It owns exactly two concerns — building each attempt and deciding whether
// to make another one — so the retry policy, the Retry-After rules and the
// cancellation guarantees are written and tested once rather than per
// provider.
//
// What it deliberately does not own is the shape of an error body. Anthropic
// and OpenRouter disagree about that, so each provider injects an
// [ErrorDecoder]; the transport falls back to [KindForStatus] when the
// decoder cannot make sense of what arrived.
//
// A 2xx response is handed back with its body still open, because that body
// is the SSE stream the caller came for. That is also why no 2xx is ever
// retried: by the time anything can go wrong with the stream, the transport
// has already let go of it.
package transport

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bensyverson/goodall"
)

// statusOverloaded is Anthropic's overloaded_error status. It is not an IANA
// status code, so net/http has no constant for it.
const statusOverloaded = 529

const (
	// maxErrorBody bounds how much of a non-2xx body is read. Error bodies
	// are small; the bound is there so a misrouted request that returns a
	// gigabyte of HTML cannot be turned into a gigabyte of error value.
	maxErrorBody = 1 << 20 // 1 MiB
	// maxErrorMessage bounds the fallback Message built from the body.
	// The whole body still travels on Raw when it is JSON, so nothing is
	// lost; this only keeps an error string printable in a log line.
	maxErrorMessage = 1024
)

// ErrorDecoder turns a provider's own error body into an [goodall.APIError].
// It may return nil when the body is not one it recognizes, in which case the
// transport classifies the response from its status alone.
//
// A decoder need not fill every field: the transport supplies Provider,
// Status, Kind, RequestID and RetryAfter wherever the decoder left them zero.
type ErrorDecoder func(status int, header http.Header, body []byte) *goodall.APIError

// KindForStatus classifies an HTTP status the way both providers' status
// codes line up. It is exported because a provider's own decoder wants the
// same mapping for the statuses its error body does not explain.
//
// Anything it cannot place — including a status below 400, which should never
// reach it — is [goodall.KindUnknown] rather than a guess.
func KindForStatus(status int) goodall.ErrorKind {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return goodall.KindInvalidRequest
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden:
		return goodall.KindUnauthorized
	case http.StatusNotFound:
		return goodall.KindNotFound
	case http.StatusTooManyRequests:
		return goodall.KindRateLimited
	case http.StatusServiceUnavailable, statusOverloaded:
		return goodall.KindOverloaded
	}
	if status >= 500 && status <= 599 {
		return goodall.KindServer
	}
	return goodall.KindUnknown
}

// Client executes requests for one provider. The zero value of every field
// takes a sensible default, so &Client{Provider: "anthropic"} works; a
// provider overrides what it needs.
//
// A Client holds no per-request state and is safe for concurrent use.
type Client struct {
	// HTTP is the client every attempt goes through, the seam for
	// logging, recording and auth round trippers. Defaults to
	// [http.DefaultClient].
	HTTP *http.Client
	// Provider names the provider and is stamped onto every
	// [goodall.APIError] that leaves this client.
	Provider string
	// Decode parses the provider's error bodies. Nil means every non-2xx
	// is classified from its status alone.
	Decode ErrorDecoder
	// MaxRetries is how many *extra* attempts a retryable failure earns,
	// so the default of 2 allows three attempts in all. A negative value
	// disables retries; zero means the default, since zero is what an
	// uninitialized struct carries.
	MaxRetries int
	// BaseDelay is the backoff ceiling for the first retry, doubling with
	// each one after it. Defaults to 500ms.
	BaseDelay time.Duration
	// MaxDelay caps that doubling. Defaults to 8s.
	MaxDelay time.Duration
	// MaxRetryAfter is the longest Retry-After the transport will sit
	// through. Beyond it the error comes back unretried with RetryAfter
	// set, because a wait that long is a scheduling decision the caller
	// should make. Defaults to 30s.
	MaxRetryAfter time.Duration
	// Rand returns a number in [0, 1) for the full-jitter backoff.
	// Defaults to math/rand/v2's Float64; a test pins it to a constant.
	Rand func() float64
}

// Request is one provider call. The body is bytes rather than an io.Reader so
// that each attempt can build a fresh [http.Request] over it — a reader is
// consumed by the first attempt and has nothing left for the second.
type Request struct {
	// Method is the HTTP method; empty means GET.
	Method string
	// URL is the absolute request URL.
	URL string
	// Header carries auth, version and attribution headers. It is cloned,
	// never retained.
	Header http.Header
	// Body is the request body, nil for a request that has none.
	Body []byte
}

// Do sends req, retrying retryable failures with backoff, and returns the
// first 2xx response with its body open and bound to ctx: a read after ctx is
// canceled reports ctx's error, canceling ctx closes the body, and closing
// it twice is harmless.
//
// A non-2xx response comes back as a *[goodall.APIError] and nothing else; a
// failure before any response comes back as the transport's own error. Either
// way the returned response is nil. A canceled ctx wins over both, so a
// caller that cancels sees ctx.Err() rather than the connection error the
// cancellation caused.
func (c *Client) Do(ctx context.Context, req Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		httpReq, err := c.build(ctx, req)
		if err != nil {
			// The request cannot be built at all; sending it again
			// would fail identically.
			return nil, err
		}
		resp, err := c.httpClient().Do(httpReq)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if attempt >= c.maxRetries() {
				return nil, err
			}
			if waitErr := c.wait(ctx, c.backoff(attempt)); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		if resp.StatusCode/100 == 2 {
			resp.Body = newContextBody(ctx, resp.Body)
			return resp, nil
		}
		apiErr := c.errorFor(resp)
		if !apiErr.Retryable() || attempt >= c.maxRetries() {
			return nil, apiErr
		}
		delay := apiErr.RetryAfter
		if delay > c.maxRetryAfter() {
			return nil, apiErr
		}
		if delay <= 0 {
			delay = c.backoff(attempt)
		}
		if waitErr := c.wait(ctx, delay); waitErr != nil {
			return nil, waitErr
		}
	}
}

// build assembles one attempt. Content-Length comes from the byte slice, and
// a JSON content type is assumed only when the caller named none, since both
// providers' endpoints take JSON and a caller sending something else says so.
func (c *Client) build(ctx context.Context, req Request) (*http.Request, error) {
	var body io.Reader
	if req.Body != nil {
		body = bytes.NewReader(req.Body)
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return nil, err
	}
	if req.Header != nil {
		httpReq.Header = req.Header.Clone()
	}
	if req.Body != nil {
		httpReq.ContentLength = int64(len(req.Body))
		if httpReq.Header.Get("Content-Type") == "" {
			httpReq.Header.Set("Content-Type", "application/json")
		}
	}
	return httpReq, nil
}

// errorFor drains and closes a non-2xx response and turns it into an
// APIError, letting the provider's decoder speak first and filling in
// whatever it left blank.
func (c *Client) errorFor(resp *http.Response) *goodall.APIError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	resp.Body.Close()

	var apiErr *goodall.APIError
	if c.Decode != nil {
		apiErr = c.Decode(resp.StatusCode, resp.Header, body)
	}
	if apiErr == nil {
		apiErr = &goodall.APIError{Message: messageFor(resp.StatusCode, body)}
		if raw := jsontext.Value(body); raw.IsValid() {
			apiErr.Raw = raw
		}
	}
	if apiErr.Provider == "" {
		apiErr.Provider = c.Provider
	}
	if apiErr.Status == 0 {
		apiErr.Status = resp.StatusCode
	}
	if apiErr.Kind == "" {
		apiErr.Kind = KindForStatus(resp.StatusCode)
	}
	if apiErr.RequestID == "" {
		apiErr.RequestID = requestIDFrom(resp.Header)
	}
	if apiErr.RetryAfter == 0 {
		apiErr.RetryAfter = retryAfterFrom(resp.Header, time.Now())
	}
	return apiErr
}

// messageFor is the human-readable fallback when no decoder claimed the body:
// the body itself where there is one, and the status text where there is not.
func messageFor(status int, body []byte) string {
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return http.StatusText(status)
	}
	if len(msg) > maxErrorMessage {
		msg = msg[:maxErrorMessage] + "…"
	}
	return msg
}

// httpClient returns the client every attempt goes through.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
