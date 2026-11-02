package decision

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// The adapter tests drive a real agent loop over internal/fake against a scripted
// Decider, because what they are about is the seam between the two: what a
// model's call turns into for the Decider, and what the Decider's answers turn
// into for the model to read. What each client sends on its own wire is tested
// beside that client.

// fakeDecider answers every call with one scripted outcome and keeps every
// request it was handed.
type fakeDecider struct {
	answers *Answers
	err     error

	mu       sync.Mutex
	requests []*Request
}

// Decide records the request and returns the scripted outcome.
func (d *fakeDecider) Decide(_ context.Context, req *Request) (*Answers, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requests = append(d.requests, req)
	return d.answers, d.err
}

// seen is every request the Decider was handed.
func (d *fakeDecider) seen() []*Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.requests)
}

// one is the single request a test expects.
func (d *fakeDecider) one(t *testing.T) *Request {
	t.Helper()
	seen := d.seen()
	if len(seen) != 1 {
		t.Fatalf("the Decider was handed %d requests, want one", len(seen))
	}
	return seen[0]
}

// rejected is what a server's refusal of the whole call looks like to an adapter.
var rejected = &goodall.APIError{
	Provider: "test-server",
	Status:   422,
	Kind:     goodall.KindInvalidRequest,
	Message:  "body.questions.vibe: this is not a question type",
}

// documentedQuestions is one question of each type, in an order that is not
// alphabetical, so an adapter that reordered them would be caught.
func documentedQuestions() Questions {
	return Questions{
		{ID: "department", Question: Choice{
			Instructions: "Which team should handle this?",
			Options: []Option{
				{Key: "billing", Description: "Payments, invoicing, refunds"},
				{Key: "technical", Description: "Bugs, outages, integrations"},
				{Key: "sales"},
			},
		}},
		{ID: "is_urgent", Question: Predicate{
			Instructions: "Does this convey urgency?",
			True:         "Explicitly time-sensitive",
			False:        "No urgency expressed",
		}},
		{ID: "frustration", Question: Score{
			Instructions: "How frustrated is the customer?",
			Levels:       []string{"Calm", "Frustrated", "Very angry"},
		}},
	}
}

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

// oneToolCallEnd is the run's single ToolCallEnd, which is where what the call
// spent travels and what the model reads next.
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
	return oneToolCallEnd(t, runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "judge this"})).Result
}

// fixedTool is the developer-fixed judgment tool over the documented questions.
func fixedTool(t *testing.T, decider Decider, opts ...ToolOption) goodall.Tool {
	t.Helper()
	tool, err := Tool(decider, "judge_ticket", "Judge one support ticket.", documentedQuestions(), opts...)
	if err != nil {
		t.Fatalf("building the judgment tool: %v", err)
	}
	return tool
}

// stateOf reads a request's state the way a codec would write it.
func stateOf(t *testing.T, req *Request) string {
	t.Helper()
	data, err := json.Marshal(req.State)
	if err != nil {
		t.Fatalf("marshaling the state: %v", err)
	}
	return string(data)
}

// TestToolSendsTheFixedQuestionsAndTheStateAsGiven is the whole point of the
// fixed-questions tool: the model supplies the state and nothing else.
func TestToolSendsTheFixedQuestionsAndTheStateAsGiven(t *testing.T) {
	decider := &fakeDecider{answers: mixedAnswers()}
	result := judgeOneCall(t, fixedTool(t, decider), `{"state":{"subject":"Payouts failing","body":"Three days now."}}`)

	if result.IsError {
		t.Fatalf("the tool result is an error: %q", result.Text())
	}
	req := decider.one(t)
	if got, want := stateOf(t, req), `{"subject":"Payouts failing","body":"Three days now."}`; got != want {
		t.Errorf("the state is %s, want it as the model wrote it: %s", got, want)
	}
	var ids []string
	for _, nq := range req.Questions {
		ids = append(ids, nq.ID)
	}
	if want := []string{"department", "is_urgent", "frustration"}; !slices.Equal(ids, want) {
		t.Errorf("the questions are %v, want %v in that order", ids, want)
	}
	if req.Model != "" {
		t.Errorf("the model is %q, want the choice left to the Decider", req.Model)
	}
}

// TestToolResultDecodesBackIntoAnswers is what [goodall.JSONResult] buys: the
// text the model reads is the answer set, and a consumer watching the run can
// read it back as typed answers instead of parsing prose.
func TestToolResultDecodesBackIntoAnswers(t *testing.T) {
	result := judgeOneCall(t, fixedTool(t, &fakeDecider{answers: mixedAnswers()}), `{"state":"anything"}`)

	if result.IsError {
		t.Fatalf("the tool result is an error: %q", result.Text())
	}
	var answers Answers
	if err := json.Unmarshal([]byte(result.Text()), &answers); err != nil {
		t.Fatalf("the tool result does not decode back into Answers: %v\n%s", err, result.Text())
	}
	if got, want := answers.IDs(), []string{"department", "is_urgent", "frustration"}; !slices.Equal(got, want) {
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

// TestToolReportsARefusalBesideTheOtherAnswers is what the model has to be able
// to read: one question was declined, so it has no answer there, and the others
// are good to act on. It is not an error result, because the call worked and most
// of what it paid for came back.
func TestToolReportsARefusalBesideTheOtherAnswers(t *testing.T) {
	agent, _ := agentCalling(fixedTool(t, &fakeDecider{answers: refusedAnswers()}), `{"state":"anything"}`)
	end := oneToolCallEnd(t, runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "judge this"}))

	if end.Result.IsError {
		t.Fatalf("a set with one refusal is an error result: %q", end.Result.Text())
	}
	text := end.Result.Text()
	if !strings.Contains(text, `"self_harm":{"type":"refusal","note":"The judge declined this question`) {
		t.Errorf("the result does not tell the model the question was declined:\n%s", text)
	}
	if !strings.Contains(text, "The other answers in this result stand.") {
		t.Errorf("the result does not tell the model the others stand:\n%s", text)
	}
	var answers Answers
	if err := json.Unmarshal([]byte(text), &answers); err != nil {
		t.Fatalf("the tool result does not decode back into Answers: %v\n%s", err, text)
	}
	if got, want := answers.IDs(), []string{"department", "self_harm", "is_urgent"}; !slices.Equal(got, want) {
		t.Errorf("the answers carry %v, want %v in that order", got, want)
	}
	if choice, err := answers.Choice("department"); err != nil || choice.Choice != "billing" {
		t.Errorf("Choice = %+v, %v; want billing still readable beside the refusal", choice, err)
	}
	if predicate, err := answers.Predicate("is_urgent"); err != nil || predicate.Probability != 0.96 {
		t.Errorf("Predicate = %+v, %v; want 0.96 still readable beside the refusal", predicate, err)
	}
	if _, err := answers.Predicate("self_harm"); err == nil || !strings.Contains(err.Error(), `"self_harm"`) {
		t.Errorf("reading the refusal back gave %v, want an error naming the id", err)
	}
	if want := (goodall.Usage{Input: 40, Output: 8}); end.Usage != want {
		t.Errorf("ToolCallEnd usage = %+v, want %+v: the call was still paid for", end.Usage, want)
	}
}

// TestToolReportsWhatTheJudgmentSpent is the per-call accounting: the judge's
// own tokens reach the parent's stream on the call that spent them, so a
// consumer can bill a judgment it never sent itself, and a cost the Decider
// reported travels with them.
func TestToolReportsWhatTheJudgmentSpent(t *testing.T) {
	amount, err := goodall.ParseDecimal("0.00125")
	if err != nil {
		t.Fatalf("parsing the amount: %v", err)
	}
	reported := mixedAnswers()
	reported.Cost = goodall.Cost{Amount: amount, Currency: "USD", Reported: true}

	cases := map[string]struct {
		answers  *Answers
		reported bool
	}{
		"a cost the Decider did not report": {mixedAnswers(), false},
		"a cost the Decider reported":       {reported, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			agent, _ := agentCalling(fixedTool(t, &fakeDecider{answers: c.answers}), `{"state":"anything"}`)
			end := oneToolCallEnd(t, runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "judge this"}))

			if end.Result.IsError {
				t.Fatalf("the tool result is an error: %q", end.Result.Text())
			}
			if want := (goodall.Usage{Input: 448, Output: 73}); end.Usage != want {
				t.Errorf("ToolCallEnd usage = %+v, want the judgment's own %+v", end.Usage, want)
			}
			if end.Cost.Reported != c.reported {
				t.Errorf("ToolCallEnd cost = %+v, want Reported %v: a figure nobody reported must not read as free", end.Cost, c.reported)
			}
		})
	}
}

// TestToolReportsNothingWhileItRuns is the other half of the interface the
// judgment tools implement: they have tokens to declare and no progress to
// show, so the parent's stream carries no nested events at all.
func TestToolReportsNothingWhileItRuns(t *testing.T) {
	agent, _ := agentCalling(fixedTool(t, &fakeDecider{answers: mixedAnswers()}), `{"state":"anything"}`)
	events := runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "judge this"})

	for _, ev := range events {
		if nested, ok := ev.(goodall.ToolEvent); ok {
			t.Errorf("the judgment reported %s while it ran", nested.Event.Type())
		}
	}
}

// TestToolReportsARejectionAsAnErrorResultCarryingTheServersMessage keeps a
// failed judgment in the model's hands, and keeps the words the model reads the
// server's own: a Go error's wrapping would tell it about goodall instead of
// about the request.
func TestToolReportsARejectionAsAnErrorResultCarryingTheServersMessage(t *testing.T) {
	result := judgeOneCall(t, fixedTool(t, &fakeDecider{err: rejected}), `{"state":"anything"}`)

	if !result.IsError {
		t.Fatalf("a rejected judgment is not an error result: %q", result.Text())
	}
	if got := result.Text(); got != rejected.Message {
		t.Errorf("the error text is %q, want the server's own %q", got, rejected.Message)
	}
}

// TestToolLeavesABrokenCallToTheLoop is the other side of that line: a lost
// connection is not something the model can correct, so it is a Go error and the
// loop reports it.
func TestToolLeavesABrokenCallToTheLoop(t *testing.T) {
	broken := errors.New("connection lost")
	result := judgeOneCall(t, fixedTool(t, &fakeDecider{err: broken}), `{"state":"anything"}`)
	if !result.IsError || !strings.Contains(result.Text(), "connection lost") {
		t.Errorf("the result is %q, IsError %v; want the loop's error result", result.Text(), result.IsError)
	}
}

// TestWithModelPinsTheModelForEveryCallOfATool is why the option exists: a caller
// whose thresholds are tuned against one version pins it once, at the tool,
// rather than hoping every call site remembers.
func TestWithModelPinsTheModelForEveryCallOfATool(t *testing.T) {
	decider := &fakeDecider{answers: mixedAnswers()}
	judgeOneCall(t, fixedTool(t, decider, WithModel("model-1.0")), `{"state":"anything"}`)
	if got := decider.one(t).Model; got != "model-1.0" {
		t.Errorf("the request asks for %q, want the pinned model", got)
	}
}

// TestToolSchemaIsOneRequiredState is what the model sees: one member, required,
// described, and no second way to say the same thing.
func TestToolSchemaIsOneRequiredState(t *testing.T) {
	schema := fixedTool(t, &fakeDecider{}).Schema()

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

// TestToolKeepsTheStateRaw is the state contract's other half: what the model
// wrote is what the Decider receives, as raw JSON, so a client can send it
// without a second encoding.
func TestToolKeepsTheStateRaw(t *testing.T) {
	decider := &fakeDecider{answers: mixedAnswers()}
	judgeOneCall(t, fixedTool(t, decider), `{"state":"plain text"}`)
	raw, ok := decider.one(t).State.(jsontext.Value)
	if !ok {
		t.Fatalf("the state is %T, want raw JSON", decider.one(t).State)
	}
	if string(raw) != `"plain text"` {
		t.Errorf("the state is %s", raw)
	}
}

// TestToolRefusesADefinitionItCannotServe keeps every mistake a developer can
// make at construction, where they see it, rather than inside a run.
func TestToolRefusesADefinitionItCannotServe(t *testing.T) {
	decider := &fakeDecider{}
	var nilPointer *fakeDecider
	cases := map[string]struct {
		decider   Decider
		name      string
		desc      string
		questions Questions
	}{
		"no decider":     {nil, "judge", "Judge a ticket.", documentedQuestions()},
		"a nil pointer":  {nilPointer, "judge", "Judge a ticket.", documentedQuestions()},
		"no name":        {decider, "", "Judge a ticket.", documentedQuestions()},
		"no description": {decider, "judge", "", documentedQuestions()},
		"no questions":   {decider, "judge", "Judge a ticket.", nil},
		"bad question":   {decider, "judge", "Judge a ticket.", Questions{{ID: "x", Question: Score{Instructions: "rate", Levels: []string{"only"}}}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tool, err := Tool(c.decider, c.name, c.desc, c.questions)
			if err == nil {
				t.Fatalf("Tool accepted a definition with %s and returned %v", name, tool)
			}
		})
	}
}

// TestADefinitionMistakeNamesTheToolAndKeepsItsKind reads as one sentence and
// stays reachable, which is what a developer debugging a registration needs.
func TestADefinitionMistakeNamesTheToolAndKeepsItsKind(t *testing.T) {
	_, err := Tool(nil, "judge", "Judge a ticket.", documentedQuestions())
	if err == nil {
		t.Fatal("Tool accepted a nil Decider")
	}
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("error is not KindInvalidRequest: %v", err)
	}
	if !strings.Contains(err.Error(), `tool "judge"`) {
		t.Errorf("error %q does not name the tool", err)
	}
	if strings.Count(err.Error(), "decision:") != 1 {
		t.Errorf("error %q repeats the package prefix", err)
	}
}
