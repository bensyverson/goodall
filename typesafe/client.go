package typesafe

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/transport"
)

// ProviderName is the name this package stamps on every
// *[goodall.APIError] it produces. It is not a [goodall.Provider] name: Jev
// answers no conversation, and nothing in goodall's agent loop can be pointed
// at it.
const ProviderName = "typesafe"

// DefaultBaseURL is the TypeSafe API origin. A gateway, a proxy or a recording
// server is selected with [WithBaseURL].
const DefaultBaseURL = "https://api.typesafe.ai"

// DefaultModel is the model every call is answered by unless [WithModel] says
// otherwise. It is TypeSafe's own default, the alias for the current stable
// release; a caller whose thresholds are tuned against one version pins that
// version's id instead.
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
func New(apiKey string, opts ...ClientOption) *Client {
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

// Ask evaluates state against questions and returns one answer per question.
// Every question is judged over the same state, in one call, which is what
// makes a batch of judgments cheap.
//
// state is marshaled as JSON exactly as given: a string for text, a struct or
// a map for a record, a slice for a list of them. It is *data* the model reads
// and not something it defends against — see the package comment — so
// instructions belong in the questions, never in the state.
//
// A request the API would refuse is refused here instead, before the call, so
// an empty question list or a score with one level costs nothing; those errors
// carry [goodall.KindInvalidRequest]. A failing status comes back as a
// *[goodall.APIError], and a body that is not a well-formed answer set as a
// *[goodall.ProtocolError].
//
// The transport cannot tell a request that never reached the server from a
// response lost after the server already evaluated it, so a retried call may
// be billed twice for one evaluation; pass [WithMaxRetries] a negative value
// for at-most-once behavior, at the cost of surfacing that failure instead of
// retrying it.
func (c *Client) Ask(ctx context.Context, state any, questions Questions, opts ...AskOption) (*Answers, error) {
	cfg := askConfig{model: DefaultModel}
	for _, opt := range opts {
		opt(&cfg)
	}
	if state == nil {
		return nil, invalid("a request needs a state to evaluate, and this one is nil")
	}
	if cfg.model == "" {
		return nil, invalid("a request needs a model, and WithModel was given an empty one")
	}
	if err := questions.validate(); err != nil {
		return nil, err
	}

	// Deterministic marshaling is what makes two identical requests
	// identical bytes (invariant 8) even when the caller's state is a map:
	// nothing goodall builds here is a map, but the state is the caller's
	// value and json/v2 leaves map order to chance without this.
	body, err := json.Marshal(&wireRequest{
		State:     state,
		Model:     cfg.model,
		Questions: questions,
	}, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("typesafe: %w: the state cannot be marshaled as JSON: %w",
			goodall.KindInvalidRequest, err)
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
	var answers Answers
	if err := json.Unmarshal(data, &answers); err != nil {
		return nil, &goodall.ProtocolError{Reason: "the answer body could not be decoded: " + err.Error()}
	}
	return &answers, nil
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

// invalid builds the error a locally refused request comes back as. It wraps
// [goodall.KindInvalidRequest] so that errors.Is places it beside the 422 the
// API would have returned for the same mistake.
func invalid(format string, args ...any) error {
	return fmt.Errorf("typesafe: %w: "+format,
		append([]any{goodall.KindInvalidRequest}, args...)...)
}
