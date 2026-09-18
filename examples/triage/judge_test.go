package main

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bensyverson/goodall/typesafe"
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
	// Body is the request exactly as it arrived.
	Body string
	// Model is the model the caller asked for.
	Model string
	// IDs are the question ids, in the order they were sent.
	IDs []string
	// State is the state as text, for keying answers off what was sent.
	State string
}

// judge is an httptest stand-in for Jev that answers whatever question ids it
// is sent and records every request. It answers from the state's own words, so
// a test scripts the judgments by writing the fixture and the drafts rather
// than by wiring an answer per call.
type judge struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []judgeRequest
	// refuse, when it reports true for a request, makes the judge answer
	// 422 instead, which is how a one-message failure is scripted.
	refuse func(judgeRequest) bool
}

// newJudge starts a fake judge for the life of the test.
func newJudge(t *testing.T) *judge {
	t.Helper()
	j := &judge{}
	j.server = httptest.NewServer(http.HandlerFunc(j.serve))
	t.Cleanup(j.server.Close)
	return j
}

// client is a typesafe client pointed at this judge.
func (j *judge) client() *typesafe.Client {
	return typesafe.New("test-key", typesafe.WithBaseURL(j.server.URL))
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

// serve answers one judgment request.
func (j *judge) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var wire struct {
		State jsontext.Value `json:"state"`
		Model string         `json:"model"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := judgeRequest{
		Body:  string(body),
		Model: wire.Model,
		IDs:   questionIDsOf(string(body)),
		State: string(wire.State),
	}
	j.mu.Lock()
	j.requests = append(j.requests, req)
	refuse := j.refuse
	j.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if refuse != nil && refuse(req) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"detail":[{"type":"value_error","loc":["body","state"],"msg":"this state cannot be evaluated"}]}`)
		return
	}
	fmt.Fprint(w, answerBody(req))
}

// questionIDsOf reads the ids out of a request body in the order they were
// written, which is the order the caller built them in.
func questionIDsOf(body string) []string {
	var wire struct {
		Questions jsontext.Value `json:"questions"`
	}
	if err := json.Unmarshal([]byte(body), &wire); err != nil {
		return nil
	}
	var ids []string
	rest := string(wire.Questions)
	for _, id := range allQuestionIDs {
		if strings.Contains(rest, `"`+string(id)+`":`) {
			ids = append(ids, string(id))
		}
	}
	return ids
}

// allQuestionIDs is every id this example sends, which is how the fake judge
// recognizes what it was asked without parsing the questions object.
var allQuestionIDs = []questionID{
	qDepartment, qNeedsReply, qIsUrgent, qIntent,
	qAnswersTheQuestion, qInventsAFact, qUnaskedPromise, qToneMismatch,
}

// answerBody is the response for one request: one answer per question id,
// decided from the words of the state.
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
		return noulAnswer(boolNoul(containsAny(state, "CSV", "purchase order", "print vendor", "liability", "pricing")))
	case qIsUrgent:
		return noulAnswer(boolNoul(containsAny(state, "print vendor")))
	case qIntent:
		return choiceAnswer(intentOf(state), []string{string(intentSummary), string(intentDrafts)})
	case qAnswersTheQuestion:
		return noulAnswer(boolNoul(!strings.Contains(state, "OFFTARGET")))
	case qInventsAFact:
		return noulAnswer(boolNoul(strings.Contains(state, "INVENTED")))
	case qUnaskedPromise:
		return noulAnswer(boolNoul(strings.Contains(state, "PROMISED")))
	case qToneMismatch:
		return noulAnswer(boolNoul(strings.Contains(state, "BRUSQUE")))
	}
	return noulAnswer(0.5)
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

// boolNoul is a decided probability, well clear of every threshold.
func boolNoul(yes bool) float64 {
	if yes {
		return 0.92
	}
	return 0.04
}

// noulAnswer writes a noul answer body.
func noulAnswer(p float64) string {
	return fmt.Sprintf(`{"type":"noul","noul":%v}`, p)
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
