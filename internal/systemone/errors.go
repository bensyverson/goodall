package systemone

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"strings"
)

// wireErrorBody is the shapes an error body takes on this wire, which every server
// words its own way: a "detail" that is a string, an object with a message or a
// per-field list, a plain "message", and an "error" that is a string or an object
// with a message.
type wireErrorBody struct {
	// Detail is a failure of the request: a string, an object with a message,
	// which is how TypeSafe answers an unknown model, or a list of per-field
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

// ErrorMessage reads the human-readable sentence of an error body, in the order
// the shapes are likely: the validation detail first, since that is the one that
// names a field, then the two plain forms. A JSON object holding none of them
// reads as empty, and a body that is no such object is its own message, trimmed
// and bounded, since a gateway's text is the best sentence there is.
//
// It answers only what the sentence is. Whether a body is an error at all, and
// the provider name, the kind and the status, are the calling client's, since a
// success body may hold a "message" of its own.
func ErrorMessage(body []byte) string {
	var w wireErrorBody
	if err := json.Unmarshal(body, &w); err != nil {
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

// detailMessage renders a body's detail member, which is a string, an object with
// a message or a list of per-field objects. A list is joined so that a body naming
// three bad questions names all three.
func detailMessage(detail jsontext.Value) string {
	if len(detail) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(detail, &text) == nil {
		return text
	}
	var object struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(detail, &object) == nil && object.Message != "" {
		return object.Message
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
// elements may be strings or array indices, so each one is rendered as its JSON
// text with a string's quotes removed.
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
// travels on the error's Raw, so nothing is lost; this only keeps an error string
// printable in a log line.
const maxErrorMessage = 1024

// truncate bounds one message.
func truncate(s string) string {
	if len(s) <= maxErrorMessage {
		return s
	}
	return s[:maxErrorMessage] + "…"
}
