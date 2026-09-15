package openrouter

import (
	"errors"
	"net/http"
	"testing"

	"github.com/bensyverson/goodall"
)

func TestDecodeErrorMapsErrorType(t *testing.T) {
	for _, tc := range []struct {
		errorType string
		want      goodall.ErrorKind
	}{
		{"context_length_exceeded", goodall.KindContextLength},
		{"rate_limit_exceeded", goodall.KindRateLimited},
		{"provider_overloaded", goodall.KindOverloaded},
		{"provider_unavailable", goodall.KindServer},
		{"server", goodall.KindServer},
		{"timeout", goodall.KindServer},
		{"invalid_image", goodall.KindUnsupportedInput},
		{"image_too_large", goodall.KindUnsupportedInput},
		{"unsupported_image_format", goodall.KindUnsupportedInput},
		{"payment_required", goodall.KindUnauthorized},
		{"refusal", goodall.KindInvalidRequest},
		{"content_policy_violation", goodall.KindInvalidRequest},
	} {
		t.Run(tc.errorType, func(t *testing.T) {
			body := []byte(`{"error":{"code":400,"message":"nope","metadata":{"error_type":"` + tc.errorType + `"}}}`)
			apiErr := decodeError(http.StatusBadRequest, http.Header{}, body)
			if apiErr == nil {
				t.Fatal("decodeError returned nil")
			}
			if apiErr.Kind != tc.want {
				t.Errorf("Kind = %v, want %v", apiErr.Kind, tc.want)
			}
			if apiErr.Type != tc.errorType {
				t.Errorf("Type = %q, want the provider's own error_type verbatim", apiErr.Type)
			}
		})
	}
}

func TestDecodeErrorLeavesUnmappedKindToTheStatus(t *testing.T) {
	body := []byte(`{"error":{"message":"something new","metadata":{"error_type":"a_type_goodall_has_never_seen"}}}`)
	apiErr := decodeError(http.StatusServiceUnavailable, http.Header{}, body)
	if apiErr == nil {
		t.Fatal("decodeError returned nil")
	}
	if apiErr.Kind != "" {
		t.Errorf("Kind = %v, want it left empty so the transport fills it from the status", apiErr.Kind)
	}
	if apiErr.Type != "a_type_goodall_has_never_seen" {
		t.Errorf("Type = %q", apiErr.Type)
	}
}

func TestDecodeErrorUsesNumericCodeAsStatus(t *testing.T) {
	body := []byte(`{"error":{"code":502,"message":"upstream fell over"}}`)
	apiErr := decodeError(http.StatusOK, http.Header{}, body)
	if apiErr == nil {
		t.Fatal("decodeError returned nil")
	}
	if apiErr.Status != 502 {
		t.Errorf("Status = %d, want the body's numeric code", apiErr.Status)
	}
	if apiErr.Kind != goodall.KindServer {
		t.Errorf("Kind = %v, want the kind the numeric code implies", apiErr.Kind)
	}
}

func TestDecodeErrorReadsGenerationIDHeader(t *testing.T) {
	header := http.Header{}
	header.Set("X-Generation-Id", "gen-abc")
	apiErr := decodeError(http.StatusTooManyRequests, header, []byte(`{"error":{"message":"slow down"}}`))
	if apiErr == nil {
		t.Fatal("decodeError returned nil")
	}
	if apiErr.RequestID != "gen-abc" {
		t.Errorf("RequestID = %q, want the x-generation-id header OpenRouter actually sends", apiErr.RequestID)
	}
}

func TestDecodeErrorPrefersRequestIDHeader(t *testing.T) {
	header := http.Header{}
	header.Set("X-Request-Id", "req-abc")
	header.Set("X-Generation-Id", "gen-abc")
	apiErr := decodeError(http.StatusTooManyRequests, header, []byte(`{"error":{"message":"slow down"}}`))
	if apiErr.RequestID != "req-abc" {
		t.Errorf("RequestID = %q, want the request id to win", apiErr.RequestID)
	}
}

func TestDecodeErrorIgnoresABodyWithNoError(t *testing.T) {
	if apiErr := decodeError(http.StatusBadGateway, http.Header{}, []byte(`<html>gateway</html>`)); apiErr != nil {
		t.Errorf("decodeError claimed a body it does not recognize: %+v", apiErr)
	}
}

// A model with no endpoint that reads the modality names no error_type, so the
// message is the only signal. Verified live on 2026-09-15: HTTP 404,
// {"error":{"message":"No endpoints found that support image input","code":404,
// "metadata":{"routing_funnel":[…],"failed_routing_step":"Filter by Image Support"}}}.
func TestImageRefusalIsUnsupportedInput(t *testing.T) {
	body := []byte(`{"error":{"message":"No endpoints found that support image input","code":404,"metadata":{"failed_routing_step":"Filter by Image Support"}}}`)
	apiErr := decodeError(http.StatusNotFound, http.Header{}, body)
	if apiErr == nil {
		t.Fatal("decodeError returned nil")
	}
	if apiErr.Kind != goodall.KindUnsupportedInput {
		t.Errorf("Kind = %v, want unsupported_input", apiErr.Kind)
	}
}

func TestModalityRefusalsAreUnsupportedInput(t *testing.T) {
	for _, message := range []string{
		"No endpoints found that support image input",
		"No endpoints found that support audio input",
		"No endpoints found that support file input",
		"no endpoints found that support tool use",
	} {
		apiErr := decodeError(http.StatusNotFound, http.Header{}, []byte(`{"error":{"message":`+quote(message)+`,"code":404}}`))
		if apiErr == nil || apiErr.Kind != goodall.KindUnsupportedInput {
			t.Errorf("%q -> %v, want unsupported_input", message, apiErr)
		}
	}
}

// A model with no live endpoints at all is a neighboring message that must
// stay not-found: it says nothing about the modality.
func TestMissingModelStaysNotFound(t *testing.T) {
	body := []byte(`{"error":{"message":"No endpoints found for mistralai/mistral-7b-instruct.","code":404}}`)
	apiErr := decodeError(http.StatusNotFound, http.Header{}, body)
	if apiErr == nil {
		t.Fatal("decodeError returned nil")
	}
	if apiErr.Kind == goodall.KindUnsupportedInput {
		t.Error("a model with no endpoints at all was read as an unsupported input")
	}
	if apiErr.Kind != goodall.KindNotFound {
		t.Errorf("Kind = %v, want not_found", apiErr.Kind)
	}
}

func TestImageRefusalThroughTheClient(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Generation-Id", "gen-refused")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"message":"No endpoints found that support image input","code":404,"metadata":{"failed_routing_step":"Filter by Image Support"}}}`))
	})
	req := simpleRequest()
	req.Messages = []goodall.Message{{
		Role: goodall.RoleUser,
		Content: goodall.Blocks{
			goodall.Text{Text: "what is this"},
			goodall.Image{Source: goodall.URLSource("https://example.test/cat.png")},
		},
	}}
	_, err := c.Complete(t.Context(), req)
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("Complete error = %v (%T), want *goodall.APIError", err, err)
	}
	if apiErr.Kind != goodall.KindUnsupportedInput {
		t.Errorf("Kind = %v, want unsupported_input", apiErr.Kind)
	}
	if apiErr.RequestID != "gen-refused" {
		t.Errorf("RequestID = %q", apiErr.RequestID)
	}
}

// quote renders a Go string as a JSON string literal.
func quote(s string) string {
	out := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"', '\\':
			out = append(out, '\\', byte(r))
		default:
			out = append(out, string(r)...)
		}
	}
	return string(append(out, '"'))
}
