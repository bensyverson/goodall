package openrouter

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/transport"
)

// DefaultBaseURL is the OpenRouter API root. A client pointed elsewhere — an
// OpenAI endpoint, a local LM Studio server — overrides it with
// [WithBaseURL] and usually a [Dialect] to match.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// Endpoint paths, relative to the base URL.
const (
	// pathChatCompletions is the completion endpoint, streamed or not.
	pathChatCompletions = "/chat/completions"
	// pathModels is the catalogue. OpenRouter publishes no per-model
	// endpoint that returns a catalogue entry: /models/{id} is a 404 and
	// /models/{id}/endpoints returns a different shape with no
	// supported_parameters, top_provider or reasoning. So a lookup reads
	// the catalogue and finds the id, which the client's memo makes a
	// once-per-client cost for every model at once.
	pathModels = "/models"
)

// Client talks to OpenRouter, or to any other server that speaks the OpenAI
// Chat Completions format. It implements [goodall.Provider],
// [goodall.Completer] and [goodall.ModelLister].
//
// A Client holds no per-call state, so one value serves every goroutine in a
// process (invariant 12); the [goodall.Stream] it returns is single-consumer.
type Client struct {
	http        transport.Client
	baseURL     string
	apiKey      string
	dialect     Dialect
	attribution Attribution

	// models memoises the catalogue. One fetch reads every model, so it is
	// stored per identifier and answers for all of them; a model the
	// catalogue did not list is not stored, because OpenRouter adds models
	// continuously and a miss is worth asking about again.
	modelsMu sync.Mutex
	models   map[string]*goodall.ModelInfo
}

// Option configures a [Client] at construction. Options are the only way to
// configure one, because the fields are unexported: a client that cannot be
// reconfigured after construction is a client that is safe to share.
type Option func(*Client)

// New builds a client.
//
// The API key is an option rather than a first argument because a keyless
// server is an ordinary case here, not an edge one: a local LM Studio or
// llama.cpp server wants New(WithBaseURL(…), WithDialect(LMStudio)) and
// nothing else, where a key argument would force a meaningless "" on it. With
// no key set, no Authorization header is sent at all.
func New(opts ...Option) *Client {
	c := &Client{baseURL: DefaultBaseURL}
	for _, opt := range opts {
		opt(c)
	}
	c.http.Provider = ProviderName
	c.http.Decode = errorDecoder
	return c
}

// WithAPIKey sets the bearer token. An empty key sends no Authorization
// header, which is what a local server expects.
func WithAPIKey(key string) Option {
	return func(c *Client) { c.apiKey = key }
}

// WithHTTPClient sets the [http.Client] every request goes through, the seam
// for logging, recording and proxy round trippers.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) { c.http.HTTP = client }
}

// WithBaseURL points the client at another server. A trailing slash is
// trimmed, so both spellings of a URL behave the same.
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(url, "/") }
}

// WithDialect says which flavour of the Chat Completions format the server
// speaks, which decides the members a request may carry. The zero value is
// [OpenRouter].
func WithDialect(d Dialect) Option {
	return func(c *Client) { c.dialect = d }
}

// WithAttribution names the app on OpenRouter's public leaderboards. It
// travels as headers, so it belongs to the client rather than to a request.
func WithAttribution(a Attribution) Option {
	return func(c *Client) { c.attribution = a }
}

// WithMaxRetries is how many extra attempts a retryable failure earns. Zero
// leaves the transport's default of two; a negative value disables retries.
func WithMaxRetries(n int) Option {
	return func(c *Client) { c.http.MaxRetries = n }
}

// Dialect is the flavour of the Chat Completions format this client speaks.
func (c *Client) Dialect() Dialect { return c.dialect }

// Complete sends the request and waits for the whole answer.
//
// It exists alongside [Client.Stream] for the calls where streaming is refused
// or pointless. HTTP 200 is not success: a body carrying an error object, a
// choice carrying one, or a body with no choices at all comes back as a
// *[goodall.APIError].
func (c *Client) Complete(ctx context.Context, req *goodall.Request) (*goodall.Response, error) {
	body, err := c.requestBody(req, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(ctx, transport.Request{
		Method: http.MethodPost,
		URL:    c.baseURL + pathChatCompletions,
		Header: c.header(),
		Body:   body,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var decoded chatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		// A 200 whose body is not a completion at all is still worth
		// reporting as the provider's failure rather than as a decode
		// error nobody can act on.
		if apiErr := decodeError(0, resp.Header, raw); apiErr != nil {
			return nil, apiErr
		}
		return nil, fmt.Errorf("openrouter: decoding the response: %w", err)
	}
	if apiErr := errorInBody(&decoded, resp.Header, raw); apiErr != nil {
		return nil, apiErr
	}
	return translateResponse(&decoded)
}

// requestBody translates and marshals one request.
func (c *Client) requestBody(req *goodall.Request, stream bool) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("openrouter: %w: the request is nil", goodall.KindInvalidRequest)
	}
	wire, err := translateRequest(req, c.dialect, stream)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("openrouter: encoding the request: %w", err)
	}
	return body, nil
}

// header builds the per-call headers: the bearer token when a key is set, the
// attribution headers, and the content type. It returns a fresh value every
// call, since the transport clones and the client keeps no state.
func (c *Client) header() http.Header {
	h := make(http.Header, 4)
	if c.apiKey != "" {
		h.Set("Authorization", "Bearer "+c.apiKey)
	}
	for _, header := range c.attribution.Headers() {
		h.Set(header.Name, header.Value)
	}
	h.Set("Content-Type", "application/json")
	return h
}

// Compile-time proof that the client fills the three provider seams.
var (
	_ goodall.Provider    = (*Client)(nil)
	_ goodall.Completer   = (*Client)(nil)
	_ goodall.ModelLister = (*Client)(nil)
)
