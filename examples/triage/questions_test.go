package main

import (
	json "encoding/json/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/bensyverson/goodall/decision"
)

// answersFrom decodes a response body into an Answers, which is how a test
// builds the judge's side of a judgment without a server.
func answersFrom(t *testing.T, body string) *decision.Answers {
	t.Helper()
	var answers decision.Answers
	if err := json.Unmarshal([]byte(body), &answers); err != nil {
		t.Fatalf("decoding the answers: %v", err)
	}
	return &answers
}

// predicateBody is a response carrying one predicate answer per id, in the order given.
func predicateBody(ids []questionID, values []float64) string {
	var b strings.Builder
	b.WriteString(`{"model":"jev-1.13.0","answers":{`)
	for i, id := range ids {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + string(id) + `":{"type":"predicate","probability":`)
		b.WriteString(floatText(values[i]))
		b.WriteString("}")
	}
	b.WriteString(`},"usage":{"input_tokens":10,"output_tokens":1}}`)
	return b.String()
}

// floatText writes a probability the way a JSON body spells it.
func floatText(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// TestTheQuestionSetsAreOnesTheAPIWouldAccept leans on decision.Tool, which
// validates a question set at construction exactly as a Decider does before
// sending, so a malformed question is a test failure rather than a 422 in
// front of a person.
func TestTheQuestionSetsAreOnesTheAPIWouldAccept(t *testing.T) {
	sets := map[string]decision.Questions{
		"triage":     triageQuestions(),
		"inspectors": inspectorQuestions(),
		"routing":    routingQuestions(),
	}
	for name, questions := range sets {
		if _, err := decision.Tool(newJudge(t), "probe", "A question set under test.", questions); err != nil {
			t.Errorf("the %s question set would be refused: %v", name, err)
		}
	}
}

// TestEveryDepartmentIsAnOptionOfTheDepartmentQuestion keeps the report's
// vocabulary and the judge's the same set.
func TestEveryDepartmentIsAnOptionOfTheDepartmentQuestion(t *testing.T) {
	var choice decision.Choice
	for _, named := range triageQuestions() {
		if named.ID == string(qDepartment) {
			c, ok := named.Question.(decision.Choice)
			if !ok {
				t.Fatalf("the department question is a %T, want a Choice", named.Question)
			}
			choice = c
		}
	}
	if len(choice.Options) != len(departments) {
		t.Fatalf("the department question offers %d options and the report knows %d", len(choice.Options), len(departments))
	}
	for i, dept := range departments {
		if choice.Options[i].Key != string(dept) {
			t.Errorf("option %d is %q, want %q", i, choice.Options[i].Key, dept)
		}
		if choice.Options[i].Description == "" {
			t.Errorf("option %q has no description, so the judge is guessing what it means", dept)
		}
	}
}

// TestFlaggedNamesOnlyTheInspectorsOverTheirThreshold is the policy the
// cascade turns on: which inspectors fired decides whether a draft is shown,
// redrafted or escalated.
func TestFlaggedNamesOnlyTheInspectorsOverTheirThreshold(t *testing.T) {
	ids := make([]questionID, 0, len(inspectors))
	for _, ins := range inspectors {
		ids = append(ids, ins.ID)
	}

	clean := []float64{1, 0, 0, 0}
	fired, err := flagged(answersFrom(t, predicateBody(ids, clean)))
	if err != nil {
		t.Fatalf("reading a clean check: %v", err)
	}
	if len(fired) != 0 {
		t.Errorf("a clean draft fired %v", labels(fired))
	}

	// The first inspector asks whether the draft answers the question, so
	// it fires low; the other three ask about a defect and fire high.
	dirty := []float64{0.1, 0.9, 0.05, 0.05}
	fired, err = flagged(answersFrom(t, predicateBody(ids, dirty)))
	if err != nil {
		t.Fatalf("reading a flagged check: %v", err)
	}
	want := []string{string(qAnswersTheQuestion), string(qInventsAFact)}
	got := make([]string, 0, len(fired))
	for _, ins := range fired {
		got = append(got, string(ins.ID))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("fired %v, want %v", got, want)
	}
}

// TestFlaggedReportsAnAnswerItCannotRead fails loudly rather than reading a
// missing judgment as a clean draft.
func TestFlaggedReportsAnAnswerItCannotRead(t *testing.T) {
	ids := []questionID{qAnswersTheQuestion, qInventsAFact, qUnaskedPromise}
	_, err := flagged(answersFrom(t, predicateBody(ids, []float64{1, 0, 0})))
	if err == nil {
		t.Fatal("a check missing an inspector was read as an answer")
	}
	if !strings.Contains(err.Error(), string(qToneMismatch)) {
		t.Errorf("the error does not name the missing inspector: %v", err)
	}
}

// TestReadTriageAppliesTheThresholds checks the two predicates are read through the
// named thresholds rather than through a bare comparison at a call site.
func TestReadTriageAppliesTheThresholds(t *testing.T) {
	body := `{"model":"jev-1.13.0","answers":{` +
		`"department":{"type":"choice","choice":"billing","confidence":0.9,"probabilities":{"billing":0.82,"technical":0.18}},` +
		`"needs_reply":{"type":"predicate","probability":` + floatText(needsReplyThreshold) + `},` +
		`"is_urgent":{"type":"predicate","probability":` + floatText(urgentThreshold-0.01) + `}` +
		`},"usage":{"input_tokens":10,"output_tokens":1}}`
	verdict, err := readTriage(answersFrom(t, body))
	if err != nil {
		t.Fatalf("reading a triage answer: %v", err)
	}
	if verdict.Department != departmentBilling {
		t.Errorf("department is %q, want %q", verdict.Department, departmentBilling)
	}
	if verdict.DepartmentProbability != 0.82 {
		t.Errorf("department probability is %v, want 0.82", verdict.DepartmentProbability)
	}
	if !verdict.NeedsReply {
		t.Error("a needs_reply exactly at the threshold was read as no; the threshold is inclusive")
	}
	if verdict.Urgent {
		t.Error("an is_urgent below the threshold was read as yes")
	}
}

// TestReadTriageMarksAnUnsureDepartment keeps a close call out of the report's
// plain statements: a probability under the floor is reported as unsure.
func TestReadTriageMarksAnUnsureDepartment(t *testing.T) {
	body := `{"model":"jev-1.13.0","answers":{` +
		`"department":{"type":"choice","choice":"sales","confidence":0.3,"probabilities":{"sales":0.34,"other":0.33,"personal":0.33}},` +
		`"needs_reply":{"type":"predicate","probability":0.1},` +
		`"is_urgent":{"type":"predicate","probability":0.1}` +
		`},"usage":{"input_tokens":10,"output_tokens":1}}`
	verdict, err := readTriage(answersFrom(t, body))
	if err != nil {
		t.Fatalf("reading a triage answer: %v", err)
	}
	if !verdict.Unsure {
		t.Errorf("a department at %v was reported as settled; the floor is %v",
			verdict.DepartmentProbability, departmentFloor)
	}
}

// TestReadIntentRefusesAnIntentItDoesNotKnow keeps a routing answer the code
// has no policy for from silently narrowing a turn.
func TestReadIntentRefusesAnIntentItDoesNotKnow(t *testing.T) {
	body := `{"model":"jev-1.13.0","answers":{"intent":{"type":"choice","choice":"weather","confidence":0.99,` +
		`"probabilities":{"weather":0.99,"summary":0.01}}},"usage":{"input_tokens":1,"output_tokens":1}}`
	if _, err := readIntent(answersFrom(t, body)); err == nil {
		t.Fatal("an unknown intent was accepted")
	}
}
