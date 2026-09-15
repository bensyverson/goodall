package anthropic

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"strings"

	"github.com/bensyverson/goodall"
)

// errorType is the "type" member of Anthropic's error object. The API has one
// vocabulary for every path — the Messages endpoint, the catalogue, a
// mid-stream failure — so this table is the whole of it.
type errorType string

const (
	// errorInvalidRequest is a malformed or unacceptable request (400).
	errorInvalidRequest errorType = "invalid_request_error"
	// errorAuthentication is a missing or rejected key (401).
	errorAuthentication errorType = "authentication_error"
	// errorBilling is an account that cannot pay for the call (402).
	errorBilling errorType = "billing_error"
	// errorPermission is a key without access to the resource (403).
	errorPermission errorType = "permission_error"
	// errorNotFound is an unknown endpoint or model (404).
	errorNotFound errorType = "not_found_error"
	// errorRequestTooLarge is a body over the size limit (413).
	errorRequestTooLarge errorType = "request_too_large"
	// errorRateLimit is too many requests or tokens for now (429).
	errorRateLimit errorType = "rate_limit_error"
	// errorAPI is a fault on Anthropic's side (500).
	errorAPI errorType = "api_error"
	// errorOverloaded is Anthropic being busy (529), which can also arrive
	// mid-stream inside a 200.
	errorOverloaded errorType = "overloaded_error"
)

// contextLengthPhrase is the heuristic that separates a prompt over the
// model's window from every other bad request. Anthropic has no distinct error
// type for it — both arrive as invalid_request_error — and the difference
// matters to a caller: a too-long prompt is fixed by compacting the
// conversation, while the rest are fixed by changing the request. The phrase
// is Anthropic's own wording, matched case-insensitively; a release that
// rewords it costs a caller the finer classification, not the error.
const contextLengthPhrase = "prompt is too long"

// statusInBody is the HTTP status recorded for an error that arrived inside a
// successful response — a mid-stream error event, or a 200 whose body is an
// error envelope. goodall.APIError documents zero as exactly that, and
// recording 200 would make a caller's `Status >= 400` test miss it.
const statusInBody = 0

// kindFor maps Anthropic's error vocabulary onto goodall's. A type this
// version does not know leaves the kind empty, which the transport fills from
// the HTTP status rather than guessing.
func kindFor(t errorType, message string) goodall.ErrorKind {
	switch t {
	case errorInvalidRequest:
		if strings.Contains(strings.ToLower(message), contextLengthPhrase) {
			return goodall.KindContextLength
		}
		return goodall.KindInvalidRequest
	case errorAuthentication, errorBilling, errorPermission:
		// All three mean the same thing to a caller: this credential
		// cannot make this call until a human does something about it.
		return goodall.KindUnauthorized
	case errorNotFound:
		return goodall.KindNotFound
	case errorRequestTooLarge:
		return goodall.KindInvalidRequest
	case errorRateLimit:
		return goodall.KindRateLimited
	case errorAPI:
		return goodall.KindServer
	case errorOverloaded:
		return goodall.KindOverloaded
	}
	return ""
}

// decodeError is this package's transport.ErrorDecoder. It reads Anthropic's
// error envelope — {"type":"error","error":{"type","message"},"request_id"} —
// and returns nil for anything else, which leaves the transport to classify
// the response by its status alone.
//
// The same function serves all three places an error body arrives: a failing
// status, an error event mid-stream, and a 200 whose body is an error. They
// are one shape, so they get one decoder.
func decodeError(status int, header http.Header, body []byte) *goodall.APIError {
	var w wireErrorEvent
	if err := json.Unmarshal(body, &w); err != nil {
		return nil
	}
	if w.Type != streamError {
		return nil
	}
	apiErr := &goodall.APIError{
		Provider:  ProviderName,
		Status:    status,
		Kind:      kindFor(errorType(w.Error.Type), w.Error.Message),
		Type:      w.Error.Type,
		Message:   w.Error.Message,
		RequestID: w.RequestID,
	}
	if apiErr.RequestID == "" && header != nil {
		apiErr.RequestID = requestIDFrom(header)
	}
	if raw := jsontext.Value(body); raw.IsValid() {
		apiErr.Raw = raw
	}
	return apiErr
}

// apiErrorFrom is decodeError for the two paths that already know the body is
// an error and must produce one: a mid-stream error event and a 200 carrying
// an error envelope. A body the decoder cannot read still becomes an error
// there, because the alternative is treating a failure as an answer.
func apiErrorFrom(status int, header http.Header, body []byte) *goodall.APIError {
	if apiErr := decodeError(status, header, body); apiErr != nil {
		return apiErr
	}
	apiErr := &goodall.APIError{
		Provider: ProviderName,
		Status:   status,
		Kind:     goodall.KindUnknown,
		Message:  strings.TrimSpace(string(body)),
	}
	if header != nil {
		apiErr.RequestID = requestIDFrom(header)
	}
	if raw := jsontext.Value(body); raw.IsValid() {
		apiErr.Raw = raw
	}
	return apiErr
}

// requestIDFrom reads Anthropic's request identifier off a response header. A
// support conversation starts with one, so it is carried even when the body
// omits it.
func requestIDFrom(h http.Header) string {
	return h.Get("Request-Id")
}
