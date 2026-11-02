package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

func TestNoKeysMeansEveryLiveRowIsAGapAndNoCallIsMade(t *testing.T) {
	limit, err := newBudget("1", 100)
	if err != nil {
		t.Fatal(err)
	}
	o := observation{
		catalogs:   map[string][]string{catalogDecisions: {"a/b"}},
		changelogs: map[string][]section{},
		live:       map[string]liveRow{},
		members:    map[string][]string{},
	}
	runLive(t.Context(), map[string]string{}, limit, &o)
	if len(o.live) != 0 || limit.spent().calls != 0 {
		t.Errorf("rows %v, calls %d, with no keys", o.live, limit.spent().calls)
	}
	for _, prefix := range []string{"loop/anthropic/", "loop/openrouter/", "loop/openai/", "decide/typesafe/", "decide/openrouter/", "decide/openai/"} {
		if !slices.Contains(o.skippedLive, prefix) {
			t.Errorf("%s is not marked as skipped: %v", prefix, o.skippedLive)
		}
		if !slices.ContainsFunc(o.gaps, func(g gap) bool {
			return g.Check == checkLive && g.Name == prefix && strings.Contains(g.Reason, "_API_KEY")
		}) {
			t.Errorf("%s has no gap naming its key: %+v", prefix, o.gaps)
		}
	}
}

func TestASweepWithNoCatalogIsAGap(t *testing.T) {
	limit, _ := newBudget("1", 0)
	o := observation{live: map[string]liveRow{}, members: map[string][]string{}}
	runLive(t.Context(), map[string]string{"OPENROUTER_API_KEY": "k"}, limit, &o)
	if !slices.ContainsFunc(o.gaps, func(g gap) bool {
		return g.Name == "decide/openrouter/" && strings.Contains(g.Reason, "catalog")
	}) {
		t.Errorf("gaps %+v", o.gaps)
	}
}

func TestARowPastTheBudgetIsNotRunAndIsAGap(t *testing.T) {
	limit, _ := newBudget("1", 0)
	o := observation{live: map[string]liveRow{}}
	run := &liveRun{budget: limit, o: &o}
	ran := false
	run.do(t.Context(), job{row: "loop/x/y/low", run: func(context.Context) liveRow {
		ran = true
		return liveRow{Outcome: outcomePass}
	}})
	if ran {
		t.Error("the call went out past the budget")
	}
	if !slices.Contains(o.skippedLive, "loop/x/y/low") || len(o.gaps) != 1 || !strings.Contains(o.gaps[0].Reason, "spend limit") {
		t.Errorf("skipped %v, gaps %+v", o.skippedLive, o.gaps)
	}
}

func TestTapKeepsMembersAndHandsTheBodyOn(t *testing.T) {
	bodies := map[string]struct {
		status int
		body   string
	}{
		"/json":   {200, `{"usage":{"cost":1}}`},
		"/broken": {200, `<html>`},
		"/down":   {502, `<html>bad gateway</html>`},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := bodies[r.URL.Path]
		w.WriteHeader(b.status)
		fmt.Fprint(w, b.body)
	}))
	defer srv.Close()
	tapped := &tap{}
	client := &http.Client{Transport: tapped}
	for path, want := range bodies {
		resp, err := client.Post(srv.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(got) != want.body {
			t.Errorf("%s: the client read %q", path, got)
		}
	}
	if !tapped.paths["usage.cost"] {
		t.Errorf("paths %v", tapped.paths)
	}
	// A 200 nobody can read is news; a gateway's error page is not.
	if len(tapped.unreadable) != 1 {
		t.Errorf("unreadable %v", tapped.unreadable)
	}
}

func TestTapIgnoresWhatIsNotAModelCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"catalog"}]}`)
	}))
	defer srv.Close()
	tapped := &tap{}
	resp, err := (&http.Client{Transport: tapped}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(tapped.paths) != 0 {
		t.Errorf("a GET's members were kept: %v", tapped.paths)
	}
}

func TestEveryLoopTargetsWireHasAKeyAndAKnownWire(t *testing.T) {
	for _, target := range loopTargets {
		if wireKeys[target.Wire] == "" {
			t.Errorf("%s has no key name", target.Wire)
		}
		if !slices.ContainsFunc(wires, func(w wire) bool { return w.Name == target.Wire }) {
			t.Errorf("%s is not a wire", target.Wire)
		}
	}
}

// A vendor out of capacity says nothing about its API. Calling that a failure would
// raise drift on a row that works, and accepting it would write the bad hour down.
func TestARowTheVendorHadNoCapacityForIsAGapNotAFailure(t *testing.T) {
	limit, _ := newBudget("1", 5)
	o := observation{live: map[string]liveRow{}}
	run := &liveRun{budget: limit, o: &o}
	run.do(t.Context(), job{row: "decide/x/y/text", run: func(context.Context) liveRow {
		return liveRow{Outcome: outcomeFail, Detail: "HTTP 429: Model busy", Unreached: true}
	}})
	if _, ok := o.live["decide/x/y/text"]; ok {
		t.Errorf("the row was compared: %+v", o.live)
	}
	if !slices.Contains(o.skippedLive, "decide/x/y/text") || len(o.gaps) != 1 || !strings.Contains(o.gaps[0].Reason, "Model busy") {
		t.Errorf("skipped %v, gaps %+v", o.skippedLive, o.gaps)
	}
}

func TestOnlyARefusalForCapacityIsUnreached(t *testing.T) {
	for kind, want := range map[goodall.ErrorKind]bool{
		goodall.KindRateLimited:    true,
		goodall.KindOverloaded:     true,
		goodall.KindServer:         true,
		goodall.KindInvalidRequest: false,
		goodall.KindNotFound:       false,
		goodall.KindUnauthorized:   false,
		goodall.KindUnknown:        false,
	} {
		if got := unreached(kind); got != want {
			t.Errorf("unreached(%s) = %v, want %v", kind, got, want)
		}
	}
}
