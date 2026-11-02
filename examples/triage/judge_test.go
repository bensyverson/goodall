package main

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
)

// The tokens every scripted judgment reports, so a test can assert that a
// tool summed the calls it made rather than reporting one of them.
const (
	judgeInputTokens  = 100
	judgeOutputTokens = 5
)

// judgeRequest is one request the fake judge received, kept whole: the raw
// body is what the privacy test reads, and the decoded parts are what the
// answering logic switches on.
type judgeRequest struct {
	// Body is everything the request carried, as text, which is what a
	// privacy assertion has to search.
	Body string
	// Model is the model the caller asked for.
	Model string
	// IDs are the question ids, in the order they were sent.
	IDs []string
	// State is the state as text, for keying answers off what was sent.
	State string
}

// judge is a stand-in [decision.Decider] that answers whatever question ids it is
// sent and records every request. It answers from the state's own words, so a
// test scripts the judgments by writing the fixture and the drafts rather than by
// wiring an answer per call.
//
// It sits at the seam rather than on one server's wire, since this example's
// questions and policy are not about any wire; goodall/typesafe tests its own.
type judge struct {
	mu       sync.Mutex
	requests []judgeRequest
	// refuse, when it reports true for a request, makes the judge reject it
	// instead, which is how a one-message failure is scripted.
	refuse func(judgeRequest) bool
}

// newJudge builds a fake judge.
func newJudge(t *testing.T) *judge {
	t.Helper()
	return &judge{}
}

// seen is every request the judge received, in arrival order.
func (j *judge) seen() []judgeRequest {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]judgeRequest, len(j.requests))
	copy(out, j.requests)
	return out
}

// asked is how many requests carried the given question id.
func (j *judge) asked(id questionID) int {
	n := 0
	for _, req := range j.seen() {
		if slices.Contains(req.IDs, string(id)) {
			n++
		}
	}
	return n
}

// Decide answers one judgment request, doing what a real client does first and
// refusing a request that could never be answered.
func (j *judge) Decide(_ context.Context, r *decision.Request) (*decision.Answers, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	state, err := json.Marshal(r.State, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	req := judgeRequest{
		Body:  fmt.Sprintf("%s %+v", state, r.Questions),
		Model: r.Model,
		IDs:   questionIDsOf(r.Questions),
		State: string(state),
	}
	j.mu.Lock()
	j.requests = append(j.requests, req)
	refuse := j.refuse
	j.mu.Unlock()

	if refuse != nil && refuse(req) {
		return nil, &goodall.APIError{
			Provider: "fake-judge",
			Status:   http.StatusUnprocessableEntity,
			Kind:     goodall.KindInvalidRequest,
			Message:  "body.state: this state cannot be evaluated",
		}
	}
	var answers decision.Answers
	if err := json.Unmarshal([]byte(answerBody(req)), &answers); err != nil {
		return nil, err
	}
	return &answers, nil
}

// questionIDsOf lists the ids in the order the caller built them in.
func questionIDsOf(questions decision.Questions) []string {
	ids := make([]string, 0, len(questions))
	for _, question := range questions {
		ids = append(ids, question.ID)
	}
	return ids
}

// answerBody is the answer set for one request, in [decision.Answers]'s own JSON
// form: one answer per question id, decided from the words of the state.
func answerBody(req judgeRequest) string {
	var answers []string
	for _, id := range req.IDs {
		answers = append(answers, fmt.Sprintf("%q:%s", id, answerFor(questionID(id), req.State)))
	}
	return fmt.Sprintf(`{"model":"jev-1.13.0","answers":{%s},"usage":{"input_tokens":%d,"output_tokens":%d}}`,
		strings.Join(answers, ","), judgeInputTokens, judgeOutputTokens)
}

// answerFor is one scripted answer. The triage answers key off the sample
// inbox's subjects, the inspectors off markers a test writes into a draft, and
// the router off the person's own words.
func answerFor(id questionID, state string) string {
	switch id {
	case qDepartment:
		return choiceAnswer(string(departmentOf(state)), departmentKeys())
	case qNeedsReply:
		return predicateAnswer(decided(containsAny(state, "CSV", "purchase order", "print vendor", "liability", "pricing")))
	case qIsUrgent:
		return predicateAnswer(decided(containsAny(state, "print vendor")))
	case qIntent:
		return choiceAnswer(intentOf(state), []string{string(intentSummary), string(intentDrafts)})
	case qAnswersTheQuestion:
		return predicateAnswer(decided(!strings.Contains(state, "OFFTARGET")))
	case qInventsAFact:
		return predicateAnswer(decided(strings.Contains(state, "INVENTED")))
	case qUnaskedPromise:
		return predicateAnswer(decided(strings.Contains(state, "PROMISED")))
	case qToneMismatch:
		return predicateAnswer(decided(strings.Contains(state, "BRUSQUE")))
	}
	return predicateAnswer(0.5)
}

// departmentOf is the department the sample inbox's subjects imply.
func departmentOf(state string) department {
	switch {
	case containsAny(state, "purchase order", "receipt", "Invoice"):
		return departmentBilling
	case containsAny(state, "CSV", "shipped", "backup", "print vendor", "redlines", "Ops sync"):
		return departmentTechnical
	case containsAny(state, "seats", "pricing"):
		return departmentSales
	case containsAny(state, "ridge"):
		return departmentPersonal
	}
	return departmentOther
}

// intentOf is what a person's turn asks for, by its own words.
func intentOf(state string) string {
	if containsAny(state, "draft", "reply", "replies", "answer them") {
		return string(intentDrafts)
	}
	return string(intentSummary)
}

// departmentKeys is the option set of the department question.
func departmentKeys() []string {
	keys := make([]string, 0, len(departments))
	for _, dept := range departments {
		keys = append(keys, string(dept))
	}
	return keys
}

// containsAny reports whether s holds any of the needles.
func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// decided is a settled probability, well clear of every threshold.
func decided(yes bool) float64 {
	if yes {
		return 0.92
	}
	return 0.04
}

// predicateAnswer writes a predicate answer.
func predicateAnswer(p float64) string {
	return fmt.Sprintf(`{"type":"predicate","probability":%v}`, p)
}

// choiceAnswer writes a choice answer body: the pick at 0.88 and the rest
// sharing what is left, which is a distribution a report can print.
func choiceAnswer(pick string, options []string) string {
	rest := 0.0
	if len(options) > 1 {
		rest = 0.12 / float64(len(options)-1)
	}
	parts := make([]string, 0, len(options))
	for _, option := range options {
		p := rest
		if option == pick {
			p = 0.88
		}
		parts = append(parts, fmt.Sprintf("%q:%v", option, p))
	}
	return fmt.Sprintf(`{"type":"choice","choice":%q,"confidence":0.91,"probabilities":{%s}}`, pick, strings.Join(parts, ","))
}
