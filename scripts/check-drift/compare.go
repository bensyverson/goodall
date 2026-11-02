package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// datedSuffix is the release date some vendors append to an id.
var datedSuffix = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2})$`)

// version is the run of numbers that changes between releases of one family.
var version = regexp.MustCompile(`\d+([.-]\d+)*`)

// family reduces a model id to the part that survives a new release, so a new
// version of a known line reads differently from a line nobody has seen.
func family(id string) string {
	return version.ReplaceAllString(datedSuffix.ReplaceAllString(id, ""), "N")
}

// compare is the whole judgment this tool makes: what differs between the last
// accepted state and this run. It looks at shape and pass or fail, never at the
// values, which differ on every run.
func compare(b baseline, o observation, k knowledge) report {
	r := report{gaps: slices.Clone(o.gaps), spend: o.spend}
	r.findings = append(r.findings, compareCatalogs(b, o, k)...)
	r.findings = append(r.findings, compareChangelogs(b, o)...)
	r.findings = append(r.findings, compareLive(b, o)...)
	shape, gaps := compareMembers(b, o, k, r.gaps)
	r.findings = append(r.findings, shape...)
	r.gaps = append(r.gaps, gaps...)
	r.covered = covered(o)
	return r
}

// compareCatalogs reports retired pins, newcomers and departures. A catalog this
// run did not read says nothing, so it is skipped rather than read as empty.
func compareCatalogs(b baseline, o observation, k knowledge) []finding {
	var out []finding
	retired := map[string]bool{}
	for _, pin := range k.pins {
		ids, read := o.catalogs[pin.Catalog]
		if !read || slices.Contains(ids, pin.Model) {
			continue
		}
		retired[pin.Model] = true
		out = append(out, finding{Check: checkCatalog, Name: pin.Model,
			What: fmt.Sprintf("pin livemodel.%s is gone from the %s catalog", pin.Const, pin.Catalog)})
	}
	for _, name := range sortedKeys(o.catalogs) {
		ids := o.catalogs[name]
		before, known := b.Catalogs[name]
		if !known {
			out = append(out, finding{Check: checkCatalog, Name: name,
				What: fmt.Sprintf("catalog has no baseline, and lists %d models", len(ids))})
			continue
		}
		families := map[string]bool{}
		for _, id := range before {
			families[family(id)] = true
		}
		for _, id := range ids {
			if slices.Contains(before, id) {
				continue
			}
			what := "new version of a known family"
			if !families[family(id)] {
				what = "new family"
			}
			out = append(out, finding{Check: checkCatalog, Name: id, What: what + " in the " + name + " catalog"})
		}
		for _, id := range before {
			if !slices.Contains(ids, id) && !retired[id] {
				out = append(out, finding{Check: checkCatalog, Name: id, What: "gone from the " + name + " catalog"})
			}
		}
	}
	return out
}

// compareChangelogs reports sections by heading. A digest that is new under a
// heading the baseline knows is one edit, not an addition and a removal.
func compareChangelogs(b baseline, o observation) []finding {
	var out []finding
	for _, name := range sortedKeys(o.changelogs) {
		sections := o.changelogs[name]
		before, known := b.Changelogs[name]
		if !known {
			out = append(out, finding{Check: checkChangelog, Name: name,
				What: fmt.Sprintf("page has no baseline, and has %d sections", len(sections))})
			continue
		}
		oldDigests, oldHeadings := index(before)
		newDigests, newHeadings := index(sections)
		for _, s := range sections {
			if oldDigests[s.Digest] {
				continue
			}
			what := "new entry"
			if oldHeadings[s.Heading] {
				what = "changed entry"
			}
			out = append(out, finding{Check: checkChangelog, Name: name + ": " + s.Heading, What: what, Detail: s.Text})
		}
		for _, s := range before {
			if !newDigests[s.Digest] && !newHeadings[s.Heading] {
				out = append(out, finding{Check: checkChangelog, Name: name + ": " + s.Heading, What: "entry removed from the page"})
			}
		}
	}
	return out
}

// index sets a page's digests and headings.
func index(sections []section) (digests, headings map[string]bool) {
	digests, headings = map[string]bool{}, map[string]bool{}
	for _, s := range sections {
		digests[s.Digest] = true
		headings[s.Heading] = true
	}
	return digests, headings
}

// compareLive reports a row whose outcome differs, including one that recovered,
// since a known failure going away is news too.
func compareLive(b baseline, o observation) []finding {
	var out []finding
	for _, name := range sortedKeys(o.live) {
		row := o.live[name]
		before, known := b.Live[name]
		switch {
		case !known:
			out = append(out, finding{Check: checkLive, Name: name, What: "new row, " + string(row.Outcome), Detail: row.Detail})
		case before != row.Outcome:
			was := "was passing"
			if before == outcomeFail {
				was = "was failing"
			}
			out = append(out, finding{Check: checkLive, Name: name, What: was + ", now " + string(row.Outcome), Detail: row.Detail})
		}
	}
	for _, name := range sortedKeys(b.Live) {
		if _, seen := o.live[name]; !seen && !o.skipped(name) {
			out = append(out, finding{Check: checkLive, Name: name, What: "row gone: this run no longer asks it"})
		}
	}
	return out
}

// skipped reports whether a live row sits under a prefix this run could not reach.
func (o observation) skipped(row string) bool {
	return slices.ContainsFunc(o.skippedLive, func(prefix string) bool { return strings.HasPrefix(row, prefix) })
}

// compareMembers reports members in fresh responses that neither a recording nor
// the baseline has, and says whether a wire struct knows the name. A member that
// stops arriving is not reported, since most are optional; a rename shows up as
// its new name. A wire it cannot judge is a gap, never a pass.
func compareMembers(b baseline, o observation, k knowledge, existing []gap) ([]finding, []gap) {
	var out []finding
	var gaps []gap
	addGap := func(wire, reason string) {
		known := slices.ContainsFunc(existing, func(g gap) bool { return g.Check == checkShape && g.Name == wire })
		if !known {
			gaps = append(gaps, gap{Check: checkShape, Name: wire, Reason: reason})
		}
	}
	for _, wire := range k.wires {
		fresh, seen := o.members[wire]
		switch {
		case !seen:
			addGap(wire, "no fresh response from this wire to compare")
			continue
		case len(k.modeled[wire]) == 0:
			addGap(wire, "no wire struct names were found to compare against")
			continue
		case len(k.recorded[wire]) == 0:
			addGap(wire, "no recorded members were found to compare against")
			continue
		}
		for _, path := range fresh {
			if k.recorded[wire][path] || slices.Contains(b.Members[wire], path) {
				continue
			}
			what := "new member: no wire struct models this name"
			if k.modeled[wire][leaf(path)] {
				what = "new member: not in any recording, though a wire struct has the name"
			}
			out = append(out, finding{Check: checkShape, Name: wire + ": " + path, What: what})
		}
	}
	return out, gaps
}

// leaf is the last member name of a path.
func leaf(path string) string {
	return strings.TrimSuffix(path[strings.LastIndexByte(path, '.')+1:], "[]")
}

// covered says what a run actually looked at, because a green result over zero
// cases is not green.
func covered(o observation) []string {
	ids, sections, members := 0, 0, 0
	for _, list := range o.catalogs {
		ids += len(list)
	}
	for _, list := range o.changelogs {
		sections += len(list)
	}
	for _, list := range o.members {
		members += len(list)
	}
	return []string{
		fmt.Sprintf("%d catalogs (%d models)", len(o.catalogs), ids),
		fmt.Sprintf("%d changelog pages (%d sections)", len(o.changelogs), sections),
		fmt.Sprintf("%d live rows", len(o.live)),
		fmt.Sprintf("%d wires (%d member paths)", len(o.members), members),
	}
}

// accept folds a run into the baseline. What the run did not read stays as it
// was, so an outage cannot quietly forget a source; a pin gone from a catalog
// stays reported, since only repinning repairs it.
func accept(b baseline, o observation, k knowledge, date string) baseline {
	out := baseline{
		Accepted:   date,
		Catalogs:   map[string][]string{},
		Changelogs: map[string][]section{},
		Live:       map[string]outcome{},
		Members:    map[string][]string{},
	}
	copyInto(out.Catalogs, b.Catalogs, o.catalogs)
	copyInto(out.Changelogs, b.Changelogs, o.changelogs)
	for name, was := range b.Live {
		if o.skipped(name) {
			out.Live[name] = was
		}
	}
	for name, row := range o.live {
		out.Live[name] = row.Outcome
	}
	for wire, paths := range b.Members {
		out.Members[wire] = slices.Clone(paths)
	}
	for wire, paths := range o.members {
		for _, path := range paths {
			if !k.recorded[wire][path] && !slices.Contains(out.Members[wire], path) {
				out.Members[wire] = append(out.Members[wire], path)
			}
		}
		slices.Sort(out.Members[wire])
	}
	return out
}

// copyInto takes the old values, then lays the new ones over them.
func copyInto[V any](dst, old, fresh map[string]V) {
	maps.Copy(dst, old)
	maps.Copy(dst, fresh)
}

// sortedKeys is a map's names in a stable order, so two runs read alike.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
