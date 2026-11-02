package typesafe

import (
	"encoding/json/jsontext"
	"net/http"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/systemone"
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

// decodeError is this package's [transport.ErrorDecoder]. The API reference says
// only that an error body is JSON describing what went wrong and that a 422
// details the offending field, so the message comes from the wire's tolerant
// reader. A body that holds no sentence still becomes an error, classified by its
// status, with the body verbatim on Raw; the transport supplies the provider, the
// status, the request id and Retry-After, and kindFor supplies the kind.
func decodeError(status int, header http.Header, body []byte) *goodall.APIError {
	apiErr := &goodall.APIError{
		Provider: ProviderName,
		Status:   status,
		Kind:     kindFor(status),
		Message:  systemone.ErrorMessage(body),
	}
	if raw := jsontext.Value(body); raw.IsValid() {
		apiErr.Raw = raw
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(status)
	}
	return apiErr
}
