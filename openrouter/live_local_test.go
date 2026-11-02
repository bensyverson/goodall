package openrouter_test

import (
	"errors"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/livemodel"
	"github.com/bensyverson/goodall/openrouter"
)

// liveLMStudio returns a keyless client, or skips loudly when no server answers.
// It costs nothing, so it runs whenever a server is up.
func liveLMStudio(t *testing.T) *openrouter.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the live LM Studio test under -short")
	}
	base := livemodel.LMStudioBase()
	if err := livemodel.ProbeLMStudio(); err != nil {
		t.Skipf("no local server at %s (%v); start LM Studio, load a decision model, or set %s", base, err, livemodel.LMStudioURLName)
	}
	return openrouter.New(openrouter.WithBaseURL(base), openrouter.WithDialect(openrouter.LMStudio))
}

// TestLiveLMStudioDecideAnswersEveryQuestionType is the same Decide against a real
// local server, with no key and no model named, so the server uses what is loaded.
func TestLiveLMStudioDecideAnswersEveryQuestionType(t *testing.T) {
	client := liveLMStudio(t)
	answers, err := client.Decide(t.Context(), &decision.Request{
		State: "Help! My payouts have been failing for 3 days and I have staff to pay.",
		Questions: decision.Questions{
			{ID: "department", Question: decision.Choice{
				Instructions: "Which team should handle this ticket?",
				Options: []decision.Option{
					{Key: "billing", Description: "Payments, payouts, invoicing, refunds"},
					{Key: "technical", Description: "Bugs, outages, integrations"},
				},
			}},
			{ID: "is_urgent", Question: decision.Predicate{
				Instructions: "Does this ticket convey urgency?",
				True:         "Money or work is blocked right now",
				False:        "The sender can wait",
			}},
			{ID: "frustration", Question: decision.Score{
				Instructions: "How frustrated is the customer?",
				Levels:       []string{"Calm", "Mildly annoyed", "Frustrated", "Furious"},
			}},
		},
	})
	if apiErr, ok := errors.AsType[*goodall.APIError](err); ok && apiErr.Type == livemodel.LMStudioNoModels {
		t.Skipf("the server at %s has no model loaded; run lms load with a decision model", livemodel.LMStudioBase())
	}
	if err != nil {
		t.Fatalf("the live judgment failed: %v", err)
	}
	t.Logf("model=%s usage=%+v cost=%+v", answers.Model, answers.Usage, answers.Cost)

	if answers.Model == "" {
		t.Error("the response named no model")
	}
	if answers.Cost.Reported {
		t.Errorf("Cost = %+v, and a local server charges nothing", answers.Cost)
	}
	choice, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	t.Logf("choice=%+v", choice)
	if choice.Choice != "billing" && choice.Choice != "technical" {
		t.Errorf("Choice = %q, which was not offered", choice.Choice)
	}
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	t.Logf("predicate=%+v", predicate)
	if predicate.Probability < 0 || predicate.Probability > 1 {
		t.Errorf("Probability = %v, outside 0 to 1", predicate.Probability)
	}
	score, err := answers.Score("frustration")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	t.Logf("score=%+v", score)
	if score.Score < 0 || score.Score > 3 {
		t.Errorf("Score = %v, outside the 4 levels", score.Score)
	}
}

// TestLiveLMStudioErrorCarriesItsSentence reads a real refusal from the server,
// worded differently from OpenRouter's. A model that is not there passes this
// client's validation, because only the server knows what it has.
func TestLiveLMStudioErrorCarriesItsSentence(t *testing.T) {
	client := liveLMStudio(t)
	_, err := client.Decide(t.Context(), &decision.Request{
		Model: "no-such-model",
		State: "x",
		Questions: decision.Questions{{ID: "q", Question: decision.Predicate{
			Instructions: "Is it true?",
		}}},
	})
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
	}
	t.Logf("status=%d kind=%v type=%q message=%q", apiErr.Status, apiErr.Kind, apiErr.Type, apiErr.Message)
	if apiErr.Message == "" || apiErr.Message[0] == '{' {
		t.Errorf("Message = %q, want the server's sentence", apiErr.Message)
	}
}
