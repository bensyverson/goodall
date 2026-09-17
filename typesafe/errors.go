package typesafe

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"strings"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/transport"
)

// statusOverloaded is TypeSafe's "overloaded" status, the same code Anthropic
// uses. It is not an IANA status code, so net/http has no constant for it.
const statusOverloaded = 529

// kindFor maps a TypeSafe status onto goodall's cross-provider kinds. The four
// statuses the API documents are named here so the mapping is readable as a
// contract; anything else is classified by the shared table, which places 404,
// the 5xx range and the rest the same way it does for the two providers.
func kindFor(status int) goodall.ErrorKind {
	switch status {
	case http.StatusUnauthorized:
		return goodall.KindUnauthorized
	case http.StatusUnprocessableEntity:
		return goodall.KindInvalidRequest
	case http.StatusTooManyRequests:
		return goodall.KindRateLimited
	case statusOverloaded:
		return goodall.KindOverloaded
	}
	return transport.KindForStatus(status)
}

// wireError is the shapes a TypeSafe error body takes. The API reference says
// only that the body is JSON describing what went wrong and that a 422 details
// the offending field, so all three member names are read: "detail" is what a
// validation failure carries, and "message" and "error" are the plainer forms.
// A body that matches none of them still becomes an error, classified by its
// status, with the body verbatim on Raw.
type wireError struct {
	// Detail is a validation failure: a string, or a list of per-field
	// objects.
	Detail jsontext.Value `json:"detail,omitzero"`
	// Message is the plain message form.
	Message string `json:"message,omitzero"`
	// Error is the other plain form, which may itself be an object with a
	// message.
	Error jsontext.Value `json:"error,omitzero"`
}

// wireErrorDetail is one entry of a field-by-field validation body.
type wireErrorDetail struct {
	// Loc is the path to the offending member.
	Loc []jsontext.Value `json:"loc,omitzero"`
	// Msg is what is wrong with it.
	Msg string `json:"msg,omitzero"`
	// Type names the validation rule that failed.
	Type string `json:"type,omitzero"`
}

// decodeError is this package's [transport.ErrorDecoder]. It fills the message
// from whichever member the body used and keeps the body on Raw; the transport
// supplies the provider, the status, the request id and Retry-After, and
// kindFor supplies the kind.
func decodeError(status int, header http.Header, body []byte) *goodall.APIError {
	apiErr := &goodall.APIError{
		Provider: ProviderName,
		Status:   status,
		Kind:     kindFor(status),
		Message:  errorMessage(body),
	}
	if raw := jsontext.Value(body); raw.IsValid() {
		apiErr.Raw = raw
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(status)
	}
	return apiErr
}

// errorMessage reads the human-readable part of an error body, in the order
// the shapes are likely: the validation detail first, since that is the one
// that names a field, then the two plain forms.
func errorMessage(body []byte) string {
	var w wireError
	if err := json.Unmarshal(body, &w); err != nil {
		// Not JSON at all, or not an object: the whole body is the best
		// message there is, and the transport's own fallback would say
		// the same thing.
		return strings.TrimSpace(truncate(string(body)))
	}
	if msg := detailMessage(w.Detail); msg != "" {
		return msg
	}
	if w.Message != "" {
		return w.Message
	}
	return nestedMessage(w.Error)
}

// detailMessage renders a validation body's detail member, which is either a
// string or a list of per-field objects. A list is joined so that a body
// naming three bad questions names all three.
func detailMessage(detail jsontext.Value) string {
	if len(detail) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(detail, &text) == nil {
		return text
	}
	var entries []wireErrorDetail
	if json.Unmarshal(detail, &entries) != nil {
		return truncate(string(detail))
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Msg == "" {
			continue
		}
		if loc := joinLoc(entry.Loc); loc != "" {
			parts = append(parts, loc+": "+entry.Msg)
			continue
		}
		parts = append(parts, entry.Msg)
	}
	if len(parts) == 0 {
		return truncate(string(detail))
	}
	return strings.Join(parts, "; ")
}

// joinLoc renders a detail entry's path as "body.questions.frustration". The
// elements may be strings or array indices, so each one is rendered as its
// JSON text with a string's quotes removed.
func joinLoc(loc []jsontext.Value) string {
	parts := make([]string, 0, len(loc))
	for _, element := range loc {
		var text string
		if json.Unmarshal(element, &text) == nil {
			parts = append(parts, text)
			continue
		}
		parts = append(parts, string(element))
	}
	return strings.Join(parts, ".")
}

// nestedMessage reads an "error" member that is either a string or an object
// carrying a message.
func nestedMessage(value jsontext.Value) string {
	if len(value) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}
	var nested struct {
		Message string `json:"message,omitzero"`
		Detail  string `json:"detail,omitzero"`
	}
	if json.Unmarshal(value, &nested) == nil {
		if nested.Message != "" {
			return nested.Message
		}
		if nested.Detail != "" {
			return nested.Detail
		}
	}
	return truncate(string(value))
}

// maxErrorMessage bounds the message built from a body. The whole body still
// travels on Raw, so nothing is lost; this only keeps an error string
// printable in a log line.
const maxErrorMessage = 1024

// truncate bounds one message.
func truncate(s string) string {
	if len(s) <= maxErrorMessage {
		return s
	}
	return s[:maxErrorMessage] + "…"
}
