package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"strings"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/systemone"
	"github.com/bensyverson/goodall/internal/transport"
)

// errorType is one of the values OpenRouter puts in error.metadata.error_type.
// It is the stable field to switch on: error.code mirrors the HTTP status only
// until the response is committed, and after that it can be an upstream
// provider's own string.
type errorType string

const (
	// errContextLength is a prompt longer than the model's window.
	errContextLength errorType = "context_length_exceeded"
	// errRateLimit is too many requests or tokens for now.
	errRateLimit errorType = "rate_limit_exceeded"
	// errProviderOverloaded is the upstream provider being busy.
	errProviderOverloaded errorType = "provider_overloaded"
	// errProviderUnavailable is the upstream provider being down.
	errProviderUnavailable errorType = "provider_unavailable"
	// errServer is a fault on OpenRouter's or the provider's side.
	errServer errorType = "server"
	// errTimeout is an upstream call that never answered.
	errTimeout errorType = "timeout"
	// errInvalidImage is an image the provider could not read.
	errInvalidImage errorType = "invalid_image"
	// errImageTooLarge is an image past the provider's size limit.
	errImageTooLarge errorType = "image_too_large"
	// errUnsupportedImageFormat is an image in a format the model rejects.
	errUnsupportedImageFormat errorType = "unsupported_image_format"
	// errPaymentRequired is an account with no credit.
	errPaymentRequired errorType = "payment_required"
	// errRefusal is the moderation layer declining the request. A model's
	// own refusal is not an error: it arrives as message.refusal.
	errRefusal errorType = "refusal"
	// errContentPolicy is a request the content policy rejects.
	errContentPolicy errorType = "content_policy_violation"
)

// modalityRefusalPrefix is the start of the message OpenRouter returns when
// routing found the model but no endpoint that reads the modality the request
// carried — "No endpoints found that support image input", and the same
// sentence for audio, file and tool use. It is a message heuristic because the
// error carries no error_type: the metadata holds only a routing funnel and a
// failed_routing_step, neither of which is documented as stable. The
// neighboring message "No endpoints found for <model>." says nothing about
// the modality and must stay a not-found, which is why the match runs to the
// word "support".
const modalityRefusalPrefix = "no endpoints found that support"

// errorDecoder is the [transport.ErrorDecoder] this package injects. It is a
// var so the client wires one function into the transport and the streaming
// path calls the same logic on a frame that arrived inside an HTTP 200.
var errorDecoder transport.ErrorDecoder = decodeError

// decodeError turns an error body into a *goodall.APIError, or nil when the body
// is not one — an HTML error page from a proxy, say — which leaves the transport
// to classify the response from its status alone.
//
// status is the HTTP status the body arrived with; pass zero for a frame that
// came inside a successful response, which is what leaves APIError.Status at
// zero the way the root package documents. On a failing status a body with no
// error member is still read for a sentence, since a local server may word it
// another way. With no status only the error member counts, because a successful
// body may hold a "message" of its own.
func decodeError(status int, header http.Header, body []byte) *goodall.APIError {
	var envelope struct {
		Error *wireError `json:"error,omitzero"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.said() {
		return apiError(envelope.Error, status, header, body)
	}
	if status == 0 {
		return nil
	}
	if raw := jsontext.Value(body); !raw.IsValid() || raw.Kind() != '{' {
		return nil
	}
	message := systemone.ErrorMessage(body)
	if message == "" {
		return nil
	}
	return apiError(&wireError{Message: message}, status, header, body)
}

// noAnswersError is the error for a 200 decision response with no answers. Where
// the body says why, that is the server's error; where it says nothing, the
// response is broken in a way only the calling loop can report.
func noAnswersError(header http.Header, raw []byte) error {
	if message := systemone.ErrorMessage(raw); message != "" {
		return inBodyError(&wireError{Message: message}, header, raw)
	}
	return &goodall.ProtocolError{Reason: "the decision response carried neither answers nor an error"}
}

// apiError builds the neutral error from one wire error object, wherever it
// was found: at the top level of a body, inside a choice, or as the only frame
// of a stream.
//
// Kind is left empty when nothing in the error explains it, so the transport
// can fill it from the HTTP status; a caller that has no status of its own
// (a frame inside a 200) reads the empty Kind as [goodall.KindUnknown], which
// is what ErrorKind's zero value already means.
func apiError(we *wireError, status int, header http.Header, body []byte) *goodall.APIError {
	if we == nil {
		return nil
	}
	code, numeric := errorCode(we.Code)
	out := &goodall.APIError{
		Provider: ProviderName,
		Kind:     kindFor(we, code, numeric),
		Type:     errorTypeOf(we),
		Message:  readableMessage(we.Message),
		Status:   status,
	}
	if numeric {
		out.Status = code
	}
	if header != nil {
		out.RequestID = requestID(header)
	}
	if raw := jsontext.Value(body); len(raw) > 0 && raw.IsValid() {
		out.Raw = raw
	}
	return out
}

// wireIssue is one entry of a schema failure's issue list.
type wireIssue struct {
	// Path is the way to the offending member, as names and array indices.
	Path []jsontext.Value `json:"path,omitzero"`
	// Message is what is wrong with it.
	Message string `json:"message,omitzero"`
}

// readableMessage turns a schema failure into sentences. OpenRouter's message for
// one is a JSON array of issues inside the string (observed 2026-11-02 on an
// unknown question type, testdata/live_decision_schema_error.json), which reaches
// a person in a log line and a model in a tool result, and both need the member
// and what is wrong with it. Any other message is the server's own wording and is
// left alone; the whole envelope stays on [goodall.APIError].Raw either way.
func readableMessage(message string) string {
	var issues []wireIssue
	if !strings.HasPrefix(strings.TrimSpace(message), "[") || json.Unmarshal([]byte(message), &issues) != nil {
		return message
	}
	sentences := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.Message == "" {
			return message
		}
		sentence := issue.Message
		if path := issuePath(issue.Path); path != "" {
			sentence = path + ": " + sentence
		}
		sentences = append(sentences, sentence)
	}
	if len(sentences) == 0 {
		return message
	}
	return strings.Join(sentences, "; ")
}

// issuePath renders an issue's path as "questions.vibe.type". The elements may be
// names or array indices, so each is its JSON text with a string's quotes removed.
func issuePath(path []jsontext.Value) string {
	parts := make([]string, 0, len(path))
	for _, element := range path {
		var name string
		if json.Unmarshal(element, &name) == nil {
			parts = append(parts, name)
			continue
		}
		parts = append(parts, string(element))
	}
	return strings.Join(parts, ".")
}

// kindFor classifies the error: the documented error_type first, the routing
// message next, and the numeric code last. Anything else leaves the kind empty
// for the transport to fill from the status.
func kindFor(we *wireError, code int, numeric bool) goodall.ErrorKind {
	switch errorType(metadataType(we)) {
	case errContextLength:
		return goodall.KindContextLength
	case errRateLimit:
		return goodall.KindRateLimited
	case errProviderOverloaded:
		return goodall.KindOverloaded
	case errProviderUnavailable, errServer, errTimeout:
		return goodall.KindServer
	case errInvalidImage, errImageTooLarge, errUnsupportedImageFormat:
		return goodall.KindUnsupportedInput
	case errPaymentRequired:
		return goodall.KindUnauthorized
	case errRefusal, errContentPolicy:
		return goodall.KindInvalidRequest
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(we.Message)), modalityRefusalPrefix) {
		return goodall.KindUnsupportedInput
	}
	if numeric {
		return transport.KindForStatus(code)
	}
	return ""
}

// errorTypeOf is the provider's own error type, kept verbatim: the metadata's
// error_type where there is one, and otherwise a non-numeric code, which is
// where an upstream provider's own string lands.
func errorTypeOf(we *wireError) string {
	if t := metadataType(we); t != "" {
		return t
	}
	if text, ok := codeString(we.Code); ok {
		return text
	}
	return ""
}

// metadataType reads error.metadata.error_type, empty when the error carries
// no metadata.
func metadataType(we *wireError) string {
	if we.Metadata == nil {
		return ""
	}
	return we.Metadata.ErrorType
}

// errorCode reads error.code as an HTTP error status. OpenRouter sends the
// status there before the response is committed and an upstream provider's own
// string after, so the caller is told which of the two arrived.
//
// Only a number that is an error status counts. api.openai.com sends "code":
// null, which decodes into an int as 0 with no error, and anything classified
// from a status that is not one comes back as [goodall.KindUnknown]: a kind that
// is set, so the transport would not fill it from the real status.
func errorCode(code jsontext.Value) (int, bool) {
	if code.Kind() != '0' {
		return 0, false
	}
	var n int
	if err := json.Unmarshal(code, &n); err != nil {
		return 0, false
	}
	return n, n >= 400 && n <= 599
}

// codeString reads error.code as a string, for the provider codes that are
// not numbers.
func codeString(code jsontext.Value) (string, bool) {
	if code.Kind() != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(code, &s); err != nil {
		return "", false
	}
	return s, s != ""
}

// requestID reads the identifier that names this call in OpenRouter's logs.
// The header on a live response is x-generation-id; x-request-id is checked
// first because it is the one the API reference documents and the one a proxy
// in front of OpenRouter is likelier to set.
func requestID(header http.Header) string {
	if id := header.Get(headerRequestID); id != "" {
		return id
	}
	return header.Get(headerGenerationID)
}

const (
	// headerRequestID is the documented request identifier header.
	headerRequestID = "X-Request-Id"
	// headerGenerationID is the header OpenRouter actually sets, naming
	// the generation the call produced.
	headerGenerationID = "X-Generation-Id"
)

// errorInBody is the error a 200 response carries, if it carries one: a
// top-level error object, or a choice whose own error or "error" finish reason
// says the turn failed. HTTP 200 is not success, on either path.
func errorInBody(body *chatResponse, header http.Header, raw []byte) *goodall.APIError {
	if body == nil {
		return nil
	}
	if body.Error.said() {
		return inBodyError(body.Error, header, raw)
	}
	for _, c := range body.Choices {
		if c.Error.said() {
			return inBodyError(c.Error, header, raw)
		}
		if c.FinishReason == finishError {
			return bareFinishError(header, raw)
		}
	}
	if len(body.Choices) == 0 {
		return &goodall.APIError{
			Provider:  ProviderName,
			Kind:      goodall.KindUnknown,
			Message:   "the response carried neither a choice nor an error",
			RequestID: requestID(header),
			Raw:       validRaw(raw),
		}
	}
	return nil
}

// finishError is the finish reason of a turn that failed. It usually arrives
// beside an error object, but the object is not promised, and a turn that
// finished in an error did not succeed whether or not it was explained.
const finishError = "error"

// bareFinishError is the error for a choice that finished in an error and
// carried no error object to say why.
func bareFinishError(header http.Header, raw []byte) *goodall.APIError {
	return &goodall.APIError{
		Provider:  ProviderName,
		Kind:      goodall.KindUnknown,
		Type:      finishError,
		Message:   "the turn finished with an error the provider did not describe",
		RequestID: requestID(header),
		Raw:       validRaw(raw),
	}
}

// inBodyError builds the error for a frame or body that arrived inside an HTTP
// 200, where there is no status for the transport to classify from and an
// unplaced error is therefore [goodall.KindUnknown] rather than blank.
func inBodyError(we *wireError, header http.Header, raw []byte) *goodall.APIError {
	out := apiError(we, 0, header, raw)
	if out.Kind == "" {
		out.Kind = goodall.KindUnknown
	}
	return out
}

// validRaw is the body when it is JSON and nothing when it is not, so a
// malformed body never travels as a jsontext.Value that cannot be marshalled.
func validRaw(body []byte) jsontext.Value {
	if raw := jsontext.Value(body); len(raw) > 0 && raw.IsValid() {
		return raw
	}
	return nil
}
