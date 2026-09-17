package typesafe

import (
	json "encoding/json/v2"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The edge cases no recording can provide: an id nobody asked about, an answer
// of the wrong type, an answer type a later release adds, and a tie in the
// probability distribution. The bodies here are the shapes of the recordings in
// testdata, edited; testdata/README.md says which of the two a file is.

// answersFrom decodes one response body through the client.
func answersFrom(t *testing.T, body string) *Answers {
	t.Helper()
	client, _ := serveJSON(t, http.StatusOK, body)
	answers, err := client.Ask(t.Context(), "state", simpleQuestions())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	return answers
}

const mixedBody = `{"model":"jev-1.13.0","answers":{` +
	`"department":{"type":"choice","choice":"billing","confidence":0.96,"probabilities":{"technical":0.03,"billing":0.97,"sales":0.0}},` +
	`"is_urgent":{"type":"noul","noul":0.96},` +
	`"frustration":{"type":"score","score":2.12,"confidence":0.87,"legend":{"0":"Calm","1":"Mildly annoyed","2":"Frustrated","3":"Furious"},"probabilities":{"0":0.0,"1":0.0,"2":0.88,"3":0.12}}},` +
	`"usage":{"input_tokens":448,"output_tokens":73}}`

func TestAnAnswerForAnUnaskedIDNamesTheID(t *testing.T) {
	answers := answersFrom(t, mixedBody)
	for _, read := range map[string]func(string) error{
		"Noul":   func(id string) error { _, err := answers.Noul(id); return err },
		"Choice": func(id string) error { _, err := answers.Choice(id); return err },
		"Score":  func(id string) error { _, err := answers.Score(id); return err },
	} {
		err := read("sentiment")
		if err == nil {
			t.Fatal("reading an answer nobody asked for succeeded")
		}
		if !strings.Contains(err.Error(), `"sentiment"`) {
			t.Errorf("error %q does not name the id", err)
		}
		var answerErr *AnswerError
		if !errors.As(err, &answerErr) {
			t.Fatalf("error is %T, want *AnswerError", err)
		}
		if answerErr.ID != "sentiment" {
			t.Errorf("AnswerError.ID = %q, want sentiment", answerErr.ID)
		}
		if answerErr.Got != "" {
			t.Errorf("AnswerError.Got = %q, want empty for an answer that is not there", answerErr.Got)
		}
	}
}

func TestAnAnswerOfTheWrongTypeNamesBothTypes(t *testing.T) {
	answers := answersFrom(t, mixedBody)
	_, err := answers.Score("department")
	if err == nil {
		t.Fatal("reading a choice as a score succeeded")
	}
	var answerErr *AnswerError
	if !errors.As(err, &answerErr) {
		t.Fatalf("error is %T, want *AnswerError", err)
	}
	if answerErr.ID != "department" || answerErr.Want != TypeScore || answerErr.Got != TypeChoice {
		t.Errorf("AnswerError = %+v, want department, want score, got choice", answerErr)
	}
	for _, want := range []string{`"department"`, "score", "choice"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// TestAnUnknownAnswerTypeIsSurfaced is invariant 6: an answer this version does
// not model comes back as an Unknown carrying its bytes, never dropped. The API
// already serves a fourth type this package does not model — a 422 names
// bounding_box among its expected tags — so this is the live case, not a
// hypothetical one.
func TestAnUnknownAnswerTypeIsSurfaced(t *testing.T) {
	const body = `{"model":"jev-1.13.0","answers":{` +
		`"box":{"type":"bounding_box","bounding_box":[0.1,0.2,0.3,0.4]}},` +
		`"usage":{"input_tokens":10,"output_tokens":4}}`
	answers := answersFrom(t, body)
	answer, ok := answers.Lookup("box")
	if !ok {
		t.Fatal("the unknown answer was dropped")
	}
	unknown, ok := answer.(UnknownAnswer)
	if !ok {
		t.Fatalf("answer is %T, want UnknownAnswer", answer)
	}
	if unknown.Type != "bounding_box" {
		t.Errorf("Type = %q, want bounding_box", unknown.Type)
	}
	if !strings.Contains(string(unknown.Raw), "0.3") {
		t.Errorf("Raw = %s, want the answer's own bytes", unknown.Raw)
	}
	_, err := answers.Noul("box")
	if err == nil {
		t.Fatal("reading an unknown answer as a noul succeeded")
	}
	if !strings.Contains(err.Error(), "bounding_box") {
		t.Errorf("error %q does not say what type arrived", err)
	}
}

func TestRankedBreaksTiesByKeySoItIsStable(t *testing.T) {
	const body = `{"model":"jev-1.13.0","answers":{` +
		`"department":{"type":"choice","choice":"billing","confidence":0.4,` +
		`"probabilities":{"sales":0.4,"billing":0.4,"technical":0.2}}},` +
		`"usage":{"input_tokens":10,"output_tokens":4}}`
	answers := answersFrom(t, body)
	choice, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	want := []Outcome{
		{Key: "billing", Probability: 0.4},
		{Key: "sales", Probability: 0.4},
		{Key: "technical", Probability: 0.2},
	}
	got := choice.Ranked()
	if len(got) != len(want) {
		t.Fatalf("Ranked() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Ranked()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestAnswersRoundTripThroughJSON is what the tool adapter needs: an answer set
// handed to a model as JSON and read back must be the same answer set, with the
// ids in the same order.
func TestAnswersRoundTripThroughJSON(t *testing.T) {
	answers := answersFrom(t, mixedBody)
	encoded, err := json.Marshal(answers)
	if err != nil {
		t.Fatalf("marshaling the answers: %v", err)
	}
	var back Answers
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("decoding the answers again: %v", err)
	}
	if back.Model != answers.Model {
		t.Errorf("Model = %q, want %q", back.Model, answers.Model)
	}
	if back.Usage != answers.Usage {
		t.Errorf("Usage = %+v, want %+v", back.Usage, answers.Usage)
	}
	if got, want := strings.Join(back.IDs(), ","), strings.Join(answers.IDs(), ","); got != want {
		t.Errorf("IDs = %s, want %s", got, want)
	}
	again, err := json.Marshal(&back)
	if err != nil {
		t.Fatalf("marshaling the answers again: %v", err)
	}
	if string(again) != string(encoded) {
		t.Errorf("a second encoding differs\n got: %s\nwant: %s", again, encoded)
	}
}

// TestQuestionsRoundTripThroughJSON is the other half of that seam: the
// authored-questions tool reads a question list a model wrote, and the list it
// gets back must carry the author's option order.
func TestQuestionsRoundTripThroughJSON(t *testing.T) {
	questions := documentedQuestions()
	encoded, err := json.Marshal(questions)
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
	choice, ok := back[0].Question.(Choice)
	if !ok {
		t.Fatalf("question 0 is %T, want Choice", back[0].Question)
	}
	if len(choice.Options) != 3 || choice.Options[0].Key != "billing" || choice.Options[2].Key != "sales" {
		t.Errorf("options = %+v, want billing, technical, sales", choice.Options)
	}
	if choice.Options[2].Description != "" {
		t.Errorf("the null description came back as %q, want empty", choice.Options[2].Description)
	}
}

func TestAnUnknownQuestionTypeIsRefusedWhenDecoded(t *testing.T) {
	var questions Questions
	err := json.Unmarshal([]byte(`{"vibe":{"type":"vibe","instructions":"What is the vibe?"}}`), &questions)
	if err == nil {
		t.Fatal("an unknown question type decoded without complaint")
	}
	for _, want := range []string{`"vibe"`, "noul"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}
