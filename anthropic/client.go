package anthropic

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"strings"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/transport"
)

// apiVersion is the Messages API version every request declares. It is a
// constant rather than an option because it names the wire contract these
// structs were written against: a caller who sent a different one would get a
// body this package cannot read.
const apiVersion = "2023-06-01"

// DefaultBaseURL is the Anthropic API origin. A proxy, a gateway or a
// recording server is selected with WithBaseURL.
const DefaultBaseURL = "https://api.anthropic.com"

// The paths this package calls, relative to the base URL.
const (
	messagesPath = "/v1/messages"
	modelsPath   = "/v1/models"
)

// The headers Anthropic defines. Named so a typo is a compile error rather
// than a request that is quietly unauthenticated.
const (
	headerAPIKey  = "x-api-key"
	headerVersion = "anthropic-version"
	headerBeta    = "anthropic-beta"
)

// Client is the Anthropic provider: a goodall.Provider, a goodall.Completer
// and a goodall.ModelLister over the Messages API.
//
// It holds no per-call state (invariant 12), so one client serves every
// goroutine in a process; the streams it returns are single-consumer. Build
// one with New and hand it to an agent.
type Client struct {
	apiKey  string
	baseURL string
	betas   []Beta
	http    transport.Client
}

// Compile-time proof that the client fills all three provider seams.
var (
	_ goodall.Provider    = (*Client)(nil)
	_ goodall.Completer   = (*Client)(nil)
	_ goodall.ModelLister = (*Client)(nil)
)

// Option configures a Client at construction. Per-call options live on
// goodall.Request and on Extensions; these are the ones that belong to the
// connection rather than to a message.
type Option func(*Client)

// WithHTTPClient sends every request through h, which is the seam for
// logging, recording, proxies and custom timeouts. The default is
// http.DefaultClient, whose zero timeout is what a long stream needs.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http.HTTP = h }
}

// WithBaseURL points the client at an origin other than DefaultBaseURL — a
// gateway, a regional endpoint or a test server. A trailing slash is ignored.
func WithBaseURL(base string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(base, "/") }
}

// WithBetas names beta features every request from this client asks for. A
// request's own Extensions.Betas are appended to these, and a beta named
// twice is sent once, in the position it first appeared.
func WithBetas(betas ...Beta) Option {
	return func(c *Client) { c.betas = append(c.betas, betas...) }
}

// WithMaxRetries caps how many extra attempts a retryable failure — a rate
// limit, an overload, a server fault — earns. The default is two, so three
// attempts in all; a negative value disables retrying. Nothing is retried
// once the response body has started arriving, since the stream is already
// the caller's.
func WithMaxRetries(n int) Option {
	return func(c *Client) { c.http.MaxRetries = n }
}

// New builds a client for one API key.
//
// The key is sent as x-api-key on every request and is never logged by this
// package; a caller who needs another authentication scheme, such as a
// gateway's bearer token, supplies it with WithHTTPClient.
func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: DefaultBaseURL,
		http: transport.Client{
			Provider: ProviderName,
			Decode:   transport.ErrorDecoder(decodeError),
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// endpoint joins the base URL and one of this package's paths.
func (c *Client) endpoint(path string) string {
	return c.baseURL + path
}

// headers builds the header set for one call, merging the client's betas with
// the request's. Order is preserved and duplicates are dropped, because the
// header is a list the server reads in order and a repeated beta reads as a
// caller mistake.
func (c *Client) headers(betas []Beta) http.Header {
	h := http.Header{}
	h.Set(headerAPIKey, c.apiKey)
	h.Set(headerVersion, apiVersion)
	h.Set("Content-Type", "application/json")
	if joined := joinBetas(c.betas, betas); joined != "" {
		h.Set(headerBeta, joined)
	}
	return h
}

// joinBetas renders the anthropic-beta header value, first-wins on duplicates.
func joinBetas(sets ...[]Beta) string {
	var out []string
	seen := make(map[Beta]bool)
	for _, set := range sets {
		for _, b := range set {
			if b == "" || seen[b] {
				continue
			}
			seen[b] = true
			out = append(out, string(b))
		}
	}
	return strings.Join(out, ",")
}

// messageRequest translates a neutral request into one transport request.
// stream says which of the two paths it is for, since one body serves both.
func (c *Client) messageRequest(req *goodall.Request, stream bool) (transport.Request, error) {
	body, betas, err := translateRequest(req)
	if err != nil {
		return transport.Request{}, err
	}
	body.Stream = stream
	raw, err := json.Marshal(body)
	if err != nil {
		return transport.Request{}, err
	}
	return transport.Request{
		Method: http.MethodPost,
		URL:    c.endpoint(messagesPath),
		Header: c.headers(betas),
		Body:   raw,
	}, nil
}

// Complete sends the request and waits for the whole answer, which is what
// goodall.Completer asks for. It exists beside Stream for the calls where
// streaming buys nothing — a zero-token cache pre-warm, a one-word
// classification — and yields the same Response that collecting the stream
// would.
//
// HTTP 200 is not success: a body whose type is "error" comes back as a
// *goodall.APIError, exactly as a failing status does.
func (c *Client) Complete(ctx context.Context, req *goodall.Request) (*goodall.Response, error) {
	call, err := c.messageRequest(req, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(ctx, call)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	var w wireResponse
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, &goodall.ProtocolError{Reason: "the response body is not a message: " + err.Error()}
	}
	if w.Type == string(streamError) {
		// HTTP 200 is not success (invariant 6): Anthropic delivers an
		// overload or a refusal in the body of a 200 as readily as in a
		// 529, and a caller must see the same error either way.
		return nil, apiErrorFrom(statusInBody, resp.Header, data)
	}
	return translateResponse(&w), nil
}

// maxResponseBody bounds a blocking response body. A message is small; the
// bound is there so a misrouted request cannot be read into memory without
// limit.
const maxResponseBody = 64 << 20 // 64 MiB

// readBody reads a whole response body. The body is bound to the call's
// context by the transport, so a cancelled read reports the context's error
// rather than the shrapnel the cancellation produced downstream.
func readBody(resp *http.Response) ([]byte, error) {
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
}
