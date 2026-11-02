package main

import (
	"bytes"
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bensyverson/goodall/internal/livemodel"
)

// sameAsBaseline is an observation and baseline with nothing between them, so
// each test states only the difference it is about.
func sameAsBaseline() (baseline, observation, knowledge) {
	b := baseline{
		Accepted:   "2026-11-02",
		Catalogs:   map[string][]string{"openai": {"gpt-6-luna", "gpt-6.1-sol"}},
		Changelogs: map[string][]section{"openai": {{Digest: "aaa", Heading: "October, 2026 > Oct 30"}}},
		Live:       map[string]outcome{"loop/openai/gpt-6-luna": outcomePass, "loop/openai/gpt-6.1-sol": outcomeFail},
		Members:    map[string][]string{"openai": {"usage.accepted_extra"}},
	}
	o := observation{
		catalogs:   map[string][]string{"openai": {"gpt-6-luna", "gpt-6.1-sol"}},
		changelogs: map[string][]section{"openai": {{Digest: "aaa", Heading: "October, 2026 > Oct 30", Text: "words"}}},
		live: map[string]liveRow{
			"loop/openai/gpt-6-luna":  {Outcome: outcomePass, Detail: "turns=2 in=10"},
			"loop/openai/gpt-6.1-sol": {Outcome: outcomeFail, Detail: "known refusal"},
		},
		members: map[string][]string{"openai": {"id", "usage.accepted_extra"}},
	}
	k := knowledge{
		pins:     []livemodel.Pin{{Const: "OpenAIDecision", Model: "gpt-6-luna", Catalog: "openai"}},
		wires:    []string{"openai"},
		recorded: map[string]map[string]bool{"openai": {"id": true}},
		modeled:  map[string]map[string]bool{"openai": {"id": true, "known_name": true}},
	}
	return b, o, k
}

// named returns the finding for a name, or fails.
func named(t *testing.T, r report, c check, name string) finding {
	t.Helper()
	for _, f := range r.findings {
		if f.Check == c && f.Name == name {
			return f
		}
	}
	t.Fatalf("no %s finding named %q in %+v", c, name, r.findings)
	return finding{}
}

func TestNothingChangedReportsNoDrift(t *testing.T) {
	b, o, k := sameAsBaseline()
	r := compare(b, o, k)
	if len(r.findings) != 0 || len(r.gaps) != 0 {
		t.Fatalf("findings %+v, gaps %+v, want none", r.findings, r.gaps)
	}
	if got := r.exitCode(); got != exitClean {
		t.Errorf("exit code %d, want %d", got, exitClean)
	}
	// A clean result must still state what it covered: a check over zero cases
	// is not green.
	var out bytes.Buffer
	r.render(&out, b)
	if !strings.Contains(out.String(), "RESULT: no drift") {
		t.Errorf("render has no clean result line:\n%s", out.String())
	}
}

func TestLiveValuesAreNotCompared(t *testing.T) {
	b, o, k := sameAsBaseline()
	o.live["loop/openai/gpt-6-luna"] = liveRow{Outcome: outcomePass, Detail: "turns=3 in=99999"}
	if r := compare(b, o, k); len(r.findings) != 0 {
		t.Errorf("a differing detail was reported: %+v", r.findings)
	}
}

func TestARetiredPinIsReportedByName(t *testing.T) {
	b, o, k := sameAsBaseline()
	o.catalogs["openai"] = []string{"gpt-6.1-sol"}
	r := compare(b, o, k)
	f := named(t, r, checkCatalog, "gpt-6-luna")
	if !strings.Contains(f.What, "livemodel.OpenAIDecision") {
		t.Errorf("the finding does not name the pin: %+v", f)
	}
	if r.exitCode() != exitDrift {
		t.Errorf("exit code %d, want %d", r.exitCode(), exitDrift)
	}
}

func TestAPinGoneFromTheBaselineToo(t *testing.T) {
	// Accepting a baseline cannot silence a pin that is gone.
	b, o, k := sameAsBaseline()
	b.Catalogs["openai"] = []string{"gpt-6.1-sol"}
	o.catalogs["openai"] = []string{"gpt-6.1-sol"}
	named(t, compare(b, o, k), checkCatalog, "gpt-6-luna")
}

func TestAPinWithNoCatalogIsNotJudged(t *testing.T) {
	b, o, k := sameAsBaseline()
	k.pins = append(k.pins, livemodel.Pin{Const: "TypeSafe", Model: "jev-1.13.0"})
	if r := compare(b, o, k); len(r.findings) != 0 {
		t.Errorf("a pin no catalog lists was judged: %+v", r.findings)
	}
}

func TestCatalogNewcomersAreLabeled(t *testing.T) {
	b, o, k := sameAsBaseline()
	o.catalogs["openai"] = append(o.catalogs["openai"], "gpt-6.2-sol", "gpt-7-nova")
	r := compare(b, o, k)
	if f := named(t, r, checkCatalog, "gpt-6.2-sol"); !strings.Contains(f.What, "new version") {
		t.Errorf("gpt-6.2-sol: %+v", f)
	}
	if f := named(t, r, checkCatalog, "gpt-7-nova"); !strings.Contains(f.What, "new family") {
		t.Errorf("gpt-7-nova: %+v", f)
	}
}

func TestAnIDGoneFromACatalogIsReported(t *testing.T) {
	b, o, k := sameAsBaseline()
	k.pins = nil
	o.catalogs["openai"] = []string{"gpt-6-luna"}
	f := named(t, compare(b, o, k), checkCatalog, "gpt-6.1-sol")
	if !strings.Contains(f.What, "gone") {
		t.Errorf("%+v", f)
	}
}

func TestFamily(t *testing.T) {
	for id, want := range map[string]string{
		"gpt-6.1-sol":                 "gpt-N-sol",
		"gpt-5.6-terra":               "gpt-N-terra",
		"claude-opus-5-5":             "claude-opus-N",
		"claude-3-5-haiku-20241022":   "claude-N-haiku",
		"anthropic/claude-sonnet-5.5": "anthropic/claude-sonnet-N",
		"jev-1.13.0":                  "jev-N",
		"gpt-5.6-terra-2026-06-01":    "gpt-N-terra",
	} {
		if got := family(id); got != want {
			t.Errorf("family(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestANewChangelogEntryIsReportedWithItsHeadingAndText(t *testing.T) {
	b, o, k := sameAsBaseline()
	o.changelogs["openai"] = append(o.changelogs["openai"],
		section{Digest: "bbb", Heading: "October, 2026 > Oct 31", Text: "Use the Responses API for tool calling."})
	f := named(t, compare(b, o, k), checkChangelog, "openai: October, 2026 > Oct 31")
	if !strings.Contains(f.What, "new") || !strings.Contains(f.Detail, "Responses API") {
		t.Errorf("%+v", f)
	}
}

func TestAnEditedChangelogEntryReadsAsChanged(t *testing.T) {
	b, o, k := sameAsBaseline()
	o.changelogs["openai"] = []section{{Digest: "ccc", Heading: "October, 2026 > Oct 30", Text: "reworded"}}
	r := compare(b, o, k)
	if f := named(t, r, checkChangelog, "openai: October, 2026 > Oct 30"); !strings.Contains(f.What, "changed") {
		t.Errorf("%+v", f)
	}
	if len(r.findings) != 1 {
		t.Errorf("an edit should be one finding: %+v", r.findings)
	}
}

func TestARemovedChangelogEntryIsReported(t *testing.T) {
	b, o, k := sameAsBaseline()
	o.changelogs["openai"] = []section{{Digest: "ddd", Heading: "September, 2026", Text: "x"}}
	f := named(t, compare(b, o, k), checkChangelog, "openai: October, 2026 > Oct 30")
	if !strings.Contains(f.What, "removed") {
		t.Errorf("%+v", f)
	}
}

func TestAnUnmodeledMemberIsReportedByPath(t *testing.T) {
	b, o, k := sameAsBaseline()
	o.members["openai"] = append(o.members["openai"], "usage.surprise_tokens")
	f := named(t, compare(b, o, k), checkShape, "openai: usage.surprise_tokens")
	if !strings.Contains(f.What, "no wire struct models") {
		t.Errorf("%+v", f)
	}
}

func TestANewPathOfAModeledNameIsToldApart(t *testing.T) {
	b, o, k := sameAsBaseline()
	o.members["openai"] = append(o.members["openai"], "usage.known_name")
	f := named(t, compare(b, o, k), checkShape, "openai: usage.known_name")
	if strings.Contains(f.What, "no wire struct models") || !strings.Contains(f.What, "not in any recording") {
		t.Errorf("%+v", f)
	}
}

func TestLiveChangesAreReported(t *testing.T) {
	for name, tc := range map[string]struct {
		row  string
		lr   liveRow
		what string
	}{
		"regressed": {"loop/openai/gpt-6-luna", liveRow{Outcome: outcomeFail, Detail: "use /v1/responses"}, "was passing"},
		"recovered": {"loop/openai/gpt-6.1-sol", liveRow{Outcome: outcomePass}, "was failing"},
		"new fail":  {"loop/openai/gpt-7", liveRow{Outcome: outcomeFail, Detail: "use /v1/responses"}, "new row"},
		"new pass":  {"loop/openai/gpt-8", liveRow{Outcome: outcomePass}, "new row"},
	} {
		t.Run(name, func(t *testing.T) {
			b, o, k := sameAsBaseline()
			o.live[tc.row] = tc.lr
			f := named(t, compare(b, o, k), checkLive, tc.row)
			if !strings.Contains(f.What, tc.what) || !strings.Contains(f.What, string(tc.lr.Outcome)) || f.Detail != tc.lr.Detail {
				t.Errorf("%+v", f)
			}
		})
	}
}

func TestALiveRowGoneIsReportedUnlessItWasSkipped(t *testing.T) {
	b, o, k := sameAsBaseline()
	delete(o.live, "loop/openai/gpt-6-luna")
	named(t, compare(b, o, k), checkLive, "loop/openai/gpt-6-luna")

	o.skippedLive = []string{"loop/openai/"}
	if r := compare(b, o, k); len(r.findings) != 0 {
		t.Errorf("a skipped row read as gone: %+v", r.findings)
	}
}

func TestWhatWasNotCheckedIsAGapNotAClean(t *testing.T) {
	b, o, k := sameAsBaseline()
	delete(o.catalogs, "openai")
	o.gaps = append(o.gaps, gap{Check: checkCatalog, Name: "openai", Reason: "no OPENAI_API_KEY in the env file"})
	r := compare(b, o, k)
	if len(r.findings) != 0 {
		t.Errorf("an unread catalog reported findings: %+v", r.findings)
	}
	if r.exitCode() != exitNotChecked {
		t.Errorf("exit code %d, want %d", r.exitCode(), exitNotChecked)
	}
	var out bytes.Buffer
	r.render(&out, b)
	text := out.String()
	if !strings.Contains(text, "NOT CHECKED") || !strings.Contains(text, "OPENAI_API_KEY") {
		t.Errorf("render does not say what was not checked:\n%s", text)
	}
	if strings.Contains(text, "RESULT: no drift") {
		t.Errorf("an incomplete run claims a clean result:\n%s", text)
	}
}

func TestAWireWithNoFreshResponseIsAGap(t *testing.T) {
	b, o, k := sameAsBaseline()
	delete(o.members, "openai")
	r := compare(b, o, k)
	if len(r.gaps) != 1 || r.gaps[0].Check != checkShape || r.gaps[0].Name != "openai" {
		t.Errorf("gaps %+v", r.gaps)
	}
}

func TestAWireWithNoKnownModelIsAGap(t *testing.T) {
	// A scan that found no wire structs would call every member unmodeled.
	b, o, k := sameAsBaseline()
	k.modeled = map[string]map[string]bool{}
	r := compare(b, o, k)
	if len(r.gaps) != 1 || r.gaps[0].Check != checkShape {
		t.Errorf("gaps %+v", r.gaps)
	}
}

func TestDriftAndGapsBothShowInTheExitCode(t *testing.T) {
	b, o, k := sameAsBaseline()
	o.catalogs["openai"] = append(o.catalogs["openai"], "gpt-7-nova")
	o.gaps = append(o.gaps, gap{Check: checkChangelog, Name: "openai", Reason: "HTTP 503"})
	if got := compare(b, o, k).exitCode(); got != exitDrift|exitNotChecked {
		t.Errorf("exit code %d, want %d", got, exitDrift|exitNotChecked)
	}
}

func TestRenderNamesFindingsAndMarksVendorTextAsData(t *testing.T) {
	b, o, k := sameAsBaseline()
	o.changelogs["openai"] = append(o.changelogs["openai"],
		section{Digest: "bbb", Heading: "Oct 31", Text: strings.Repeat("long ", 400)})
	r := compare(b, o, k)
	var out bytes.Buffer
	r.render(&out, b)
	text := out.String()
	for _, want := range []string{"Oct 31", "RESULT: drift", "untrusted", "2026-11-02"} {
		if !strings.Contains(text, want) {
			t.Errorf("render lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "long ") > 100 {
		t.Errorf("a long section was not clipped")
	}
}

func TestASourceWithNoBaselineIsOneFindingNotAFlood(t *testing.T) {
	b, o, k := sameAsBaseline()
	o.catalogs["google"] = []string{"a", "b", "c"}
	o.changelogs["google"] = []section{{Digest: "1", Heading: "A"}, {Digest: "2", Heading: "B"}}
	r := compare(b, o, k)
	if f := named(t, r, checkCatalog, "google"); !strings.Contains(f.What, "no baseline") {
		t.Errorf("%+v", f)
	}
	if f := named(t, r, checkChangelog, "google"); !strings.Contains(f.What, "no baseline") {
		t.Errorf("%+v", f)
	}
	if len(r.findings) != 2 {
		t.Errorf("%+v", r.findings)
	}
}

func TestAcceptTakesWhatWasSeenAndKeepsWhatWasNot(t *testing.T) {
	b, o, k := sameAsBaseline()
	b.Catalogs["anthropic"] = []string{"claude-sonnet-5-5"}
	b.Changelogs["anthropic"] = []section{{Digest: "zzz", Heading: "kept"}}
	b.Live["loop/anthropic/claude-sonnet-5-5"] = outcomePass
	b.Live["loop/openai/retired"] = outcomePass
	o.skippedLive = []string{"loop/anthropic/"}
	o.catalogs["openai"] = []string{"gpt-7-nova"}
	o.members["openai"] = append(o.members["openai"], "usage.new_member")
	k.pins = nil // a pin gone from a catalog is the one thing accepting cannot clear

	got := accept(b, o, k, "2026-12-01")

	if got.Accepted != "2026-12-01" {
		t.Errorf("Accepted = %q", got.Accepted)
	}
	if !reflect.DeepEqual(got.Catalogs["anthropic"], []string{"claude-sonnet-5-5"}) {
		t.Errorf("an unread catalog was lost: %v", got.Catalogs)
	}
	if !reflect.DeepEqual(got.Catalogs["openai"], []string{"gpt-7-nova"}) {
		t.Errorf("a read catalog was not taken: %v", got.Catalogs)
	}
	if len(got.Changelogs["anthropic"]) != 1 {
		t.Errorf("an unread page was lost: %v", got.Changelogs)
	}
	if got.Live["loop/anthropic/claude-sonnet-5-5"] != outcomePass {
		t.Errorf("a skipped row was lost: %v", got.Live)
	}
	if _, ok := got.Live["loop/openai/retired"]; ok {
		t.Errorf("a row gone from the run stayed: %v", got.Live)
	}
	// The recorded "id" stays in the recordings and needs no baseline entry.
	if want := []string{"usage.accepted_extra", "usage.new_member"}; !reflect.DeepEqual(got.Members["openai"], want) {
		t.Errorf("Members = %v, want %v", got.Members["openai"], want)
	}
	// Accepting leaves nothing to report, apart from what was not read.
	if r := compare(got, o, k); len(r.findings) != 0 {
		t.Errorf("drift straight after accepting: %+v", r.findings)
	}
}

func TestBaselineRoundTripsAndKeepsNoVendorText(t *testing.T) {
	b, _, _ := sameAsBaseline()
	b.Changelogs["openai"][0].Text = "VENDOR WORDS"
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := writeBaseline(path, b); err != nil {
		t.Fatal(err)
	}
	got, err := readBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Changelogs["openai"][0].Text != "" {
		t.Errorf("vendor text reached the baseline")
	}
	b.Changelogs["openai"][0].Text = ""
	if !maps.EqualFunc(got.Live, b.Live, func(a, b outcome) bool { return a == b }) ||
		!reflect.DeepEqual(got.Changelogs, b.Changelogs) || !reflect.DeepEqual(got.Catalogs, b.Catalogs) {
		t.Errorf("round trip: %+v, want %+v", got, b)
	}
	if _, err := readBaseline(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Errorf("a missing baseline read as an empty one")
	}
}
