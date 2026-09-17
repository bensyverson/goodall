package typesafe

import (
	json "encoding/json/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// The adapter tests drive a real agent loop over internal/fake against a real
// HTTP server, because what they are about is the seam between the two: what a
// model's call turns into on the wire, and what the wire turns into for the
// model to read.

// callTurn scripts one model turn that calls a tool with the input written as
// JSON text, the way a model writes it.
func callTurn(name, input string) fake.Turn {
	return fake.Answer(goodall.StopToolUse, fake.Use("tu_1", name, input))
}

// agentCalling is a scripted parent that calls tool once with input and then
// answers, which is the shortest run that exercises a tool end to end.
func agentCalling(tool goodall.Tool, input string) (*goodall.Agent, *fake.Provider) {
	provider := &fake.Provider{Script: []fake.Turn{
		callTurn(tool.Name(), input),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: "read the judgment"}),
	}}
	return &goodall.Agent{Provider: provider, Model: "test-model", Tools: []goodall.Tool{tool}}, provider
}

// runEvents drives a run to its end, failing the test if the stream yielded an
// error, which it never should: the terminal event is the contract. The loop is
// drained before anything is reported, because a t.Fatal inside the body of a
// range-over-func unwinds the iterator from the wrong place.
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
	if len(events) == 0 {
		t.Fatal("the run emitted no events at all")
	}
	return events
}

// oneToolResult is the run's single tool result, which is what the model reads
// on its next turn.
func oneToolResult(t *testing.T, events []goodall.Event) goodall.ToolResult {
	t.Helper()
	var out []goodall.ToolResult
	for _, ev := range events {
		if end, ok := ev.(goodall.ToolCallEnd); ok {
			out = append(out, end.Result)
		}
	}
	if len(out) != 1 {
		t.Fatalf("the run carried %d tool results, want exactly one", len(out))
	}
	return out[0]
}

// oneToolCallEnd is the run's single ToolCallEnd, which is where what the call
// spent travels.
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

// judgeOneCall runs a scripted parent that calls the tool once and returns the
// tool result the model was given.
func judgeOneCall(t *testing.T, tool goodall.Tool, input string) goodall.ToolResult {
	t.Helper()
	agent, _ := agentCalling(tool, input)
	return oneToolResult(t, runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "judge this"}))
}

// fixedTool is the developer-fixed judgment tool over the documented questions,
// built against the given client.
func fixedTool(t *testing.T, client *Client, opts ...ToolOption) goodall.Tool {
	t.Helper()
	tool, err := Tool(client, "judge_ticket", "Judge one support ticket.", documentedQuestions(), opts...)
	if err != nil {
		t.Fatalf("building the judgment tool: %v", err)
	}
	return tool
}

// TestToolSendsTheFixedQuestionsAndTheStateAsGiven is the whole point of the
// fixed-questions tool: the model supplies the state and nothing else, and the
// request is byte-identical to the one the same questions produce through Ask.
func TestToolSendsTheFixedQuestionsAndTheStateAsGiven(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, mixedBody)
	result := judgeOneCall(t, fixedTool(t, client), `{"state":"`+documentedState+`"}`)

	if result.IsError {
		t.Fatalf("the tool result is an error: %q", result.Text())
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
	result := judgeOneCall(t, fixedTool(t, client), `{"state":`+state+`}`)

	if result.IsError {
		t.Fatalf("the tool result is an error: %q", result.Text())
	}
	if want := `{"state":` + state + `,`; !strings.HasPrefix(string(seen.Body), want) {
		t.Errorf("the request body starts %q, want the state as given: %q", string(seen.Body), want)
	}
}

// TestToolResultDecodesBackIntoAnswers is what [goodall.JSONResult] buys: the
// text the model reads is the answer set, and a consumer watching the run can
// read it back as typed answers instead of parsing prose.
func TestToolResultDecodesBackIntoAnswers(t *testing.T) {
	client, _ := serveJSON(t, http.StatusOK, mixedBody)
	result := judgeOneCall(t, fixedTool(t, client), `{"state":"anything"}`)

	if result.IsError {
		t.Fatalf("the tool result is an error: %q", result.Text())
	}
	var answers Answers
	if err := json.Unmarshal([]byte(result.Text()), &answers); err != nil {
		t.Fatalf("the tool result does not decode back into Answers: %v\n%s", err, result.Text())
	}
	if got, want := answers.IDs(), []string{"department", "is_urgent", "frustration"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the answers carry %v, want %v in that order", got, want)
	}
	choice, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if choice.Choice != "billing" {
		t.Errorf("department = %q, want billing", choice.Choice)
	}
	if _, err := answers.Score("frustration"); err != nil {
		t.Errorf("Score: %v", err)
	}
}

// TestToolReportsWhatTheJudgmentSpent is the per-call accounting: the judge's
// own tokens reach the parent's stream on the call that spent them, so a
// consumer can bill a judgment it never sent itself. The cost is TypeSafe's
// silence, not a zero: a figure nobody reported must not read as free.
func TestToolReportsWhatTheJudgmentSpent(t *testing.T) {
	client, _ := serveJSON(t, http.StatusOK, mixedBody)
	agent, _ := agentCalling(fixedTool(t, client), `{"state":"anything"}`)
	end := oneToolCallEnd(t, runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "judge this"}))

	if end.Result.IsError {
		t.Fatalf("the tool result is an error: %q", end.Result.Text())
	}
	if want := (goodall.Usage{Input: 448, Output: 73}); end.Usage != want {
		t.Errorf("ToolCallEnd usage = %+v, want the judgment's own %+v", end.Usage, want)
	}
	if end.Cost.Reported {
		t.Errorf("ToolCallEnd cost = %+v, want an unreported cost: TypeSafe publishes no price on the response", end.Cost)
	}
}

// TestToolReportsNothingWhileItRuns is the other half of the interface the
// judgment tools implement: they have tokens to declare and no progress to
// show, so the parent's stream carries no nested events at all.
func TestToolReportsNothingWhileItRuns(t *testing.T) {
	client, _ := serveJSON(t, http.StatusOK, mixedBody)
	agent, _ := agentCalling(fixedTool(t, client), `{"state":"anything"}`)
	events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "judge this"})

	for _, ev := range events {
		if nested, ok := ev.(goodall.ToolEvent); ok {
			t.Errorf("the judgment reported %s while it ran", nested.Event.Type())
		}
	}
}

// TestToolReportsARefusalAsAnErrorResultCarryingTheAPIsMessage keeps a failed
// judgment in the model's hands, and keeps the words the model reads the API's
// own: a Go error's wrapping would tell it about goodall instead of about the
// request.
func TestToolReportsARefusalAsAnErrorResultCarryingTheAPIsMessage(t *testing.T) {
	client, _ := serveJSON(t, http.StatusUnprocessableEntity, string(fixture(t, "live_refused.json")))
	result := judgeOneCall(t, fixedTool(t, client), `{"state":"anything"}`)

	if !result.IsError {
		t.Fatalf("a refused judgment is not an error result: %q", result.Text())
	}
	text := result.Text()
	if !strings.Contains(text, "body.questions.vibe") {
		t.Errorf("the error text %q does not carry the API's message, which names the member", text)
	}
	if strings.Contains(text, "typesafe: invalid_request") {
		t.Errorf("the error text %q carries goodall's own wrapping rather than the API's message", text)
	}
}

// TestWithToolModelPinsTheModelForEveryCall is why the option exists: a caller
// whose thresholds are tuned against one version pins it once, at the tool,
// rather than hoping every call site remembers.
func TestWithToolModelPinsTheModelForEveryCall(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, mixedBody)
	judgeOneCall(t, fixedTool(t, client, WithToolModel("jev-1.13.0")), `{"state":"anything"}`)

	if want := `"model":"jev-1.13.0"`; !strings.Contains(string(seen.Body), want) {
		t.Errorf("the request body %s does not carry %s", seen.Body, want)
	}
}

// TestToolSchemaIsOneRequiredState is what the model sees: one member, required,
// described, and no second way to say the same thing.
func TestToolSchemaIsOneRequiredState(t *testing.T) {
	client, _ := serveJSON(t, http.StatusOK, mixedBody)
	schema := fixedTool(t, client).Schema()

	if got, want := len(schema.Properties), 1; got != want {
		t.Fatalf("the schema has %d properties, want %d: %+v", got, want, schema.Properties)
	}
	if got := schema.Properties[0].Name; got != "state" {
		t.Errorf("the one property is %q, want state", got)
	}
	if got, want := strings.Join(schema.Required, ","), "state"; got != want {
		t.Errorf("required = %q, want %q", got, want)
	}
	if schema.Properties[0].Schema.Description == "" {
		t.Error("the state property has no description, so the model must guess what to send")
	}
}

// TestToolRefusesADefinitionItCannotServe keeps every mistake a developer can
// make at construction, where they see it, rather than inside a run.
func TestToolRefusesADefinitionItCannotServe(t *testing.T) {
	client, _ := serveJSON(t, http.StatusOK, mixedBody)
	cases := map[string]struct {
		client    *Client
		name      string
		desc      string
		questions Questions
	}{
		"no client":      {nil, "judge", "Judge a ticket.", documentedQuestions()},
		"no name":        {client, "", "Judge a ticket.", documentedQuestions()},
		"no description": {client, "judge", "", documentedQuestions()},
		"no questions":   {client, "judge", "Judge a ticket.", nil},
		"bad question":   {client, "judge", "Judge a ticket.", Questions{{ID: "x", Question: Score{Instructions: "rate", Levels: []string{"only"}}}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tool, err := Tool(c.client, c.name, c.desc, c.questions)
			if err == nil {
				t.Fatalf("Tool accepted a definition with %s and returned %v", name, tool)
			}
		})
	}
}
