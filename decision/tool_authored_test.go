package decision

import (
	"slices"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// authoredTool is the model-authored judgment tool over the given Decider.
func authoredTool(t *testing.T, decider Decider, opts ...ToolOption) goodall.Tool {
	t.Helper()
	tool, err := AuthoredTool(decider, "judge", opts...)
	if err != nil {
		t.Fatalf("building the authored judgment tool: %v", err)
	}
	return tool
}

// authoredCall is a call a model could write: all three question types, in an
// order that is not the order this package declares them in, so a translation
// that reordered them would be caught.
const authoredCall = `{"state":{"ticket":"payouts failing"},"questions":[` +
	`{"id":"is_urgent","type":"predicate","instructions":"Is work or money blocked right now?",` +
	`"true":"Blocked right now","false":"Can wait"},` +
	`{"id":"department","type":"choice","instructions":"Which team should handle this?",` +
	`"options":[{"key":"billing","description":"Payments, invoicing, refunds"},{"key":"other","description":"None of the above"}]},` +
	`{"id":"frustration","type":"score","instructions":"How frustrated is the customer?",` +
	`"levels":["Calm","Furious"]}]}`

// TestAuthoredToolSendsTheModelsQuestionsInTheModelsOrder is the round trip the
// authored tool exists for: the questions a model wrote become the request, each
// as the typed question it describes.
func TestAuthoredToolSendsTheModelsQuestionsInTheModelsOrder(t *testing.T) {
	decider := &fakeDecider{answers: mixedAnswers()}
	result := judgeOneCall(t, authoredTool(t, decider), authoredCall)

	if result.IsError {
		t.Fatalf("the tool result is an error: %q", result.Text())
	}
	req := decider.one(t)
	if got, want := stateOf(t, req), `{"ticket":"payouts failing"}`; got != want {
		t.Errorf("the state is %s, want %s", got, want)
	}
	if len(req.Questions) != 3 {
		t.Fatalf("the request carries %d questions, want 3", len(req.Questions))
	}
	if got, want := req.Questions[0], (NamedQuestion{ID: "is_urgent", Question: Predicate{
		Instructions: "Is work or money blocked right now?", True: "Blocked right now", False: "Can wait",
	}}); got != want {
		t.Errorf("question 0 = %+v, want %+v", got, want)
	}
	choice, ok := req.Questions[1].Question.(Choice)
	if !ok || req.Questions[1].ID != "department" {
		t.Fatalf("question 1 = %+v, want the department choice", req.Questions[1])
	}
	if want := []Option{
		{Key: "billing", Description: "Payments, invoicing, refunds"},
		{Key: "other", Description: "None of the above"},
	}; !slices.Equal(choice.Options, want) {
		t.Errorf("options = %+v, want %+v", choice.Options, want)
	}
	score, ok := req.Questions[2].Question.(Score)
	if !ok || req.Questions[2].ID != "frustration" {
		t.Fatalf("question 2 = %+v, want the frustration score", req.Questions[2])
	}
	if !slices.Equal(score.Levels, []string{"Calm", "Furious"}) {
		t.Errorf("levels = %v", score.Levels)
	}
}

// TestAuthoredToolReportsARefusalBesideTheOtherAnswers: a question a model wrote
// is a question a judge may decline, and the model that wrote it has to be told
// that one has no answer while it acts on the rest.
func TestAuthoredToolReportsARefusalBesideTheOtherAnswers(t *testing.T) {
	result := judgeOneCall(t, authoredTool(t, &fakeDecider{answers: refusedAnswers()}), authoredCall)

	if result.IsError {
		t.Fatalf("a set with one refusal is an error result: %q", result.Text())
	}
	for _, want := range []string{`"self_harm":{"type":"refusal"`, "declined", `"is_urgent":{"type":"predicate","probability":0.96}`} {
		if !strings.Contains(result.Text(), want) {
			t.Errorf("the result does not carry %q:\n%s", want, result.Text())
		}
	}
}

// TestAuthoredToolReportsWhatTheJudgmentSpent is the per-call accounting on the
// delegation shape: the tokens a model-written question set spent travel on the
// call that spent them, where a consumer can sum the ones it chose to count.
func TestAuthoredToolReportsWhatTheJudgmentSpent(t *testing.T) {
	agent, _ := agentCalling(authoredTool(t, &fakeDecider{answers: mixedAnswers()}), authoredCall)
	end := oneToolCallEnd(t, runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "judge this"}))

	if end.Result.IsError {
		t.Fatalf("the tool result is an error: %q", end.Result.Text())
	}
	if want := (goodall.Usage{Input: 448, Output: 73}); end.Usage != want {
		t.Errorf("ToolCallEnd usage = %+v, want the judgment's own %+v", end.Usage, want)
	}
	if end.Cost.Reported {
		t.Errorf("ToolCallEnd cost = %+v, want an unreported cost", end.Cost)
	}
}

// TestAuthoredToolReportsNoUsageForAQuestionSetItRefused is the shape of a
// refusal that never reached a server: the model reads what to write instead,
// and nothing was spent, so nothing is reported.
func TestAuthoredToolReportsNoUsageForAQuestionSetItRefused(t *testing.T) {
	decider := &fakeDecider{answers: mixedAnswers()}
	agent, _ := agentCalling(authoredTool(t, decider), `{"state":"anything","questions":[]}`)
	end := oneToolCallEnd(t, runEvents(t, agent, goodall.Conversation{}, goodall.Text{Text: "judge this"}))

	if !end.Result.IsError {
		t.Fatalf("an empty question set is not an error result: %q", end.Result.Text())
	}
	if n := len(decider.seen()); n != 0 {
		t.Errorf("the Decider was handed %d requests, want none for a set this package refuses", n)
	}
	if end.Usage != (goodall.Usage{}) || end.Cost != (goodall.Cost{}) {
		t.Errorf("ToolCallEnd = %+v / %+v, want nothing reported for a call that spent nothing", end.Usage, end.Cost)
	}
}

// TestAuthoredToolRefusesAnUnaskableQuestionBeforeTheDecider is the guard the
// authored tool needs and the fixed one does not: the questions are the model's,
// so they are checked before a call is paid for, whichever Decider is behind the
// tool, and the model is told which question it was and what to write instead.
func TestAuthoredToolRefusesAnUnaskableQuestionBeforeTheDecider(t *testing.T) {
	cases := map[string]struct {
		input string
		// names is what the refusal must say, so the model can act on it.
		names []string
	}{
		"an unmodeled type": {
			`{"state":"x","questions":[{"id":"where","type":"box","instructions":"Where is the logo?"}]}`,
			[]string{"where", "box", "predicate", "choice", "score"},
		},
		"an unknown type": {
			`{"state":"x","questions":[{"id":"vibe","type":"vibe","instructions":"What is the vibe?"}]}`,
			[]string{"vibe", "predicate", "choice", "score"},
		},
		"no questions at all": {
			`{"state":"x","questions":[]}`,
			[]string{"question"},
		},
		"a choice with no options": {
			`{"state":"x","questions":[{"id":"department","type":"choice","instructions":"Which team?"}]}`,
			[]string{"department", "option"},
		},
		"a score with one level": {
			`{"state":"x","questions":[{"id":"frustration","type":"score","instructions":"How angry?","levels":["Calm"]}]}`,
			[]string{"frustration", "level"},
		},
		"two questions sharing an id": {
			`{"state":"x","questions":[` +
				`{"id":"urgent","type":"predicate","instructions":"Is it urgent?"},` +
				`{"id":"urgent","type":"predicate","instructions":"Is it really urgent?"}]}`,
			[]string{"urgent"},
		},
		"a predicate with no instructions": {
			`{"state":"x","questions":[{"id":"urgent","type":"predicate","instructions":""}]}`,
			[]string{"urgent", "instructions"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			decider := &fakeDecider{answers: mixedAnswers()}
			result := judgeOneCall(t, authoredTool(t, decider), c.input)

			if !result.IsError {
				t.Fatalf("%s was accepted: %q", name, result.Text())
			}
			for _, want := range c.names {
				if !strings.Contains(result.Text(), want) {
					t.Errorf("the refusal %q does not name %q", result.Text(), want)
				}
			}
			if n := len(decider.seen()); n != 0 {
				t.Errorf("the Decider was handed %d requests; a question set this package can refuse costs nothing", n)
			}
		})
	}
}

// TestAuthoredToolDescriptionDefaultsAndCanBeReplaced is the guidance ruling in
// code: the tool carries the package's own description unless the consumer,
// whose prompt it competes with, replaces it.
func TestAuthoredToolDescriptionDefaultsAndCanBeReplaced(t *testing.T) {
	decider := &fakeDecider{}
	if got := authoredTool(t, decider).Description(); got != DefaultAuthoredDescription {
		t.Errorf("the default description is %q, want DefaultAuthoredDescription", got)
	}
	const own = "Ask the judge about a node tree."
	if got := authoredTool(t, decider, WithDescription(own)).Description(); got != own {
		t.Errorf("WithDescription left the description %q, want %q", got, own)
	}
}

// TestAuthoredToolSchemaNamesTheThreeTypes is what the model is looking at when
// it writes a question: two required members, and a type it can fill in without
// guessing.
func TestAuthoredToolSchemaNamesTheThreeTypes(t *testing.T) {
	schema := authoredTool(t, &fakeDecider{}).Schema()

	if got, want := strings.Join(schema.Required, ","), "state,questions"; got != want {
		t.Errorf("required = %q, want %q", got, want)
	}
	questions := propertyNamed(t, schema, "questions")
	if questions.Type != goodall.SchemaArray || questions.Items == nil {
		t.Fatalf("the questions property is %+v, want an array with items", questions)
	}
	typeMember := propertyNamed(t, questions.Items, "type")
	if got, want := typeMember.Enum, []string{"predicate", "choice", "score"}; !slices.Equal(got, want) {
		t.Errorf("the type member's enum is %v, want exactly %v", got, want)
	}
	for _, name := range []string{"id", "type", "instructions"} {
		if p := propertyNamed(t, questions.Items, name); p.Description == "" {
			t.Errorf("the %s member has no description, so the model must guess what to write", name)
		}
	}
	if got, want := strings.Join(questions.Items.Required, ","), "id,type,instructions"; got != want {
		t.Errorf("a question requires %q, want %q", got, want)
	}
}

// propertyNamed reads one property out of an object schema.
func propertyNamed(t *testing.T, s *goodall.Schema, name string) *goodall.Schema {
	t.Helper()
	for i := range s.Properties {
		if s.Properties[i].Name == name {
			return &s.Properties[i].Schema
		}
	}
	t.Fatalf("the schema has no %q property: %+v", name, s.Properties)
	return nil
}

// TestWithModelPinsTheModelOfAnAuthoredTool is the shared option on the second
// of its three adapters.
func TestWithModelPinsTheModelOfAnAuthoredTool(t *testing.T) {
	decider := &fakeDecider{answers: mixedAnswers()}
	judgeOneCall(t, authoredTool(t, decider, WithModel("model-1.0")), authoredCall)
	if got := decider.one(t).Model; got != "model-1.0" {
		t.Errorf("the request asks for %q, want the pinned model", got)
	}
}

// TestAuthoredToolRefusesADefinitionItCannotServe keeps a registration mistake
// at construction, as [Tool] does.
func TestAuthoredToolRefusesADefinitionItCannotServe(t *testing.T) {
	decider := &fakeDecider{}
	if tool, err := AuthoredTool(nil, "judge"); err == nil {
		t.Errorf("AuthoredTool accepted a nil Decider and returned %v", tool)
	}
	var nilPointer *fakeDecider
	if tool, err := AuthoredTool(nilPointer, "judge"); err == nil {
		t.Errorf("AuthoredTool accepted a nil pointer and returned %v", tool)
	}
	if tool, err := AuthoredTool(decider, "judge this"); err == nil {
		t.Errorf("AuthoredTool accepted a name with a space and returned %v", tool)
	}
	if tool, err := AuthoredTool(decider, "judge", WithDescription("")); err == nil {
		t.Errorf("AuthoredTool accepted an empty description and returned %v", tool)
	}
}
