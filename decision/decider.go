package decision

import "context"

// Decider is the seam every decision model sits behind: one blocking call that
// carries a state and typed questions, and returns one typed answer per
// question. It is not a [github.com/bensyverson/goodall.Provider], because there
// is no conversation, no tool call and no stream, and agents reach it through
// [Tool], [AuthoredTool] and [Route] rather than through the loop.
//
// An implementation calls [Request.Validate] before sending, so that a request
// that could never be answered costs nothing. A failing status comes back as a
// *[github.com/bensyverson/goodall.APIError], and a body that is not a
// well-formed answer set as a *[github.com/bensyverson/goodall.ProtocolError];
// the adapters read those two to tell what the model should hear from what the
// loop should report. An implementation holds no per-call state, so one value
// serves every goroutine in a process.
type Decider interface {
	// Decide evaluates the request's state against its questions, all in one
	// call, which is what makes a batch of judgments cheap. It must not
	// modify the request.
	Decide(ctx context.Context, req *Request) (*Answers, error)
}

// Request is one call to a [Decider].
type Request struct {
	// Model names the model that should answer. Empty leaves the choice to
	// the Decider: one may have a default of its own and one may refuse the
	// call, since thresholds do not travel between models and a model picked
	// for the caller would quietly invalidate them.
	Model string
	// State is the material to judge, marshaled as JSON exactly as given: a
	// string for text, a struct or a map for a record, a slice for a list of
	// them. It is data the model reads and not something it defends against,
	// so instructions belong in the questions, never here. What a model makes
	// of an object or an array differs between servers, as the package
	// documentation sets out.
	State any
	// Questions are the typed questions, each judged over the same state.
	Questions Questions
}

// Validate reports a request that could never be answered, so a [Decider] can
// refuse it before any call is made. The error carries
// [github.com/bensyverson/goodall.KindInvalidRequest]. It says nothing about the
// model, whose rules are each Decider's own.
func (r *Request) Validate() error {
	if r == nil {
		return invalid("there is no request")
	}
	if r.State == nil {
		return invalid("a request needs a state to evaluate, and this one is nil")
	}
	return r.Questions.Validate()
}
