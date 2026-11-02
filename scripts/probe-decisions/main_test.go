package main

import (
	"slices"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/systemone"
	"github.com/bensyverson/goodall/openrouter"
)

// TestConcreteModelsDropsWhatNamesAModelAlreadyListed: the count the sweep reports
// is reconciled across dates, so what it counts has to stay what it counted.
func TestConcreteModelsDropsWhatNamesAModelAlreadyListed(t *testing.T) {
	got := concreteModels([]openrouter.DecisionModel{
		{ID: "z/model"},
		{ID: "~z/model-latest"},
		{ID: "a/model"},
		{ID: "a/model:free"},
	})
	if want := []string{"a/model", "z/model"}; !slices.Equal(got, want) {
		t.Errorf("concreteModels = %v, want %v, sorted", got, want)
	}
}

// answerBody has one answer of each type under the ids the probe reads.
const answerBody = `{"model":"m","usage":{"input_tokens":10,"output_tokens":0,"cost":0.5},"answers":{
	"department":{"type":"choice","choice":"billing","confidence":1,"probabilities":{"billing":1}},
	"is_urgent":{"type":"noul","noul":0.9},
	"frustration":{"type":"score","score":2,"confidence":1,"legend":{"0":"a","1":"b"},"probabilities":{"0":0,"1":1}}}}`

// TestAReadableCallIsOneWhereEveryAccessorWorks is the definition behind the
// figure, tested from both sides.
func TestAReadableCallIsOneWhereEveryAccessorWorks(t *testing.T) {
	answers, err := systemone.DecodeAnswers([]byte(answerBody))
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if row := rowFor("m", "string", answers, nil); !row.readable {
		t.Errorf("a full answer set read as %q", row.text)
	}

	partial, err := systemone.DecodeAnswers([]byte(`{"model":"m","answers":{"is_urgent":{"type":"noul","noul":0.9}}}`))
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if row := rowFor("m", "string", partial, nil); row.readable {
		t.Errorf("an answer set missing two questions counted as readable: %q", row.text)
	}

	if row := rowFor("m", "string", nil, &goodall.APIError{Status: 400, Message: "no"}); row.readable {
		t.Errorf("a refused call counted as readable: %q", row.text)
	}
}

// TestTallyCountsReadableCallsAndWhatTheyCost: a call the model refused spent
// nothing, and a count taken by searching the rendered text could match a server's
// sentence.
func TestTallyCountsReadableCallsAndWhatTheyCost(t *testing.T) {
	amount, err := goodall.ParseDecimal("0.5")
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	half := goodall.Cost{Amount: amount, Currency: "USD", Reported: true}
	readable, cost := tally([]row{
		{readable: true, cost: half},
		{readable: false, text: "a sentence with the word ok in it"},
		{readable: true, cost: half},
	})
	if readable != 2 {
		t.Errorf("readable = %d, want 2", readable)
	}
	if !cost.Reported || cost.Amount.Cmp(amount.MulInt(2)) != 0 {
		t.Errorf("cost = %+v, want a reported 1", cost)
	}
}
