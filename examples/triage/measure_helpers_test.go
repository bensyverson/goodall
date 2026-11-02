package main

import (
	"net/mail"
	"slices"
	"strings"
	"testing"
)

// stratum is which kind of mail a sampled message stands for. None of these is
// truth about one message; each is a signal that needs nobody to read the mail.
type stratum string

const (
	// stratumReplied is a message the owner's Sent mail answers, the nearest thing
	// to a label for "this needed a reply".
	stratumReplied stratum = "replied"
	// stratumBulk carries a mailing-list or auto-submission header.
	stratumBulk stratum = "bulk"
	// stratumAutomated comes from an address nobody reads.
	stratumAutomated stratum = "automated"
	// stratumOther is everything else, and the mix of people and companies in it
	// is what the review file is for.
	stratumOther stratum = "other"
)

// classify puts a message in its stratum. A reply outranks a header, because a
// person answering a list message is evidence about that message.
func classify(id string, header mail.Header, fromAddress string, replied map[string]bool) stratum {
	switch {
	case replied[id]:
		return stratumReplied
	case isBulk(header):
		return stratumBulk
	case isUnattended(fromAddress):
		return stratumAutomated
	}
	return stratumOther
}

// isBulk reads the headers mailing software adds.
func isBulk(h mail.Header) bool {
	if h.Get("List-Unsubscribe") != "" || h.Get("List-Id") != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(h.Get("Precedence"))) {
	case "bulk", "list", "junk":
		return true
	}
	auto := strings.ToLower(strings.TrimSpace(h.Get("Auto-Submitted")))
	return auto != "" && auto != "no"
}

// isUnattended reads the local part of an address for the words that mean nobody
// is there.
func isUnattended(address string) bool {
	local, _, _ := strings.Cut(strings.ToLower(address), "@")
	local = strings.NewReplacer("-", "", "_", "", ".", "").Replace(local)
	return slices.ContainsFunc([]string{"noreply", "donotreply", "notification", "notify", "mailerdaemon", "postmaster"},
		func(word string) bool { return strings.Contains(local, word) })
}

// repliedTo reads the id out of an In-Reply-To header, which may hold brackets and
// folding.
func repliedTo(value string) string {
	value = strings.TrimSpace(value)
	if open := strings.IndexByte(value, '<'); open >= 0 {
		if end := strings.IndexByte(value[open:], '>'); end > 0 {
			return strings.TrimSpace(value[open+1 : open+end])
		}
	}
	return value
}

// pickSample takes up to want ids from each stratum, the same ones for the same
// input, so a sample does not depend on the order a store listed them.
func pickSample(byStratum map[stratum][]string, want map[stratum]int) map[string]stratum {
	out := map[string]stratum{}
	for s, ids := range byStratum {
		ordered := slices.Clone(ids)
		slices.SortFunc(ordered, func(a, b string) int { return strings.Compare(stableHash(a), stableHash(b)) })
		for _, id := range ordered[:min(want[s], len(ordered))] {
			out[id] = s
		}
	}
	return out
}

func TestClassifyPrefersAReplyThenHeadersThenTheAddress(t *testing.T) {
	list := mail.Header{"List-Unsubscribe": {"<mailto:x@example.com>"}}
	replied := map[string]bool{"r": true}
	for name, tc := range map[string]struct {
		id      string
		header  mail.Header
		address string
		want    stratum
	}{
		"replied list mail":         {"r", list, "news@example.com", stratumReplied},
		"list header":               {"a", list, "jo@example.com", stratumBulk},
		"precedence":                {"a", mail.Header{"Precedence": {" Bulk "}}, "jo@example.com", stratumBulk},
		"auto submitted":            {"a", mail.Header{"Auto-Submitted": {"auto-generated"}}, "jo@example.com", stratumBulk},
		"auto submitted no":         {"a", mail.Header{"Auto-Submitted": {"no"}}, "jo@example.com", stratumOther},
		"no-reply":                  {"a", mail.Header{}, "No-Reply@example.com", stratumAutomated},
		"do_not_reply":              {"a", mail.Header{}, "do_not_reply@example.com", stratumAutomated},
		"a word in the domain only": {"a", mail.Header{}, "jo@noreply.example.com", stratumOther},
		"a person":                  {"a", mail.Header{}, "jo@example.com", stratumOther},
	} {
		t.Run(name, func(t *testing.T) {
			if got := classify(tc.id, tc.header, tc.address, replied); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRepliedToReadsTheID(t *testing.T) {
	for in, want := range map[string]string{
		"<abc@example.com>":            "abc@example.com",
		" <abc@example.com> ":          "abc@example.com",
		"Your mail of Tuesday <a@b.c>": "a@b.c",
		"a@b.c":                        "a@b.c",
		"":                             "",
	} {
		if got := repliedTo(in); got != want {
			t.Errorf("repliedTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickSampleIsStableAndBounded(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f"}
	reversed := slices.Clone(ids)
	slices.Reverse(reversed)
	want := map[stratum]int{stratumBulk: 3, stratumOther: 9}
	one := pickSample(map[stratum][]string{stratumBulk: ids, stratumOther: {"z"}}, want)
	two := pickSample(map[stratum][]string{stratumBulk: reversed, stratumOther: {"z"}}, want)
	if len(one) != 4 || one["z"] != stratumOther {
		t.Errorf("sample = %v", one)
	}
	for id, s := range one {
		if two[id] != s {
			t.Errorf("the sample depends on listing order: %v against %v", one, two)
		}
	}
}
