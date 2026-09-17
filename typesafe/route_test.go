package typesafe

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// routingQuestions is the one question a router asks: which way this turn goes.
func routingQuestions() Questions {
	return Questions{
		{ID: "intent", Question: Choice{
			Instructions: "What does this turn need?",
			Options: []Option{
				{Key: "chat", Description: "Small talk, or a question the cheap model answers"},
				{Key: "escalate", Description: "A hard problem that needs the flagship"},
			},
		}},
	}
}

// routingAnswer answers that question with escalate, which is what the decide
// callback in these tests acts on.
const routingAnswer = `{"model":"jev-1.13.0","answers":{` +
	`"intent":{"type":"choice","choice":"escalate","confidence":0.91,"probabilities":{"escalate":0.95,"chat":0.05}}},` +
	`"usage":{"input_tokens":40,"output_tokens":8}}`

// escalateModel is the model the router swaps in.
const escalateModel = "flagship-model"

// escalate is the consumer callback: one typed answer in, a shaped request out.
func escalate(t *testing.T, called *int) func(context.Context, *goodall.Request, *goodall.Message, *Answers) error {
	t.Helper()
	return func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message, answers *Answers) error {
		*called++
		if len(req.Messages) != 0 {
			t.Errorf("decide saw %d messages on the request; BeforeSend runs before the loop fills them in", len(req.Messages))
		}
		intent, err := answers.Choice("intent")
		if err != nil {
			return err
		}
		if intent.Choice == "escalate" {
			req.Model = escalateModel
			req.Tools = nil
		}
		return nil
	}
}

// routedAgent is an agent whose BeforeSend hook is a Route over the routing
// question.
func routedAgent(t *testing.T, client *Client, decide func(context.Context, *goodall.Request, *goodall.Message, *Answers) error, opts ...RouteOption) (*goodall.Agent, *fake.Provider) {
	t.Helper()
	provider := &fake.Provider{Script: []fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "answered"}),
	}}
	agent := &goodall.Agent{
		Provider: provider,
		Model:    "cheap-model",
		Tools:    []goodall.Tool{echoTool(t)},
		Hooks:    goodall.Hooks{BeforeSend: Route(client, routingQuestions(), decide, opts...)},
	}
	return agent, provider
}

// echoTool is a tool the routing tests can watch a decide callback take away.
func echoTool(t *testing.T) goodall.Tool {
	t.Helper()
	tool, err := goodall.NewTool("echo", "Echo the text back to the model.", func(ctx context.Context, in struct {
		Text string `json:"text" desc:"the text to echo"`
	}) (goodall.ToolResult, error) {
		return goodall.TextResult(in.Text), nil
	})
	if err != nil {
		t.Fatalf("building the echo tool: %v", err)
	}
	return tool
}

// history is a conversation that already has a turn in it, which is what makes
// "the router never touches earlier turns" a statement about something.
func history() goodall.Conversation {
	return goodall.Conversation{}.Append(
		goodall.UserMessage(goodall.Text{Text: "hello"}),
		goodall.AssistantMessage(goodall.Text{Text: "hi"}),
	)
}

// TestRouteShapesTheRequestFromATypedAnswer is the routing pattern itself: the
// new turn goes to the judge, the judge's answer picks the model, and the
// conversation is exactly what it was.
func TestRouteShapesTheRequestFromATypedAnswer(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, routingAnswer)
	called := 0
	agent, provider := routedAgent(t, client, escalate(t, &called))

	events := runEvents(t, agent, history(), goodall.Text{Text: "my payouts have been failing for three days"})
	if _, ok := events[len(events)-1].(goodall.Done); !ok {
		t.Fatalf("the run ended with %T, want Done", events[len(events)-1])
	}
	if called != 1 {
		t.Errorf("decide was called %d times, want once per turn", called)
	}
	if seen.Requests != 1 {
		t.Fatalf("the judge saw %d requests, want one", seen.Requests)
	}
	if want := `"state":"my payouts have been failing for three days"`; !strings.Contains(string(seen.Body), want) {
		t.Errorf("the judge was sent %s, want the new turn's text as the state: %s", seen.Body, want)
	}
	if strings.Contains(string(seen.Body), "hello") {
		t.Errorf("the judge was sent an earlier turn: %s", seen.Body)
	}

	reqs := provider.Requests()
	if len(reqs) != 1 {
		t.Fatalf("the provider saw %d requests, want one", len(reqs))
	}
	if reqs[0].Model != escalateModel {
		t.Errorf("the provider was sent model %q, want the routed %q", reqs[0].Model, escalateModel)
	}
	if len(reqs[0].Tools) != 0 {
		t.Errorf("the provider was sent %d tools, want the routed none", len(reqs[0].Tools))
	}
	if got, want := len(reqs[0].Messages), 3; got != want {
		t.Fatalf("the provider was sent %d messages, want %d: the two earlier turns plus the new one", got, want)
	}
	if got, want := reqs[0].Messages[0].Text(), "hello"; got != want {
		t.Errorf("the first message reads %q, want the untouched %q", got, want)
	}
	if got, want := reqs[0].Messages[2].Text(), "my payouts have been failing for three days"; got != want {
		t.Errorf("the new turn reads %q, want %q", got, want)
	}
}

// TestRouteSkipsATurnItHasNothingToJudge is the nil contract: a run continuing
// from a conversation that already ends in a user message has no new turn, and
// a router with nothing to read spends nothing and decides nothing.
func TestRouteSkipsATurnItHasNothingToJudge(t *testing.T) {
	cases := map[string]struct {
		conv  goodall.Conversation
		input []goodall.Block
	}{
		"no new turn at all": {
			history().Append(goodall.UserMessage(goodall.Text{Text: "carry on"})),
			nil,
		},
		"a new turn with no text": {
			history(),
			[]goodall.Block{goodall.Text{Text: ""}},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			client, seen := serveJSON(t, http.StatusOK, routingAnswer)
			called := 0
			agent, provider := routedAgent(t, client, escalate(t, &called))

			events := runEvents(t, agent, c.conv, c.input...)
			if _, ok := events[len(events)-1].(goodall.Done); !ok {
				t.Fatalf("the run ended with %T, want Done", events[len(events)-1])
			}
			if seen.Requests != 0 {
				t.Errorf("the judge saw %d requests, want none", seen.Requests)
			}
			if called != 0 {
				t.Errorf("decide was called %d times, want none", called)
			}
			if got := provider.Requests()[0].Model; got != "cheap-model" {
				t.Errorf("the provider was sent model %q, want the agent's own", got)
			}
		})
	}
}

// TestRouteFailsClosedWhenTheJudgeRefuses is the default this package argues
// for: a router that quietly stopped routing would send every turn to the
// wrong model and say nothing, so a failed judgment ends the run with the
// cause in the open.
func TestRouteFailsClosedWhenTheJudgeRefuses(t *testing.T) {
	client, _ := serveJSON(t, http.StatusUnprocessableEntity, string(fixture(t, "live_refused.json")))
	called := 0
	agent, provider := routedAgent(t, client, escalate(t, &called))

	events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "route this"})
	stopped, ok := events[len(events)-1].(goodall.Stopped)
	if !ok {
		t.Fatalf("the run ended with %T, want Stopped", events[len(events)-1])
	}
	if stopped.Cause != goodall.StopCauseHook {
		t.Errorf("the run stopped because %q, want %q", stopped.Cause, goodall.StopCauseHook)
	}
	if !strings.Contains(stopped.Message, "body.questions.vibe") {
		t.Errorf("the stop message %q does not carry the API's message", stopped.Message)
	}
	if called != 0 {
		t.Errorf("decide was called %d times on a failed judgment, want none", called)
	}
	if got := provider.Calls(); got != 0 {
		t.Errorf("the provider was called %d times, want none: the turn was never sent", got)
	}
}

// TestRouteWithFallthroughLetsTheTurnProceedUnrouted is the opt-in for a
// consumer whose routing is an optimization rather than a policy.
func TestRouteWithFallthroughLetsTheTurnProceedUnrouted(t *testing.T) {
	client, _ := serveJSON(t, http.StatusUnprocessableEntity, string(fixture(t, "live_refused.json")))
	called := 0
	agent, provider := routedAgent(t, client, escalate(t, &called), WithFallthrough())

	events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "route this"})
	if _, ok := events[len(events)-1].(goodall.Done); !ok {
		t.Fatalf("the run ended with %T, want Done", events[len(events)-1])
	}
	if called != 0 {
		t.Errorf("decide was called %d times on a failed judgment, want none", called)
	}
	reqs := provider.Requests()
	if len(reqs) != 1 {
		t.Fatalf("the provider saw %d requests, want one", len(reqs))
	}
	if reqs[0].Model != "cheap-model" {
		t.Errorf("the unrouted turn went to %q, want the agent's own model", reqs[0].Model)
	}
}

// TestRouteWithStateSendsWhatTheConsumerBuilt is for a router that needs more
// than the words: the records the question is really about travel with the
// turn, and the judge still sees nothing of the history.
func TestRouteWithStateSendsWhatTheConsumerBuilt(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, routingAnswer)
	called := 0
	state := func(newTurn *goodall.Message) any {
		return struct {
			Turn string `json:"turn"`
			Plan string `json:"plan"`
		}{Turn: newTurn.Text(), Plan: "enterprise"}
	}
	agent, _ := routedAgent(t, client, escalate(t, &called), WithState(state))

	runEvents(t, agent, history(), goodall.Text{Text: "my payouts are failing"})
	if called != 1 {
		t.Errorf("decide was called %d times, want once", called)
	}
	if want := `"state":{"turn":"my payouts are failing","plan":"enterprise"}`; !strings.Contains(string(seen.Body), want) {
		t.Errorf("the judge was sent %s, want %s", seen.Body, want)
	}
}

// TestRouteRefusesABadDefinitionOnTheFirstTurn is the price of a hook that
// cannot return an error at construction: the mistake is reported where it can
// be seen, on the run, rather than by routing nothing forever.
func TestRouteRefusesABadDefinitionOnTheFirstTurn(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, routingAnswer)
	cases := map[string]func(context.Context, *goodall.Request, *goodall.Message) error{
		"no client":    Route(nil, routingQuestions(), func(context.Context, *goodall.Request, *goodall.Message, *Answers) error { return nil }),
		"no questions": Route(client, nil, func(context.Context, *goodall.Request, *goodall.Message, *Answers) error { return nil }),
		"no decide":    Route(client, routingQuestions(), nil),
	}
	for name, hook := range cases {
		t.Run(name, func(t *testing.T) {
			provider := &fake.Provider{Script: []fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "answered"})}}
			agent := &goodall.Agent{Provider: provider, Model: "cheap-model", Hooks: goodall.Hooks{BeforeSend: hook}}

			events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "route this"})
			stopped, ok := events[len(events)-1].(goodall.Stopped)
			if !ok {
				t.Fatalf("the run ended with %T, want Stopped", events[len(events)-1])
			}
			if stopped.Cause != goodall.StopCauseHook {
				t.Errorf("the run stopped because %q, want %q", stopped.Cause, goodall.StopCauseHook)
			}
			if provider.Calls() != 0 {
				t.Errorf("the provider was called %d times, want none", provider.Calls())
			}
			if seen.Requests != 0 {
				t.Errorf("the judge saw %d requests, want none", seen.Requests)
			}
		})
	}
}
