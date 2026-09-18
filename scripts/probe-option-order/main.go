// Command probe-option-order measures whether the order a [typesafe.Choice]
// question's options are written in moves Jev's distribution.
//
// goodall sends a choice's options in the order the author wrote them, because
// deterministic request bytes are invariant 8 of the architecture plan. The
// Option doc comment used to claim the order also "measurably changes how a
// model weighs them", carried over from an issue about a conversational
// model's tool schemas; this script is the measurement that claim never had.
//
// It asks one fixed question about one fixed ticket through
// [typesafe.Client.Ask] — the production path — n times with the options in
// author order and n times with the slice reversed, and nothing else differs
// between the two arms: it reads the option order back out of the bytes it
// actually sent and refuses a run whose two arms differ in anything else. It
// then prints every sample's distribution, the widest same-order spread (the
// noise floor), the widest cross-order spread, and the input tokens the run
// spent. A cross-order spread inside the noise floor means no order effect was
// seen at that sample size, which is not the same as there being none.
//
// -state picks which ticket is judged. On the "plain" ticket one department is
// obviously right, the distribution saturates and there is nothing for an
// order effect to move; on the default "ambiguous" one the probabilities sit
// away from 0 and 1. The figures both produced are in
// project/2026-09-17-typesafe-go-comparison.md.
//
// Every run spends real money, which is why this is a script and not a test:
//
//	go run ./scripts/probe-option-order -env /abs/path/.env -n 3 -model jev-1.13.0 -state ambiguous
package main

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/typesafe"
)

// apiKeyName is the .env entry holding the key this probe spends.
const apiKeyName = "TYPESAFE_API_KEY"

// inputPricePerMillion is TypeSafe's published price in US dollars per million
// input tokens, recorded in project/2026-09-17-typesafe-jev-findings.md. Output
// tokens are free, and the API reports no cost of its own, so the money figure
// here is this constant times the tokens the run actually consumed.
const inputPricePerMillion = 0.042

// questionID is the id of the one question this probe asks.
const questionID = "department"

// ticket is the state every sample evaluates: a support ticket as a record,
// which is the shape a caller with rows to judge sends.
type ticket struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Plan    string `json:"plan"`
}

// The two states, either of which a run evaluates. How close the call is
// decides what the probe can see at all: a ticket that plainly belongs to one
// department pins the distribution against its ceiling, and a saturated
// distribution leaves an order effect nowhere to show up. Both are here so
// that both figures are reproducible from one command.
var states = map[string]ticket{
	// ambiguous straddles all three departments, so the probabilities sit
	// away from 0 and 1 and have room to move.
	"ambiguous": {
		Subject: "Do we pay twice if we move to Scale mid-cycle?",
		Body: "We are on Growth with 12 seats and want Scale before our renewal. Support " +
			"told me the proration would show up as a credit, but the invoice preview in " +
			"the dashboard shows the full amount again, and the /billing/preview endpoint " +
			"returns a different total than the dashboard does. Who should I be talking to " +
			"before we commit to the bigger plan?",
		Plan: "growth",
	},
	// plain is the control: one department is obviously right, so the
	// distribution saturates and the probe should see nothing.
	"plain": {
		Subject: "Charged twice after the API retried my upgrade",
		Body: "Your checkout call timed out, my client retried it, and now I have two " +
			"invoices for the same plan change. I want one refunded, and I would like " +
			"to know whether the retry was mine or yours before I upgrade the rest of " +
			"my seats.",
		Plan: "growth",
	},
}

// authorOrder is the option order as an author would write it. The reverse arm
// sends exactly these options, reversed, and changes nothing else.
var authorOrder = []typesafe.Option{
	{Key: "billing", Description: "Payments, invoicing, refunds, duplicate charges"},
	{Key: "technical", Description: "Bugs, timeouts, retries, integrations"},
	{Key: "sales", Description: "Pricing, upgrades, adding seats, new accounts"},
}

// instructions is the one question asked of the one state.
const instructions = "Which team should handle this ticket?"

// distribution is one sample's probability per option key.
type distribution map[string]float64

// sample is one billed call: which arm it belongs to, what came back, and the
// exact bytes that were sent.
type sample struct {
	arm        string
	index      int
	model      string
	choice     string
	confidence float64
	dist       distribution
	input      int
	body       []byte
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "probe-option-order:", err)
		os.Exit(1)
	}
}

// run is main with an error return, so every failure leaves by one path.
func run() error {
	env := flag.String("env", ".env", "the .env file holding "+apiKeyName)
	n := flag.Int("n", 3, "samples per ordering; the run makes twice this many billed calls")
	model := flag.String("model", "jev-1.13.0", "the model to ask, pinned to a version so the figure names one")
	stateName := flag.String("state", "ambiguous", "which fixed ticket to judge ("+strings.Join(slices.Sorted(maps.Keys(states)), ", ")+")")
	flag.Parse()

	if *n < 1 {
		return fmt.Errorf("-n must be at least 1, got %d", *n)
	}
	state, ok := states[*stateName]
	if !ok {
		return fmt.Errorf("unknown -state %q; the states are %s", *stateName, strings.Join(slices.Sorted(maps.Keys(states)), ", "))
	}
	key, haveKey := dotenv.Lookup(*env, apiKeyName)
	if !haveKey {
		return fmt.Errorf("no %s in %s; this probe needs a live key and makes no call without one", apiKeyName, *env)
	}

	log := &bodyLog{next: http.DefaultTransport}
	client := typesafe.New(key, typesafe.WithHTTPClient(&http.Client{Transport: log}))

	reverseOrder := slices.Clone(authorOrder)
	slices.Reverse(reverseOrder)

	fmt.Printf("probe-option-order: the %s ticket, %d samples per ordering against %s, %d billed calls\n\n",
		*stateName, *n, *model, 2*(*n))

	forward, err := collect(client, "forward", state, authorOrder, *n, *model, log)
	if err != nil {
		return err
	}
	reverse, err := collect(client, "reverse", state, reverseOrder, *n, *model, log)
	if err != nil {
		return err
	}

	if err := reportWire(forward, reverse); err != nil {
		return err
	}
	reportSamples(forward, reverse)
	reportSpreads(forward, reverse)
	reportCost(forward, reverse)
	return nil
}

// collect makes n calls with one option order and returns what came back,
// pairing each answer with the request body that produced it.
func collect(client *typesafe.Client, arm string, state ticket, options []typesafe.Option, n int, model string, log *bodyLog) ([]sample, error) {
	questions := typesafe.Questions{{ID: questionID, Question: typesafe.Choice{
		Instructions: instructions,
		Options:      options,
	}}}
	out := make([]sample, 0, n)
	for i := range n {
		mark := log.mark()
		answers, err := client.Ask(context.Background(), state, questions, typesafe.WithModel(model))
		if err != nil {
			return nil, fmt.Errorf("%s sample %d: %w", arm, i+1, err)
		}
		choice, err := answers.Choice(questionID)
		if err != nil {
			return nil, fmt.Errorf("%s sample %d: %w", arm, i+1, err)
		}
		body, ok := log.since(mark)
		if !ok {
			return nil, fmt.Errorf("%s sample %d: no request body was recorded, so the wire order cannot be verified", arm, i+1)
		}
		out = append(out, sample{
			arm:        arm,
			index:      i + 1,
			model:      answers.Model,
			choice:     choice.Choice,
			confidence: choice.Confidence,
			dist:       distribution(choice.Probabilities),
			input:      answers.Usage.Input,
			body:       body,
		})
	}
	return out, nil
}

// reportWire proves the two arms differed on the wire in the one way they were
// meant to. It reads the option order back out of the recorded bytes through
// the package's own decoder, checks that every sample in an arm sent identical
// bytes, and refuses a run whose arms are not reverses of each other: a
// measurement whose input was not verified is not a measurement.
func reportWire(forward, reverse []sample) error {
	fwdOrder, err := sentOrder(forward[0].body)
	if err != nil {
		return fmt.Errorf("forward sample 1: %w", err)
	}
	revOrder, err := sentOrder(reverse[0].body)
	if err != nil {
		return fmt.Errorf("reverse sample 1: %w", err)
	}
	fmt.Println("wire check")
	fmt.Printf("  forward criteria keys: %s\n", strings.Join(fwdOrder, ", "))
	fmt.Printf("  reverse criteria keys: %s\n", strings.Join(revOrder, ", "))

	want := make([]string, len(authorOrder))
	for i, opt := range authorOrder {
		want[i] = opt.Key
	}
	if !slices.Equal(fwdOrder, want) {
		return fmt.Errorf("the forward arm sent %v, not the author order %v", fwdOrder, want)
	}
	slices.Reverse(want)
	if !slices.Equal(revOrder, want) {
		return fmt.Errorf("the reverse arm sent %v, not the reversed order %v", revOrder, want)
	}

	if err := identicalBodies(forward); err != nil {
		return err
	}
	if err := identicalBodies(reverse); err != nil {
		return err
	}
	fmt.Printf("  every sample within an arm sent byte-identical requests (%d and %d bytes)\n",
		len(forward[0].body), len(reverse[0].body))
	fmt.Printf("  the two arms differ: %v\n\n", !bytes.Equal(forward[0].body, reverse[0].body))
	return nil
}

// identicalBodies reports an arm whose samples did not send the same bytes,
// which would mean something other than the option order moved between them.
func identicalBodies(samples []sample) error {
	for _, s := range samples[1:] {
		if !bytes.Equal(s.body, samples[0].body) {
			return fmt.Errorf("%s sample %d sent different bytes from sample 1, so more than the option order moved", s.arm, s.index)
		}
	}
	return nil
}

// sentRequest is as much of a recorded request body as the wire check needs.
// Questions decodes in document order, so the options come back in the order
// they were sent.
type sentRequest struct {
	Questions typesafe.Questions `json:"questions"`
}

// sentOrder lists the option keys a recorded request body carried, in the
// order the body carried them.
func sentOrder(body []byte) ([]string, error) {
	var req sentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("the recorded request body could not be decoded: %w", err)
	}
	for _, q := range req.Questions {
		if q.ID != questionID {
			continue
		}
		choice, ok := q.Question.(typesafe.Choice)
		if !ok {
			return nil, fmt.Errorf("question %q came back as a %s, not a choice", q.ID, q.Question.QuestionType())
		}
		keys := make([]string, len(choice.Options))
		for i, opt := range choice.Options {
			keys[i] = opt.Key
		}
		return keys, nil
	}
	return nil, fmt.Errorf("the recorded request body carries no question %q", questionID)
}

// reportSamples prints every distribution, in author-order columns so the two
// arms are read off the same axis, together with the version that answered.
func reportSamples(forward, reverse []sample) {
	fmt.Println("samples")
	fmt.Printf("  %-8s %-6s %-12s %-24s %-11s %s\n", "arm", "sample", "model", "probabilities", "choice", "confidence")
	for _, s := range slices.Concat(forward, reverse) {
		fmt.Printf("  %-8s %-6d %-12s %-24s %-11s %.4f\n",
			s.arm, s.index, s.model, s.dist.String(), s.choice, s.confidence)
	}
	fmt.Println()
}

// String renders a distribution in author-option order, so two of them line up
// column by column.
func (d distribution) String() string {
	parts := make([]string, 0, len(d))
	for _, opt := range authorOrder {
		parts = append(parts, fmt.Sprintf("%.4f", d[opt.Key]))
	}
	for key := range d {
		if !slices.ContainsFunc(authorOrder, func(o typesafe.Option) bool { return o.Key == key }) {
			parts = append(parts, key+"?"+fmt.Sprintf("%.4f", d[key]))
		}
	}
	return strings.Join(parts, " ")
}

// reportSpreads prints the two figures the probe exists to compare.
func reportSpreads(forward, reverse []sample) {
	fwdNoise, fwdKey := spread(dists(forward))
	revNoise, revKey := spread(dists(reverse))
	noise, noiseKey := fwdNoise, fwdKey
	if revNoise > noise {
		noise, noiseKey = revNoise, revKey
	}
	cross, crossKey := crossSpread(dists(forward), dists(reverse))

	fmt.Println("spreads (widest absolute difference in any one option's probability)")
	fmt.Printf("  within forward:  %.4f%s\n", fwdNoise, onKey(fwdKey))
	fmt.Printf("  within reverse:  %.4f%s\n", revNoise, onKey(revKey))
	fmt.Printf("  noise floor:     %.4f%s\n", noise, onKey(noiseKey))
	fmt.Printf("  across orders:   %.4f%s\n", cross, onKey(crossKey))
	switch {
	case cross > noise:
		fmt.Printf("  the cross-order spread exceeds the noise floor by %.4f\n\n", cross-noise)
	default:
		fmt.Printf("  the cross-order spread sits inside the noise floor: no order effect was seen at this sample size, which is not the same as there being none\n\n")
	}
}

// onKey names the option a spread was found on, or says nothing when the
// spread was zero and no option is responsible for it.
func onKey(key string) string {
	if key == "" {
		return ""
	}
	return " (on " + key + ")"
}

// reportCost prints what the run spent, from the tokens the API reported and
// TypeSafe's published input price.
func reportCost(forward, reverse []sample) {
	total := 0
	for _, s := range slices.Concat(forward, reverse) {
		total += s.input
	}
	fmt.Println("cost")
	fmt.Printf("  %d calls, %d input tokens, $%.6f at $%.3f per million input tokens (output tokens are free)\n",
		len(forward)+len(reverse), total, float64(total)*inputPricePerMillion/1e6, inputPricePerMillion)
}

// dists pulls the distributions out of a run of samples.
func dists(samples []sample) []distribution {
	out := make([]distribution, len(samples))
	for i, s := range samples {
		out[i] = s.dist
	}
	return out
}

// spread reports the widest absolute per-option difference between any two of
// the distributions, and the option it was found on. Identical samples spread
// by zero and name no option.
func spread(d []distribution) (float64, string) { return crossSpread(d, d) }

// crossSpread reports the widest absolute per-option difference between any
// distribution in a and any in b, and the option it was found on. An option
// one side did not report counts as zero probability there, so an arm that
// came back with a different option set shows up as a large spread rather than
// as a silent skip.
func crossSpread(a, b []distribution) (float64, string) {
	widest, where := 0.0, ""
	for _, x := range a {
		for _, y := range b {
			for _, key := range slices.Sorted(union(x, y)) {
				diff := x[key] - y[key]
				if diff < 0 {
					diff = -diff
				}
				if diff > widest {
					widest, where = diff, key
				}
			}
		}
	}
	return widest, where
}

// union iterates every option key either distribution reports.
func union(x, y distribution) func(func(string) bool) {
	return func(yield func(string) bool) {
		for key := range x {
			if !yield(key) {
				return
			}
		}
		for key := range y {
			if _, dup := x[key]; dup {
				continue
			}
			if !yield(key) {
				return
			}
		}
	}
}

// bodyLog records the body of every request that passes through it, so the
// probe can prove what actually reached the wire rather than what it meant to
// send. It counts retries too: each attempt is its own entry.
type bodyLog struct {
	next   http.RoundTripper
	mu     sync.Mutex
	bodies [][]byte
}

// mark returns the current end of the log, so a caller can ask which bodies
// one call produced.
func (l *bodyLog) mark() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.bodies)
}

// since returns the first body recorded after mark, which is the first attempt
// of the call that mark was taken before.
func (l *bodyLog) since(mark int) ([]byte, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if mark >= len(l.bodies) {
		return nil, false
	}
	return l.bodies[mark], true
}

// RoundTrip records the request body and sends the request on unchanged.
func (l *bodyLog) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		l.mu.Lock()
		l.bodies = append(l.bodies, body)
		l.mu.Unlock()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	return l.next.RoundTrip(req)
}
