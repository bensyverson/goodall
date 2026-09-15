package transport

import (
	"context"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Defaults for a zero-valued Client. Two retries over a 500ms base is the
// shape both providers' own SDKs settle on: long enough to ride out a
// momentary 529, short enough that a caller waiting on a stream notices
// nothing.
const (
	defaultMaxRetries    = 2
	defaultBaseDelay     = 500 * time.Millisecond
	defaultMaxDelay      = 8 * time.Second
	defaultMaxRetryAfter = 30 * time.Second
)

func (c *Client) maxRetries() int {
	if c.MaxRetries == 0 {
		return defaultMaxRetries
	}
	if c.MaxRetries < 0 {
		return 0
	}
	return c.MaxRetries
}

func (c *Client) baseDelay() time.Duration {
	if c.BaseDelay <= 0 {
		return defaultBaseDelay
	}
	return c.BaseDelay
}

func (c *Client) maxDelay() time.Duration {
	if c.MaxDelay <= 0 {
		return defaultMaxDelay
	}
	return c.MaxDelay
}

func (c *Client) maxRetryAfter() time.Duration {
	if c.MaxRetryAfter <= 0 {
		return defaultMaxRetryAfter
	}
	return c.MaxRetryAfter
}

// backoff is full jitter: a uniform draw from [0, ceiling) where ceiling is
// BaseDelay doubled once per retry already made and clamped to MaxDelay.
// Jitter rather than a fixed delay because every client that hit the same
// rate limit is waiting on the same clock, and a fixed delay marches them
// back in step.
func (c *Client) backoff(attempt int) time.Duration {
	ceiling := c.baseDelay()
	maxDelay := c.maxDelay()
	// Doubling by loop rather than by shift: attempt is unbounded and a
	// shift past 63 bits is undefined where this is merely saturating.
	for range attempt {
		if ceiling >= maxDelay {
			break
		}
		ceiling *= 2
	}
	if ceiling > maxDelay {
		ceiling = maxDelay
	}
	r := c.random()
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	return time.Duration(r * float64(ceiling))
}

// random draws the jitter factor, defaulting to math/rand/v2's global source.
func (c *Client) random() float64 {
	if c.Rand != nil {
		return c.Rand()
	}
	return rand.Float64()
}

// wait sleeps for d, or returns ctx's error the moment ctx is done. A
// zero or negative d still checks ctx, so a canceled caller never gets one
// more attempt out of a delay that happened to round to nothing.
func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// requestIDFrom reads the provider's request identifier. Anthropic sends
// request-id and OpenRouter names a call only by x-generation-id, with
// x-request-id the documented name a proxy is likelier to set; all are worth
// carrying because a support conversation starts with one.
func requestIDFrom(h http.Header) string {
	if id := h.Get("Request-Id"); id != "" {
		return id
	}
	if id := h.Get("X-Request-Id"); id != "" {
		return id
	}
	return h.Get("X-Generation-Id")
}

// retryAfterFrom reads a Retry-After header in either RFC 9110 form,
// delta-seconds or an HTTP-date, and returns how long to wait from now.
// Anything unparseable, non-positive, or already in the past reads as "the
// provider asked for no particular wait", which leaves the caller on jittered
// backoff rather than on a number nobody sent.
func retryAfterFrom(h http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(h.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := when.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
