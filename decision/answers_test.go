package decision

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// The edge cases of reading an answer set: an id nobody asked about, an answer
// of the wrong type, a question the model declined, a type a later release adds
// and a tie in the distribution. The values are authored, since no recorded
// server has produced a refusal on demand.

// mixedAnswers is one answer of each type a question can ask for, in an order
// that is not alphabetical, so a writer that sorted the ids would be caught.
func mixedAnswers() *Answers {
	answers := NewAnswers([]NamedAnswer{
		{ID: "department", Answer: ChoiceAnswer{
			Choice:        "billing",
			Confidence:    0.96,
			Probabilities: map[string]float64{"technical": 0.03, "billing": 0.97, "sales": 0},
		}},
		{ID: "is_urgent", Answer: PredicateAnswer{Probability: 0.96}},
		{ID: "frustration", Answer: ScoreAnswer{
			Score:         2.12,
			Confidence:    0.87,
			Legend:        map[string]string{"0": "Calm", "1": "Mildly annoyed", "2": "Frustrated", "3": "Furious"},
			Probabilities: map[string]float64{"0": 0, "1": 0, "2": 0.88, "3": 0.12},
		}},
	})
	answers.Model = "model-1"
	answers.Usage = goodall.Usage{Input: 448, Output: 73}
	return answers
}

// refusedAnswers is a set in which the model answered two questions and declined
// the one between them.
func refusedAnswers() *Answers {
	answers := NewAnswers([]NamedAnswer{
		{ID: "department", Answer: ChoiceAnswer{
			Choice:        "billing",
			Confidence:    0.9,
			Probabilities: map[string]float64{"billing": 0.95, "other": 0.05},
		}},
		{ID: "self_harm", Answer: RefusalAnswer{}},
		{ID: "is_urgent", Answer: PredicateAnswer{Probability: 0.96}},
	})
	answers.Model = "model-1"
	answers.Usage = goodall.Usage{Input: 40, Output: 8}
	return answers
}

// TestARefusedQuestionIsAnErrorNamingTheID is invariant 6 for a declined
// question: a zero value from a typed accessor would read as a confident no, or
// as an option nobody picked, so all three accessors say what happened instead.
func TestARefusedQuestionIsAnErrorNamingTheID(t *testing.T) {
	answers := refusedAnswers()
	for name, read := range map[string]func(string) error{
		"Predicate": func(id string) error { _, err := answers.Predicate(id); return err },
		"Choice":    func(id string) error { _, err := answers.Choice(id); return err },
		"Score":     func(id string) error { _, err := answers.Score(id); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := read("self_harm")
			if err == nil {
				t.Fatal("reading a refused question succeeded")
			}
			if !strings.Contains(err.Error(), `"self_harm"`) {
				t.Errorf("error %q does not name the id", err)
			}
			if !strings.Contains(err.Error(), "declined") {
				t.Errorf("error %q reads as a mismatch rather than saying the model declined", err)
			}
			answerErr, ok := errors.AsType[*AnswerError](err)
			if !ok {
				t.Fatalf("error is %T, want *AnswerError", err)
			}
			if answerErr.ID != "self_harm" || answerErr.Got != TypeRefusal {
				t.Errorf("AnswerError = %+v, want the id and a Got of %s", answerErr, TypeRefusal)
			}
		})
	}
}

// TestTheOtherAnswersStandBesideARefusal is why a refusal is an answer rather
// than an error on the whole call: what the model did answer was paid for.
func TestTheOtherAnswersStandBesideARefusal(t *testing.T) {
	answers := refusedAnswers()
	if got, want := answers.IDs(), []string{"department", "self_harm", "is_urgent"}; !slices.Equal(got, want) {
		t.Errorf("IDs = %v, want %v", got, want)
	}
	choice, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if choice.Choice != "billing" {
		t.Errorf("department = %q, want billing", choice.Choice)
	}
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	if predicate.Probability != 0.96 {
		t.Errorf("is_urgent = %v, want 0.96", predicate.Probability)
	}
	answer, ok := answers.Lookup("self_harm")
	if !ok {
		t.Fatal("the refusal was dropped from the set")
	}
	if _, ok := answer.(RefusalAnswer); !ok {
		t.Errorf("the answer is %T, want RefusalAnswer", answer)
	}
}

func TestAnAnswerForAnUnaskedIDNamesTheID(t *testing.T) {
	answers := mixedAnswers()
	for _, read := range map[string]func(string) error{
		"Predicate": func(id string) error { _, err := answers.Predicate(id); return err },
		"Choice":    func(id string) error { _, err := answers.Choice(id); return err },
		"Score":     func(id string) error { _, err := answers.Score(id); return err },
	} {
		err := read("sentiment")
		if err == nil {
			t.Fatal("reading an answer nobody asked for succeeded")
		}
		if !strings.Contains(err.Error(), `"sentiment"`) {
			t.Errorf("error %q does not name the id", err)
		}
		answerErr, ok := errors.AsType[*AnswerError](err)
		if !ok {
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
	answers := mixedAnswers()
	_, err := answers.Score("department")
	if err == nil {
		t.Fatal("reading a choice as a score succeeded")
	}
	answerErr, ok := errors.AsType[*AnswerError](err)
	if !ok {
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

// TestAnUnknownAnswerTypeIsReportedByItsType is the read side of invariant 6: an
// answer this version does not model is there to look up, and a typed accessor
// says what arrived rather than returning a zero value.
func TestAnUnknownAnswerTypeIsReportedByItsType(t *testing.T) {
	answers := NewAnswers([]NamedAnswer{{ID: "box", Answer: UnknownAnswer{
		Type: "box",
		Raw:  jsontext.Value(`{"type":"box","box":[0.1,0.2,0.3,0.4]}`),
	}}})
	_, err := answers.Predicate("box")
	if err == nil {
		t.Fatal("reading an unknown answer as a predicate succeeded")
	}
	if !strings.Contains(err.Error(), `"box"`) || !strings.Contains(err.Error(), "predicate") {
		t.Errorf("error %q does not say what arrived and what was asked for", err)
	}
}

func TestRankedBreaksTiesByKeySoItIsStable(t *testing.T) {
	choice := ChoiceAnswer{Probabilities: map[string]float64{"sales": 0.4, "billing": 0.4, "technical": 0.2}}
	want := []Outcome{
		{Key: "billing", Probability: 0.4},
		{Key: "sales", Probability: 0.4},
		{Key: "technical", Probability: 0.2},
	}
	if got := choice.Ranked(); !slices.Equal(got, want) {
		t.Errorf("Ranked() = %v, want %v", got, want)
	}
}

func TestRankedOrdersLevelIndicesNumerically(t *testing.T) {
	score := ScoreAnswer{Probabilities: map[string]float64{"10": 0.5, "2": 0.5}}
	want := []Outcome{{Key: "2", Probability: 0.5}, {Key: "10", Probability: 0.5}}
	if got := score.Ranked(); !slices.Equal(got, want) {
		t.Errorf("Ranked() = %v, want %v", got, want)
	}
}

// TestNewAnswersKeepsItsOwnEntries is what lets a codec reuse a buffer, and what
// keeps a built set from changing under a reader.
func TestNewAnswersKeepsItsOwnEntries(t *testing.T) {
	entries := []NamedAnswer{{ID: "a", Answer: PredicateAnswer{Probability: 0.5}}}
	answers := NewAnswers(entries)
	entries[0].ID = "changed"
	if got := answers.IDs(); !slices.Equal(got, []string{"a"}) {
		t.Errorf("IDs = %v after the caller's slice changed, want [a]", got)
	}
}

// TestAnswersHaveAJSONFormOfTheirOwn pins what a conversational model reads in a
// tool result, byte for byte. The form belongs to this package rather than to
// any server: a predicate is a probability under its own name, the ids stay in
// the order they arrived, and a distribution leads with its most likely outcome.
func TestAnswersHaveAJSONFormOfTheirOwn(t *testing.T) {
	encoded, err := json.Marshal(mixedAnswers())
	if err != nil {
		t.Fatalf("marshaling the answers: %v", err)
	}
	const want = `{"model":"model-1","answers":{` +
		`"department":{"type":"choice","choice":"billing","confidence":0.96,"probabilities":{"billing":0.97,"technical":0.03,"sales":0}},` +
		`"is_urgent":{"type":"predicate","probability":0.96},` +
		`"frustration":{"type":"score","score":2.12,"confidence":0.87,` +
		`"legend":{"0":"Calm","1":"Mildly annoyed","2":"Frustrated","3":"Furious"},` +
		`"probabilities":{"2":0.88,"3":0.12,"0":0,"1":0}}},` +
		`"usage":{"input_tokens":448,"output_tokens":73}}`
	if string(encoded) != want {
		t.Errorf("the JSON form is\n%s\nwant\n%s", encoded, want)
	}
}

// TestARefusalTellsTheModelTheOthersStand pins the words a model reads where an
// answer would be: without them it has a bare type member to interpret, and the
// load-bearing instruction belongs where it is looking when it acts.
func TestARefusalTellsTheModelTheOthersStand(t *testing.T) {
	encoded, err := json.Marshal(refusedAnswers())
	if err != nil {
		t.Fatalf("marshaling the answers: %v", err)
	}
	text := string(encoded)
	for _, want := range []string{
		`"self_harm":{"type":"refusal","note":"`,
		"declined",
		"stand",
		`"department":{"type":"choice"`,
		`"is_urgent":{"type":"predicate","probability":0.96}`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the JSON form does not carry %q:\n%s", want, text)
		}
	}
}

// TestAnswersRoundTripThroughJSON is what the tool adapters need: an answer set
// handed to a model as JSON and read back must be the same answer set, with the
// ids in the same order and every shape intact, including the two a typed
// accessor will not read.
func TestAnswersRoundTripThroughJSON(t *testing.T) {
	original := NewAnswers([]NamedAnswer{
		{ID: "is_urgent", Answer: PredicateAnswer{Probability: 0.25}},
		{ID: "declined", Answer: RefusalAnswer{}},
		{ID: "box", Answer: UnknownAnswer{Type: "box", Raw: jsontext.Value(`{"type":"box","box":[0.1,0.2]}`)}},
		{ID: "department", Answer: ChoiceAnswer{Choice: "billing", Confidence: 0.5, Probabilities: map[string]float64{"billing": 0.75, "other": 0.25}}},
		{ID: "anger", Answer: ScoreAnswer{Score: 0.5, Confidence: 0.25, Legend: map[string]string{"0": "Calm", "1": "Furious"}, Probabilities: map[string]float64{"0": 0.5, "1": 0.5}}},
	})
	original.Model = "model-1"
	original.Usage = goodall.Usage{Input: 10, Output: 2, CacheRead: 3, Reasoning: 1}
	amount, err := goodall.ParseDecimal("0.00125")
	if err != nil {
		t.Fatalf("parsing the amount: %v", err)
	}
	original.Cost = goodall.Cost{Amount: amount, Currency: "USD", Reported: true}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshaling the answers: %v", err)
	}
	var back Answers
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("decoding the answers again: %v\n%s", err, encoded)
	}
	if back.Model != original.Model {
		t.Errorf("Model = %q, want %q", back.Model, original.Model)
	}
	if back.Usage != original.Usage {
		t.Errorf("Usage = %+v, want %+v", back.Usage, original.Usage)
	}
	if back.Cost.Amount.Cmp(original.Cost.Amount) != 0 || back.Cost.Currency != "USD" || !back.Cost.Reported {
		t.Errorf("Cost = %+v, want %+v", back.Cost, original.Cost)
	}
	if got, want := back.IDs(), original.IDs(); !slices.Equal(got, want) {
		t.Errorf("IDs = %v, want %v", got, want)
	}
	for _, entry := range original.All() {
		answer, ok := back.Lookup(entry.ID)
		if !ok {
			t.Errorf("the answer under %q was lost", entry.ID)
			continue
		}
		if answer.AnswerType() != entry.Answer.AnswerType() {
			t.Errorf("%q came back as %s, want %s", entry.ID, answer.AnswerType(), entry.Answer.AnswerType())
		}
	}
	predicate, err := back.Predicate("is_urgent")
	if err != nil || predicate.Probability != 0.25 {
		t.Errorf("Predicate = %+v, %v; want 0.25", predicate, err)
	}
	again, err := json.Marshal(&back)
	if err != nil {
		t.Fatalf("marshaling the answers again: %v", err)
	}
	if string(again) != string(encoded) {
		t.Errorf("a second encoding differs\n got: %s\nwant: %s", again, encoded)
	}
}

// TestAnUnknownAnswerKeepsItsBytesThroughJSON is invariant 6 across the tool
// seam: what no type here models still reaches the reader unchanged.
func TestAnUnknownAnswerKeepsItsBytesThroughJSON(t *testing.T) {
	const raw = `{"type":"box","box":[0.1,0.2,0.3,0.4]}`
	var answers Answers
	body := `{"model":"model-1","answers":{"where":` + raw + `}}`
	if err := json.Unmarshal([]byte(body), &answers); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	answer, ok := answers.Lookup("where")
	if !ok {
		t.Fatal("the unknown answer was dropped")
	}
	unknown, ok := answer.(UnknownAnswer)
	if !ok {
		t.Fatalf("answer is %T, want UnknownAnswer", answer)
	}
	if unknown.Type != "box" {
		t.Errorf("Type = %q, want box", unknown.Type)
	}
	if string(unknown.Raw) != raw {
		t.Errorf("Raw = %s, want %s", unknown.Raw, raw)
	}
}

// TestANonObjectAnswerSetIsRefusedWhenDecoded keeps a body that is not an answer
// set from decoding into an empty one, which would read as no answers at all.
func TestANonObjectAnswerSetIsRefusedWhenDecoded(t *testing.T) {
	var answers Answers
	if err := json.Unmarshal([]byte(`{"model":"m","answers":["not","an","object"]}`), &answers); err == nil {
		t.Fatal("an answers member that is an array decoded without complaint")
	}
}
