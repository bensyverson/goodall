package openrouter

import (
	"encoding/json/jsontext"
	"errors"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/livemodel"
)

// These read the recordings scripts/record-fixtures made from api.openai.com on
// 2026-11-02 (testdata/README.md), so they are statements about OpenAI's wire
// rather than about what the fixture author believed it does.

// recordedOpenAIAnswers is the recorded mixed exchange, read through Decide.
func recordedOpenAIAnswers(t *testing.T) (*decision.Answers, *seenRequest) {
	t.Helper()
	client, seen := decideServer(t, http.StatusOK, string(fixture(t, "live_openai_decision_mixed.json")), WithDialect(OpenAI))
	answers, err := client.Decide(t.Context(), recordedDecisionRequest(livemodel.OpenAIDecision))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return answers, seen
}

// TestARecordedOpenAIDecisionDecodesThroughEveryAccessor is GPT-6 Luna's real answer
// read the way a consumer reads one.
func TestARecordedOpenAIDecisionDecodesThroughEveryAccessor(t *testing.T) {
	answers, _ := recordedOpenAIAnswers(t)
	if answers.Model != livemodel.OpenAIDecision {
		t.Errorf("Model = %q", answers.Model)
	}
	if want := (goodall.Usage{Input: 433}); answers.Usage != want {
		t.Errorf("Usage = %+v, want %+v", answers.Usage, want)
	}
	if answers.Cost.Reported {
		t.Errorf("Cost = %+v, and the response states none", answers.Cost)
	}
	if got, want := answers.IDs(), []string{"department", "is_urgent", "frustration"}; !slices.Equal(got, want) {
		t.Errorf("IDs = %v, want %v", got, want)
	}
	choice, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if choice.Choice != "billing" || choice.Confidence != 1 || choice.Probabilities["billing"] != 1 {
		t.Errorf("choice = %+v", choice)
	}
	if ranked := choice.Ranked(); len(ranked) != 3 || ranked[0].Key != "billing" {
		t.Errorf("Ranked = %v", ranked)
	}
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	if predicate.Probability != 0.98 || !predicate.Yes(0.5) {
		t.Errorf("predicate = %+v", predicate)
	}
	score, err := answers.Score("frustration")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score.Score != 2 || score.Confidence != 0.96 || score.Probabilities["2"] != 0.96 {
		t.Errorf("score = %+v", score)
	}
	if got, want := score.Levels(), []string{"Calm", "Mildly annoyed", "Frustrated", "Furious"}; !slices.Equal(got, want) {
		t.Errorf("Levels = %v, want %v", got, want)
	}
	if ranked := score.Ranked(); ranked[0].Key != "2" {
		t.Errorf("Ranked = %v, want level 2 first", ranked)
	}
}

// TestBothWiresReadAsTheSameNeutralShapes is what makes the types neutral: the same
// model, asked the same questions over the same state, once on each wire, and a
// consumer holding either answer set cannot tell which wire it came from. The
// figures are compared too because on this recording they agree, as does the
// token count, so a key that drifted would show.
func TestBothWiresReadAsTheSameNeutralShapes(t *testing.T) {
	direct, _ := recordedOpenAIAnswers(t)
	client, _ := decideServer(t, http.StatusOK, string(fixture(t, "live_decision_mixed.json")))
	routed, err := client.Decide(t.Context(), recordedDecisionRequest(livemodel.OpenRouterDecision))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if direct.Usage != routed.Usage {
		t.Errorf("Usage: direct %+v, routed %+v", direct.Usage, routed.Usage)
	}
	if !slices.Equal(direct.IDs(), routed.IDs()) {
		t.Errorf("IDs: direct %v, routed %v", direct.IDs(), routed.IDs())
	}
	for _, id := range direct.IDs() {
		d, _ := direct.Lookup(id)
		r, _ := routed.Lookup(id)
		switch want := r.(type) {
		case decision.PredicateAnswer:
			if d != decision.Answer(want) {
				t.Errorf("%s: direct %+v, routed %+v", id, d, want)
			}
		case decision.ChoiceAnswer:
			got, ok := d.(decision.ChoiceAnswer)
			if !ok || got.Choice != want.Choice || got.Confidence != want.Confidence || !maps.Equal(got.Probabilities, want.Probabilities) {
				t.Errorf("%s: direct %+v, routed %+v", id, d, want)
			}
		case decision.ScoreAnswer:
			got, ok := d.(decision.ScoreAnswer)
			if !ok || got.Score != want.Score || got.Confidence != want.Confidence ||
				!maps.Equal(got.Legend, want.Legend) || !maps.Equal(got.Probabilities, want.Probabilities) {
				t.Errorf("%s: direct %+v, routed %+v", id, d, want)
			}
		default:
			t.Errorf("%s: the routed recording holds %T", id, r)
		}
	}
}

// TestARecordedOpenAIRequestIsStillTheBytesSent is the encode side's guard, on both
// the state's input_json part and the folded criteria.
func TestARecordedOpenAIRequestIsStillTheBytesSent(t *testing.T) {
	_, seen := recordedOpenAIAnswers(t)
	assertRecordedBody(t, seen.body, "live_openai_decision_mixed.request.json")

	client, seen := decideServer(t, http.StatusOK, string(fixture(t, "live_openai_decision_criteria.json")), WithDialect(OpenAI))
	answers, err := client.Decide(t.Context(), &decision.Request{
		Model: livemodel.OpenAIDecision,
		State: "Help! My payouts have been failing for 3 days and I have staff to pay.",
		Questions: decision.Questions{{ID: "is_urgent", Question: decision.Predicate{
			Instructions: "Does this ticket convey urgency?",
			True:         "Money or work is blocked right now",
			False:        "The sender can wait",
		}}},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if _, err := answers.Predicate("is_urgent"); err != nil {
		t.Errorf("Predicate: %v", err)
	}
	assertRecordedBody(t, seen.body, "live_openai_decision_criteria.request.json")
}

// assertRecordedBody compares what was sent with a recording, indented alike.
func assertRecordedBody(t *testing.T, body, name string) {
	t.Helper()
	sent := jsontext.Value(body)
	if err := sent.Indent(jsontext.WithIndent("  ")); err != nil {
		t.Fatalf("indenting what was sent: %v", err)
	}
	if got, want := string(sent)+"\n", string(fixture(t, name)); got != want {
		t.Errorf("the request is no longer the recorded bytes\n got: %s\nwant: %s", got, want)
	}
}

// TestARecordedOpenAIErrorSurfacesItsSentence reads both spellings of the code this
// server was seen to send, a string and a null. Each carries a sentence a caller,
// or a model holding a tool result, can act on, and nothing else in the neutral
// error is worth losing: the type and the param stay on Raw.
func TestARecordedOpenAIErrorSurfacesItsSentence(t *testing.T) {
	for _, tc := range []struct {
		fixture, message, errorType string
	}{
		{
			"live_openai_decision_one_option.json",
			"Invalid 'questions[0].choices': array too short. Expected an array with minimum length 2, but got an array with length 1 instead.",
			"array_below_min_length",
		},
		{
			"live_openai_decision_too_deep.json",
			"input_json data exceeds the maximum depth of 32",
			"",
		},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			client, _ := decideServer(t, http.StatusBadRequest, string(fixture(t, tc.fixture)), WithDialect(OpenAI))
			_, err := client.Decide(t.Context(), recordedDecisionRequest(livemodel.OpenAIDecision))
			apiErr, ok := errors.AsType[*goodall.APIError](err)
			if !ok {
				t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
			}
			if apiErr.Provider != OpenAI.String() || apiErr.Kind != goodall.KindInvalidRequest || apiErr.Status != http.StatusBadRequest {
				t.Errorf("APIError = %+v", apiErr)
			}
			if apiErr.Message != tc.message {
				t.Errorf("Message = %q, want %q", apiErr.Message, tc.message)
			}
			if apiErr.Type != tc.errorType {
				t.Errorf("Type = %q, want %q", apiErr.Type, tc.errorType)
			}
			if len(apiErr.Raw) == 0 {
				t.Error("Raw is empty, want the envelope kept whole")
			}
		})
	}
}
