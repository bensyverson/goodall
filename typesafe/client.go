package typesafe

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/systemone"
	"github.com/bensyverson/goodall/internal/transport"
)

// Client implements the seam every decision model sits behind.
var _ decision.Decider = (*Client)(nil)

// ProviderName is the name this package stamps on every
// *[goodall.APIError] it produces. It is not a [goodall.Provider] name: Jev
// answers no conversation, and nothing in goodall's agent loop can be pointed
// at it.
const ProviderName = "typesafe"

// DefaultBaseURL is the TypeSafe API origin. A gateway, a proxy or a recording
// server is selected with [WithBaseURL].
const DefaultBaseURL = "https://api.typesafe.ai"

// DefaultModel is the model a [decision.Request] with no model is answered by.
// It is TypeSafe's own default, the alias for the current stable release. Pass a
// versioned id such as "jev-1.13.0" wherever thresholds have been tuned: an alias
// moves when a release ships, and the confidence a caller compares against is
// tuned per version. The answer reports whichever versioned id actually answered.
const DefaultModel = "jev-latest"

// The paths this package calls, relative to the base URL.
const (
	systemOnePath = "/v1/systemone"
	modelsPath    = "/v1/models"
)

// Client is a TypeSafe client: one blocking call per request, no streaming,
// because the API has none.
//
// It holds no per-call state, so one client serves every goroutine in a
// process. Build one with [New].
type Client struct {
	apiKey  string
	baseURL string
	http    transport.Client
}

// New builds a client for one API key.
//
// The key is sent as an Authorization bearer token on every request and is
// never logged by this package.
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

// Decide evaluates the request's state against its questions and returns one
// answer per question, implementing [decision.Decider]. A request with no model
// is answered by [DefaultModel].
//
// A request the API would refuse is refused here instead, before the call, so
// an empty question list or a score with one level costs nothing; those errors
// carry [goodall.KindInvalidRequest]. A failing status comes back as a
// *[goodall.APIError], and a body that is not a well-formed answer set as a
// *[goodall.ProtocolError]. The cost stays unreported: TypeSafe publishes its
// prices on its website rather than on this response.
//
// The transport cannot tell a request that never reached the server from a
// response lost after the server already evaluated it, so a retried call may
// be billed twice for one evaluation; pass [WithMaxRetries] a negative value
// for at-most-once behavior, at the cost of surfacing that failure instead of
// retrying it.
func (c *Client) Decide(ctx context.Context, req *decision.Request) (*decision.Answers, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	send := *req
	if send.Model == "" {
		send.Model = DefaultModel
	}
	body, err := systemone.EncodeRequest(&send)
	if err != nil {
		return nil, fmt.Errorf("typesafe: %w", err)
	}

	data, err := c.call(ctx, transport.Request{
		Method: http.MethodPost,
		URL:    c.endpoint(systemOnePath),
		Header: c.headers(),
		Body:   body,
	})
	if err != nil {
		return nil, err
	}
	answers, err := systemone.DecodeAnswers(data)
	if err != nil {
		return nil, &goodall.ProtocolError{Reason: "the answer body could not be decoded: " + err.Error()}
	}
	return answers, nil
}

// call sends one request and reads the whole body. Every call this package
// makes is small and blocking, so there is one path for all of them.
func (c *Client) call(ctx context.Context, req transport.Request) ([]byte, error) {
	resp, err := c.http.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
}

// maxResponseBody bounds a response body. An answer set is small; the bound is
// there so a misrouted request cannot be read into memory without limit.
const maxResponseBody = 16 << 20 // 16 MiB

// endpoint joins the base URL and one of this package's paths.
func (c *Client) endpoint(path string) string {
	return c.baseURL + path
}

// headers builds the header set for one call.
func (c *Client) headers() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+c.apiKey)
	h.Set("Content-Type", "application/json")
	return h
}
