package goodall

import (
	"encoding/json/jsontext"
	"strconv"
	"strings"
	"time"
)

// ErrorKind is what went wrong, in terms that hold across providers: each
// provider maps its own error vocabulary onto these so a caller can decide
// what to do without knowing whose API it is talking to.
//
// A kind is itself an error, so it works as an errors.Is target:
//
//	if errors.Is(err, goodall.KindRateLimited) { ... }
type ErrorKind string

const (
	// KindRateLimited is too many requests or tokens for now.
	KindRateLimited ErrorKind = "rate_limited"
	// KindOverloaded is the provider or the upstream model being busy.
	KindOverloaded ErrorKind = "overloaded"
	// KindUnauthorized is a missing, rejected or unfunded credential.
	KindUnauthorized ErrorKind = "unauthorized"
	// KindInvalidRequest is a request the provider will never accept.
	KindInvalidRequest ErrorKind = "invalid_request"
	// KindContextLength is a prompt longer than the model's window.
	KindContextLength ErrorKind = "context_length"
	// KindUnsupportedInput is a modality or feature this model rejects.
	KindUnsupportedInput ErrorKind = "unsupported_input"
	// KindNotFound is an unknown model or endpoint.
	KindNotFound ErrorKind = "not_found"
	// KindServer is a fault on the provider's side.
	KindServer ErrorKind = "server"
	// KindUnknown is an error goodall could not place. The provider's own
	// type and the raw body travel with it on the APIError.
	KindUnknown ErrorKind = "unknown"
)

// normalize reads the zero value as KindUnknown, so an APIError nobody
// classified still answers questions about its kind.
func (k ErrorKind) normalize() ErrorKind {
	if k == "" {
		return KindUnknown
	}
	return k
}

// String names the kind, calling the zero value "unknown".
func (k ErrorKind) String() string { return string(k.normalize()) }

// Error makes a kind usable as an errors.Is target and as an error in its own
// right, for the rare case where there is nothing but a classification to
// report.
func (k ErrorKind) Error() string { return k.String() }

// Retryable reports whether sending the same request again could succeed:
// rate limits, overload and provider faults pass, everything else is the
// caller's to fix.
func (k ErrorKind) Retryable() bool {
	switch k.normalize() {
	case KindRateLimited, KindOverloaded, KindServer:
		return true
	}
	return false
}

// APIError is an error a provider returned. It is always used as a pointer, so
// errors.AsType[*goodall.APIError](err) extracts it from whatever a caller has
// wrapped it in.
//
// HTTP 200 is not success: both providers can deliver an error in the body of
// a 200 response, mid-stream included, and those arrive here too.
type APIError struct {
	// Provider names the provider that produced the error.
	Provider string
	// Status is the HTTP status, zero for an error that arrived inside a
	// successful response.
	Status int
	// Kind is the cross-provider classification.
	Kind ErrorKind
	// Type is the provider's own error type, kept verbatim:
	// "rate_limit_error", "provider_overloaded" and so on.
	Type string
	// Message is the provider's human-readable message.
	Message string
	// RequestID identifies the call in the provider's logs.
	RequestID string
	// RetryAfter is the wait the provider asked for, zero if it asked for
	// none.
	RetryAfter time.Duration
	// Raw is the error body as received, for the cases the typed fields do
	// not cover.
	Raw jsontext.Value
}

// Error renders the error as, at most,
// "anthropic: rate_limited (429 rate_limit_error): slow down [req_123]",
// leaving out whatever the provider did not give.
func (e *APIError) Error() string {
	var b strings.Builder
	if e.Provider != "" {
		b.WriteString(e.Provider)
		b.WriteString(": ")
	}
	b.WriteString(e.Kind.String())
	var detail []string
	if e.Status != 0 {
		detail = append(detail, strconv.Itoa(e.Status))
	}
	if e.Type != "" {
		detail = append(detail, e.Type)
	}
	if len(detail) > 0 {
		b.WriteString(" (")
		b.WriteString(strings.Join(detail, " "))
		b.WriteString(")")
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.RequestID != "" {
		b.WriteString(" [")
		b.WriteString(e.RequestID)
		b.WriteString("]")
	}
	return b.String()
}

// Is matches an ErrorKind target, which is what lets a caller ask
// errors.Is(err, goodall.KindRateLimited) instead of reaching for the concrete
// type to read one field.
func (e *APIError) Is(target error) bool {
	kind, ok := target.(ErrorKind)
	return ok && e.Kind.normalize() == kind.normalize()
}

// Retryable reports whether sending the same request again could succeed.
func (e *APIError) Retryable() bool { return e.Kind.Retryable() }

// CapabilityError is a request a model is known not to accept — an image for a
// text-only model, a tool for a model without tool support. It is raised
// before the network call when a catalogue said so, and after one when the
// provider's refusal named the same fact. It is always used as a pointer, so
// errors.AsType[*goodall.CapabilityError](err) extracts it.
type CapabilityError struct {
	// Provider names the provider that was asked.
	Provider string
	// Model is the model that does not accept it.
	Model string
	// Capability is the fact the request required.
	Capability Capability
	// Message is the provider's own words, where there were any.
	Message string
}

// Error renders the error as, at most,
// `openrouter: model "mistralai/mistral-7b" does not support image_input: no
// endpoints found that support image input`.
func (e *CapabilityError) Error() string {
	var b strings.Builder
	if e.Provider != "" {
		b.WriteString(e.Provider)
		b.WriteString(": ")
	}
	if e.Model != "" {
		b.WriteString("model ")
		b.WriteString(strconv.Quote(e.Model))
	} else {
		b.WriteString("the model")
	}
	b.WriteString(" does not support ")
	b.WriteString(e.Capability.String())
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	return b.String()
}
