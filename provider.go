package goodall

import "context"

// Provider is one model API goodall can talk to. Streaming is the only
// required path: a blocking call is Collect over the stream, so both paths
// share one accumulator and one result type by construction, and a consumer
// who never streams still gets the partial answer when a call is cut short.
//
// A Provider holds no per-call state, so one value serves every goroutine in
// a process; the Stream it returns is single-consumer.
type Provider interface {
	// Stream sends the request and yields the events it produces. The
	// stream owns the HTTP response: ranging to the end, or breaking out
	// early, closes it. Cancelling ctx ends the stream.
	Stream(ctx context.Context, req *Request) Stream
}

// Completer is the optional non-streaming path, for the cases where streaming
// is refused or pointless — a zero-token cache pre-warm, a provider endpoint
// that has no streaming form. It yields the same Response that Collect does.
type Completer interface {
	// Complete sends the request and waits for the whole answer.
	Complete(ctx context.Context, req *Request) (*Response, error)
}

// ModelLister is the optional capability lookup. A provider that implements
// it lets the agent refuse a request the model is known not to accept before
// the network call, with a *CapabilityError naming the fact.
//
// The agent calls Model once at the start of every run, so an implementation
// should memoise what it read rather than fetching it each time; both
// built-in providers do, per identifier and for successes only.
type ModelLister interface {
	// Model describes one model by its provider identifier. The value it
	// returns may be shared between callers, so a caller must not modify
	// it.
	Model(ctx context.Context, id string) (*ModelInfo, error)
}
