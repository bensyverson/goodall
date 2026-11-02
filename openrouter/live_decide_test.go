package openrouter_test

import (
	"testing"

	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/livemodel"
	"github.com/bensyverson/goodall/openrouter"
)

// TestLiveOpenAIDecideAnswersEveryQuestionType is one real judgment on OpenAI's own
// wire: an object state, so the input_json part is what the server reads, and a
// predicate with descriptions, so the fold is. It is what no recording can prove.
func TestLiveOpenAIDecideAnswersEveryQuestionType(t *testing.T) {
	client := openrouter.New(
		openrouter.WithAPIKey(liveKey(t, openAIKeyName)),
		openrouter.WithBaseURL("https://api.openai.com/v1"),
		openrouter.WithDialect(openrouter.OpenAI),
	)
	answers, err := client.Decide(t.Context(), &decision.Request{
		Model: livemodel.OpenAIDecision,
		State: map[string]string{"body": "Help! My payouts have been failing for 3 days and I have staff to pay."},
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
	if err != nil {
		t.Fatalf("the live judgment failed: %v", err)
	}
	t.Logf("model=%s generation=%s usage=%+v", answers.Model, answers.Generation, answers.Usage)

	if answers.Usage.TotalInput() == 0 {
		t.Error("the live call reported no input tokens")
	}
	if answers.Cost.Reported {
		t.Errorf("Cost = %+v, and this server states none", answers.Cost)
	}
	if answers.Generation == "" {
		t.Error("the live call named no request id")
	}
	choice, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if choice.Choice != "billing" {
		t.Errorf("department = %q, want billing; a ticket about failed payouts is a billing ticket", choice.Choice)
	}
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	if !predicate.Yes(0.5) {
		t.Errorf("is_urgent = %v, want the yes side; the ticket says money is blocked", predicate.Probability)
	}
	score, err := answers.Score("frustration")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score.Score <= 0 {
		t.Errorf("frustration = %v, want above the calmest level", score.Score)
	}
	if got := len(score.Levels()); got != 4 {
		t.Errorf("Levels has %d entries, want the 4 that were sent", got)
	}
}

// TestLiveDecideAnswersEveryQuestionType is one real judgment on a pinned non-Jev
// model, and what no recording can prove: that the bytes this client sends are
// bytes OpenRouter accepts. Its assertions are the ones a caller's thresholds
// would make, so a failure means the request stopped being understood rather than
// that a figure moved.
func TestLiveDecideAnswersEveryQuestionType(t *testing.T) {
	client := liveClient(t)
	answers, err := client.Decide(t.Context(), &decision.Request{
		Model: livemodel.OpenRouterDecision,
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
	if err != nil {
		t.Fatalf("the live judgment failed: %v", err)
	}
	t.Logf("model=%s served_by=%s generation=%s input=%d cost=%s %s",
		answers.Model, answers.ServedBy, answers.Generation, answers.Usage.Input, answers.Cost.Amount, answers.Cost.Currency)

	if answers.Usage.Input == 0 {
		t.Error("the live call reported no input tokens")
	}
	if !answers.Cost.Reported || answers.Cost.Currency != "USD" {
		t.Errorf("Cost = %+v, want a reported figure in USD", answers.Cost)
	}
	if answers.Generation == "" || answers.ServedBy == "" {
		t.Errorf("Generation %q and ServedBy %q, want both stated", answers.Generation, answers.ServedBy)
	}
	choice, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if choice.Choice != "billing" {
		t.Errorf("department = %q, want billing; a ticket about failed payouts is a billing ticket", choice.Choice)
	}
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	if !predicate.Yes(0.5) {
		t.Errorf("is_urgent = %v, want the yes side; the ticket says money is blocked", predicate.Probability)
	}
	score, err := answers.Score("frustration")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score.Score <= 0 {
		t.Errorf("frustration = %v, want above the calmest level", score.Score)
	}
}
