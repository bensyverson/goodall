package typesafe

import (
	"net/http"
	"strings"
)

// ClientOption configures a [Client] at construction. Everything that belongs
// to one call is an [AskOption] instead.
//
// The two provider packages spell this type Option; here that name belongs to
// [Option], one choice in a [Choice] question, which is the API's own word and
// the one a caller writes far more often.
type ClientOption func(*Client)

// WithBaseURL points the client at an origin other than [DefaultBaseURL] — a
// gateway, a proxy or a test server. A trailing slash is ignored.
func WithBaseURL(base string) ClientOption {
	return func(c *Client) { c.baseURL = strings.TrimRight(base, "/") }
}

// WithHTTPClient sends every request through h, which is the seam for logging,
// recording and custom timeouts. The default is [http.DefaultClient].
func WithHTTPClient(h *http.Client) ClientOption {
	return func(c *Client) { c.http.HTTP = h }
}

// WithMaxRetries caps how many extra attempts a retryable failure — a rate
// limit, an overload, a server fault — earns. The default is two, so three
// attempts in all; a negative value disables retrying. TypeSafe sends
// Retry-After on a 429 and a 529, and the transport waits the time it asks for
// rather than its own backoff.
func WithMaxRetries(n int) ClientOption {
	return func(c *Client) { c.http.MaxRetries = n }
}

// AskOption configures one call to [Client.Ask]. There is one today; it is a
// function type rather than a struct so that adding another is not a breaking
// change to every call site.
type AskOption func(*askConfig)

// askConfig is the settled options of one call.
type askConfig struct {
	model string
}

// WithModel names the model this call is answered by, overriding
// [DefaultModel]. Pass a versioned id such as "jev-1.13.0" wherever thresholds
// have been tuned: an alias moves when a release ships, and the confidence a
// caller compares against is tuned per version. The answer reports whichever
// versioned id actually answered.
func WithModel(model string) AskOption {
	return func(cfg *askConfig) { cfg.model = model }
}
