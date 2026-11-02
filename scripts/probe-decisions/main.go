// Command probe-decisions measures how far [typesafe.Client] already reaches
// across the decision models other vendors serve.
//
// OpenRouter documents its System One route as TypeSafe's own request and
// response shape, and says every decision model in its catalog speaks it. That
// is a claim about a wire goodall already has a client for, so this script tests
// it through the production path: for every concrete model the catalog lists, it
// asks one fixed noul, choice and score through [typesafe.Client.Ask] pointed at
// OpenRouter, once over a string state and once over an object state, and reports
// whether the typed accessors read every answer. Retries are off, so a figure
// counts one attempt.
//
// It also makes the raw calls the typed client cannot: the same question set in
// the native form on TypeSafe and in OpenAI's different form on /v1/decisions,
// OpenRouter's alpha route, and one invalid request per surface to see each
// error envelope.
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
	json "encoding/json/v2"
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
	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/typesafe"
)

const (
	catalogURL       = "https://openrouter.ai/api/v1/models?output_modalities=decisions"
	openRouterBase   = "https://openrouter.ai/api"
	alphaURL         = "https://openrouter.ai/api/alpha/decisions"
	typeSafeURL      = "https://api.typesafe.ai/v1/systemone"
	openAIURL        = "https://api.openai.com/v1/decisions"
	callTimeout      = 60 * time.Second
	sweepConcurrency = 6
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
var questions = typesafe.Questions{
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
		True:         "Money or work is blocked right now",
		False:        "The sender can wait",
	}},
	{ID: "frustration", Question: typesafe.Score{
		Instructions: "How frustrated is the customer?",
		Levels:       []string{"Calm", "Mildly annoyed", "Frustrated", "Furious"},
	}},
}

// catalogEntry is the part of one catalog row this probe reads.
type catalogEntry struct {
	ID            string  `json:"id"`
	ContextLength int     `json:"context_length"`
	Pricing       pricing `json:"pricing"`
}

// pricing is a catalog row's price strings, in dollars per token.
type pricing struct {
	Prompt string `json:"prompt"`
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

	entries, err := fetchCatalog(ctx)
	if err != nil {
		fatal(err)
	}
	sweep(ctx, keys["OPENROUTER_API_KEY"], entries, *out)
	raws(ctx, keys, *out)
}

// fetchCatalog lists the catalog's models, dropping the ~aliases and the :free
// variants, which name a model already in the list.
func fetchCatalog(ctx context.Context) ([]catalogEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Data []catalogEntry `json:"data"`
	}
	if err := json.UnmarshalRead(resp.Body, &body); err != nil {
		return nil, err
	}
	total := len(body.Data)
	concrete := slices.DeleteFunc(body.Data, func(e catalogEntry) bool {
		return strings.HasPrefix(e.ID, "~") || strings.HasSuffix(e.ID, ":free")
	})
	fmt.Printf("catalog: %d rows, %d concrete models\n\n", total, len(concrete))
	slices.SortFunc(concrete, func(a, b catalogEntry) int { return strings.Compare(a.ID, b.ID) })
	return concrete, nil
}

// sweep asks every model through the production client, over both states, and
// prints one row per call in catalog order.
func sweep(ctx context.Context, key string, entries []catalogEntry, out string) {
	type arm struct {
		name  string
		state any
	}
	arms := []arm{
		{"string", complaint},
		{"object", record{Ticket: complaint, Plan: "growth"}},
	}
	rows := make([]string, len(entries)*len(arms))
	limit := make(chan struct{}, sweepConcurrency)
	var wg sync.WaitGroup
	for i, entry := range entries {
		for j, a := range arms {
			wg.Go(func() {
				limit <- struct{}{}
				defer func() { <-limit }()
				rows[i*len(arms)+j] = askOne(ctx, key, entry.ID, a.name, a.state, out)
			})
		}
	}
	wg.Wait()
	readable := 0
	for _, row := range rows {
		fmt.Println(row)
		if strings.Contains(row, " ok ") {
			readable++
		}
	}
	fmt.Printf("\nreadable through typesafe.Client: %d of %d calls\n\n", readable, len(rows))
}

// askOne makes one typed call and renders what came back.
func askOne(ctx context.Context, key, model, armName string, state any, out string) string {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	tap := &capture{}
	client := typesafe.New(key,
		typesafe.WithBaseURL(openRouterBase),
		typesafe.WithHTTPClient(&http.Client{Transport: tap}),
		typesafe.WithMaxRetries(-1))
	answers, err := client.Ask(ctx, state, questions, typesafe.WithModel(model))
	save(out, "sweep_"+model+"_"+armName, tap.body)
	head := fmt.Sprintf("%-40s %-6s", model, armName)
	if err != nil {
		if apiErr, ok := errors.AsType[*goodall.APIError](err); ok {
			return fmt.Sprintf("%s FAIL status=%d kind=%v msg=%s", head, apiErr.Status, apiErr.Kind, oneLine(apiErr.Message))
		}
		return fmt.Sprintf("%s FAIL %s", head, oneLine(err.Error()))
	}
	return head + " " + readAll(answers, tap.body)
}

// readAll runs every typed accessor and reports the values, or which one broke.
func readAll(a *typesafe.Answers, raw []byte) string {
	choice, errChoice := a.Choice("department")
	noul, errNoul := a.Noul("is_urgent")
	score, errScore := a.Score("frustration")
	if err := errors.Join(errChoice, errNoul, errScore); err != nil {
		return "PARTIAL " + oneLine(err.Error())
	}
	var extra struct {
		Provider string `json:"provider"`
		Usage    struct {
			Cost float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &extra); err != nil {
		return "ok but extras unreadable: " + err.Error()
	}
	return fmt.Sprintf("ok choice=%s/%.2f noul=%.2f score=%.2f/%.2f in=%d out=%d cost=%.8f via=%s answered=%s",
		choice.Choice, choice.Confidence, noul.Noul, score.Score, score.Confidence,
		a.Usage.Input, a.Usage.Output, extra.Usage.Cost, extra.Provider, a.Model)
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
	nativeBody := `{"state":` + quote(complaint) + `,"model":"%s","questions":{` +
		`"department":{"type":"choice","instructions":"Which team should handle this ticket?","criteria":{"billing":"Payments, payouts, invoicing, refunds","technical":"Bugs, outages, integrations","sales":"Pricing, upgrades, new accounts"}},` +
		`"is_urgent":{"type":"noul","instructions":"Does this ticket convey urgency?"},` +
		`"frustration":{"type":"score","instructions":"How frustrated is the customer?","criteria":["Calm","Mildly annoyed","Frustrated","Furious"]}}}`
	openAIBody := `{"model":"gpt-6-luna","input":` + quote(complaint) + `,"questions":[` +
		`{"type":"choice","name":"department","instructions":"Which team should handle this ticket?","choices":[{"value":"billing","description":"Payments, payouts, invoicing, refunds"},{"value":"technical","description":"Bugs, outages, integrations"},{"value":"sales","description":"Pricing, upgrades, new accounts"}]},` +
		`{"type":"predicate","name":"is_urgent","instructions":"Does this ticket convey urgency?"},` +
		`{"type":"score","name":"frustration","instructions":"How frustrated is the customer?","levels":[{"label":"Calm"},{"label":"Mildly annoyed"},{"label":"Frustrated"},{"label":"Furious"}]}]}`
	invalid := `{"state":"x","model":"%s","questions":{"q":{"type":"vibe","instructions":"What is the vibe?"}}}`

	calls := []rawCall{
		{"typesafe_native", typeSafeURL, keys["TYPESAFE_API_KEY"], fmt.Sprintf(nativeBody, "jev-1.14.0")},
		{"openrouter_alpha_jev", alphaURL, keys["OPENROUTER_API_KEY"], fmt.Sprintf(nativeBody, "typesafe/jev-1.14")},
		{"openrouter_alpha_luna", alphaURL, keys["OPENROUTER_API_KEY"], fmt.Sprintf(nativeBody, "openai/gpt-6-luna-decisions")},
		{"openai_native", openAIURL, keys["OPENAI_API_KEY"], openAIBody},
		{"openai_systemone_shape_refused", openAIURL, keys["OPENAI_API_KEY"], fmt.Sprintf(nativeBody, "gpt-6-luna")},
		{"typesafe_invalid", typeSafeURL, keys["TYPESAFE_API_KEY"], fmt.Sprintf(invalid, "jev-1.14.0")},
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
	if err := os.WriteFile(filepath.Join(dir, name+".json"), body, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "save:", err)
	}
}

// quote renders s as a JSON string.
func quote(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		fatal(err)
	}
	return string(data)
}

// clip bounds a body printed to the terminal; the whole one is in -out.
func clip(body []byte) string {
	const limit = 1800
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
