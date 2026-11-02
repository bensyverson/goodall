package decision

import (
	"errors"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// TestValidateRefusesARequestThatCouldNeverBeAnswered is the "fail loudly" half
// of the seam, independent of any server: each message names what is wrong, and
// every one carries the kind a server's own refusal would.
func TestValidateRefusesARequestThatCouldNeverBeAnswered(t *testing.T) {
	predicate := Predicate{Instructions: "Urgent?"}
	cases := map[string]struct {
		req    *Request
		phrase string
	}{
		"no request":      {nil, "no request"},
		"nil state":       {&Request{Questions: Questions{{ID: "a", Question: predicate}}}, "state"},
		"no questions":    {&Request{State: "hi"}, "at least one question"},
		"empty questions": {&Request{State: "hi", Questions: Questions{}}, "at least one question"},
		"empty id":        {&Request{State: "hi", Questions: Questions{{Question: predicate}}}, "id"},
		"duplicate id": {&Request{State: "hi", Questions: Questions{
			{ID: "dup", Question: predicate},
			{ID: "dup", Question: predicate},
		}}, `"dup"`},
		"nil question":    {&Request{State: "hi", Questions: Questions{{ID: "empty"}}}, `"empty"`},
		"no instructions": {&Request{State: "hi", Questions: Questions{{ID: "urgent", Question: Predicate{}}}}, "instructions"},
		"score with one level": {&Request{State: "hi", Questions: Questions{{ID: "anger", Question: Score{
			Instructions: "How angry?", Levels: []string{"Calm"},
		}}}}, "two levels"},
		"score with an empty level": {&Request{State: "hi", Questions: Questions{{ID: "anger", Question: Score{
			Instructions: "How angry?", Levels: []string{"Calm", ""},
		}}}}, "empty level"},
		"choice with no options": {&Request{State: "hi", Questions: Questions{{ID: "dept", Question: Choice{
			Instructions: "Which team?",
		}}}}, "option"},
		"choice with an unnamed option": {&Request{State: "hi", Questions: Questions{{ID: "dept", Question: Choice{
			Instructions: "Which team?", Options: []Option{{Key: "billing"}, {Description: "no key"}},
		}}}}, "option"},
		"choice with a repeated option": {&Request{State: "hi", Questions: Questions{{ID: "dept", Question: Choice{
			Instructions: "Which team?", Options: []Option{{Key: "billing"}, {Key: "billing", Description: "again"}},
		}}}}, `"billing"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.req.Validate()
			if err == nil {
				t.Fatal("Validate accepted a request that could never be answered")
			}
			if !errors.Is(err, goodall.KindInvalidRequest) {
				t.Errorf("error is not KindInvalidRequest: %v", err)
			}
			if !strings.Contains(err.Error(), tc.phrase) {
				t.Errorf("error %q does not name %s", err, tc.phrase)
			}
		})
	}
}

func TestValidateAcceptsARequestWithNoModel(t *testing.T) {
	req := &Request{State: "hi", Questions: Questions{{ID: "a", Question: Predicate{Instructions: "Urgent?"}}}}
	if err := req.Validate(); err != nil {
		t.Errorf("the model is each Decider's own rule, but Validate refused: %v", err)
	}
}

func TestEachQuestionNamesItsType(t *testing.T) {
	cases := map[QuestionType]Question{
		TypePredicate: Predicate{},
		TypeChoice:    Choice{},
		TypeScore:     Score{},
	}
	for want, question := range cases {
		if got := question.QuestionType(); got != want {
			t.Errorf("%T names %s, want %s", question, got, want)
		}
	}
}
