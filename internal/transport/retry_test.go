package transport

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bensyverson/goodall"
)

// outcome carries a Do result out of the goroutine a timing test runs it in.
type outcome struct {
	resp *http.Response
	err  error
}

// start runs c.Do in its own goroutine so the test goroutine is free to
// advance the bubble's clock and observe when each attempt lands.
func start(c *Client, ctx context.Context) <-chan outcome {
	done := make(chan outcome, 1)
	go func() {
		resp, err := c.Do(ctx, req())
		done <- outcome{resp, err}
	}()
	return done
}

func TestRetryAfterSecondsHonouredExactly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &scripted{steps: []reply{
			{status: 429, header: http.Header{"Retry-After": []string{"2"}}, body: `{"error":"slow down"}`},
			{status: 200, body: "ok"},
		}}
		c := testClient(s)
		c.Decode = func(int, http.Header, []byte) *goodall.APIError {
			return &goodall.APIError{Kind: goodall.KindRateLimited, Type: "rate_limit_error", Message: "slow down"}
		}
		begin := time.Now()
		done := start(c, ctx)

		synctest.Sleep(1999 * time.Millisecond)
		if n := s.count(); n != 1 {
			t.Fatalf("at 1.999s: attempts = %d, want 1 (the wait must not end early)", n)
		}
		synctest.Sleep(time.Millisecond)
		if n := s.count(); n != 2 {
			t.Fatalf("at 2s: attempts = %d, want 2", n)
		}
		got := <-done
		if got.err != nil {
			t.Fatalf("Do: %v", got.err)
		}
		got.resp.Body.Close()
		if d := s.attempt(1).at.Sub(begin); d != 2*time.Second {
			t.Errorf("retry landed at %v, want exactly 2s", d)
		}
	})
}

func TestRetryAfterHTTPDateHonoured(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		begin := time.Now()
		when := begin.Add(3 * time.Second).UTC().Format(http.TimeFormat)
		s := &scripted{steps: []reply{
			{status: 503, header: http.Header{"Retry-After": []string{when}}, body: "unavailable"},
			{status: 200, body: "ok"},
		}}
		done := start(testClient(s), ctx)

		synctest.Sleep(2999 * time.Millisecond)
		if n := s.count(); n != 1 {
			t.Fatalf("at 2.999s: attempts = %d, want 1", n)
		}
		synctest.Sleep(time.Millisecond)
		got := <-done
		if got.err != nil {
			t.Fatalf("Do: %v", got.err)
		}
		got.resp.Body.Close()
		if d := s.attempt(1).at.Sub(begin); d != 3*time.Second {
			t.Errorf("retry landed at %v, want exactly 3s", d)
		}
	})
}

// TestJitterBackoffFollowsFormula pins Rand so the full-jitter formula
// delay = Rand() * min(MaxDelay, BaseDelay<<attempt) is observable: with
// Rand 0.25 and a 500ms base the two waits are 125ms and 250ms, each inside
// (0, BaseDelay<<attempt].
func TestJitterBackoffFollowsFormula(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &scripted{steps: []reply{{status: 529, body: "overloaded"}}}
		c := testClient(s)
		c.BaseDelay = 500 * time.Millisecond
		c.MaxDelay = 8 * time.Second
		c.MaxRetries = 2
		c.Rand = func() float64 { return 0.25 }
		begin := time.Now()
		done := start(c, ctx)

		synctest.Sleep(124 * time.Millisecond)
		if n := s.count(); n != 1 {
			t.Fatalf("at 124ms: attempts = %d, want 1", n)
		}
		synctest.Sleep(time.Millisecond)
		if n := s.count(); n != 2 {
			t.Fatalf("at 125ms: attempts = %d, want 2", n)
		}
		synctest.Sleep(249 * time.Millisecond)
		if n := s.count(); n != 2 {
			t.Fatalf("at 374ms: attempts = %d, want 2", n)
		}
		synctest.Sleep(time.Millisecond)
		if n := s.count(); n != 3 {
			t.Fatalf("at 375ms: attempts = %d, want 3", n)
		}
		got := <-done
		apiErr, _ := errors.AsType[*goodall.APIError](got.err)
		if apiErr == nil {
			t.Fatalf("err = %v, want *goodall.APIError", got.err)
		}
		if apiErr.Kind != goodall.KindOverloaded {
			t.Errorf("Kind = %v, want %v", apiErr.Kind, goodall.KindOverloaded)
		}
		waits := []time.Duration{
			s.attempt(1).at.Sub(begin),
			s.attempt(2).at.Sub(s.attempt(1).at),
		}
		ceilings := []time.Duration{500 * time.Millisecond, time.Second}
		for i, w := range waits {
			if w <= 0 || w > ceilings[i] {
				t.Errorf("wait %d = %v, want within (0, %v]", i, w, ceilings[i])
			}
		}
	})
}

func TestBackoffCeilingDoublesAndClamps(t *testing.T) {
	c := &Client{BaseDelay: 500 * time.Millisecond, MaxDelay: 2 * time.Second, Rand: func() float64 { return 1 }}
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second}
	for attempt, w := range want {
		if got := c.backoff(attempt); got != w {
			t.Errorf("backoff(%d) = %v, want %v", attempt, got, w)
		}
	}
	if got := c.backoff(1000); got != 2*time.Second {
		t.Errorf("backoff(1000) = %v, want %v (no overflow)", got, 2*time.Second)
	}
}

func TestBackoffClampsRand(t *testing.T) {
	c := &Client{BaseDelay: time.Second, MaxDelay: time.Second, Rand: func() float64 { return 4 }}
	if got := c.backoff(0); got != time.Second {
		t.Errorf("backoff with Rand 4 = %v, want the ceiling %v", got, time.Second)
	}
	c.Rand = func() float64 { return -1 }
	if got := c.backoff(0); got != 0 {
		t.Errorf("backoff with Rand -1 = %v, want 0", got)
	}
}

func TestRetriesExhaustAndReturnLastError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &scripted{steps: []reply{{status: 500, body: `{"error":"boom"}`}}}
		c := testClient(s)
		c.MaxRetries = 2
		c.Rand = func() float64 { return 0.5 }
		got := <-start(c, ctx)
		if n := s.count(); n != 3 {
			t.Errorf("attempts = %d, want 3 (MaxRetries counts retries, not attempts)", n)
		}
		apiErr, _ := errors.AsType[*goodall.APIError](got.err)
		if apiErr == nil {
			t.Fatalf("err = %v, want *goodall.APIError", got.err)
		}
		if apiErr.Kind != goodall.KindServer {
			t.Errorf("Kind = %v, want %v", apiErr.Kind, goodall.KindServer)
		}
		if got.resp != nil {
			t.Error("resp is non-nil on the error path")
		}
	})
}

func TestNegativeMaxRetriesDisablesRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &scripted{steps: []reply{{status: 500, body: "boom"}}}
		c := testClient(s)
		c.MaxRetries = -1 // the opt-out: a zero field means "take the default"
		<-start(c, ctx)
		if n := s.count(); n != 1 {
			t.Errorf("attempts = %d, want 1", n)
		}
	})
}

func TestDefaultMaxRetriesIsTwo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &scripted{steps: []reply{{status: 500, body: "boom"}}}
		<-start(testClient(s), ctx)
		if n := s.count(); n != 3 {
			t.Errorf("attempts = %d, want 3 from the default of 2 retries", n)
		}
	})
}

func TestRetryAfterAboveCapIsNotRetried(t *testing.T) {
	s := &scripted{steps: []reply{
		{status: 429, header: http.Header{"Retry-After": []string{"120"}}, body: "slow down"},
	}}
	c := testClient(s)
	c.MaxRetryAfter = 30 * time.Second
	_, err := c.Do(t.Context(), req())
	apiErr, _ := errors.AsType[*goodall.APIError](err)
	if apiErr == nil {
		t.Fatalf("err = %v, want *goodall.APIError", err)
	}
	if s.count() != 1 {
		t.Errorf("attempts = %d, want 1: a wait past the cap is the caller's decision", s.count())
	}
	if apiErr.RetryAfter != 120*time.Second {
		t.Errorf("RetryAfter = %v, want 120s surfaced on the error", apiErr.RetryAfter)
	}
	if apiErr.Kind != goodall.KindRateLimited {
		t.Errorf("Kind = %v, want %v", apiErr.Kind, goodall.KindRateLimited)
	}
}

func TestTransportErrorRetriedThenSucceeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &scripted{steps: []reply{
			{err: errors.New("connect: connection refused")},
			{status: 200, body: "ok"},
		}}
		c := testClient(s)
		c.Rand = func() float64 { return 0.5 }
		begin := time.Now()
		got := <-start(c, ctx)
		if got.err != nil {
			t.Fatalf("Do: %v", got.err)
		}
		got.resp.Body.Close()
		if s.count() != 2 {
			t.Errorf("attempts = %d, want 2", s.count())
		}
		if d := s.attempt(1).at.Sub(begin); d != 250*time.Millisecond {
			t.Errorf("retry landed at %v, want 0.5 * 500ms", d)
		}
	})
}

func TestTransportErrorExhaustedIsReturnedAsIs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		refused := errors.New("connect: connection refused")
		s := &scripted{steps: []reply{{err: refused}}}
		c := testClient(s)
		c.MaxRetries = 1
		c.Rand = func() float64 { return 0.5 }
		got := <-start(c, ctx)
		if !errors.Is(got.err, refused) {
			t.Errorf("err = %v, want the transport error", got.err)
		}
		if _, ok := errors.AsType[*goodall.APIError](got.err); ok {
			t.Error("a transport failure must not be dressed up as an *APIError")
		}
		if s.count() != 2 {
			t.Errorf("attempts = %d, want 2", s.count())
		}
	})
}

func TestCancelDuringWaitReturnsPromptly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &scripted{steps: []reply{
			{status: 429, header: http.Header{"Retry-After": []string{"20"}}, body: "slow down"},
			{status: 200, body: "ok"},
		}}
		c := testClient(s)
		c.MaxRetryAfter = time.Minute
		begin := time.Now()
		done := start(c, ctx)

		synctest.Sleep(time.Second)
		cancel()
		got := <-done
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", got.err)
		}
		if d := time.Since(begin); d != time.Second {
			t.Errorf("Do returned at %v, want the moment of the cancel (1s)", d)
		}
		if s.count() != 1 {
			t.Errorf("attempts = %d, want 1", s.count())
		}
	})
}

func TestCancelMidBodyClosesBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		body := newCountingBody("event: ping\n\ndata: hello\n\n")
		s := &scripted{steps: []reply{{status: 200, stream: body}}}
		resp, err := testClient(s).Do(ctx, req())
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		buf := make([]byte, 5)
		if _, err := resp.Body.Read(buf); err != nil {
			t.Fatalf("first read: %v", err)
		}
		if n := body.closeCount(); n != 0 {
			t.Fatalf("body closed early: %d closes", n)
		}
		cancel()
		synctest.Wait()
		if n := body.closeCount(); n != 1 {
			t.Errorf("closes after cancel = %d, want 1", n)
		}
		if _, err := resp.Body.Read(buf); !errors.Is(err, context.Canceled) {
			t.Errorf("read after cancel: err = %v, want context.Canceled", err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Errorf("Close after the watcher closed it: %v", err)
		}
		if n := body.closeCount(); n != 1 {
			t.Errorf("closes after an explicit Close = %d, want 1: Close is idempotent", n)
		}
	})
}

func TestRetryAfterFrom(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"absent", "", 0},
		{"delta seconds", "2", 2 * time.Second},
		{"delta seconds padded", "  30  ", 30 * time.Second},
		{"zero seconds", "0", 0},
		{"negative seconds", "-5", 0},
		{"future date", now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{"past date", now.Add(-90 * time.Second).Format(http.TimeFormat), 0},
		{"garbage", "soon", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.value != "" {
				h.Set("Retry-After", tc.value)
			}
			if got := retryAfterFrom(h, now); got != tc.want {
				t.Errorf("retryAfterFrom(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
