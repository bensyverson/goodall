package main

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

func TestParseSectionsWalksHeadings(t *testing.T) {
	got := parseSections("preamble\n\n# Changelog\nintro\n## October, 2026\n### Oct 30\nA thing.\n```\n# not a heading\n```\n### Oct 28\nB thing.\n## September, 2026\n### Oct 30\nC thing.\n")
	var heads []string
	for _, s := range got {
		heads = append(heads, s.Heading)
	}
	want := []string{
		preambleHeading,
		"Changelog",
		"Changelog > October, 2026",
		"Changelog > October, 2026 > Oct 30",
		"Changelog > October, 2026 > Oct 28",
		"Changelog > September, 2026",
		"Changelog > September, 2026 > Oct 30",
	}
	if !slices.Equal(heads, want) {
		t.Errorf("headings:\n got %q\nwant %q", heads, want)
	}
	if !strings.Contains(got[3].Text, "# not a heading") {
		t.Errorf("a fenced line was lost: %q", got[3].Text)
	}
}

func TestSectionDigestsFollowWordsNotWhitespace(t *testing.T) {
	a := parseSections("## A\nsome  words\n\nmore\n")
	b := parseSections("## A\n\nsome words\nmore  \n")
	c := parseSections("## A\nother words\n")
	if len(a) != 1 || len(b) != 1 || len(c) != 1 {
		t.Fatalf("sections: %v %v %v", a, b, c)
	}
	if a[0].Digest != b[0].Digest {
		t.Errorf("whitespace changed the digest")
	}
	if a[0].Digest == c[0].Digest {
		t.Errorf("different words share a digest")
	}
}

func TestAnEmptySectionUnderAHeadingKeepsItsPlace(t *testing.T) {
	// The same heading under a new parent is a new entry, even if empty.
	a := parseSections("## 2025\n### Oct 30\n")
	b := parseSections("## 2026\n### Oct 30\n")
	if a[1].Digest == b[1].Digest {
		t.Errorf("sections under different parents share a digest")
	}
}

func TestReadPageRefusesWhatIsNotAPage(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		ok     bool
	}{
		"a real page":        {200, "# T\n## A\nwords\n", true},
		"not found":          {404, "# Page Not Found\n", false},
		"server error":       {503, "down", false},
		"an HTML page":       {200, "<!DOCTYPE html><html>\n# T\n</html>", false},
		"a page of nothing":  {200, "", false},
		"no headings at all": {200, "just some words", false},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			got, err := readPage(t.Context(), srv.Client(), srv.URL)
			if tc.ok && (err != nil || len(got) == 0) {
				t.Errorf("got %v, %v", got, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("got %v and no error", got)
			}
		})
	}
}

func TestAnUnreachablePageIsAGap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "# T\n## A\nwords\n")
	}))
	dead := httptest.NewServer(nil)
	deadURL := dead.URL
	dead.Close()
	defer srv.Close()

	o := observation{changelogs: map[string][]section{}}
	readPages(t.Context(), http.DefaultClient, []page{{"up", srv.URL}, {"down", deadURL}}, &o)
	if _, ok := o.changelogs["up"]; !ok {
		t.Errorf("the reachable page was not read")
	}
	if _, ok := o.changelogs["down"]; ok {
		t.Errorf("the unreachable page has an entry, which would read as a removal")
	}
	if len(o.gaps) != 1 || o.gaps[0].Check != checkChangelog || o.gaps[0].Name != "down" {
		t.Errorf("gaps %+v", o.gaps)
	}
}

func TestReadCatalogPagesAndFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Query().Get("after_id") == "" {
			fmt.Fprint(w, `{"data":[{"id":"b/two"},{"id":"a/one"}],"has_more":true,"last_id":"a/one"}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"z/three"}],"has_more":false}`)
	}))
	defer srv.Close()
	spec := catalogSpec{Name: "x", URL: srv.URL, KeyEnv: "K", Prefixes: []string{"a/", "b/"},
		Header: func(key string) http.Header { return http.Header{"X-Api-Key": {key}} }}
	got, err := readCatalog(t.Context(), srv.Client(), spec, "k")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a/one", "b/two"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestReadCatalogReadsTypeSafesShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"models":[{"name":"jev-latest"}]}`)
	}))
	defer srv.Close()
	got, err := readCatalog(t.Context(), srv.Client(), catalogSpec{Name: "x", URL: srv.URL}, "")
	if err != nil || !slices.Equal(got, []string{"jev-latest"}) {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestReadCatalogRefusesAnErrorDressedAsAnEmptyList(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"401":            {401, `{"error":"no"}`},
		"200 with error": {200, `{"error":{"message":"bad key"}}`},
		"200 empty":      {200, `{"data":[]}`},
		"200 not JSON":   {200, `<html>`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			if got, err := readCatalog(t.Context(), srv.Client(), catalogSpec{Name: "x", URL: srv.URL}, ""); err == nil {
				t.Errorf("got %v and no error", got)
			}
		})
	}
}

func TestAMissingKeyIsAGapThatNamesTheKey(t *testing.T) {
	o := observation{catalogs: map[string][]string{}}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	readCatalogs(t.Context(), srv.Client(), []catalogSpec{{Name: "openai", URL: srv.URL, KeyEnv: "OPENAI_API_KEY"}}, map[string]string{}, &o)
	if calls != 0 {
		t.Errorf("a call went out with no key")
	}
	if len(o.gaps) != 1 || !strings.Contains(o.gaps[0].Reason, "OPENAI_API_KEY") {
		t.Errorf("gaps %+v", o.gaps)
	}
	if _, ok := o.catalogs["openai"]; ok {
		t.Errorf("an unread catalog has an entry")
	}
}

func TestMemberPaths(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want []string
	}{
		"object": {`{"id":"x","usage":{"input":1}}`, []string{"id", "usage", "usage.input"}},
		"arrays": {`{"choices":[{"delta":{"content":"x"}},{"finish":"y"}]}`,
			[]string{"choices", "choices[].delta", "choices[].delta.content", "choices[].finish"}},
		"sse": {"event: a\ndata: {\"type\":\"x\"}\n\n: comment\n\ndata: {\"delta\":{\"t\":1}}\n\ndata: [DONE]\n\n",
			[]string{"delta", "delta.t", "type"}},
		"tool input is the caller's data": {`{"content":[{"input":{"city":"Paris"}}]}`,
			[]string{"content", "content[].input"}},
		"answers are keyed by the caller's question ids": {`{"answers":{"q1":{"type":"x"},"q2":{"type":"x","reason":"y"}}}`,
			[]string{"answers", "answers.*", "answers.*.reason", "answers.*.type"}},
		"answer tables are keyed by the caller's options": {`{"probabilities":{"billing":0.5},"legend":{"0":"Calm"}}`,
			[]string{"legend", "probabilities"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := memberPaths([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			keys := slices.Sorted(maps.Keys(got))
			if !slices.Equal(keys, tc.want) {
				t.Errorf("got %v, want %v", keys, tc.want)
			}
		})
	}
	if _, err := memberPaths([]byte("<html>down</html>")); err == nil {
		t.Errorf("a body that is not JSON read as having no members")
	}
}

func TestModeledNamesReadsWireTags(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("wire.go", "package x\ntype a struct {\n\tOne string `json:\"one,omitzero\"`\n\tTwo struct {\n\t\tThree int `json:\"three\"`\n\t} `json:\"two\"`\n\tSkip int `json:\"-\"`\n\tPlain int\n}\n")
	write("wire_test.go", "package x\ntype b struct {\n\tNope string `json:\"test_only\"`\n}\n")
	got, err := modeledNames(dir)
	if err != nil {
		t.Fatal(err)
	}
	keys := slices.Sorted(maps.Keys(got))
	if want := []string{"one", "three", "two"}; !slices.Equal(keys, want) {
		t.Errorf("got %v, want %v", keys, want)
	}
	if _, err := modeledNames(filepath.Join(dir, "absent")); err == nil {
		t.Errorf("a missing directory read as no names")
	}
}

func TestRecordedPathsSkipRequestsAndMatchGlobs(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("live_a.json", `{"id":"x"}`)
	write("live_a.request.json", `{"model":"x"}`)
	write("live_b.sse", "data: {\"delta\":1}\n\n")
	write("other.json", `{"nope":1}`)
	got, err := recordedPaths(dir, []string{"live_*"})
	if err != nil {
		t.Fatal(err)
	}
	if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, []string{"delta", "id"}) {
		t.Errorf("got %v", keys)
	}
	if _, err := recordedPaths(dir, []string{"none_*"}); err == nil {
		t.Errorf("a glob over no recordings read as an empty set")
	}
}

func TestEveryRepoWireHasRecordingsAndWireStructs(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, w := range wires {
		recorded, err := recordedPaths(root, w.Fixtures)
		if err != nil {
			t.Errorf("%s recordings: %v", w.Name, err)
		}
		if len(recorded) == 0 {
			t.Errorf("%s has no recorded members", w.Name)
		}
		dirs := make([]string, len(w.Source))
		for i, d := range w.Source {
			dirs[i] = filepath.Join(root, d)
		}
		modeled, err := modeledNames(dirs...)
		if err != nil {
			t.Errorf("%s structs: %v", w.Name, err)
		}
		if len(modeled) == 0 {
			t.Errorf("%s has no modeled names", w.Name)
		}
	}
}

func TestBudgetStopsAtItsLimits(t *testing.T) {
	cost := func(s string) goodall.Cost {
		d, err := goodall.ParseDecimal(s)
		if err != nil {
			t.Fatal(err)
		}
		return goodall.Cost{Amount: d, Currency: "USD", Reported: true}
	}
	b, err := newBudget("0.10", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !b.take() {
		t.Fatal("a fresh budget refused a call")
	}
	b.charge(cost("0.04"), goodall.Usage{Input: 10, Output: 5})
	if !b.take() {
		t.Fatal("a budget under its limits refused a call")
	}
	b.charge(goodall.Cost{}, goodall.Usage{Input: 100, Output: 50})
	if !b.take() {
		t.Fatal("an unpriced call used up the money")
	}
	if b.take() {
		t.Error("the call limit did not hold")
	}
	s := b.spent()
	if s.calls != 3 || s.priced != 1 || s.unpriced != 1 || s.unpricedIn != 100 || s.unpricedOut != 50 || s.reported.String() != "0.04" {
		t.Errorf("spend %+v", s)
	}

	dear, _ := newBudget("0.10", 100)
	dear.take()
	dear.charge(cost("0.11"), goodall.Usage{})
	if dear.take() {
		t.Error("the money limit did not hold")
	}
	if _, err := newBudget("lots", 1); err == nil {
		t.Error("a bad limit was accepted")
	}
}
