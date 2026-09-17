package typesafe

import (
	"context"
	"fmt"

	"github.com/bensyverson/goodall"
)

// Decide is the consumer callback a [Route] hands its answers to: one typed
// judgment of the turn that is about to be sent, and a request to shape with
// it.
//
// It may change req.Model, req.Tools, req.System, anything else on the request,
// and newTurn's blocks, which is the supported way to shape a turn; an error
// refuses the turn and ends the run with [goodall.StopCauseHook], carrying the
// error's message. req.Messages is empty while it runs — the loop fills it in
// afterwards — so a Decide shapes what is about to be said and nothing that was
// already said.
type Decide func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message, answers *Answers) error

// RouteOption configures a [Route].
type RouteOption func(*routeConfig)

// routeFailure is what a [Route] does when the judgment itself fails: the
// request was refused, the network was down, the key was wrong. It is a typed
// constant rather than a bool because the two behaviors are a policy a reader
// should find named in the code, and because the zero value has to be the
// careful one.
type routeFailure int

const (
	// routeFailClosed ends the run with the cause visible.
	routeFailClosed routeFailure = iota
	// routeFallThrough lets the turn proceed unrouted.
	routeFallThrough
)

// routeConfig is the settled options of one route.
type routeConfig struct {
	// model is the model the judgment asks for, empty for the client's
	// default.
	model string
	// state builds the state from the new turn, nil for the turn's text.
	state func(newTurn *goodall.Message) any
	// onFailure is what a failed judgment does to the turn.
	onFailure routeFailure
}

// WithRouteModel pins the model the routing judgment is answered by,
// overriding [DefaultModel]. It is the route-level spelling of [WithModel],
// which configures one call rather than a construction.
func WithRouteModel(model string) RouteOption {
	return func(cfg *routeConfig) { cfg.model = model }
}

// WithState builds the state from the new turn, in place of the turn's text.
// It is for a router whose questions are about more than the words — the
// customer's plan, the tickets already open, what the last turn did — and the
// value is marshaled as JSON exactly as [Client.Ask] would marshal it.
//
// Returning nil skips the turn: no judgment is asked for and the [Decide]
// callback is not called, which is how a consumer says "there is nothing here
// to route".
//
// It never sees the conversation, only the turn about to be sent. Earlier turns
// are the model's history and not the router's business (invariant 1), and
// whatever a consumer adds here is data the judge reads rather than instructions
// it follows — the package comment says why that matters.
func WithState(state func(newTurn *goodall.Message) any) RouteOption {
	return func(cfg *routeConfig) { cfg.state = state }
}

// WithFallthrough lets a turn proceed unrouted when the judgment fails,
// instead of ending the run.
//
// It is the right call when routing is an optimization — picking the cheaper
// model, narrowing a tool list that is fine unnarrowed — and the wrong one when
// the routing is a policy, which is why it is opt-in: see [Route] for the
// argument.
func WithFallthrough() RouteOption {
	return func(cfg *routeConfig) { cfg.onFailure = routeFallThrough }
}

// Route builds a [goodall.Hooks.BeforeSend] hook that judges each new turn and
// hands the answers to decide, which shapes the request that is about to be
// sent: the model it goes to, the tools it may call, the system prose it
// carries, the blocks of the turn itself, or an error that refuses it.
//
// It is the intent-routing pattern, and it needs no change to the loop: the
// hook already runs before anything is sent, with the new turn in hand and
// req.Messages still empty. A Route reads only that turn — its text, or
// whatever [WithState] builds from it — so a cheap judgment sits in front of an
// expensive conversation without the history ever going to the judge.
//
// A turn with nothing to judge is skipped: decide is not called and the hook
// does nothing. That is a nil newTurn, which is how a run continuing from a
// conversation that already ends in a user message arrives, a turn with no
// text, and a [WithState] func that returned nil.
//
// A failed judgment ends the run by default, with [goodall.StopCauseHook] and
// the cause in the message. The alternative —
// carrying on unrouted — is the classic quiet failure: every turn would go to
// whichever model the agent was built with, the routing would be silently gone,
// and the only symptom would be a bill or a quality drop that nothing connects
// back to the judge. A consumer whose routing is an optimization rather than a
// policy says so with [WithFallthrough].
//
// The hook returns rather than accepts a definition mistake, since a
// [goodall.Hooks] field has nowhere to put an error: a nil client, an empty or
// unaskable question set, or a nil decide make a hook that ends the first run
// it is used on with that mistake as its message, which is where a developer
// will see it.
//
//	agent.Hooks.BeforeSend = typesafe.Route(client, routing,
//	    func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message, answers *typesafe.Answers) error {
//	        intent, err := answers.Choice("intent")
//	        if err != nil {
//	            return err
//	        }
//	        if intent.Choice == "hard" && intent.Confidence > 0.8 {
//	            req.Model = "claude-opus-5"
//	        }
//	        return nil
//	    })
func Route(client *Client, questions Questions, decide Decide, opts ...RouteOption) func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message) error {
	var cfg routeConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := routeDefinition(client, questions, decide); err != nil {
		return func(context.Context, *goodall.Request, *goodall.Message) error { return err }
	}
	var ask []AskOption
	if cfg.model != "" {
		ask = []AskOption{WithModel(cfg.model)}
	}
	fixed := make(Questions, len(questions))
	copy(fixed, questions)
	return func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message) error {
		state := cfg.stateOf(newTurn)
		if state == nil {
			return nil
		}
		answers, err := client.Ask(ctx, state, fixed, ask...)
		if err != nil {
			if cfg.onFailure == routeFallThrough {
				return nil
			}
			return fmt.Errorf("typesafe: the turn was not routed: %w", err)
		}
		return decide(ctx, req, newTurn, answers)
	}
}

// stateOf is the state one turn is judged on, or nil for a turn with nothing to
// judge.
func (cfg routeConfig) stateOf(newTurn *goodall.Message) any {
	if newTurn == nil {
		return nil
	}
	if cfg.state != nil {
		return cfg.state(newTurn)
	}
	// Message.Text is the turn's text blocks and nothing else: an image, a
	// document or a tool result in the turn is not state a text-only judge
	// can read, and a consumer who wants those summarized builds the state
	// with WithState.
	if text := newTurn.Text(); text != "" {
		return text
	}
	return nil
}

// routeDefinition reports a Route that could never route anything.
func routeDefinition(client *Client, questions Questions, decide Decide) error {
	if client == nil {
		return invalid("this route has no client to ask")
	}
	if decide == nil {
		return invalid("this route has no decide callback, so a judgment would change nothing")
	}
	if err := questions.validate(); err != nil {
		return fmt.Errorf("typesafe: this route has nothing to route on: %w", err)
	}
	return nil
}
