package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/conformance"
	"github.com/bensyverson/goodall/internal/liveprobe"
)

const (
	loopTimeout     = 3 * time.Minute
	decideTimeout   = 60 * time.Second
	liveConcurrency = 6
	// typeSafeAlias is asked beside the pin, which stays behind on purpose: the
	// alias is what a new consumer would send.
	typeSafeAlias = "jev-latest"
)

// spend is what a run cost, as far as anyone said.
type spend struct {
	calls       int
	priced      int
	unpriced    int
	unpricedIn  int64
	unpricedOut int64
	reported    goodall.Decimal
}

// String says what is known and what is not: an unpriced call is never free.
func (s spend) String() string {
	return fmt.Sprintf("%d live calls; USD %s reported across %d; %d with no reported price (%d input and %d output tokens)",
		s.calls, s.reported, s.priced, s.unpriced, s.unpricedIn, s.unpricedOut)
}

// budget bounds a run's spend. It counts calls as well as money, since Anthropic
// and OpenAI report no money and a catalog can grow.
type budget struct {
	mu       sync.Mutex
	maxCost  goodall.Decimal
	maxCalls int
	s        spend
}

// newBudget parses the money limit.
func newBudget(maxCost string, maxCalls int) (*budget, error) {
	limit, err := goodall.ParseDecimal(maxCost)
	if err != nil {
		return nil, fmt.Errorf("spend limit: %w", err)
	}
	return &budget{maxCost: limit, maxCalls: maxCalls}, nil
}

// take claims one call, or reports that the limits are reached.
func (b *budget) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.s.calls >= b.maxCalls || b.s.reported.Cmp(b.maxCost) >= 0 {
		return false
	}
	b.s.calls++
	return true
}

// charge records what one finished call cost.
func (b *budget) charge(cost goodall.Cost, usage goodall.Usage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cost.Reported {
		b.s.priced++
		b.s.reported = b.s.reported.Add(cost.Amount)
		return
	}
	b.s.unpriced++
	b.s.unpricedIn += int64(usage.Input)
	b.s.unpricedOut += int64(usage.Output)
}

// spent returns the running tally.
func (b *budget) spent() spend {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.s
}

// tap passes calls through and keeps the member paths of each POST's response, so
// the live calls double as the fresh responses for the shape check. It keeps names
// only, and never a header, so no key or vendor text can reach the baseline.
type tap struct {
	mu         sync.Mutex
	paths      map[string]bool
	unreadable []string
}

// RoundTrip records the response's member paths and hands it on unread.
func (t *tap) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil || req.Method != http.MethodPost {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	found, parseErr := memberPaths(body)
	t.mu.Lock()
	defer t.mu.Unlock()
	if parseErr != nil {
		// A proxy's error page is no sign of drift, but a 200 we cannot read is.
		if resp.StatusCode < 300 {
			t.unreadable = append(t.unreadable, parseErr.Error())
		}
		return resp, nil
	}
	if t.paths == nil {
		t.paths = map[string]bool{}
	}
	for path := range found {
		t.paths[path] = true
	}
	return resp, nil
}

// liveRun carries what the live calls share.
type liveRun struct {
	keys   map[string]string
	budget *budget
	taps   map[string]*tap
	limit  chan struct{}

	mu sync.Mutex
	o  *observation
}

// job is one live call that may be refused by the budget.
type job struct {
	row string
	run func(ctx context.Context) liveRow
}

// runLive runs the agent-loop rows and the decision sweep, and folds each wire's
// fresh member paths into the observation. It needs the catalogs read first.
func runLive(ctx context.Context, keys map[string]string, b *budget, o *observation) {
	run := &liveRun{keys: keys, budget: b, taps: map[string]*tap{}, limit: make(chan struct{}, liveConcurrency), o: o}
	for _, w := range wires {
		run.taps[w.Name] = &tap{}
	}
	jobs := append(run.loopJobs(), run.decideJobs()...)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Go(func() {
			run.limit <- struct{}{}
			defer func() { <-run.limit }()
			run.do(ctx, j)
		})
	}
	wg.Wait()
	for _, w := range wires {
		t := run.taps[w.Name]
		if len(t.unreadable) > 0 {
			o.gaps = append(o.gaps, gap{Check: checkShape, Name: w.Name,
				Reason: fmt.Sprintf("%d successful responses could not be read: %s", len(t.unreadable), t.unreadable[0])})
		}
		if len(t.paths) > 0 {
			o.members[w.Name] = slices.Sorted(maps.Keys(t.paths))
		}
	}
}

// do runs one job, unless the budget has run out.
func (r *liveRun) do(ctx context.Context, j job) {
	if !r.budget.take() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.o.skippedLive = append(r.o.skippedLive, j.row)
		r.o.gaps = append(r.o.gaps, gap{Check: checkLive, Name: j.row, Reason: "the spend limit was reached first"})
		return
	}
	row := j.run(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.o.live[j.row] = row
}

// available reports whether a wire has a key, and records a gap when it does not.
func (r *liveRun) available(prefix, wireName string) bool {
	if r.keys[wireKeys[wireName]] != "" {
		return true
	}
	r.o.skippedLive = append(r.o.skippedLive, prefix)
	r.o.gaps = append(r.o.gaps, gap{Check: checkLive, Name: prefix, Reason: "no " + wireKeys[wireName] + " in the env file"})
	return false
}

// loopJobs builds one agent-loop run per target.
func (r *liveRun) loopJobs() []job {
	var jobs []job
	announced := map[string]bool{}
	for _, target := range loopTargets {
		prefix := "loop/" + target.Wire + "/"
		if !announced[target.Wire] {
			announced[target.Wire] = true
			r.available(prefix, target.Wire)
		}
		if r.keys[wireKeys[target.Wire]] == "" {
			continue
		}
		provider := newProvider(target.Wire, r.keys[wireKeys[target.Wire]], &http.Client{Transport: r.taps[target.Wire]})
		jobs = append(jobs, job{
			row: prefix + target.Model + "/" + string(target.Effort),
			run: func(ctx context.Context) liveRow {
				ctx, cancel := context.WithTimeout(ctx, loopTimeout)
				defer cancel()
				result := liveprobe.AgentLoop(ctx, provider, target.Model, target.Effort)
				r.budget.charge(result.Cost, result.Usage)
				if result.Passed() {
					return liveRow{Outcome: outcomePass, Detail: result.String()}
				}
				return liveRow{Outcome: outcomeFail, Detail: result.String()}
			},
		})
	}
	return jobs
}

// decideJobs builds the sweep: each concrete model OpenRouter's catalog lists, and
// each native wire's pin, over the two state forms.
func (r *liveRun) decideJobs() []job {
	var jobs []job
	for _, backend := range conformance.Backends {
		if backend.KeyEnv == "" {
			continue // a local server, which this check leaves out
		}
		prefix := "decide/" + backend.Name + "/"
		if !r.available(prefix, backend.Name) {
			continue
		}
		models := []string{backend.Model}
		switch backend.Name {
		case "typesafe":
			models = append(models, typeSafeAlias)
		case "openrouter":
			ids, read := r.o.catalogs[catalogDecisions]
			if !read {
				r.o.skippedLive = append(r.o.skippedLive, prefix)
				r.o.gaps = append(r.o.gaps, gap{Check: checkLive, Name: prefix, Reason: "the decision catalog was not read, so there is no list to sweep"})
				continue
			}
			models = concreteModels(ids)
		}
		client := backend.New(r.keys[backend.KeyEnv], &http.Client{Transport: r.taps[backend.Name]})
		for _, model := range models {
			for _, form := range []conformance.Form{conformance.FormText, conformance.FormObject} {
				jobs = append(jobs, job{
					row: prefix + model + "/" + string(form),
					run: func(ctx context.Context) liveRow { return r.decide(ctx, client, model, form) },
				})
			}
		}
	}
	return jobs
}

// decide asks the conformance questions and reads every answer, which is what a
// consumer needs to be true.
func (r *liveRun) decide(ctx context.Context, client decision.Decider, model string, form conformance.Form) liveRow {
	ctx, cancel := context.WithTimeout(ctx, decideTimeout)
	defer cancel()
	var state any = conformance.ObjectState
	if form == conformance.FormText {
		state = conformance.Complaint
	}
	answers, err := client.Decide(ctx, &decision.Request{Model: model, State: state, Questions: conformance.Questions})
	if err != nil {
		r.budget.charge(goodall.Cost{}, goodall.Usage{})
		return liveRow{Outcome: outcomeFail, Detail: err.Error()}
	}
	r.budget.charge(answers.Cost, goodall.Usage{Input: answers.Usage.Input, Output: answers.Usage.Output})
	_, errChoice := answers.Choice("department")
	_, errPredicate := answers.Predicate("is_urgent")
	_, errScore := answers.Score("frustration")
	if err := errors.Join(errChoice, errPredicate, errScore); err != nil {
		return liveRow{Outcome: outcomeFail, Detail: err.Error()}
	}
	return liveRow{Outcome: outcomePass, Detail: "answered by " + answers.Model}
}

// concreteModels drops the ~aliases and the :free variants, which name a model
// already in the list.
func concreteModels(ids []string) []string {
	return slices.DeleteFunc(slices.Clone(ids), func(id string) bool {
		return strings.HasPrefix(id, "~") || strings.HasSuffix(id, ":free")
	})
}
