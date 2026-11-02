package conformance_test

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/conformance"
	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/internal/livemodel"
)

// TestConformanceFromFixtures runs every backend through every adapter with no
// key and no network. The replay refuses a request whose bytes differ from the
// recording, so passing says more than that the answers decode: each adapter on
// each backend sends what a real server was shown to accept.
func TestConformanceFromFixtures(t *testing.T) {
	names := make([]string, 0, len(conformance.Backends))
	for _, backend := range conformance.Backends {
		names = append(names, backend.Name)
	}
	if want := []string{"typesafe", "openrouter", "openai", "lmstudio"}; !slices.Equal(names, want) {
		t.Fatalf("the suite covers %v, want %v", names, want)
	}
	for _, backend := range conformance.Backends {
		t.Run(backend.Name, func(t *testing.T) {
			key := "fixture-key"
			if backend.KeyEnv == "" {
				key = ""
			}
			client := &http.Client{Transport: &replay{backend: backend}}
			runSuite(t, backend, backend.New(key, client))
		})
	}
}

// TestLiveConformance is the same suite against the real servers, and it spends
// real tokens. Its output names the model that answered on each.
func TestLiveConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the live conformance suite under -short: nothing was sent to any server")
	}
	for _, backend := range conformance.Backends {
		t.Run(backend.Name, func(t *testing.T) {
			runSuite(t, backend, liveDecider(t, backend))
		})
	}
}

// liveDecider builds a real client, or skips loudly saying what was missing.
func liveDecider(t *testing.T, backend conformance.Backend) decision.Decider {
	t.Helper()
	if backend.KeyEnv == "" {
		if err := livemodel.ProbeLMStudio(); err != nil {
			t.Skipf("no local server at %s (%v); start LM Studio, load a decision model, or set %s", livemodel.LMStudioBase(), err, livemodel.LMStudioURLName)
		}
		return backend.New("", http.DefaultClient)
	}
	path, err := dotenv.RepoEnvFile()
	if err != nil {
		t.Skipf("skipping %s: %v", backend.Name, err)
	}
	key, ok := dotenv.Lookup(path, backend.KeyEnv)
	if !ok {
		t.Skipf("skipping %s: no %s in %s, so no call was made", backend.Name, backend.KeyEnv, path)
	}
	return backend.New(key, http.DefaultClient)
}

// adapter is one way into a [decision.Decider]. It asks of the given model, and
// reports the answers a consumer of that adapter reads, or the words a rejection
// reached them in.
type adapter struct {
	name string
	// form is the exchange this adapter's request matches on success.
	form conformance.Form
	ask  func(t *testing.T, d decision.Decider, model string, state any) (*decision.Answers, string)
}

// adapters is every adapter of the decision package. A Tool and an AuthoredTool
// take the state the model writes, and a Route defaults to the turn's own text.
var adapters = []adapter{
	{name: "Tool", form: conformance.FormObject, ask: askTool},
	{name: "AuthoredTool", form: conformance.FormObject, ask: askAuthoredTool},
	{name: "Route", form: conformance.FormText, ask: askRoute},
}

// runSuite is the suite, one body for fixtures and for live servers.
func runSuite(t *testing.T, backend conformance.Backend, d decision.Decider) {
	t.Helper()
	for _, a := range adapters {
		t.Run(a.name, func(t *testing.T) {
			spy := &spyDecider{Decider: d}
			answers, rejection := a.ask(t, spy, backend.Model, nil)
			if rejection != "" {
				if apiErr, ok := errors.AsType[*goodall.APIError](spy.err); ok && apiErr.Type == livemodel.LMStudioNoModels {
					t.Skip("the local server has no model loaded; run lms load with a decision model")
				}
				t.Fatalf("the judgment was rejected: %s", rejection)
			}
			if spy.answers == nil {
				t.Fatal("the adapter never reached the Decider")
			}
			t.Logf("backend=%s adapter=%s model=%s input=%d cost_reported=%v cost=%v",
				backend.Name, a.name, spy.answers.Model, spy.answers.Usage.TotalInput(), spy.answers.Cost.Reported, spy.answers.Cost.Amount)
			checkAnswers(t, answers)
			checkAccounting(t, backend, spy.answers)
		})
		t.Run(a.name+"Refused", func(t *testing.T) {
			spy := &spyDecider{Decider: d}
			_, rejection := a.ask(t, spy, conformance.UnknownModel, conformance.ObjectState)
			checkRefusal(t, backend, spy.err, rejection)
		})
	}
}

// spyDecider keeps what the server said, since a rejection reaches a tool's
// consumer as words and the suite also wants the typed error behind them.
type spyDecider struct {
	decision.Decider
	answers *decision.Answers
	err     error
}

// Decide asks the backend and remembers the outcome.
func (s *spyDecider) Decide(ctx context.Context, req *decision.Request) (*decision.Answers, error) {
	answers, err := s.Decider.Decide(ctx, req)
	s.answers, s.err = answers, err
	return answers, err
}

// toolState is the state a model would write, which is the object form unless the
// row says otherwise.
func toolState(t *testing.T, state any) jsontext.Value {
	t.Helper()
	if state == nil {
		state = conformance.ObjectState
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshaling the state: %v", err)
	}
	return data
}

// execute runs a tool the way the loop does and reads what the model would read.
func execute(t *testing.T, tool goodall.Tool, input jsontext.Value, d decision.Decider) (*decision.Answers, string) {
	t.Helper()
	reporter, ok := tool.(goodall.Reporter)
	if !ok {
		t.Fatalf("the tool is %T and does not report what a judgment spent", tool)
	}
	outcome, err := reporter.ExecuteReporting(t.Context(), input, func(goodall.Event) {})
	if err != nil {
		t.Fatalf("the tool failed: %v", err)
	}
	if outcome.Result.IsError {
		return nil, outcome.Result.Text()
	}
	spy := d.(*spyDecider)
	if outcome.Usage != spy.answers.Usage || outcome.Cost != spy.answers.Cost {
		t.Errorf("the call declared %+v and %+v, and the judgment spent %+v and %+v",
			outcome.Usage, outcome.Cost, spy.answers.Usage, spy.answers.Cost)
	}
	var answers decision.Answers
	if err := json.Unmarshal([]byte(outcome.Result.Text()), &answers); err != nil {
		t.Fatalf("the result a model reads does not decode as answers: %v\n%s", err, outcome.Result.Text())
	}
	return &answers, ""
}

// askTool asks through the fixed-questions tool, as a model would call it.
func askTool(t *testing.T, d decision.Decider, model string, state any) (*decision.Answers, string) {
	t.Helper()
	tool, err := decision.Tool(d, "triage_ticket", "Decide where a ticket goes.", conformance.Questions, decision.WithModel(model))
	if err != nil {
		t.Fatalf("building the tool: %v", err)
	}
	input, err := json.Marshal(map[string]jsontext.Value{"state": toolState(t, state)})
	if err != nil {
		t.Fatalf("marshaling the input: %v", err)
	}
	return execute(t, tool, input, d)
}

// authoredQuestions is [conformance.Questions] as a model writes them through the
// tool's schema. The replay holds the two together, so this cannot drift unseen.
const authoredQuestions = `[
	{"id":"department","type":"choice","instructions":"Which team should handle this ticket?","options":[
		{"key":"billing","description":"Payments, payouts, invoicing, refunds"},
		{"key":"technical","description":"Bugs, outages, integrations"},
		{"key":"sales","description":"Pricing, upgrades, new accounts"}]},
	{"id":"is_urgent","type":"predicate","instructions":"Does this ticket convey urgency?",
		"true":"Money or work is blocked right now","false":"The sender can wait"},
	{"id":"frustration","type":"score","instructions":"How frustrated is the customer?",
		"levels":["Calm","Mildly annoyed","Frustrated","Furious"]}]`

// askAuthoredTool asks through the tool whose questions the model writes.
func askAuthoredTool(t *testing.T, d decision.Decider, model string, state any) (*decision.Answers, string) {
	t.Helper()
	tool, err := decision.AuthoredTool(d, "judge", decision.WithModel(model))
	if err != nil {
		t.Fatalf("building the tool: %v", err)
	}
	input := fmt.Sprintf(`{"state":%s,"questions":%s}`, toolState(t, state), authoredQuestions)
	return execute(t, tool, jsontext.Value(input), d)
}

// routedModel is what the callback sets, to show the hook shaped the request.
const routedModel = "the-routed-model"

// askRoute asks through the hook. It judges the turn's own text by default, and
// the state a caller builds with [decision.WithState] when one is given.
func askRoute(t *testing.T, d decision.Decider, model string, state any) (*decision.Answers, string) {
	t.Helper()
	var seen *decision.Answers
	opts := []decision.RouteOption{decision.WithModel(model)}
	if state != nil {
		opts = append(opts, decision.WithState(func(*goodall.Message) any { return state }))
	}
	hook := decision.Route(d, conformance.Questions, func(_ context.Context, req *goodall.Request, _ *goodall.Message, answers *decision.Answers) error {
		seen = answers
		req.Model = routedModel
		return nil
	}, opts...)
	req := &goodall.Request{Model: "the-agents-model"}
	turn := goodall.UserMessage(goodall.Text{Text: conformance.Complaint})
	if err := hook(t.Context(), req, &turn); err != nil {
		return nil, err.Error()
	}
	if seen == nil {
		t.Fatal("the route never called back, so the turn was not judged")
	}
	if req.Model != routedModel {
		t.Errorf("req.Model = %q, so the callback's change did not reach the request", req.Model)
	}
	return seen, ""
}

// checkAnswers reads every typed accessor and asserts only what any model's
// answer must satisfy, so a disagreement between models cannot fail it.
func checkAnswers(t *testing.T, answers *decision.Answers) {
	t.Helper()
	department, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if !slices.Contains([]string{"billing", "technical", "sales"}, department.Choice) {
		t.Errorf("Choice = %q, which was not offered", department.Choice)
	}
	checkProbability(t, "the choice's confidence", department.Confidence)
	if ranked := department.Ranked(); len(ranked) != 3 {
		t.Errorf("the choice ranks %d options, want the 3 offered: %+v", len(ranked), ranked)
	}

	urgent, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	checkProbability(t, "the predicate", urgent.Probability)

	frustration, err := answers.Score("frustration")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if frustration.Score < 0 || frustration.Score > 3 {
		t.Errorf("Score = %v, outside the 4 levels", frustration.Score)
	}
	checkProbability(t, "the score's confidence", frustration.Confidence)
	if ranked := frustration.Ranked(); len(ranked) == 0 || len(ranked) > 4 {
		t.Errorf("the score ranks %d levels, want between 1 and the 4 offered: %+v", len(ranked), ranked)
	}

	if _, err := answers.Predicate("department"); err == nil {
		t.Error("reading a choice as a predicate gave no error, so a wrong accessor would read as a zero value")
	}
	if _, err := answers.Choice("no_such_question"); err == nil {
		t.Error("reading a question nobody asked gave no error")
	}
}

// checkProbability holds one figure to the range every wire promises.
func checkProbability(t *testing.T, what string, p float64) {
	t.Helper()
	if p < 0 || p > 1 {
		t.Errorf("%s = %v, outside 0 to 1", what, p)
	}
}

// checkAccounting reads the fields a consumer's books depend on, off the answers
// as the Decider returned them.
func checkAccounting(t *testing.T, backend conformance.Backend, answers *decision.Answers) {
	t.Helper()
	if answers.Model == "" {
		t.Error("the response named no model, so a run could not say who answered")
	}
	if backend.Model != "" && !strings.Contains(answers.Model, modelStem(backend.Model)) {
		t.Errorf("asked %q and %q answered", backend.Model, answers.Model)
	}
	if answers.Usage.TotalInput() == 0 {
		t.Error("the response reported no input tokens")
	}
	if answers.Cost.Reported != backend.ReportsCost {
		t.Errorf("Cost = %+v, and this backend is recorded as reporting cost: %v", answers.Cost, backend.ReportsCost)
	}
}

// modelStem drops a vendor prefix, since a server may answer with its own
// spelling or a dated id of the model that was asked for.
func modelStem(model string) string {
	_, stem, found := strings.Cut(model, "/")
	if found {
		return stem
	}
	return model
}

// checkRefusal holds a server's own refusal to the seam's contract: a typed error
// behind it, and the server's sentence in the words the consumer gets.
func checkRefusal(t *testing.T, backend conformance.Backend, err error, rejection string) {
	t.Helper()
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("the Decider's error is %T, want *goodall.APIError: %v", err, err)
	}
	t.Logf("backend=%s status=%d kind=%v type=%q message=%q", backend.Name, apiErr.Status, apiErr.Kind, apiErr.Type, apiErr.Message)
	if apiErr.Status < 400 || apiErr.Status >= 500 {
		t.Errorf("Status = %d, want the 4xx of a request the server refused", apiErr.Status)
	}
	if apiErr.Message == "" || strings.HasPrefix(apiErr.Message, "{") || strings.HasPrefix(apiErr.Message, "[") {
		t.Errorf("Message = %q, want the server's sentence", apiErr.Message)
	}
	if !strings.Contains(rejection, apiErr.Message) {
		t.Errorf("the consumer read %q, which does not carry the server's sentence %q", rejection, apiErr.Message)
	}
}
