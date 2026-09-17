package typesafe_test

import (
	"testing"

	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/internal/livemodel"
	"github.com/bensyverson/goodall/typesafe"
)

// This is the live test: it calls the real TypeSafe API with the key in the
// repository's .env and costs real money, so there is one judgment call in the
// whole file. It skips, saying what it skipped and why, when the key is absent
// and under -short, which is what makes `go test -short ./...` the offline
// suite.
//
// What it is for is the half of the client no recording can prove: that the
// bytes goodall sends are bytes TypeSafe accepts. The fixtures under testdata
// prove the decoding; only a live call proves the encoding.

// liveKeyName is the .env entry holding the key, named in the skip message so a
// reader knows what to add.
const liveKeyName = "TYPESAFE_API_KEY"

// liveClient builds a client from the repository's .env, or skips the test
// saying which key in which file was missing.
func liveClient(t *testing.T) *typesafe.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the live TypeSafe call: -short is set, so nothing was sent to the API")
	}
	path, err := dotenv.RepoEnvFile()
	if err != nil {
		t.Skipf("skipping the live TypeSafe call: %v", err)
	}
	key, ok := dotenv.Lookup(path, liveKeyName)
	if !ok {
		t.Skipf("skipping the live TypeSafe call: no %s in %s, so no call was made", liveKeyName, path)
	}
	return typesafe.New(key)
}

// TestLiveMixedJudgmentRoundTrip sends one request carrying all three question
// types and asserts what the answers must be *about*, not what they must be:
// the judgments are the model's, and a test that pinned 0.96 would fail on the
// next release for no reason a reader could act on. The assertions are the ones
// a caller's thresholds would make — this ticket is a billing ticket, this
// customer is not calm — so a failure means the request stopped being
// understood.
func TestLiveMixedJudgmentRoundTrip(t *testing.T) {
	client := liveClient(t)

	type ticket struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	questions := typesafe.Questions{
		{ID: "department", Question: typesafe.Choice{
			Instructions: "Which team should handle this ticket?",
			Options: []typesafe.Option{
				{Key: "billing", Description: "Payments, payouts, invoicing, refunds"},
				{Key: "technical", Description: "Bugs, outages, integrations"},
				{Key: "sales", Description: "Pricing, upgrades, new accounts"},
			},
		}},
		{ID: "is_urgent", Question: typesafe.Noul{
			Instructions: "Does this ticket convey urgency?",
			True:         "Explicitly time-sensitive, or money or work is blocked right now",
			False:        "No urgency expressed; the sender can wait",
		}},
		{ID: "frustration", Question: typesafe.Score{
			Instructions: "How frustrated is the customer?",
			Levels:       []string{"Calm", "Mildly annoyed", "Frustrated", "Furious"},
		}},
	}

	answers, err := client.Ask(t.Context(), ticket{
		Subject: "Payouts failing since Tuesday",
		Body:    "Help! My payouts have been failing for 3 days and I have staff to pay.",
	}, questions, typesafe.WithModel(livemodel.TypeSafe))
	if err != nil {
		t.Fatalf("the live judgment failed: %v", err)
	}
	t.Logf("model=%s input=%d output=%d", answers.Model, answers.Usage.Input, answers.Usage.Output)

	if answers.Model != livemodel.TypeSafe {
		t.Errorf("the answer reports model %q, want %q", answers.Model, livemodel.TypeSafe)
	}
	if answers.Usage.Input == 0 {
		t.Error("the live call reported no input tokens")
	}
	if answers.Cost.Reported {
		t.Error("the live call reported a cost; TypeSafe puts no money figure on this response")
	}
	if got, want := len(answers.IDs()), len(questions); got != want {
		t.Fatalf("the answer carries %d answers, want %d: %v", got, want, answers.IDs())
	}

	choice, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	t.Logf("department: %s confidence=%.4f ranked=%v", choice.Choice, choice.Confidence, choice.Ranked())
	if choice.Choice != "billing" {
		t.Errorf("department = %q, want billing; a ticket about failed payouts is a billing ticket", choice.Choice)
	}
	if len(choice.Probabilities) != 3 {
		t.Errorf("probabilities cover %d options, want 3", len(choice.Probabilities))
	}

	noul, err := answers.Noul("is_urgent")
	if err != nil {
		t.Fatalf("Noul: %v", err)
	}
	t.Logf("is_urgent: noul=%.4f", noul.Noul)
	if !noul.Yes(0.5) {
		t.Errorf("is_urgent = %v, want the yes side; the ticket says money is blocked", noul.Noul)
	}

	score, err := answers.Score("frustration")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	t.Logf("frustration: score=%.4f confidence=%.4f levels=%q", score.Score, score.Confidence, score.Levels())
	if score.Score <= 0 {
		t.Errorf("frustration = %v, want above the calmest level", score.Score)
	}
	if got, want := len(score.Levels()), 4; got != want {
		t.Errorf("the legend names %d levels, want %d", got, want)
	}
}

// TestLiveModelsListsTheAliases reads the catalog, which costs no tokens.
func TestLiveModelsListsTheAliases(t *testing.T) {
	client := liveClient(t)

	cards, err := client.Models(t.Context())
	if err != nil {
		t.Fatalf("the live catalog read failed: %v", err)
	}
	if len(cards) == 0 {
		t.Fatal("the live catalog is empty")
	}
	for _, card := range cards {
		released, err := card.Released()
		if err != nil {
			t.Errorf("%s: %v", card.Name, err)
			continue
		}
		t.Logf("%s (%s): %s", card.Name, released.Format("2006-01-02"), card.Description)
	}
}
