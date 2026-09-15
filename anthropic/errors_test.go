package anthropic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/bensyverson/goodall"
)

func TestDecodeErrorMapsEveryDocumentedType(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		errType errorType
		message string
		want    goodall.ErrorKind
	}{
		{"invalid request", 400, errorInvalidRequest, "max_tokens is required", goodall.KindInvalidRequest},
		{"context length", 400, errorInvalidRequest, "prompt is too long: 250000 tokens > 200000 maximum", goodall.KindContextLength},
		{"authentication", 401, errorAuthentication, "invalid x-api-key", goodall.KindUnauthorized},
		{"billing", 402, errorBilling, "credit balance is too low", goodall.KindUnauthorized},
		{"permission", 403, errorPermission, "not allowed", goodall.KindUnauthorized},
		{"not found", 404, errorNotFound, "model not found", goodall.KindNotFound},
		{"request too large", 413, errorRequestTooLarge, "request exceeds the size limit", goodall.KindInvalidRequest},
		{"rate limit", 429, errorRateLimit, "rate limit exceeded", goodall.KindRateLimited},
		{"api error", 500, errorAPI, "internal server error", goodall.KindServer},
		{"overloaded", 529, errorOverloaded, "Overloaded", goodall.KindOverloaded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := `{"type":"error","error":{"type":"` + string(c.errType) + `","message":"` + c.message + `"},"request_id":"req_1"}`
			got := decodeError(c.status, http.Header{}, []byte(body))
			if got == nil {
				t.Fatal("decodeError returned nil for its own error envelope")
			}
			if got.Kind != c.want {
				t.Errorf("Kind = %v, want %v", got.Kind, c.want)
			}
			if got.Type != string(c.errType) {
				t.Errorf("Type = %q, want the raw wire type %q", got.Type, c.errType)
			}
			if got.Message != c.message {
				t.Errorf("Message = %q, want %q", got.Message, c.message)
			}
			if got.RequestID != "req_1" {
				t.Errorf("RequestID = %q, want the body's request_id", got.RequestID)
			}
			if got.Provider != ProviderName {
				t.Errorf("Provider = %q, want %q", got.Provider, ProviderName)
			}
			if string(got.Raw) != body {
				t.Errorf("Raw = %s, want the body as received", got.Raw)
			}
		})
	}
}

func TestDecodeErrorLeavesAnUnknownTypeForTheStatusToClassify(t *testing.T) {
	body := `{"type":"error","error":{"type":"teapot_error","message":"short and stout"}}`
	got := decodeError(418, http.Header{}, []byte(body))
	if got == nil {
		t.Fatal("decodeError returned nil for its own error envelope")
	}
	if got.Kind != "" {
		t.Errorf("Kind = %q, want it left empty so the transport fills it from the status", got.Kind)
	}
	if got.Type != "teapot_error" {
		t.Errorf("Type = %q, want the unrecognised type kept verbatim", got.Type)
	}
}

func TestDecodeErrorTakesTheRequestIDFromTheHeader(t *testing.T) {
	header := http.Header{"Request-Id": []string{"req_header"}}
	got := decodeError(500, header, []byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`))
	if got == nil {
		t.Fatal("decodeError returned nil for its own error envelope")
	}
	if got.RequestID != "req_header" {
		t.Errorf("RequestID = %q, want the header's value when the body carries none", got.RequestID)
	}
}

func TestDecodeErrorDeclinesABodyItDoesNotRecognise(t *testing.T) {
	for _, body := range []string{
		`<html>502 Bad Gateway</html>`,
		`{"detail":"not anthropic's shape"}`,
		`{"type":"message","id":"msg_1"}`,
		``,
	} {
		if got := decodeError(502, http.Header{}, []byte(body)); got != nil {
			t.Errorf("decodeError(%q) = %#v, want nil so the transport classifies by status", body, got)
		}
	}
}

func TestA200CarryingAnErrorEnvelopeIsAnAPIErrorOnTheBlockingPath(t *testing.T) {
	c, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Request-Id", "req_200")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	})
	_, err := c.Complete(t.Context(), simpleRequest())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("err = %#v, want a *goodall.APIError", err)
	}
	if !errors.Is(err, goodall.KindOverloaded) {
		t.Errorf("Kind = %v, want %v", apiErr.Kind, goodall.KindOverloaded)
	}
	if apiErr.Status != 0 {
		t.Errorf("Status = %d, want 0: HTTP 200 is not a failing status", apiErr.Status)
	}
	if apiErr.RequestID != "req_200" {
		t.Errorf("RequestID = %q, want the response header's value", apiErr.RequestID)
	}
}

func TestA429IsRetriedAndThenSurfacedAsRateLimited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var mu sync.Mutex
		attempts := 0
		c := New("sk-ant-test", WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			attempts++
			mu.Unlock()
			resp := cannedResponse(http.StatusTooManyRequests,
				`{"type":"error","error":{"type":"rate_limit_error","message":"rate limit exceeded"}}`)
			resp.Header.Set("Retry-After", "1")
			return resp, nil
		})}), WithMaxRetries(2))

		begin := time.Now()
		_, err := c.Complete(ctx, simpleRequest())
		apiErr, ok := errors.AsType[*goodall.APIError](err)
		if !ok {
			t.Fatalf("err = %#v, want a *goodall.APIError", err)
		}
		if !errors.Is(err, goodall.KindRateLimited) {
			t.Errorf("Kind = %v, want %v", apiErr.Kind, goodall.KindRateLimited)
		}
		if apiErr.RetryAfter != time.Second {
			t.Errorf("RetryAfter = %v, want the header's 1s", apiErr.RetryAfter)
		}
		mu.Lock()
		got := attempts
		mu.Unlock()
		if got != 3 {
			t.Errorf("attempts = %d, want 3 (two retries over the first try)", got)
		}
		if waited := time.Since(begin); waited != 2*time.Second {
			t.Errorf("waited %v, want exactly the two 1s Retry-After pauses", waited)
		}
	})
}

func TestWithMaxRetriesNegativeSendsOnce(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	c := New("sk-ant-test", WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		attempts++
		mu.Unlock()
		return cannedResponse(http.StatusServiceUnavailable, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`), nil
	})}), WithMaxRetries(-1))
	if _, err := c.Complete(t.Context(), simpleRequest()); err == nil {
		t.Fatal("Complete succeeded on a 503")
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

func TestAPIErrorMessageNamesTheProvider(t *testing.T) {
	err := decodeError(429, http.Header{}, []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"},"request_id":"req_9"}`))
	if !strings.HasPrefix(err.Error(), ProviderName+": ") {
		t.Errorf("Error() = %q, want it to open with the provider name", err.Error())
	}
}
