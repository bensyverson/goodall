package typesafe

// wireRequest is the body of POST /v1/systemone.
//
// The member order is the order the API reference lists them, and nothing in
// it is a Go map — [Questions] and [Choice.Options] are ordered slices that
// marshal as objects — so two identical requests marshal to identical bytes
// (invariant 8). The one exception is whatever the caller passes as the state,
// which [Client.Ask] marshals deterministically for the same reason.
type wireRequest struct {
	// State is the content to evaluate, as given.
	State any `json:"state"`
	// Model is the model or alias that should answer.
	Model string `json:"model"`
	// Questions are the typed questions, as the questions object.
	Questions Questions `json:"questions"`
}

// wireUsage is the usage object of a response. TypeSafe reports two figures
// and no cost: output tokens are free and input tokens are priced on its
// website, not on the response, so goodall reports the tokens and leaves the
// money unreported rather than inventing it from a price list.
type wireUsage struct {
	// Input is the input tokens the request consumed.
	Input int `json:"input_tokens,omitzero"`
	// Output is the output tokens, which TypeSafe does not charge for.
	Output int `json:"output_tokens,omitzero"`
}
