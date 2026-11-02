// Command check-drift reports what vendors changed since the last accepted
// baseline, because go test replays against pinned models and recordings and so
// cannot see a vendor move.
//
// It compares four things. Each vendor's model list against the baseline and the
// pins in internal/livemodel. Each vendor's changelog, section by section. The
// live agent loop and decision sweep, by pass or fail. And the member paths of the
// responses those live calls got, against the recorded fixtures, saying which ones
// no wire struct models. It compares shape and outcome only, since values differ on
// every run. It decides nothing and files nothing: that judgment is the next step's.
//
//	go run ./scripts/check-drift -env /abs/path/.env
//	go run ./scripts/check-drift -env /abs/path/.env -no-live        # free
//	go run ./scripts/check-drift -env /abs/path/.env -accept         # take this run as the baseline
//
// Run it from the repository root. The exit code is a set of bits: 1 for drift, 2
// for anything not checked, 4 for a run that could not start. go run turns them all
// into 1, so build the binary to read them, or read the RESULT line, which says.
//
// A live run spends real money, bounded by -max-cost and -max-calls. The findings
// and a recorded run are in project/2026-11-02-check-drift-findings.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/internal/livemodel"
)

const (
	defaultBaseline = "scripts/check-drift/baseline.json"
	fetchTimeout    = 60 * time.Second
)

func main() {
	envPath := flag.String("env", ".env", "path to the .env holding the API keys")
	baselinePath := flag.String("baseline", defaultBaseline, "the accepted baseline")
	acceptRun := flag.Bool("accept", false, "write this run into the baseline, keeping whatever it could not check")
	noLive := flag.Bool("no-live", false, "make no model calls, which reports the live and shape checks as not checked")
	maxCost := flag.String("max-cost", "0.25", "stop making calls once this many reported dollars are spent")
	maxCalls := flag.Int("max-calls", 120, "stop making calls after this many")
	flag.Parse()

	before, err := readBaseline(*baselinePath)
	if err != nil && !(*acceptRun && errors.Is(err, fs.ErrNotExist)) {
		fatal(fmt.Errorf("%w; a first run needs -accept", err))
	}
	spendLimit, err := newBudget(*maxCost, *maxCalls)
	if err != nil {
		fatal(err)
	}
	keys, err := dotenv.Load(*envPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check-drift: %v, so every check that needs a key is not checked\n", err)
		keys = map[string]string{}
	}

	ctx := context.Background()
	o := observation{
		catalogs:   map[string][]string{},
		changelogs: map[string][]section{},
		live:       map[string]liveRow{},
		members:    map[string][]string{},
	}
	fetcher := &http.Client{Timeout: fetchTimeout}
	readCatalogs(ctx, fetcher, catalogs, keys, &o)
	readPages(ctx, fetcher, pages, &o)
	if *noLive {
		o.skippedLive = append(o.skippedLive, "")
		o.gaps = append(o.gaps, gap{Check: checkLive, Name: "all rows", Reason: "-no-live"})
	} else {
		runLive(ctx, keys, spendLimit, &o)
	}
	o.spend = spendLimit.spent()

	k := repoKnowledge(".", &o)
	result := compare(before, o, k)
	result.render(os.Stdout, before)

	if *acceptRun {
		if err := writeBaseline(*baselinePath, accept(before, o, k, time.Now().Format(time.DateOnly))); err != nil {
			fatal(err)
		}
		fmt.Println("Accepted: wrote", *baselinePath)
		result.findings = nil
	}
	os.Exit(result.exitCode())
}

// repoKnowledge reads what the repository says about each wire. A failure is a
// gap on that wire, never an empty set that would call every member new.
func repoKnowledge(root string, o *observation) knowledge {
	k := knowledge{
		pins:     livemodel.Pins,
		recorded: map[string]map[string]bool{},
		modeled:  map[string]map[string]bool{},
	}
	for _, w := range wires {
		k.wires = append(k.wires, w.Name)
		recorded, err := recordedPaths(root, w.Fixtures)
		if err != nil {
			o.gaps = append(o.gaps, gap{Check: checkShape, Name: w.Name, Reason: err.Error()})
			continue
		}
		dirs := make([]string, len(w.Source))
		for i, dir := range w.Source {
			dirs[i] = filepath.Join(root, dir)
		}
		modeled, err := modeledNames(dirs...)
		if err != nil {
			o.gaps = append(o.gaps, gap{Check: checkShape, Name: w.Name, Reason: err.Error()})
			continue
		}
		k.recorded[w.Name], k.modeled[w.Name] = recorded, modeled
	}
	return k
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "check-drift:", err)
	os.Exit(exitFatal)
}
