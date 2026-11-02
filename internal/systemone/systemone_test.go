package systemone

import (
	json "encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
)

// documentedQuestions is the documented example, reordered so that the questions
// are not in alphabetical order: a marshaler that sorted them would be caught.
func documentedQuestions() decision.Questions {
	return decision.Questions{
		{ID: "department", Question: decision.Choice{
			Instructions: "Which team should handle this?",
			Options: []decision.Option{
				{Key: "billing", Description: "Payments, invoicing, refunds"},
				{Key: "technical", Description: "Bugs, outages, integrations"},
				{Key: "sales"},
			},
		}},
		{ID: "is_urgent", Question: decision.Predicate{
			Instructions: "Does this convey urgency?",
			True:         "Explicitly time-sensitive",
			False:        "No urgency expressed",
		}},
		{ID: "frustration", Question: decision.Score{
			Instructions: "How frustrated is the customer?",
			Levels:       []string{"Calm", "Frustrated", "Very angry"},
		}},
	}
}

// documentedBody is the exact body those questions must produce: the three
// top-level members in this order, the questions in author order, each
// question's members in the order the wire's reference lists them, and the
// option with no description as null.
const documentedBody = `{"state":"Help!",` +
	`"model":"some-model",` +
	`"questions":{` +
	`"department":{"type":"choice","instructions":"Which team should handle this?",` +
	`"criteria":{"billing":"Payments, invoicing, refunds","technical":"Bugs, outages, integrations","sales":null}},` +
	`"is_urgent":{"type":"noul","instructions":"Does this convey urgency?",` +
	`"criteria":{"true":"Explicitly time-sensitive","false":"No urgency expressed"}},` +
	`"frustration":{"type":"score","instructions":"How frustrated is the customer?",` +
	`"criteria":["Calm","Frustrated","Very angry"]}}}`

func TestEncodeRequestWritesTheWireForm(t *testing.T) {
	body, err := EncodeRequest(&decision.Request{Model: "some-model", State: "Help!", Questions: documentedQuestions()})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	if string(body) != documentedBody {
		t.Errorf("the body is\n%s\nwant\n%s", body, documentedBody)
	}
}

// TestEncodeRequestOmitsAnEmptyCriteriaObject is the one optional member a
// question has: a predicate with neither description sends no criteria at all.
func TestEncodeRequestOmitsAnEmptyCriteriaObject(t *testing.T) {
	body, err := EncodeRequest(&decision.Request{Model: "m", State: "s", Questions: decision.Questions{
		{ID: "a", Question: decision.Predicate{Instructions: "Urgent?"}},
		{ID: "b", Question: decision.Predicate{Instructions: "Angry?", False: "Calm"}},
	}})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	const want = `{"state":"s","model":"m","questions":{` +
		`"a":{"type":"noul","instructions":"Urgent?"},` +
		`"b":{"type":"noul","instructions":"Angry?","criteria":{"false":"Calm"}}}}`
	if string(body) != want {
		t.Errorf("the body is\n%s\nwant\n%s", body, want)
	}
}

// TestTwoIdenticalRequestsEncodeToIdenticalBytes is invariant 8: a map anywhere
// in the path, the caller's state included, would break this at random.
func TestTwoIdenticalRequestsEncodeToIdenticalBytes(t *testing.T) {
	req := &decision.Request{
		Model:     "m",
		State:     map[string]any{"zeta": 1, "alpha": 2, "mu": 3},
		Questions: documentedQuestions(),
	}
	first, err := EncodeRequest(req)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	for range 16 {
		again, err := EncodeRequest(req)
		if err != nil {
			t.Fatalf("EncodeRequest: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("a second encoding differs\n got: %s\nwant: %s", again, first)
		}
	}
	if !strings.Contains(string(first), `"state":{"alpha":2,"mu":3,"zeta":1}`) {
		t.Errorf("a map state is not written in a stable order: %s", first)
	}
}

// TestAStateThatCannotBeMarshaledIsAnInvalidRequest keeps the kind on the error
// and leaves naming the server to whichever client called: this codec serves
// more than one, and an error here names none of them.
func TestAStateThatCannotBeMarshaledIsAnInvalidRequest(t *testing.T) {
	_, err := EncodeRequest(&decision.Request{Model: "m", State: make(chan int), Questions: documentedQuestions()})
	if err == nil {
		t.Fatal("a channel state encoded without complaint")
	}
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("error is not KindInvalidRequest: %v", err)
	}
	if !strings.Contains(err.Error(), "state") {
		t.Errorf("error %q does not name the state", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "typesafe") {
		t.Errorf("error %q bakes one server's name into the shared codec", err)
	}
}

const mixedResponse = `{"model":"some-model-1.0","answers":{` +
	`"department":{"type":"choice","choice":"billing","confidence":0.96,"probabilities":{"technical":0.03,"billing":0.97,"sales":0.0}},` +
	`"is_urgent":{"type":"noul","noul":0.96},` +
	`"frustration":{"type":"score","score":2.12,"confidence":0.87,"legend":{"0":"Calm","1":"Mildly annoyed","2":"Frustrated","3":"Furious"},"probabilities":{"0":0.0,"1":0.0,"2":0.88,"3":0.12}}},` +
	`"usage":{"input_tokens":448,"output_tokens":73}}`

// TestDecodeAnswersReadsTheWireIntoNeutralAnswers is the seam the package exists
// for: the wire's spelling of a predicate goes no further than this codec, and
// the accessors read what it built.
func TestDecodeAnswersReadsTheWireIntoNeutralAnswers(t *testing.T) {
	answers, err := DecodeAnswers([]byte(mixedResponse))
	if err != nil {
		t.Fatalf("DecodeAnswers: %v", err)
	}
	if answers.Model != "some-model-1.0" {
		t.Errorf("Model = %q", answers.Model)
	}
	if want := (goodall.Usage{Input: 448, Output: 73}); answers.Usage != want {
		t.Errorf("Usage = %+v, want %+v", answers.Usage, want)
	}
	if answers.Cost.Reported {
		t.Error("Cost is reported, and nothing on this body reports one")
	}
	if got, want := answers.IDs(), []string{"department", "is_urgent", "frustration"}; !slices.Equal(got, want) {
		t.Errorf("IDs = %v, want %v in document order", got, want)
	}
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	if predicate.Probability != 0.96 {
		t.Errorf("Probability = %v, want 0.96", predicate.Probability)
	}
	choice, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if choice.Choice != "billing" || choice.Confidence != 0.96 || choice.Probabilities["billing"] != 0.97 {
		t.Errorf("choice = %+v", choice)
	}
	score, err := answers.Score("frustration")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score.Score != 2.12 || score.Confidence != 0.87 || score.Legend["3"] != "Furious" || score.Probabilities["2"] != 0.88 {
		t.Errorf("score = %+v", score)
	}
}

// TestTheNeutralFormDoesNotCarryTheWiresSpelling is the other half of that seam:
// once decoded, what a conversational model reads is the package's own form.
func TestTheNeutralFormDoesNotCarryTheWiresSpelling(t *testing.T) {
	answers, err := DecodeAnswers([]byte(mixedResponse))
	if err != nil {
		t.Fatalf("DecodeAnswers: %v", err)
	}
	encoded, err := json.Marshal(answers)
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}
	if strings.Contains(string(encoded), WirePredicate) {
		t.Errorf("the neutral form carries the wire's word: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"is_urgent":{"type":"predicate","probability":0.96}`) {
		t.Errorf("the neutral form does not carry the predicate: %s", encoded)
	}
}

// TestAnUnknownAnswerTypeDecodesCarryingItsBytes is invariant 6: a type this
// version does not model is an answer that was paid for, never dropped.
func TestAnUnknownAnswerTypeDecodesCarryingItsBytes(t *testing.T) {
	const raw = `{"type":"box","box":[0.1,0.2,0.3,0.4]}`
	answers, err := DecodeAnswers([]byte(`{"model":"m","answers":{"where":` + raw + `},"usage":{"input_tokens":10,"output_tokens":4}}`))
	if err != nil {
		t.Fatalf("DecodeAnswers: %v", err)
	}
	answer, ok := answers.Lookup("where")
	if !ok {
		t.Fatal("the unknown answer was dropped")
	}
	unknown, ok := answer.(decision.UnknownAnswer)
	if !ok {
		t.Fatalf("answer is %T, want decision.UnknownAnswer", answer)
	}
	if unknown.Type != "box" {
		t.Errorf("Type = %q, want box", unknown.Type)
	}
	if string(unknown.Raw) != raw {
		t.Errorf("Raw = %s, want %s", unknown.Raw, raw)
	}
}

func TestDecodeAnswersRefusesABodyThatIsNotAnAnswerSet(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":        `not json`,
		"answers a list":  `{"model":"m","answers":[1,2]}`,
		"a broken answer": `{"model":"m","answers":{"a":{"type":"choice","choice":17}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeAnswers([]byte(body))
			if err == nil {
				t.Fatal("DecodeAnswers accepted a body that is not an answer set")
			}
			if strings.Contains(strings.ToLower(err.Error()), "typesafe") {
				t.Errorf("error %q bakes one server's name into the shared codec", err)
			}
		})
	}
}

// TestDecodeAnswersNamesTheAnswerItCouldNotRead: a set of twenty answers gives a
// reader nothing else to go on.
func TestDecodeAnswersNamesTheAnswerItCouldNotRead(t *testing.T) {
	_, err := DecodeAnswers([]byte(`{"model":"m","answers":{"dept":{"type":"choice","choice":17}}}`))
	if err == nil || !strings.Contains(err.Error(), `"dept"`) {
		t.Errorf("error = %v, want one naming the answer", err)
	}
}

// TestQuestionsRoundTripThroughTheWire is what lets a script read back the
// bytes it sent: the list keeps the author's order, questions and options both.
func TestQuestionsRoundTripThroughTheWire(t *testing.T) {
	encoded, err := json.Marshal(Questions(documentedQuestions()))
	if err != nil {
		t.Fatalf("marshaling the questions: %v", err)
	}
	var back Questions
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("decoding the questions again: %v", err)
	}
	again, err := json.Marshal(back)
	if err != nil {
		t.Fatalf("marshaling the questions again: %v", err)
	}
	if string(again) != string(encoded) {
		t.Errorf("a second encoding differs\n got: %s\nwant: %s", again, encoded)
	}
	if len(back) != 3 || back[0].ID != "department" {
		t.Fatalf("questions = %+v, want the three in author order", back)
	}
	choice, ok := back[0].Question.(decision.Choice)
	if !ok {
		t.Fatalf("question 0 is %T, want Choice", back[0].Question)
	}
	if len(choice.Options) != 3 || choice.Options[0].Key != "billing" || choice.Options[2].Key != "sales" {
		t.Errorf("options = %+v, want billing, technical, sales", choice.Options)
	}
	if choice.Options[2].Description != "" {
		t.Errorf("the null description came back as %q, want empty", choice.Options[2].Description)
	}
	predicate, ok := back[1].Question.(decision.Predicate)
	if !ok {
		t.Fatalf("question 1 is %T, want Predicate", back[1].Question)
	}
	if predicate.True != "Explicitly time-sensitive" || predicate.False != "No urgency expressed" {
		t.Errorf("predicate = %+v, want both criteria", predicate)
	}
	score, ok := back[2].Question.(decision.Score)
	if !ok {
		t.Fatalf("question 2 is %T, want Score", back[2].Question)
	}
	if !slices.Equal(score.Levels, []string{"Calm", "Frustrated", "Very angry"}) {
		t.Errorf("levels = %v", score.Levels)
	}
}

func TestAnUnknownQuestionTypeIsRefusedWhenDecoded(t *testing.T) {
	var questions Questions
	err := json.Unmarshal([]byte(`{"vibe":{"type":"vibe","instructions":"What is the vibe?"}}`), &questions)
	if err == nil {
		t.Fatal("an unknown question type decoded without complaint")
	}
	for _, want := range []string{`"vibe"`, WirePredicate} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestAQuestionWithNoTypeIsRefusedWhenDecoded(t *testing.T) {
	var questions Questions
	err := json.Unmarshal([]byte(`{"x":{"instructions":"What?"}}`), &questions)
	if err == nil || !strings.Contains(err.Error(), "type") {
		t.Errorf("error = %v, want one saying the type member is missing", err)
	}
}

// TestAnAnswerWithNoValueIsRefusedWhenDecoded is invariant 6 at the wire: a
// predicate with no probability would otherwise read as a confident no, and a
// score with no score as the lowest level, from a server that said neither. This
// codec serves many models, so a body it cannot read says so rather than
// inventing a zero.
func TestAnAnswerWithNoValueIsRefusedWhenDecoded(t *testing.T) {
	for name, answer := range map[string]string{
		"a predicate with no value": `{"type":"noul"}`,
		"a predicate with null":     `{"type":"noul","noul":null}`,
		"a choice with no pick":     `{"type":"choice","confidence":0.5,"probabilities":{"a":1}}`,
		"a score with no score":     `{"type":"score","confidence":0.5,"probabilities":{"0":1}}`,
		"a score with a null score": `{"type":"score","score":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeAnswers([]byte(`{"model":"m","answers":{"the_id":` + answer + `}}`))
			if err == nil {
				t.Fatal("an answer with no value decoded as if it had one")
			}
			if !strings.Contains(err.Error(), `"the_id"`) {
				t.Errorf("error %q does not name the answer", err)
			}
		})
	}
}

// TestARefusalOnThisWireIsTheNeutralRefusal keeps a declined question from
// reaching a model as a bare type member: as a [decision.RefusalAnswer] it carries
// the note saying the others stand, while its sibling stays readable.
func TestARefusalOnThisWireIsTheNeutralRefusal(t *testing.T) {
	answers, err := DecodeAnswers([]byte(`{"model":"m","answers":{"a":{"type":"refusal"},"b":{"type":"noul","noul":0.5}}}`))
	if err != nil {
		t.Fatalf("DecodeAnswers: %v", err)
	}
	answer, ok := answers.Lookup("a")
	if !ok {
		t.Fatal("the refusal was dropped")
	}
	if _, ok := answer.(decision.RefusalAnswer); !ok {
		t.Errorf("answer is %T, want decision.RefusalAnswer", answer)
	}
	if predicate, err := answers.Predicate("b"); err != nil || predicate.Probability != 0.5 {
		t.Errorf("the sibling reads %+v, %v; want 0.5", predicate, err)
	}
}
