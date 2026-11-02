package decision

import (
	"context"
	"errors"
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

// routingAnswers answers that question with escalate, which is what the callback
// in these tests acts on.
func routingAnswers() *Answers {
	answers := NewAnswers([]NamedAnswer{{ID: "intent", Answer: ChoiceAnswer{
		Choice:        "escalate",
		Confidence:    0.91,
		Probabilities: map[string]float64{"escalate": 0.95, "chat": 0.05},
	}}})
	answers.Model = "model-1"
	answers.Usage = goodall.Usage{Input: 40, Output: 8}
	return answers
}

// escalateModel is the model the router swaps in.
const escalateModel = "flagship-model"

// escalate is the consumer callback: one typed answer in, a shaped request out.
func escalate(t *testing.T, called *int) RouteFunc {
	t.Helper()
	return func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message, answers *Answers) error {
		*called++
		if len(req.Messages) != 0 {
			t.Errorf("the callback saw %d messages on the request; BeforeSend runs before the loop fills them in", len(req.Messages))
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
func routedAgent(t *testing.T, decider Decider, route RouteFunc, opts ...RouteOption) (*goodall.Agent, *fake.Provider) {
	t.Helper()
	provider := &fake.Provider{Script: []fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "answered"}),
	}}
	agent := &goodall.Agent{
		Provider: provider,
		Model:    "cheap-model",
		Tools:    []goodall.Tool{echoTool(t)},
		Hooks:    goodall.Hooks{BeforeSend: Route(decider, routingQuestions(), route, opts...)},
	}
	return agent, provider
}

// echoTool is a tool the routing tests can watch a callback take away.
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
	decider := &fakeDecider{answers: routingAnswers()}
	called := 0
	agent, provider := routedAgent(t, decider, escalate(t, &called))

	events := runEvents(t, agent, history(), goodall.Text{Text: "my payouts have been failing for three days"})
	if _, ok := events[len(events)-1].(goodall.Done); !ok {
		t.Fatalf("the run ended with %T, want Done", events[len(events)-1])
	}
	if called != 1 {
		t.Errorf("the callback was called %d times, want once per turn", called)
	}
	req := decider.one(t)
	if want := "my payouts have been failing for three days"; req.State != want {
		t.Errorf("the judge was sent %v, want the new turn's text as the state: %s", req.State, want)
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
			decider := &fakeDecider{answers: routingAnswers()}
			called := 0
			agent, provider := routedAgent(t, decider, escalate(t, &called))

			events := runEvents(t, agent, c.conv, c.input...)
			if _, ok := events[len(events)-1].(goodall.Done); !ok {
				t.Fatalf("the run ended with %T, want Done", events[len(events)-1])
			}
			if n := len(decider.seen()); n != 0 {
				t.Errorf("the judge saw %d requests, want none", n)
			}
			if called != 0 {
				t.Errorf("the callback was called %d times, want none", called)
			}
			if got := provider.Requests()[0].Model; got != "cheap-model" {
				t.Errorf("the provider was sent model %q, want the agent's own", got)
			}
		})
	}
}

// TestRouteFailsClosedWhenTheJudgeRejects is the default this package argues
// for: a router that quietly stopped routing would send every turn to the wrong
// model and say nothing, so a failed judgment ends the run with the cause in the
// open.
func TestRouteFailsClosedWhenTheJudgeRejects(t *testing.T) {
	called := 0
	agent, provider := routedAgent(t, &fakeDecider{err: rejected}, escalate(t, &called))

	events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "route this"})
	stopped, ok := events[len(events)-1].(goodall.Stopped)
	if !ok {
		t.Fatalf("the run ended with %T, want Stopped", events[len(events)-1])
	}
	if stopped.Cause != goodall.StopCauseHook {
		t.Errorf("the run stopped because %q, want %q", stopped.Cause, goodall.StopCauseHook)
	}
	if !strings.Contains(stopped.Message, "body.questions.vibe") {
		t.Errorf("the stop message %q does not carry the server's message", stopped.Message)
	}
	if called != 0 {
		t.Errorf("the callback was called %d times on a failed judgment, want none", called)
	}
	if got := provider.Calls(); got != 0 {
		t.Errorf("the provider was called %d times, want none: the turn was never sent", got)
	}
}

// TestRouteFailsClosedOnADeclinedQuestion is the refusal through the third
// adapter: the callback's accessor returns an error naming the question, and
// returning that error ends the run the way a failed judgment does, rather than
// routing on a zero value.
func TestRouteFailsClosedOnADeclinedQuestion(t *testing.T) {
	declined := NewAnswers([]NamedAnswer{{ID: "intent", Answer: RefusalAnswer{}}})
	called := 0
	agent, provider := routedAgent(t, &fakeDecider{answers: declined}, escalate(t, &called))

	events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "route this"})
	stopped, ok := events[len(events)-1].(goodall.Stopped)
	if !ok {
		t.Fatalf("the run ended with %T, want Stopped", events[len(events)-1])
	}
	if stopped.Cause != goodall.StopCauseHook {
		t.Errorf("the run stopped because %q, want %q", stopped.Cause, goodall.StopCauseHook)
	}
	if !strings.Contains(stopped.Message, `"intent"`) {
		t.Errorf("the stop message %q does not name the question", stopped.Message)
	}
	if got := provider.Calls(); got != 0 {
		t.Errorf("the provider was called %d times, want none", got)
	}
}

// TestARouteCanCarryOnPastADeclinedQuestion is the documented way out, for a
// consumer whose routing can do without the one answer.
func TestARouteCanCarryOnPastADeclinedQuestion(t *testing.T) {
	declined := NewAnswers([]NamedAnswer{{ID: "intent", Answer: RefusalAnswer{}}})
	agent, provider := routedAgent(t, &fakeDecider{answers: declined},
		func(_ context.Context, _ *goodall.Request, _ *goodall.Message, answers *Answers) error {
			_, err := answers.Choice("intent")
			if answerErr, ok := errors.AsType[*AnswerError](err); ok && answerErr.Got == TypeRefusal {
				return nil
			}
			return err
		})

	events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "route this"})
	if _, ok := events[len(events)-1].(goodall.Done); !ok {
		t.Fatalf("the run ended with %T, want Done", events[len(events)-1])
	}
	if got := provider.Requests()[0].Model; got != "cheap-model" {
		t.Errorf("the turn went to %q, want the agent's own model", got)
	}
}

// TestRouteWithFallthroughLetsTheTurnProceedUnrouted is the opt-in for a
// consumer whose routing is an optimization rather than a policy.
func TestRouteWithFallthroughLetsTheTurnProceedUnrouted(t *testing.T) {
	called := 0
	agent, provider := routedAgent(t, &fakeDecider{err: rejected}, escalate(t, &called), WithFallthrough())

	events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "route this"})
	if _, ok := events[len(events)-1].(goodall.Done); !ok {
		t.Fatalf("the run ended with %T, want Done", events[len(events)-1])
	}
	if called != 0 {
		t.Errorf("the callback was called %d times on a failed judgment, want none", called)
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
	decider := &fakeDecider{answers: routingAnswers()}
	called := 0
	state := func(newTurn *goodall.Message) any {
		return struct {
			Turn string `json:"turn"`
			Plan string `json:"plan"`
		}{Turn: newTurn.Text(), Plan: "enterprise"}
	}
	agent, _ := routedAgent(t, decider, escalate(t, &called), WithState(state))

	runEvents(t, agent, history(), goodall.Text{Text: "my payouts are failing"})
	if called != 1 {
		t.Errorf("the callback was called %d times, want once", called)
	}
	if got, want := stateOf(t, decider.one(t)), `{"turn":"my payouts are failing","plan":"enterprise"}`; got != want {
		t.Errorf("the judge was sent %s, want %s", got, want)
	}
}

// TestWithModelPinsTheModelOfARoute is the shared option on the third of its
// three adapters.
func TestWithModelPinsTheModelOfARoute(t *testing.T) {
	decider := &fakeDecider{answers: routingAnswers()}
	called := 0
	agent, _ := routedAgent(t, decider, escalate(t, &called), WithModel("model-1.0"))
	runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "route this"})
	if got := decider.one(t).Model; got != "model-1.0" {
		t.Errorf("the judgment asks for %q, want the pinned model", got)
	}
}

// TestRouteRefusesABadDefinitionOnTheFirstTurn is the price of a hook that
// cannot return an error at construction: the mistake is reported where it can
// be seen, on the run, rather than by routing nothing forever.
func TestRouteRefusesABadDefinitionOnTheFirstTurn(t *testing.T) {
	decider := &fakeDecider{answers: routingAnswers()}
	var nilPointer *fakeDecider
	noop := func(context.Context, *goodall.Request, *goodall.Message, *Answers) error { return nil }
	cases := map[string]func(context.Context, *goodall.Request, *goodall.Message) error{
		"no decider":    Route(nil, routingQuestions(), noop),
		"a nil pointer": Route(nilPointer, routingQuestions(), noop),
		"no questions":  Route(decider, nil, noop),
		"no callback":   Route(decider, routingQuestions(), nil),
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
			if n := len(decider.seen()); n != 0 {
				t.Errorf("the judge saw %d requests, want none", n)
			}
		})
	}
}
