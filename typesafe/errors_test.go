package typesafe

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// simpleQuestions is the smallest valid question list, for the tests that are
// about the response rather than the request.
func simpleQuestions() Questions {
	return Questions{{ID: "is_urgent", Question: Noul{Instructions: "Does this convey urgency?"}}}
}

// TestEachStatusMapsToTheDocumentedKind is the error contract: the four
// statuses TypeSafe documents, plus the ones only transport.KindForStatus can
// place, each reach a caller as a *goodall.APIError of a known kind.
func TestEachStatusMapsToTheDocumentedKind(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   goodall.ErrorKind
	}{
		{http.StatusUnauthorized, `{"detail":"Missing or invalid API key."}`, goodall.KindUnauthorized},
		{http.StatusUnprocessableEntity, `{"detail":"questions.frustration.criteria: at least two levels are required"}`, goodall.KindInvalidRequest},
		{http.StatusTooManyRequests, `{"detail":"Rate limit exceeded."}`, goodall.KindRateLimited},
		{529, `{"detail":"TypeSafe is temporarily overloaded."}`, goodall.KindOverloaded},
		{http.StatusNotFound, `{"detail":"Not Found"}`, goodall.KindNotFound},
		{http.StatusInternalServerError, `{"detail":"boom"}`, goodall.KindServer},
		{http.StatusBadRequest, `not json at all`, goodall.KindInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status)+"/"+string(tc.want), func(t *testing.T) {
			// Retries are off so a retryable status costs one call and
			// the test does not wait out a backoff.
			client, _ := serveJSON(t, tc.status, tc.body, WithMaxRetries(-1))
			_, err := client.Ask(t.Context(), "hi", simpleQuestions())
			if err == nil {
				t.Fatal("a failing status came back as success")
			}
			apiErr, ok := errors.AsType[*goodall.APIError](err)
			if !ok {
				t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
			}
			if apiErr.Provider != ProviderName {
				t.Errorf("Provider = %q, want %q", apiErr.Provider, ProviderName)
			}
			if apiErr.Status != tc.status {
				t.Errorf("Status = %d, want %d", apiErr.Status, tc.status)
			}
			if apiErr.Kind != tc.want {
				t.Errorf("Kind = %s, want %s", apiErr.Kind, tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("errors.Is(err, %s) is false", tc.want)
			}
		})
	}
}

func TestTheErrorBodyMessageAndRawSurvive(t *testing.T) {
	const body = `{"detail":"questions.frustration.criteria: at least two levels are required"}`
	client, _ := serveJSON(t, http.StatusUnprocessableEntity, body, WithMaxRetries(-1))
	_, err := client.Ask(t.Context(), "hi", simpleQuestions())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
	}
	if !strings.Contains(apiErr.Message, "at least two levels") {
		t.Errorf("Message = %q, want the body's own detail", apiErr.Message)
	}
	if string(apiErr.Raw) != body {
		t.Errorf("Raw = %s, want the body verbatim", apiErr.Raw)
	}
}

// TestTheErrorBodyMessageIsReadFromEitherShape covers the member names an
// error body may carry. FastAPI-style validation bodies use "detail"; the
// plainer shapes use "message" or "error".
func TestTheErrorBodyMessageIsReadFromEitherShape(t *testing.T) {
	cases := map[string]string{
		`{"detail":"by detail"}`:                    "by detail",
		`{"message":"by message"}`:                  "by message",
		`{"error":"by error"}`:                      "by error",
		`{"error":{"message":"by nested message"}}`: "by nested message",
		`{"detail":[{"loc":["body","questions"],"msg":"field required","type":"value_error.missing"}]}`: "field required",
	}
	for body, want := range cases {
		t.Run(want, func(t *testing.T) {
			client, _ := serveJSON(t, http.StatusUnprocessableEntity, body, WithMaxRetries(-1))
			_, err := client.Ask(t.Context(), "hi", simpleQuestions())
			apiErr, ok := errors.AsType[*goodall.APIError](err)
			if !ok {
				t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
			}
			if !strings.Contains(apiErr.Message, want) {
				t.Errorf("Message = %q, want it to carry %q", apiErr.Message, want)
			}
		})
	}
}

func TestRetryAfterIsHonoredAsTheTransportPromises(t *testing.T) {
	// The transport owns the waiting; this asserts only that the header
	// reaches the error a caller receives, since a scheduler upstream of
	// goodall needs the figure.
	client, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}, WithMaxRetries(-1))
	_, err := client.Ask(t.Context(), "hi", simpleQuestions())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
	}
	if apiErr.RetryAfter.Seconds() != 120 {
		t.Errorf("RetryAfter = %v, want 2m", apiErr.RetryAfter)
	}
}

func TestAnUndecodableSuccessBodyIsAProtocolError(t *testing.T) {
	client, _ := serveJSON(t, http.StatusOK, `{"model":"jev-1.13.0","answers":`)
	_, err := client.Ask(t.Context(), "hi", simpleQuestions())
	if err == nil {
		t.Fatal("a truncated body came back as an answer")
	}
	if _, ok := errors.AsType[*goodall.ProtocolError](err); !ok {
		t.Fatalf("error is %T, want *goodall.ProtocolError: %v", err, err)
	}
}
