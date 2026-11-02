// Package systemone is the System One wire format: how a [decision.Request] is
// written as the body of a POST to a System One route, and how the response is
// read back as a [decision.Answers].
//
// It is shared so that every client speaking this wire reuses one translation
// and no provider package imports another. It knows the wire and nothing about
// which server is at the other end, so its errors name no server, and each
// client keeps its own error-body decoder, which stamps its own provider name.
//
// The wire's spelling stops here. The yes-or-no question is a noul on this wire
// and a [decision.Predicate] everywhere above it.
package systemone

import (
	json "encoding/json/v2"
	"fmt"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
)

// WirePredicate is how this wire spells a yes-or-no question and its answer: the
// value of the type member, and the name of the member holding the answer's
// probability.
const WirePredicate = "noul"

// wireRequest is the request body.
//
// The member order is the order the wire's reference lists them, and nothing in
// it is a Go map, since [Questions] and the options inside it are ordered slices
// that marshal as objects, so two identical requests marshal to identical bytes
// (invariant 8). The one exception is the caller's state, which [EncodeRequest]
// marshals deterministically for the same reason.
type wireRequest struct {
	// State is the content to evaluate, as given.
	State any `json:"state"`
	// Model is the model or alias that should answer.
	Model string `json:"model"`
	// Questions are the typed questions, as the questions object.
	Questions Questions `json:"questions"`
}

// EncodeRequest writes req as the request body. It sends what it is given: a
// default model and the validation of the request are the calling client's.
//
// A state that cannot be marshaled is an error carrying
// [goodall.KindInvalidRequest]. It names no package, so that the calling client
// can prefix its own.
func EncodeRequest(req *decision.Request) ([]byte, error) {
	// Deterministic marshaling is what makes two identical requests
	// identical bytes (invariant 8) even when the caller's state is a map:
	// nothing built here is a map, but the state is the caller's value and
	// json/v2 leaves map order to chance without this.
	body, err := json.Marshal(&wireRequest{
		State:     req.State,
		Model:     req.Model,
		Questions: Questions(req.Questions),
	}, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("%w: the state cannot be marshaled as JSON: %w",
			goodall.KindInvalidRequest, err)
	}
	return body, nil
}
