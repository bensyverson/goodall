package typesafe

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/fake"
)

// The adapters in goodall/decision know nothing of a wire. These tests drive each
// one over this package's real client against a real HTTP server, because what
// they are about is the whole path: what a model's call turns into on TypeSafe's
// wire byte for byte, and what that wire turns into for the model to read. The
// adapters' own behavior is tested in goodall/decision over a scripted Decider.

// agentCalling is a scripted parent that calls tool once with input and then
// answers, which is the shortest run that exercises a tool end to end.
func agentCalling(tool goodall.Tool, input string) *goodall.Agent {
	provider := &fake.Provider{Script: []fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("tu_1", tool.Name(), input)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "read the judgment"}),
	}}
	return &goodall.Agent{Provider: provider, Model: "test-model", Tools: []goodall.Tool{tool}}
}

// runEvents drives a run to its end, draining the loop before reporting anything,
// because a t.Fatal inside the body of a range-over-func unwinds the iterator
// from the wrong place.
func runEvents(t *testing.T, agent *goodall.Agent, conv goodall.Conversation, input ...goodall.Block) []goodall.Event {
	t.Helper()
	var events []goodall.Event
	var streamErr error
	for ev, err := range agent.Run(t.Context(), conv, input...) {
		if err != nil {
			streamErr = err
			break
		}
		events = append(events, ev)
	}
	if streamErr != nil {
		t.Fatalf("the run stream yielded an error, which it must never do: %v", streamErr)
	}
	return events
}

// oneToolCallEnd is the run's single ToolCallEnd.
func oneToolCallEnd(t *testing.T, events []goodall.Event) goodall.ToolCallEnd {
	t.Helper()
	var out []goodall.ToolCallEnd
	for _, ev := range events {
		if end, ok := ev.(goodall.ToolCallEnd); ok {
			out = append(out, end)
		}
	}
	if len(out) != 1 {
		t.Fatalf("the run carried %d tool call ends, want exactly one", len(out))
	}
	return out[0]
}

// callOnce runs a scripted parent that calls the tool once and returns the
// ToolCallEnd it produced.
func callOnce(t *testing.T, tool goodall.Tool, input string) goodall.ToolCallEnd {
	t.Helper()
	return oneToolCallEnd(t, runEvents(t, agentCalling(tool, input), goodall.Conversation{}, goodall.Text{Text: "judge this"}))
}

// fixedTool is the developer-fixed judgment tool over the documented questions.
func fixedTool(t *testing.T, client *Client, opts ...decision.ToolOption) goodall.Tool {
	t.Helper()
	tool, err := decision.Tool(client, "judge_ticket", "Judge one support ticket.", documentedQuestions(), opts...)
	if err != nil {
		t.Fatalf("building the judgment tool: %v", err)
	}
	return tool
}

// TestToolSendsTheDocumentedRequest is the request through the tool: byte-identical
// to the one the same questions produce called directly.
func TestToolSendsTheDocumentedRequest(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, mixedBody)
	end := callOnce(t, fixedTool(t, client), `{"state":"`+documentedState+`"}`)

	if end.Result.IsError {
		t.Fatalf("the tool result is an error: %q", end.Result.Text())
	}
	if seen.Requests != 1 {
		t.Fatalf("the server saw %d requests, want one", seen.Requests)
	}
	if got := string(seen.Body); got != documentedBody {
		t.Errorf("the request body is\n%s\nwant\n%s", got, documentedBody)
	}
}

// TestToolSendsAnObjectStateAsGiven is the other half of the state contract: a
// model that hands over a record sends the record, member order and all, rather
// than a string of JSON.
func TestToolSendsAnObjectStateAsGiven(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, mixedBody)
	const state = `{"subject":"Payouts failing","body":"Three days now."}`
	end := callOnce(t, fixedTool(t, client), `{"state":`+state+`}`)

	if end.Result.IsError {
		t.Fatalf("the tool result is an error: %q", end.Result.Text())
	}
	if want := `{"state":` + state + `,`; !strings.HasPrefix(string(seen.Body), want) {
		t.Errorf("the request body starts %q, want the state as given: %q", string(seen.Body), want)
	}
}

// TestToolResultIsTheNeutralFormOfTheWiresAnswer is the seam in one assertion: the
// wire says noul, and the model reads a predicate that decodes back into typed
// answers.
func TestToolResultIsTheNeutralFormOfTheWiresAnswer(t *testing.T) {
	client, _ := serveJSON(t, http.StatusOK, mixedBody)
	end := callOnce(t, fixedTool(t, client), `{"state":"anything"}`)

	text := end.Result.Text()
	if strings.Contains(text, "noul") {
		t.Errorf("the model reads the wire's word:\n%s", text)
	}
	var answers decision.Answers
	if err := json.Unmarshal([]byte(text), &answers); err != nil {
		t.Fatalf("the tool result does not decode back into Answers: %v\n%s", err, text)
	}
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	if predicate.Probability != 0.96 {
		t.Errorf("is_urgent = %v, want 0.96", predicate.Probability)
	}
	if want := (goodall.Usage{Input: 448, Output: 73}); end.Usage != want {
		t.Errorf("ToolCallEnd usage = %+v, want the judgment's own %+v", end.Usage, want)
	}
	if end.Cost.Reported {
		t.Errorf("ToolCallEnd cost = %+v, want unreported: TypeSafe publishes no price on the response", end.Cost)
	}
}

// TestToolReportsARefusalCarryingTheAPIsMessage keeps a rejected judgment in the
// model's hands, and keeps the words the model reads the API's own: a Go error's
// wrapping would tell it about goodall instead of about the request.
func TestToolReportsARefusalCarryingTheAPIsMessage(t *testing.T) {
	client, _ := serveJSON(t, http.StatusUnprocessableEntity, string(fixture(t, "live_refused.json")))
	end := callOnce(t, fixedTool(t, client), `{"state":"anything"}`)

	if !end.Result.IsError {
		t.Fatalf("a refused judgment is not an error result: %q", end.Result.Text())
	}
	text := end.Result.Text()
	if !strings.Contains(text, "body.questions.vibe") {
		t.Errorf("the error text %q does not carry the API's message, which names the member", text)
	}
	if strings.Contains(text, "invalid_request") {
		t.Errorf("the error text %q carries goodall's own wrapping rather than the API's message", text)
	}
}

// TestWithModelReachesTheWire is why the option exists: a pinned model is what
// the request says, on every call.
func TestWithModelReachesTheWire(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, mixedBody)
	callOnce(t, fixedTool(t, client, decision.WithModel("jev-1.13.0")), `{"state":"anything"}`)

	if want := `"model":"jev-1.13.0"`; !strings.Contains(string(seen.Body), want) {
		t.Errorf("the request body %s does not carry %s", seen.Body, want)
	}
}

// authoredCall is a call a model could write: all three question types, in an
// order that is not the order the package declares them in.
const authoredCall = `{"state":{"ticket":"payouts failing"},"questions":[` +
	`{"id":"is_urgent","type":"predicate","instructions":"Is work or money blocked right now?",` +
	`"true":"Blocked right now","false":"Can wait"},` +
	`{"id":"department","type":"choice","instructions":"Which team should handle this?",` +
	`"options":[{"key":"billing","description":"Payments, invoicing, refunds"},{"key":"other","description":"None of the above"}]},` +
	`{"id":"frustration","type":"score","instructions":"How frustrated is the customer?",` +
	`"levels":["Calm","Furious"]}]}`

// authoredBody is the request those questions must produce: the state as given,
// the questions as the questions object in the model's own order, each in the
// wire shape its type takes.
const authoredBody = `{"state":{"ticket":"payouts failing"},` +
	`"model":"jev-latest",` +
	`"questions":{` +
	`"is_urgent":{"type":"noul","instructions":"Is work or money blocked right now?",` +
	`"criteria":{"true":"Blocked right now","false":"Can wait"}},` +
	`"department":{"type":"choice","instructions":"Which team should handle this?",` +
	`"criteria":{"billing":"Payments, invoicing, refunds","other":"None of the above"}},` +
	`"frustration":{"type":"score","instructions":"How frustrated is the customer?",` +
	`"criteria":["Calm","Furious"]}}}`

// TestAuthoredToolSendsTheModelsQuestionsOnTheWire is the rename's byte guard:
// the model writes predicate, and the wire still says what it always said.
func TestAuthoredToolSendsTheModelsQuestionsOnTheWire(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, mixedBody)
	tool, err := decision.AuthoredTool(client, "judge")
	if err != nil {
		t.Fatalf("building the authored judgment tool: %v", err)
	}
	end := callOnce(t, tool, authoredCall)

	if end.Result.IsError {
		t.Fatalf("the tool result is an error: %q", end.Result.Text())
	}
	if got := string(seen.Body); got != authoredBody {
		t.Errorf("the request body is\n%s\nwant\n%s", got, authoredBody)
	}
}

// routingHook is a Route asking one question of the client, whose callback counts
// its calls.
func routingHook(client *Client, called *int) func(context.Context, *goodall.Request, *goodall.Message) error {
	return decision.Route(client, decision.Questions{
		{ID: "is_urgent", Question: decision.Predicate{Instructions: "Does this convey urgency?"}},
	}, func(context.Context, *goodall.Request, *goodall.Message, *decision.Answers) error {
		*called++
		return nil
	})
}

// TestRouteSendsTheTurnsTextAsTheState is the routing path on the wire: the new
// turn is the state, and no earlier turn goes to the judge.
func TestRouteSendsTheTurnsTextAsTheState(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, okAnswers)
	called := 0
	provider := &fake.Provider{Script: []fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "answered"})}}
	agent := &goodall.Agent{Provider: provider, Model: "cheap-model", Hooks: goodall.Hooks{BeforeSend: routingHook(client, &called)}}
	history := goodall.Conversation{}.Append(
		goodall.UserMessage(goodall.Text{Text: "hello"}),
		goodall.AssistantMessage(goodall.Text{Text: "hi"}),
	)

	runEvents(t, agent, history, goodall.Text{Text: "my payouts are failing"})
	if called != 1 {
		t.Errorf("the callback was called %d times, want once", called)
	}
	if want := `"state":"my payouts are failing"`; !strings.Contains(string(seen.Body), want) {
		t.Errorf("the judge was sent %s, want %s", seen.Body, want)
	}
	if strings.Contains(string(seen.Body), "hello") {
		t.Errorf("the judge was sent an earlier turn: %s", seen.Body)
	}
}

// TestRouteFailsClosedOnARecordedRefusal is the default over a real 422: the run
// ends with the API's own words on it and the turn is never sent.
func TestRouteFailsClosedOnARecordedRefusal(t *testing.T) {
	client, _ := serveJSON(t, http.StatusUnprocessableEntity, string(fixture(t, "live_refused.json")))
	called := 0
	provider := &fake.Provider{Script: []fake.Turn{fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "answered"})}}
	agent := &goodall.Agent{Provider: provider, Model: "cheap-model", Hooks: goodall.Hooks{BeforeSend: routingHook(client, &called)}}

	events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "route this"})
	stopped, ok := events[len(events)-1].(goodall.Stopped)
	if !ok {
		t.Fatalf("the run ended with %T, want Stopped", events[len(events)-1])
	}
	if !strings.Contains(stopped.Message, "body.questions.vibe") {
		t.Errorf("the stop message %q does not carry the API's message", stopped.Message)
	}
	if called != 0 || provider.Calls() != 0 {
		t.Errorf("the callback ran %d times and the provider %d; want neither on a refused judgment", called, provider.Calls())
	}
}
