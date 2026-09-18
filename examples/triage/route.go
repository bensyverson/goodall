package main

import (
	"context"
	"sync"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/typesafe"
)

// router judges what the person asked for, once, and keeps the decision for
// the rest of the run.
//
// The judgment happens on the first turn, because that is the only turn that
// carries the person's own words: every later turn's new message is tool
// results, which a text-only judge has nothing to read in. The decision is
// then applied to each turn, because a model or a tool list that changed
// halfway through a run would break the cached prefix the whole conversation
// is built on and would quietly hand a narrowed turn a tool it could not call
// before.
//
// Holding a decision means a router belongs to one run, which is why
// [newAgent] builds one per run; a consumer serving many runs from one agent
// keys the decision by the run instead.
type router struct {
	// route is the typesafe.Route hook that asks the judge and calls
	// decide.
	route func(context.Context, *goodall.Request, *goodall.Message) error

	mu sync.Mutex
	// decided is whether a judgment has shaped this run yet.
	decided bool
	// model is the model every turn of this run goes to.
	model string
	// tools is what every turn of this run may call.
	tools []goodall.Tool
}

// newRouter builds the run's router over the whole tool set.
//
// Two decisions are policy and live here rather than with the judge. A
// judgment under routeFloor leaves the turn as the agent was built — the
// capable default — because narrowing on a guess takes away a tool the person
// may have been asking for. And a judgment that fails ends the run, which is
// typesafe.Route's own default: carrying on unrouted would send every turn to
// whichever model the agent was built with and say nothing about it, and a
// bill is a poor way to find out that the routing stopped.
func newRouter(d deps, tools []goodall.Tool) *router {
	r := &router{}
	r.route = typesafe.Route(d.Judge, routingQuestions(),
		func(ctx context.Context, req *goodall.Request, _ *goodall.Message, answers *typesafe.Answers) error {
			answer, err := readIntent(answers)
			if err != nil {
				return err
			}
			if answer.Confidence < routeFloor {
				return nil
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			r.decided = true
			switch intent(answer.Choice) {
			case intentSummary:
				// A question about the inbox needs the two tools
				// that read it and the middle model, not the
				// writing cascade.
				r.model, r.tools = d.Models.Parent, readingTools(tools)
			case intentDrafts:
				// Writing replies is the expensive job: the
				// strong model orchestrates it, with every tool.
				r.model, r.tools = d.Models.Strong, tools
			}
			return nil
		},
		typesafe.WithRouteModel(d.JudgeModel))
	return r
}

// beforeSend is the agent's Hooks.BeforeSend: judge the turn if it carries the
// person's words, then shape this turn the way the run was routed.
func (r *router) beforeSend(ctx context.Context, req *goodall.Request, newTurn *goodall.Message) error {
	if err := r.route(ctx, req, newTurn); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.decided {
		return nil
	}
	req.Model = r.model
	req.Tools = r.tools
	return nil
}

// readingTools is the subset a summary turn needs: the two that read the inbox
// and judge it. A turn that is not writing replies has no use for a writer,
// and a tool that is not sent is one the model cannot be tempted by.
func readingTools(tools []goodall.Tool) []goodall.Tool {
	var out []goodall.Tool
	for _, tool := range tools {
		switch tool.Name() {
		case toolListInbox, toolTriage:
			out = append(out, tool)
		}
	}
	return out
}
