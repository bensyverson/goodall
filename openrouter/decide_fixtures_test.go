package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/livemodel"
)

// These read the recordings scripts/record-fixtures made from the live API on
// 2026-11-02 (testdata/README.md), so they are statements about OpenRouter's
// wire rather than about what the fixture author believed it does.

// recordedDecisionQuestions is the question set the recorder asks.
func recordedDecisionQuestions() decision.Questions {
	return decision.Questions{
		{ID: "department", Question: decision.Choice{
			Instructions: "Which team should handle this ticket?",
			Options: []decision.Option{
				{Key: "billing", Description: "Payments, payouts, invoicing, refunds"},
				{Key: "technical", Description: "Bugs, outages, integrations"},
				{Key: "sales", Description: "Pricing, upgrades, new accounts"},
			},
		}},
		{ID: "is_urgent", Question: decision.Predicate{Instructions: "Does this ticket convey urgency?"}},
		{ID: "frustration", Question: decision.Score{
			Instructions: "How frustrated is the customer?",
			Levels:       []string{"Calm", "Mildly annoyed", "Frustrated", "Furious"},
		}},
	}
}

// recordedTicket is the state the recorder sends.
type recordedTicket struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Plan    string `json:"plan"`
}

// recordedDecisionRequest is the request the recorder made.
func recordedDecisionRequest(model string) *decision.Request {
	return &decision.Request{
		Model: model,
		State: recordedTicket{
			Subject: "Payouts failing since Tuesday",
			Body:    "Help! My payouts have been failing for 3 days and I have staff to pay.",
			Plan:    "growth",
		},
		Questions: recordedDecisionQuestions(),
	}
}

// TestARecordedDecisionDecodesThroughEveryAccessor is a non-Jev model's real answer
// read the way a consumer reads one.
func TestARecordedDecisionDecodesThroughEveryAccessor(t *testing.T) {
	client, _ := decideServer(t, http.StatusOK, string(fixture(t, "live_decision_mixed.json")))
	answers, err := client.Decide(t.Context(), recordedDecisionRequest(livemodel.OpenRouterDecision))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if answers.Model != "openai/gpt-6-luna-decisions-20261006" {
		t.Errorf("Model = %q, want the dated id that answered", answers.Model)
	}
	if want := (goodall.Usage{Input: 433}); answers.Usage != want {
		t.Errorf("Usage = %+v, want %+v", answers.Usage, want)
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
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	if predicate.Probability != 0.98 {
		t.Errorf("Probability = %v, want 0.98", predicate.Probability)
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
	if answers.Generation != "gen-dec-1793632930-RVbvos2VcfYjkPtWPfxT" {
		t.Errorf("Generation = %q", answers.Generation)
	}
	if answers.ServedBy != "OpenAI" {
		t.Errorf("ServedBy = %q", answers.ServedBy)
	}
}

// TestARecordedCostIsTheFixturesFigureExactly reads the figure out of the file
// itself, so the assertion cannot agree with the decoder by being written from it.
func TestARecordedCostIsTheFixturesFigureExactly(t *testing.T) {
	raw := fixture(t, "live_decision_mixed.json")
	var body struct {
		Usage struct {
			Cost jsontext.Value `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	want, err := goodall.ParseDecimal(string(body.Usage.Cost))
	if err != nil {
		t.Fatalf("parsing %s: %v", body.Usage.Cost, err)
	}

	client, _ := decideServer(t, http.StatusOK, string(raw))
	answers, err := client.Decide(t.Context(), recordedDecisionRequest(livemodel.OpenRouterDecision))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !answers.Cost.Reported {
		t.Error("Cost is not reported, and the response states one")
	}
	if answers.Cost.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", answers.Cost.Currency)
	}
	if answers.Cost.Amount.Cmp(want) != 0 {
		t.Errorf("Amount = %s, want %s", answers.Cost.Amount, want)
	}
	if got := answers.Cost.Amount.String(); got != "0.0000433" {
		t.Errorf("Amount prints as %s, want 0.0000433", got)
	}
}

// TestARecordedRequestIsStillTheBytesSent is the encode side's guard on this wire.
func TestARecordedRequestIsStillTheBytesSent(t *testing.T) {
	client, seen := decideServer(t, http.StatusOK, string(fixture(t, "live_decision_mixed.json")))
	if _, err := client.Decide(t.Context(), recordedDecisionRequest(livemodel.OpenRouterDecision)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	sent := jsontext.Value(seen.body)
	if err := sent.Indent(jsontext.WithIndent("  ")); err != nil {
		t.Fatalf("indenting what was sent: %v", err)
	}
	if got, want := string(sent)+"\n", string(fixture(t, "live_decision_mixed.request.json")); got != want {
		t.Errorf("the request is no longer the recorded bytes\n got: %s\nwant: %s", got, want)
	}
}

// TestARecordedModelLimitSurfacesItsSentence is the rule that stands in for a
// capability table: the model says what it will not take, and the caller reads
// that sentence on an error stamped with this client's name.
func TestARecordedModelLimitSurfacesItsSentence(t *testing.T) {
	client, _ := decideServer(t, http.StatusBadRequest, string(fixture(t, "live_decision_model_limit.json")))
	_, err := client.Decide(t.Context(), recordedDecisionRequest(livemodel.OpenRouterDecisionLimited))
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
	}
	if apiErr.Provider != ProviderName {
		t.Errorf("Provider = %q, want %q", apiErr.Provider, ProviderName)
	}
	if apiErr.Kind != goodall.KindInvalidRequest {
		t.Errorf("Kind = %s, want %s", apiErr.Kind, goodall.KindInvalidRequest)
	}
	if apiErr.Status != http.StatusBadRequest {
		t.Errorf("Status = %d", apiErr.Status)
	}
	if want := "ScaleDown state must be a string or an object with only text, document, and document_mime_type, including text or document"; apiErr.Message != want {
		t.Errorf("Message = %q, want %q", apiErr.Message, want)
	}
}

// TestARecordedSchemaFailureReadsAsASentence: this message is a JSON array inside
// a string, multi-line and full of braces, and a model handed it in a tool result
// has to act on it. The envelope stays whole on Raw.
func TestARecordedSchemaFailureReadsAsASentence(t *testing.T) {
	client, _ := decideServer(t, http.StatusBadRequest, string(fixture(t, "live_decision_schema_error.json")))
	_, err := client.Decide(t.Context(), recordedDecisionRequest(livemodel.OpenRouterDecision))
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
	}
	if apiErr.Kind != goodall.KindInvalidRequest {
		t.Errorf("Kind = %s, want %s", apiErr.Kind, goodall.KindInvalidRequest)
	}
	if !strings.HasPrefix(apiErr.Message, "questions.vibe.type: Invalid discriminator value.") {
		t.Errorf("Message = %q, want the member's path and then its sentence", apiErr.Message)
	}
	if strings.ContainsAny(apiErr.Message, "[{\n") {
		t.Errorf("Message = %q, which still reads as the array it arrived as", apiErr.Message)
	}
	if !strings.Contains(string(apiErr.Raw), "invalid_union") {
		t.Errorf("Raw = %s, want the envelope kept whole", apiErr.Raw)
	}
}

// TestAMessageThatMerelyLooksLikeAListIsLeftAlone: only the issue-list shape is
// rewritten, since a server's sentence is otherwise the server's to word.
func TestAMessageThatMerelyLooksLikeAListIsLeftAlone(t *testing.T) {
	for _, message := range []string{
		"[beta] this model is not available",
		"[]",
		"[1,2,3]",
		`[{"code":"no_message_member"}]`,
		"plain sentence",
	} {
		apiErr := apiError(&wireError{Message: message}, http.StatusBadRequest, nil, nil)
		if apiErr.Message != message {
			t.Errorf("%q came back as %q", message, apiErr.Message)
		}
	}
}

// TestSeveralIssuesAreAllNamed: a body naming three bad questions names all three.
func TestSeveralIssuesAreAllNamed(t *testing.T) {
	message := `[{"path":["questions",0,"a"],"message":"first"},{"path":[],"message":"second"}]`
	apiErr := apiError(&wireError{Message: message}, http.StatusBadRequest, nil, nil)
	if want := "questions.0.a: first; second"; apiErr.Message != want {
		t.Errorf("Message = %q, want %q", apiErr.Message, want)
	}
}

// TestTheRecordedCatalogReadsItsRows is the decision models as OpenRouter listed
// them, with what each one reads.
func TestTheRecordedCatalogReadsItsRows(t *testing.T) {
	client, _ := decideServer(t, http.StatusOK, string(fixture(t, "live_decision_models.json")))
	models, err := client.DecisionModels(t.Context())
	if err != nil {
		t.Fatalf("DecisionModels: %v", err)
	}
	if len(models) != 33 {
		t.Fatalf("got %d rows, want the 33 on 2026-11-02", len(models))
	}
	first := models[0]
	if first.ID != "baseten/joey-1" || first.Name != "Baseten: Joey 1" || first.ContextLength != 16384 {
		t.Errorf("first row = %+v", first)
	}
	if !slices.Equal(first.InputModalities, []string{"text", "image"}) {
		t.Errorf("InputModalities = %v, want text and image", first.InputModalities)
	}
	if first.Pricing == nil || first.Pricing.Input.String() != "0.00000001" {
		t.Errorf("Pricing = %+v", first.Pricing)
	}
	if !slices.ContainsFunc(models, func(m DecisionModel) bool { return m.ID == livemodel.OpenRouterDecision }) {
		t.Errorf("the catalog no longer lists the pinned %s", livemodel.OpenRouterDecision)
	}
	for _, model := range models {
		if len(model.InputModalities) == 0 {
			t.Errorf("%s has no input modalities", model.ID)
		}
	}
}
