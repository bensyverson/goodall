package goodall

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestErrorKindString(t *testing.T) {
	cases := []struct {
		in   ErrorKind
		want string
	}{
		{KindRateLimited, "rate_limited"},
		{KindOverloaded, "overloaded"},
		{KindUnauthorized, "unauthorized"},
		{KindInvalidRequest, "invalid_request"},
		{KindContextLength, "context_length"},
		{KindUnsupportedInput, "unsupported_input"},
		{KindNotFound, "not_found"},
		{KindServer, "server"},
		{KindUnknown, "unknown"},
		{ErrorKind(""), "unknown"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("ErrorKind(%q).String() = %q, want %q", string(c.in), got, c.want)
		}
		if got := c.in.Error(); got != c.want {
			t.Errorf("ErrorKind(%q).Error() = %q, want %q", string(c.in), got, c.want)
		}
	}
}

func TestErrorKindRetryable(t *testing.T) {
	cases := map[ErrorKind]bool{
		KindRateLimited:      true,
		KindOverloaded:       true,
		KindServer:           true,
		KindUnauthorized:     false,
		KindInvalidRequest:   false,
		KindContextLength:    false,
		KindUnsupportedInput: false,
		KindNotFound:         false,
		KindUnknown:          false,
		ErrorKind(""):        false,
	}
	for kind, want := range cases {
		if got := kind.Retryable(); got != want {
			t.Errorf("ErrorKind(%q).Retryable() = %v, want %v", string(kind), got, want)
		}
		err := &APIError{Kind: kind}
		if got := err.Retryable(); got != want {
			t.Errorf("(&APIError{Kind: %q}).Retryable() = %v, want %v", string(kind), got, want)
		}
	}
}

func TestAPIErrorError(t *testing.T) {
	cases := []struct {
		name string
		in   *APIError
		want string
	}{
		{
			"everything",
			&APIError{Provider: "anthropic", Status: 429, Kind: KindRateLimited, Type: "rate_limit_error", Message: "rate limit exceeded", RequestID: "req_123"},
			"anthropic: rate_limited (429 rate_limit_error): rate limit exceeded [req_123]",
		},
		{
			"no request id",
			&APIError{Provider: "anthropic", Status: 429, Kind: KindRateLimited, Type: "rate_limit_error", Message: "rate limit exceeded"},
			"anthropic: rate_limited (429 rate_limit_error): rate limit exceeded",
		},
		{
			"no provider type",
			&APIError{Provider: "openrouter", Status: 503, Kind: KindOverloaded, Message: "provider overloaded"},
			"openrouter: overloaded (503): provider overloaded",
		},
		{
			"no status",
			&APIError{Provider: "openrouter", Kind: KindOverloaded, Type: "provider_overloaded", Message: "provider overloaded"},
			"openrouter: overloaded (provider_overloaded): provider overloaded",
		},
		{
			"message only",
			&APIError{Kind: KindServer, Message: "boom"},
			"server: boom",
		},
		{
			"bare",
			&APIError{},
			"unknown",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Error(); got != c.want {
				t.Fatalf("Error() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestAPIErrorIsKind is the criterion: a caller switches on the kind through
// errors.Is without reaching for the concrete type.
func TestAPIErrorIsKind(t *testing.T) {
	err := error(&APIError{Provider: "anthropic", Status: 429, Kind: KindRateLimited, Type: "rate_limit_error"})
	if !errors.Is(err, KindRateLimited) {
		t.Error("errors.Is(err, KindRateLimited) = false, want true")
	}
	for _, kind := range []ErrorKind{KindOverloaded, KindServer, KindUnknown} {
		if errors.Is(err, kind) {
			t.Errorf("errors.Is(err, %q) = true, want false", string(kind))
		}
	}
	if errors.Is(err, errors.New("rate_limited")) {
		t.Error("errors.Is matched an unrelated error with the same text")
	}
	// A kind goodall could not map still answers the question it was asked.
	unknown := error(&APIError{Provider: "openrouter", Status: 418})
	if !errors.Is(unknown, KindUnknown) {
		t.Error("errors.Is(unmapped, KindUnknown) = false, want true")
	}
	// The error wrapped in a caller's own context keeps answering.
	wrapped := fmt.Errorf("sending the request: %w", err)
	if !errors.Is(wrapped, KindRateLimited) {
		t.Error("errors.Is(wrapped, KindRateLimited) = false, want true")
	}
}

// TestAPIErrorAsType: the details a retry needs come out of an opaque error.
func TestAPIErrorAsType(t *testing.T) {
	raw := jsontext.Value(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	err := error(&APIError{
		Provider:   "anthropic",
		Status:     429,
		Kind:       KindRateLimited,
		Type:       "rate_limit_error",
		Message:    "slow down",
		RequestID:  "req_123",
		RetryAfter: 30 * time.Second,
		Raw:        raw,
	})
	wrapped := fmt.Errorf("turn 3: %w", err)
	got, ok := errors.AsType[*APIError](wrapped)
	if !ok {
		t.Fatal("errors.AsType[*APIError] did not find the error")
	}
	if got.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", got.RetryAfter)
	}
	if got.RequestID != "req_123" {
		t.Errorf("RequestID = %q, want %q", got.RequestID, "req_123")
	}
	if string(got.Raw) != string(raw) {
		t.Errorf("Raw = %s, want %s", got.Raw, raw)
	}
	if _, ok := errors.AsType[*CapabilityError](wrapped); ok {
		t.Error("errors.AsType[*CapabilityError] matched an *APIError")
	}
}

func TestCapabilityErrorError(t *testing.T) {
	cases := []struct {
		name string
		in   *CapabilityError
		want string
	}{
		{
			"everything",
			&CapabilityError{Provider: "openrouter", Model: "mistralai/mistral-7b", Capability: CapImageInput, Message: "no endpoints found that support image input"},
			`openrouter: model "mistralai/mistral-7b" does not support image_input: no endpoints found that support image input`,
		},
		{
			"no message",
			&CapabilityError{Provider: "anthropic", Model: "claude-haiku-4-5", Capability: CapPDFInput},
			`anthropic: model "claude-haiku-4-5" does not support pdf_input`,
		},
		{
			"no provider or model",
			&CapabilityError{Capability: CapTools},
			"the model does not support tools",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Error(); got != c.want {
				t.Fatalf("Error() = %q, want %q", got, c.want)
			}
		})
	}
	var err error = &CapabilityError{Provider: "anthropic", Model: "m", Capability: CapAudioInput}
	got, ok := errors.AsType[*CapabilityError](fmt.Errorf("checking the request: %w", err))
	if !ok {
		t.Fatal("errors.AsType[*CapabilityError] did not find the error")
	}
	if got.Capability != CapAudioInput {
		t.Errorf("Capability = %q, want %q", string(got.Capability), string(CapAudioInput))
	}
}
