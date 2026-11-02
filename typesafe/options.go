package typesafe

import (
	"net/http"
	"strings"
)

// Option configures a [Client] at construction. What belongs to one call is on
// the [decision.Request] instead.
type Option func(*Client)

// WithBaseURL points the client at an origin other than [DefaultBaseURL] — a
// gateway, a proxy or a test server. A trailing slash is ignored.
func WithBaseURL(base string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(base, "/") }
}

// WithHTTPClient sends every request through h, which is the seam for logging,
// recording and custom timeouts. The default is [http.DefaultClient].
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http.HTTP = h }
}

// WithMaxRetries caps how many extra attempts a retryable failure — a rate
// limit, an overload, a server fault — earns. The default is two, so three
// attempts in all; a negative value disables retrying, which is how to get
// at-most-once behavior: the client cannot tell a request that never reached
// the server from a response lost after the server already evaluated it, so
// retrying either one may bill TypeSafe twice for the same evaluation.
// TypeSafe sends Retry-After on a 429 and a 529, and the transport waits the
// time it asks for rather than its own backoff.
func WithMaxRetries(n int) Option {
	return func(c *Client) { c.http.MaxRetries = n }
}
