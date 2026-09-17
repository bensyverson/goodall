package typesafe

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// These tests read the recordings scripts/record-fixtures made from the live
// API (see testdata/README.md for the date and the model). They are what makes
// the offline suite a statement about TypeSafe rather than about what the
// fixture author believed TypeSafe does: every byte here came off the wire.
//
// The answers are the ones jev-1.13.0 gave to questions written so that the
// answer is not a coin toss — a ticket about failed payouts is a billing
// ticket, and a customer with staff to pay is not calm — so an assertion on
// them is a statement about the decoding and about the model both.

// fixture reads one recording.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading the recording: %v", err)
	}
	return data
}

// askFixture replays one recorded answer body through the client, which is the
// same path a live response takes.
func askFixture(t *testing.T, name string) (*Answers, error) {
	t.Helper()
	client, _ := serveJSON(t, http.StatusOK, string(fixture(t, name)))
	return client.Ask(t.Context(), "replayed", simpleQuestions())
}

func TestRecordedMixedAnswersDecode(t *testing.T) {
	answers, err := askFixture(t, "live_mixed.json")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if answers.Model != "jev-1.13.0" {
		t.Errorf("Model = %q, want the versioned id that answered", answers.Model)
	}
	if answers.Usage.Input != 448 || answers.Usage.Output != 73 {
		t.Errorf("Usage = %+v, want input 448 and output 73", answers.Usage)
	}
	if answers.Cost.Reported {
		t.Error("Cost is reported; TypeSafe puts no money figure on this response")
	}
	if ids := answers.IDs(); len(ids) != 3 || ids[0] != "department" {
		t.Errorf("IDs() = %v, want the three ids in the order they arrived", ids)
	}

	choice, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if choice.Choice != "billing" {
		t.Errorf("choice = %q, want billing", choice.Choice)
	}
	if choice.Confidence != 0.96 {
		t.Errorf("confidence = %v, want 0.96", choice.Confidence)
	}
	if got := choice.Probabilities["billing"]; got != 0.97 {
		t.Errorf("probabilities[billing] = %v, want 0.97", got)
	}
	wantRanked := []Outcome{
		{Key: "billing", Probability: 0.97},
		{Key: "technical", Probability: 0.03},
		{Key: "sales", Probability: 0},
	}
	if ranked := choice.Ranked(); len(ranked) != 3 {
		t.Errorf("Ranked() = %v, want three outcomes", ranked)
	} else {
		for i := range wantRanked {
			if ranked[i] != wantRanked[i] {
				t.Errorf("Ranked()[%d] = %+v, want %+v", i, ranked[i], wantRanked[i])
			}
		}
	}

	noul, err := answers.Noul("is_urgent")
	if err != nil {
		t.Fatalf("Noul: %v", err)
	}
	if noul.Noul != 0.96 {
		t.Errorf("noul = %v, want 0.96", noul.Noul)
	}

	score, err := answers.Score("frustration")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score.Score != 2.12 {
		t.Errorf("score = %v, want 2.12", score.Score)
	}
	if score.Confidence != 0.87 {
		t.Errorf("confidence = %v, want 0.87", score.Confidence)
	}
	wantLevels := []string{"Calm", "Mildly annoyed", "Frustrated", "Furious"}
	levels := score.Levels()
	if len(levels) != len(wantLevels) {
		t.Fatalf("Levels() = %v, want %v", levels, wantLevels)
	}
	for i := range wantLevels {
		if levels[i] != wantLevels[i] {
			t.Errorf("Levels()[%d] = %q, want %q", i, levels[i], wantLevels[i])
		}
	}
	if got := score.Probabilities["2"]; got != 0.88 {
		t.Errorf("probabilities[2] = %v, want 0.88", got)
	}
	if top := score.Ranked(); len(top) == 0 || top[0].Key != "2" {
		t.Errorf("Ranked() = %v, want the level with 0.88 first", top)
	}
}

func TestRecordedNoulWithCriteriaDecodes(t *testing.T) {
	answers, err := askFixture(t, "live_noul_criteria.json")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	noul, err := answers.Noul("is_urgent")
	if err != nil {
		t.Fatalf("Noul: %v", err)
	}
	if noul.Noul != 0.98 {
		t.Errorf("noul = %v, want 0.98", noul.Noul)
	}
	// A noul answer carries no confidence at all, which is why NoulAnswer
	// has no field for one: 0.5 means "yes and no are equally likely", not
	// "medium confidence".
	if _, err := answers.Choice("is_urgent"); err == nil {
		t.Error("reading a noul as a choice succeeded")
	}
}

func TestRecordedModelCatalogDecodes(t *testing.T) {
	client, _ := serveJSON(t, http.StatusOK, string(fixture(t, "live_models.json")))
	cards, err := client.Models(t.Context())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2: %+v", len(cards), cards)
	}
	if cards[0].Name != "jev-latest" || cards[1].Name != "jev-preview" {
		t.Errorf("names = %q and %q, want jev-latest then jev-preview", cards[0].Name, cards[1].Name)
	}
	for _, card := range cards {
		if card.Description == "" {
			t.Errorf("%s has no description", card.Name)
		}
		released, err := card.Released()
		if err != nil {
			t.Errorf("%s: Released: %v", card.Name, err)
			continue
		}
		if released.Year() != 2026 {
			t.Errorf("%s released in %d, want 2026", card.Name, released.Year())
		}
	}
}

// TestRecordedRefusalDecodes reads the real 422 envelope. Its detail is a list
// of per-field objects, not a string, and the message a caller sees must name
// the offending question rather than being the word "error".
func TestRecordedRefusalDecodes(t *testing.T) {
	client, _ := serveJSON(t, http.StatusUnprocessableEntity,
		string(fixture(t, "live_refused.json")), WithMaxRetries(-1))
	_, err := client.Ask(t.Context(), "hi", simpleQuestions())
	if err == nil {
		t.Fatal("a recorded 422 came back as an answer")
	}
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
	}
	if apiErr.Kind != goodall.KindInvalidRequest {
		t.Errorf("Kind = %s, want %s", apiErr.Kind, goodall.KindInvalidRequest)
	}
	for _, want := range []string{"body.questions.vibe", "'vibe'"} {
		if !strings.Contains(apiErr.Message, want) {
			t.Errorf("Message %q does not name %s", apiErr.Message, want)
		}
	}
	if len(apiErr.Raw) == 0 {
		t.Error("Raw is empty, so the envelope the API sent is lost")
	}
}
