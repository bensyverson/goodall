package typesafe

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bensyverson/goodall/decision"
)

// The answer edge cases live with the answers, in goodall/decision and in
// internal/systemone. These are the ones worth proving on this client's whole
// path. The bodies are the shapes of the recordings in testdata, edited;
// testdata/README.md says which of the two a file is.

// answersFrom decodes one response body through the client.
func answersFrom(t *testing.T, body string) *decision.Answers {
	t.Helper()
	client, _ := serveJSON(t, http.StatusOK, body)
	answers, err := client.Decide(t.Context(), &decision.Request{State: "state", Questions: simpleQuestions()})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return answers
}

const mixedBody = `{"model":"jev-1.13.0","answers":{` +
	`"department":{"type":"choice","choice":"billing","confidence":0.96,"probabilities":{"technical":0.03,"billing":0.97,"sales":0.0}},` +
	`"is_urgent":{"type":"noul","noul":0.96},` +
	`"frustration":{"type":"score","score":2.12,"confidence":0.87,"legend":{"0":"Calm","1":"Mildly annoyed","2":"Frustrated","3":"Furious"},"probabilities":{"0":0.0,"1":0.0,"2":0.88,"3":0.12}}},` +
	`"usage":{"input_tokens":448,"output_tokens":73}}`

// TestAnUnknownAnswerTypeIsSurfaced is invariant 6: an answer this version does
// not model comes back as an Unknown carrying its bytes, never dropped. The API
// already serves a fourth type this package does not model, since a 422 names
// bounding_box among its expected tags, so this is the live case, not a
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
	unknown, ok := answer.(decision.UnknownAnswer)
	if !ok {
		t.Fatalf("answer is %T, want UnknownAnswer", answer)
	}
	if unknown.Type != "bounding_box" {
		t.Errorf("Type = %q, want bounding_box", unknown.Type)
	}
	if !strings.Contains(string(unknown.Raw), "0.3") {
		t.Errorf("Raw = %s, want the answer's own bytes", unknown.Raw)
	}
	_, err := answers.Predicate("box")
	if err == nil {
		t.Fatal("reading an unknown answer as a predicate succeeded")
	}
	if !strings.Contains(err.Error(), "bounding_box") {
		t.Errorf("error %q does not say what type arrived", err)
	}
}

// TestAnEmptyStateIsRefusedOnTheClientPath: a Decider's Validate call is the
// contract the seam states, so this client has to make it.
func TestAnEmptyStateIsRefusedOnTheClientPath(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, okAnswers)
	if _, err := client.Decide(t.Context(), &decision.Request{Questions: simpleQuestions()}); err == nil {
		t.Fatal("Decide accepted a nil state")
	}
	if seen.Requests != 0 {
		t.Errorf("the client made %d calls for a request it should have refused", seen.Requests)
	}
}

// TestDecideDoesNotModifyTheRequest is the seam's other promise: an adapter hands
// one request's questions to every call, and a filled-in default model must not
// stick to it.
func TestDecideDoesNotModifyTheRequest(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, okAnswers)
	req := &decision.Request{State: "state", Questions: simpleQuestions()}
	if _, err := client.Decide(t.Context(), req); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if req.Model != "" {
		t.Errorf("the request's model is now %q, want it left empty", req.Model)
	}
	if want := `"model":"` + DefaultModel + `"`; !strings.Contains(string(seen.Body), want) {
		t.Errorf("the body %s does not carry the default model", seen.Body)
	}
}
