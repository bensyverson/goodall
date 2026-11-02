package openrouter

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/systemone"
)

// decideQuestions is one question of each type, in an order that is not
// alphabetical, so a marshaler that sorted them would be caught.
func decideQuestions() decision.Questions {
	return decision.Questions{
		{ID: "department", Question: decision.Choice{
			Instructions: "Which team should handle this?",
			Options: []decision.Option{
				{Key: "billing", Description: "Payments, invoicing, refunds"},
				{Key: "technical"},
			},
		}},
		{ID: "is_urgent", Question: decision.Predicate{Instructions: "Does this convey urgency?"}},
	}
}

// decideBody is an answer to those questions with everything OpenRouter adds to
// the common wire: the generation id, the serving host and the cost. The cost is
// a figure a float64 could not carry and print back.
func decideBody() string {
	return `{"id":"gen-123","provider":"DeepInfra","model":"vendor/model-20261029","answers":{` +
		`"department":{"type":"choice","choice":"billing","confidence":0.9,"probabilities":{"billing":0.95,"technical":0.05}},` +
		`"is_urgent":{"type":"` + systemone.WirePredicate + `","` + systemone.WirePredicate + `":0.25}},` +
		`"usage":{"input_tokens":12,"output_tokens":3,"cost":0.000017934}}`
}

// seenRequest is what a test server received.
type seenRequest struct {
	calls  int
	method string
	path   string
	query  string
	auth   string
	body   string
}

// decideServer answers every call with one status and body and records the last
// request.
func decideServer(t *testing.T, status int, body string, opts ...Option) (*Client, *seenRequest) {
	t.Helper()
	seen := &seenRequest{}
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		seen.calls++
		seen.method, seen.path, seen.query = r.Method, r.URL.Path, r.URL.RawQuery
		seen.auth = r.Header.Get("Authorization")
		seen.body = string(data)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}, opts...)
	return client, seen
}

// TestDecideSendsTheSystemOneWire is the path and the bytes: relative to the base
// URL, and exactly what the shared codec writes, so this client cannot drift from
// the other one on that wire.
func TestDecideSendsTheSystemOneWire(t *testing.T) {
	client, seen := decideServer(t, http.StatusOK, decideBody())
	req := &decision.Request{Model: "vendor/model", State: "Help!", Questions: decideQuestions()}
	if _, err := client.Decide(t.Context(), req); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if seen.method != http.MethodPost || seen.path != "/systemone" {
		t.Errorf("sent %s %s, want POST /systemone", seen.method, seen.path)
	}
	if seen.auth != "Bearer test-key" {
		t.Errorf("Authorization = %q", seen.auth)
	}
	want, err := systemone.EncodeRequest(req)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	if seen.body != string(want) {
		t.Errorf("the body is\n%s\nwant\n%s", seen.body, want)
	}
}

// TestDecideReadsWhatOpenRouterAddsToTheWire is why this client does not just
// hand the body to the codec: the money, the generation and the host are on the
// response, and they are what a caller audits a judgment with.
func TestDecideReadsWhatOpenRouterAddsToTheWire(t *testing.T) {
	client, _ := decideServer(t, http.StatusOK, decideBody())
	answers, err := client.Decide(t.Context(), &decision.Request{Model: "vendor/model", State: "Help!", Questions: decideQuestions()})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if answers.Model != "vendor/model-20261029" {
		t.Errorf("Model = %q", answers.Model)
	}
	if want := (goodall.Usage{Input: 12, Output: 3}); answers.Usage != want {
		t.Errorf("Usage = %+v, want %+v", answers.Usage, want)
	}
	if !answers.Cost.Reported || answers.Cost.Currency != "USD" {
		t.Errorf("Cost = %+v, want a reported figure in USD", answers.Cost)
	}
	if got := answers.Cost.Amount.String(); got != "0.000017934" {
		t.Errorf("Cost.Amount = %s, want the body's figure exactly", got)
	}
	if answers.Generation != "gen-123" {
		t.Errorf("Generation = %q", answers.Generation)
	}
	if answers.ServedBy != "DeepInfra" {
		t.Errorf("ServedBy = %q", answers.ServedBy)
	}
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	if predicate.Probability != 0.25 {
		t.Errorf("Probability = %v", predicate.Probability)
	}
}

// TestACostIsNotReportedUnlessOneArrived keeps "not known" from reading as free.
func TestACostIsNotReportedUnlessOneArrived(t *testing.T) {
	body := `{"model":"m","answers":{"is_urgent":{"type":"` + systemone.WirePredicate + `","` + systemone.WirePredicate + `":0.5}},"usage":{"input_tokens":1}}`
	client, _ := decideServer(t, http.StatusOK, body)
	answers, err := client.Decide(t.Context(), &decision.Request{Model: "m", State: "s", Questions: decideQuestions()})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if answers.Cost != (goodall.Cost{}) {
		t.Errorf("Cost = %+v, want the zero cost that means not known", answers.Cost)
	}
	if answers.Generation != "" || answers.ServedBy != "" {
		t.Errorf("Generation %q and ServedBy %q, want both empty", answers.Generation, answers.ServedBy)
	}
}

// TestACostOnAServerWithNoReportedCostIsIgnored: the dialect is what says a usage
// cost means dollars charged, so another server's number is not stated as that.
func TestACostOnAServerWithNoReportedCostIsIgnored(t *testing.T) {
	client, _ := decideServer(t, http.StatusOK, decideBody(), WithDialect(Generic))
	answers, err := client.Decide(t.Context(), &decision.Request{Model: "m", State: "s", Questions: decideQuestions()})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if answers.Cost.Reported {
		t.Errorf("Cost = %+v, want unreported under the Generic dialect", answers.Cost)
	}
}

// TestDecideRefusesAnAskingWithNoModelBeforeAnyRequest: OpenRouter's guidance is
// never to pick from memory or pin an alias, and thresholds do not travel, so
// this client picks nothing for the caller.
func TestDecideRefusesAnAskingWithNoModelBeforeAnyRequest(t *testing.T) {
	client, seen := decideServer(t, http.StatusOK, decideBody())
	_, err := client.Decide(t.Context(), &decision.Request{State: "s", Questions: decideQuestions()})
	if err == nil {
		t.Fatal("Decide accepted a request with no model")
	}
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("error is not KindInvalidRequest: %v", err)
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("error %q does not name the model", err)
	}
	if seen.calls != 0 {
		t.Errorf("the client made %d calls for a request it should have refused", seen.calls)
	}
}

// TestDecideRefusesABadRequestBeforeAnyRequest is the seam's contract.
func TestDecideRefusesABadRequestBeforeAnyRequest(t *testing.T) {
	client, seen := decideServer(t, http.StatusOK, decideBody())
	_, err := client.Decide(t.Context(), &decision.Request{Model: "m", State: "s"})
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("error is %v, want KindInvalidRequest", err)
	}
	if seen.calls != 0 {
		t.Errorf("the client made %d calls for a request it should have refused", seen.calls)
	}
}

// TestTheOpenAIDialectRefusesLocally: its server answers the System One shape
// with a 400 about a member it wants instead, so sending it would be a request
// known to fail, on the caller's account.
func TestTheOpenAIDialectRefusesLocally(t *testing.T) {
	client, seen := decideServer(t, http.StatusOK, decideBody(), WithDialect(OpenAI))
	_, err := client.Decide(t.Context(), &decision.Request{Model: "gpt-6-luna", State: "s", Questions: decideQuestions()})
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("error is %v, want KindInvalidRequest", err)
	}
	if err != nil && !strings.Contains(err.Error(), OpenAI.String()) {
		t.Errorf("error %q does not name the dialect", err)
	}
	if seen.calls != 0 {
		t.Errorf("the client made %d calls for a request it should have refused", seen.calls)
	}
}

// TestLocalDialectsTakeTheSameWire is only that they are dispatched to it. What
// local servers do with it is proved in the local-servers work.
func TestLocalDialectsTakeTheSameWire(t *testing.T) {
	for _, dialect := range []Dialect{LMStudio, Generic, Dialect("something-new")} {
		t.Run(dialect.String(), func(t *testing.T) {
			client, seen := decideServer(t, http.StatusOK, decideBody(), WithDialect(dialect))
			if _, err := client.Decide(t.Context(), &decision.Request{Model: "local", State: "s", Questions: decideQuestions()}); err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if seen.path != "/systemone" {
				t.Errorf("path = %q, want /systemone", seen.path)
			}
		})
	}
}

// TestAnErrorInsideA200IsAnAPIError: HTTP 200 is not success on this API, and a
// body with an error object has no answers to read.
func TestAnErrorInsideA200IsAnAPIError(t *testing.T) {
	client, _ := decideServer(t, http.StatusOK, `{"error":{"message":"the upstream fell over","code":502}}`)
	_, err := client.Decide(t.Context(), &decision.Request{Model: "m", State: "s", Questions: decideQuestions()})
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
	}
	if apiErr.Provider != ProviderName {
		t.Errorf("Provider = %q", apiErr.Provider)
	}
	if apiErr.Kind != goodall.KindServer || apiErr.Message != "the upstream fell over" {
		t.Errorf("APIError = %+v", apiErr)
	}
}

// TestAnUnreadableBodyIsAProtocolError keeps the adapters' line between what a
// model can correct and what the loop reports.
func TestAnUnreadableBodyIsAProtocolError(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":        `not json`,
		"a broken answer": `{"model":"m","answers":{"a":{"type":"choice"}}}`,
		"a broken cost":   `{"model":"m","answers":{},"usage":{"cost":"lots"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := decideServer(t, http.StatusOK, body)
			_, err := client.Decide(t.Context(), &decision.Request{Model: "m", State: "s", Questions: decideQuestions()})
			if _, ok := errors.AsType[*goodall.ProtocolError](err); !ok {
				t.Errorf("error is %T, want *goodall.ProtocolError: %v", err, err)
			}
		})
	}
}

// TestDecideGetsTheNullCodeFix is 1JOSCO on this wire: a 400 here goes through the
// same decoder, stamped with this package's name, with the sentence intact.
func TestDecideGetsTheNullCodeFix(t *testing.T) {
	for name, code := range map[string]string{
		"a null code":    `,"code":null`,
		"no code at all": ``,
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := decideServer(t, http.StatusBadRequest, `{"error":{"message":"state must be a string"`+code+`}}`)
			_, err := client.Decide(t.Context(), &decision.Request{Model: "m", State: "s", Questions: decideQuestions()})
			apiErr, ok := errors.AsType[*goodall.APIError](err)
			if !ok {
				t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
			}
			if apiErr.Provider != ProviderName || apiErr.Kind != goodall.KindInvalidRequest || apiErr.Status != http.StatusBadRequest {
				t.Errorf("APIError = %+v, want openrouter, invalid_request, 400", apiErr)
			}
			if apiErr.Message != "state must be a string" {
				t.Errorf("Message = %q", apiErr.Message)
			}
		})
	}
}

// TestDecisionModelsReadsTheFilteredCatalog asks for the filter, since decision
// models are left out of the default list, and reads what a caller picks one by.
func TestDecisionModelsReadsTheFilteredCatalog(t *testing.T) {
	const body = `{"data":[` +
		`{"id":"vendor/picky","name":"Picky","context_length":8000,"architecture":{"input_modalities":["text","image"],"output_modalities":["decisions"]},"pricing":{"prompt":"0.00000024","completion":"0"}},` +
		`{"id":"vendor/bare"}]}`
	client, seen := decideServer(t, http.StatusOK, body)
	models, err := client.DecisionModels(t.Context())
	if err != nil {
		t.Fatalf("DecisionModels: %v", err)
	}
	if seen.method != http.MethodGet || seen.path != "/models" || seen.query != "output_modalities=decisions" {
		t.Errorf("sent %s %s?%s", seen.method, seen.path, seen.query)
	}
	if len(models) != 2 {
		t.Fatalf("got %d rows, want both: %+v", len(models), models)
	}
	picky := models[0]
	if picky.ID != "vendor/picky" || picky.Name != "Picky" || picky.ContextLength != 8000 {
		t.Errorf("row = %+v", picky)
	}
	if got := strings.Join(picky.InputModalities, ","); got != "text,image" {
		t.Errorf("InputModalities = %q", got)
	}
	if picky.Pricing == nil || picky.Pricing.Input.String() != "0.00000024" || picky.Pricing.Currency != "USD" {
		t.Errorf("Pricing = %+v", picky.Pricing)
	}
	if models[1].ID != "vendor/bare" || models[1].Pricing != nil || models[1].InputModalities != nil {
		t.Errorf("a row with nothing published = %+v, want no invented facts", models[1])
	}
}

// TestDecisionModelsLeavesTheModelMemoAlone keeps the ModelLister's answers about
// conversational models: a decision model is not something an agent can be
// pointed at, so it must not start answering Model.
func TestDecisionModelsLeavesTheModelMemoAlone(t *testing.T) {
	client, seen := decideServer(t, http.StatusOK, `{"data":[{"id":"vendor/picky"}]}`)
	if _, err := client.DecisionModels(t.Context()); err != nil {
		t.Fatalf("DecisionModels: %v", err)
	}
	if _, ok := client.cachedModel("vendor/picky"); ok {
		t.Error("a decision model landed in the conversational memo")
	}
	if _, err := client.Model(t.Context(), "vendor/picky"); err != nil {
		t.Fatalf("Model: %v", err)
	}
	if seen.query != "" {
		t.Errorf("Model sent the query %q, want the plain catalog", seen.query)
	}
}

// TestDecisionModelsIsOpenRouters: no other server was seen to read the filter,
// and one that ignored it would answer with conversational models under this name.
func TestDecisionModelsIsOpenRouters(t *testing.T) {
	client, seen := decideServer(t, http.StatusOK, `{"data":[]}`, WithDialect(LMStudio))
	if _, err := client.DecisionModels(t.Context()); !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("error is %v, want KindInvalidRequest", err)
	}
	if seen.calls != 0 {
		t.Errorf("the client made %d calls", seen.calls)
	}
}
