package openrouter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bensyverson/goodall"
)

// TestA400WithNoUsableCodeIsClassifiedFromItsStatus is the regression test for
// api.openai.com's body, which names every member and leaves code null. The
// message below is the one observed on 2026-11-02. The kind has to survive the
// whole path through the transport, since the status is what classifies it.
func TestA400WithNoUsableCodeIsClassifiedFromItsStatus(t *testing.T) {
	const message = "Function tools with reasoning_effort are not supported for gpt-6-astra"
	for name, code := range map[string]string{
		"a null code":           `,"type":"invalid_request_error","param":null,"code":null`,
		"no code at all":        `,"type":"invalid_request_error"`,
		"a code of zero":        `,"code":0`,
		"a number no status is": `,"code":1234`,
		"a string code":         `,"code":"unsupported_value"`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":{"message":"`+message+`"`+code+`}}`)
			}))
			defer srv.Close()

			client := New(WithBaseURL(srv.URL), WithDialect(OpenAI))
			_, err := client.Complete(context.Background(), &goodall.Request{
				Model:    "gpt-6-astra",
				Messages: []goodall.Message{goodall.UserMessage(goodall.Text{Text: "hi"})},
			})
			apiErr, ok := errors.AsType[*goodall.APIError](err)
			if !ok {
				t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
			}
			if apiErr.Kind != goodall.KindInvalidRequest {
				t.Errorf("Kind = %s, want %s", apiErr.Kind, goodall.KindInvalidRequest)
			}
			if !errors.Is(err, goodall.KindInvalidRequest) {
				t.Errorf("errors.Is does not place %v beside an invalid request", err)
			}
			if apiErr.Status != http.StatusBadRequest {
				t.Errorf("Status = %d, want the response's 400", apiErr.Status)
			}
			if apiErr.Message != message {
				t.Errorf("Message = %q, want %q", apiErr.Message, message)
			}
		})
	}
}

// TestAFrameInsideA200WithANullCodeStaysUnknown keeps the other side of that
// path: with no status to read, an unplaced error is unknown rather than blank.
func TestAFrameInsideA200WithANullCodeStaysUnknown(t *testing.T) {
	apiErr := inBodyError(&wireError{Message: "boom", Code: []byte("null")}, http.Header{}, nil)
	if apiErr.Kind != goodall.KindUnknown {
		t.Errorf("Kind = %s, want %s", apiErr.Kind, goodall.KindUnknown)
	}
	if apiErr.Status != 0 {
		t.Errorf("Status = %d, want none", apiErr.Status)
	}
}
