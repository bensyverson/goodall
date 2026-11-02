// Command probe-decisions measures which of the decision models OpenRouter serves
// can be read through [openrouter.Client.Decide].
//
// For every concrete model [openrouter.Client.DecisionModels] lists, it asks one
// fixed predicate, choice and score through Decide, once over a string state and
// once over an object state, and reports whether the typed accessors read every
// answer. A model's own limit arrives as a 400 with a sentence, which the row
// quotes. Retries are off, so a figure counts one attempt. The catalog changes, so
// a count from two dates is comparable only after looking at which rows differ.
//
// It then asks the same questions of the two native routes through Decide, and
// makes the raw calls no client can send: OpenRouter's alpha route, the System One
// shape posted to OpenAI, and one invalid request per surface to see each error
// envelope.
//
// Every raw body goes to -out so a claim can be checked against it. Every run
// spends real money, a fraction of a cent at the catalog's prices, which is why
// this is a script and not a test:
//
//	go run ./scripts/probe-decisions -env /abs/path/.env -out /abs/dir
//
// The figures it produced are in project/2026-11-02-decision-models-findings.md.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/internal/livemodel"
	"github.com/bensyverson/goodall/internal/redact"
	"github.com/bensyverson/goodall/internal/systemone"
	"github.com/bensyverson/goodall/openrouter"
	"github.com/bensyverson/goodall/typesafe"
)

const (
	openRouterBase   = "https://openrouter.ai/api"
	alphaURL         = "https://openrouter.ai/api/alpha/decisions"
	typeSafeURL      = "https://api.typesafe.ai/v1/systemone"
	openAIURL        = "https://api.openai.com/v1/decisions"
	callTimeout      = 60 * time.Second
	sweepConcurrency = 6
	// nativeJev is the Jev the native route is asked for beside OpenRouter's
	// typesafe/jev-1.14, as the 2026-11-02 sweep did, rather than the older pin
	// the live tests hold for their thresholds.
	nativeJev = "jev-1.14.0"
)

// complaint is the text every arm judges. It is written so all three answers
// are easy, because this probe asks whether a model can be read at all, not
// whether it judges well.
const complaint = "We were charged twice for the same invoice and our payouts have been failing since Tuesday. This is blocking payroll today."

// record is the object form of the same state.
type record struct {
	Ticket string `json:"ticket"`
	Plan   string `json:"plan"`
}

// questions is the fixed question set, covering each primitive every vendor has.
var questions = decision.Questions{
	{ID: "department", Question: decision.Choice{
		Instructions: "Which team should handle this ticket?",
		Options: []decision.Option{
			{Key: "billing", Description: "Payments, payouts, invoicing, refunds"},
			{Key: "technical", Description: "Bugs, outages, integrations"},
			{Key: "sales", Description: "Pricing, upgrades, new accounts"},
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
}

// capture keeps the last response body a client read, so a typed call can still
// report the bytes behind it.
type capture struct {
	mu   sync.Mutex
	body []byte
}

// RoundTrip records the body and hands the response on unread.
func (c *capture) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.body = data
	c.mu.Unlock()
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, nil
}

func main() {
	envPath := flag.String("env", ".env", "path to the .env holding the API keys")
	out := flag.String("out", "", "directory for raw response bodies (required)")
	flag.Parse()
	if *out == "" {
		fatal(errors.New("-out is required"))
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}
	keys, err := dotenv.Load(*envPath)
	if err != nil {
		fatal(err)
	}
	ctx := context.Background()

	key := keys["OPENROUTER_API_KEY"]
	if key == "" {
		fatal(fmt.Errorf("no OPENROUTER_API_KEY in %s, so nothing was swept", *envPath))
	}
	catalog, err := openrouter.New(openrouter.WithAPIKey(key)).DecisionModels(ctx)
	if err != nil {
		fatal(err)
	}
	models := concreteModels(catalog)
	fmt.Printf("catalog: %d rows, %d concrete models\n\n", len(catalog), len(models))
	sweep(ctx, key, models, *out)
	natives(ctx, keys)
	raws(ctx, keys, *out)
}

// concreteModels drops the ~aliases and the :free variants, which name a model
// already in the list, and sorts the rest so two runs can be compared by eye.
func concreteModels(catalog []openrouter.DecisionModel) []string {
	var out []string
	for _, entry := range catalog {
		if strings.HasPrefix(entry.ID, "~") || strings.HasSuffix(entry.ID, ":free") {
			continue
		}
		out = append(out, entry.ID)
	}
	slices.Sort(out)
	return out
}

// sweep asks every model through the production client, over both states, and
// prints one row per call in catalog order.
func sweep(ctx context.Context, key string, models []string, out string) {
	type arm struct {
		name  string
		state any
	}
	arms := []arm{
		{"string", complaint},
		{"object", record{Ticket: complaint, Plan: "growth"}},
	}
	rows := make([]row, len(models)*len(arms))
	limit := make(chan struct{}, sweepConcurrency)
	var wg sync.WaitGroup
	for i, model := range models {
		for j, a := range arms {
			wg.Go(func() {
				limit <- struct{}{}
				defer func() { <-limit }()
				rows[i*len(arms)+j] = askOne(ctx, key, model, a.name, a.state, out)
			})
		}
	}
	wg.Wait()
	for _, r := range rows {
		fmt.Println(r.text)
	}
	readable, cost := tally(rows)
	fmt.Printf("\nreadable through openrouter.Client.Decide: %d of %d calls, at a reported %v %s\n\n",
		readable, len(rows), cost.Amount, cost.Currency)
}

// row is one call's outcome.
type row struct {
	// readable is whether every typed accessor read its answer.
	readable bool
	// cost is what the server said this call cost.
	cost goodall.Cost
	// text is the line a person reads.
	text string
}

// tally counts the readable rows and sums what they cost.
func tally(rows []row) (int, goodall.Cost) {
	readable := 0
	var cost goodall.Cost
	for _, r := range rows {
		if r.readable {
			readable++
			cost = cost.Add(r.cost)
		}
	}
	return readable, cost
}

// askOne makes one typed call and renders what came back.
func askOne(ctx context.Context, key, model, armName string, state any, out string) row {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	tap := &capture{}
	client := openrouter.New(
		openrouter.WithAPIKey(key),
		openrouter.WithHTTPClient(&http.Client{Transport: tap}),
		openrouter.WithMaxRetries(-1))
	answers, err := client.Decide(ctx, &decision.Request{Model: model, State: state, Questions: questions})
	save(out, "sweep_"+model+"_"+armName, tap.body)
	return rowFor(model, armName, answers, err)
}

// rowFor runs every typed accessor and reports the values, or what broke.
func rowFor(model, armName string, a *decision.Answers, err error) row {
	head := fmt.Sprintf("%-40s %-6s", model, armName)
	if err != nil {
		if apiErr, ok := errors.AsType[*goodall.APIError](err); ok {
			return row{text: fmt.Sprintf("%s FAIL status=%d kind=%v msg=%s", head, apiErr.Status, apiErr.Kind, oneLine(apiErr.Message))}
		}
		return row{text: fmt.Sprintf("%s FAIL %s", head, oneLine(err.Error()))}
	}
	choice, errChoice := a.Choice("department")
	predicate, errPredicate := a.Predicate("is_urgent")
	score, errScore := a.Score("frustration")
	if err := errors.Join(errChoice, errPredicate, errScore); err != nil {
		return row{text: head + " PARTIAL " + oneLine(err.Error())}
	}
	return row{readable: true, cost: a.Cost, text: fmt.Sprintf("%s ok choice=%s/%.2f predicate=%.2f score=%.2f/%.2f in=%d out=%d cost=%v via=%s answered=%s",
		head, choice.Choice, choice.Confidence, predicate.Probability, score.Score, score.Confidence,
		a.Usage.Input, a.Usage.Output, a.Cost.Amount, a.ServedBy, a.Model)}
}

// plainQuestions is the question set with no predicate criteria, which is what the
// native routes and OpenRouter's alpha route were compared on.
func plainQuestions() decision.Questions {
	return decision.Questions{
		questions[0],
		{ID: "is_urgent", Question: decision.Predicate{Instructions: "Does this ticket convey urgency?"}},
		questions[2],
	}
}

// natives asks the same questions of the two servers that speak for themselves,
// through the same seam the sweep used.
func natives(ctx context.Context, keys map[string]string) {
	for _, call := range []struct {
		name, keyName, model string
		build                func(key string) decision.Decider
	}{
		{"typesafe_native", "TYPESAFE_API_KEY", nativeJev, func(key string) decision.Decider { return typesafe.New(key) }},
		{"openai_native", "OPENAI_API_KEY", livemodel.OpenAIDecision, func(key string) decision.Decider {
			return openrouter.New(openrouter.WithAPIKey(key), openrouter.WithBaseURL("https://api.openai.com/v1"), openrouter.WithDialect(openrouter.OpenAI))
		}},
	} {
		key := keys[call.keyName]
		if key == "" {
			fmt.Printf("--- %s: no %s in the env file, so this call was not made\n\n", call.name, call.keyName)
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		answers, err := call.build(key).Decide(ctx, &decision.Request{Model: call.model, State: complaint, Questions: plainQuestions()})
		cancel()
		fmt.Printf("--- %s\n%s\n\n", call.name, rowFor(call.model, "string", answers, err).text)
	}
}

// rawCall is one hand-built request the typed client cannot make.
type rawCall struct {
	name string
	url  string
	key  string
	body string
}

// raws makes the calls outside the typed client and prints each status and body.
func raws(ctx context.Context, keys map[string]string, out string) {
	// The System One form comes from the codec, so this probe posts exactly what
	// the production path posts, with a predicate carrying no criteria.
	nativeBody := func(model string) string {
		body, err := systemone.EncodeRequest(&decision.Request{Model: model, State: complaint, Questions: plainQuestions()})
		if err != nil {
			fatal(err)
		}
		return string(body)
	}
	invalid := `{"state":"x","model":"%s","questions":{"q":{"type":"vibe","instructions":"What is the vibe?"}}}`

	calls := []rawCall{
		{"openrouter_alpha_jev", alphaURL, keys["OPENROUTER_API_KEY"], nativeBody("typesafe/jev-1.14")},
		{"openrouter_alpha_luna", alphaURL, keys["OPENROUTER_API_KEY"], nativeBody("openai/gpt-6-luna-decisions")},
		{"openai_systemone_shape_refused", openAIURL, keys["OPENAI_API_KEY"], nativeBody(livemodel.OpenAIDecision)},
		{"typesafe_invalid", typeSafeURL, keys["TYPESAFE_API_KEY"], fmt.Sprintf(invalid, nativeJev)},
		{"openrouter_invalid", openRouterBase + "/v1/systemone", keys["OPENROUTER_API_KEY"], fmt.Sprintf(invalid, "typesafe/jev-1.14")},
		{"openai_invalid", openAIURL, keys["OPENAI_API_KEY"], `{"model":"gpt-6-luna","input":"x","questions":[{"type":"vibe","instructions":"What is the vibe?"}]}`},
	}
	for _, call := range calls {
		status, body, err := post(ctx, call)
		if err != nil {
			fmt.Printf("--- %s: %v\n\n", call.name, err)
			continue
		}
		save(out, "raw_"+call.name, body)
		fmt.Printf("--- %s: %d\n%s\n\n", call.name, status, clip(body))
	}
}

// post sends one raw call.
func post(ctx context.Context, call rawCall) (int, []byte, error) {
	if call.key == "" {
		return 0, nil, errors.New("no key in the env file, so this call was not made")
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, call.url, strings.NewReader(call.body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+call.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// save writes one raw body, naming it safely for a model id with slashes.
func save(dir, name string, body []byte) {
	name = strings.NewReplacer("/", "_", ":", "_", "~", "_").Replace(name)
	if err := os.WriteFile(filepath.Join(dir, name+".json"), redact.Account(body), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "save:", err)
	}
}

// clip bounds a body printed to the terminal; the whole one is in -out.
func clip(body []byte) string {
	const limit = 1800
	body = redact.Account(body)
	if len(body) > limit {
		return string(body[:limit]) + "…"
	}
	return string(body)
}

// oneLine keeps a message on its row.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 170 {
		return s[:170] + "…"
	}
	return s
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "probe-decisions:", err)
	os.Exit(1)
}
