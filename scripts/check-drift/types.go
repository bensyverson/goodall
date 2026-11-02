package main

import (
	"github.com/bensyverson/goodall/internal/livemodel"
)

// check names one of the four comparisons.
type check string

const (
	checkCatalog   check = "catalog"
	checkChangelog check = "changelog"
	checkLive      check = "live"
	checkShape     check = "shape"
)

// outcome is the only part of a live call that is compared: values, tokens and
// probabilities differ on every run.
type outcome string

const (
	outcomePass outcome = "pass"
	outcomeFail outcome = "fail"
)

// section is one heading's worth of a vendor page.
type section struct {
	// Digest identifies the section's normalized text.
	Digest string `json:"digest"`
	// Heading is the section's heading with its parents, joined by " > ".
	Heading string `json:"heading"`
	// Text is the section's words. It is never written to the baseline, which
	// keeps vendor documentation out of this repository.
	Text string `json:"-"`
}

// baseline is the last state a person accepted.
type baseline struct {
	// Accepted is the date of the last acceptance.
	Accepted string `json:"accepted"`
	// Catalogs holds each catalog's model ids, sorted.
	Catalogs map[string][]string `json:"catalogs"`
	// Changelogs holds each page's sections.
	Changelogs map[string][]section `json:"changelogs"`
	// Live holds the accepted outcome of each live row.
	Live map[string]outcome `json:"live"`
	// Members holds, per wire, accepted member paths no recording has.
	Members map[string][]string `json:"members"`
}

// liveRow is one live call's result.
type liveRow struct {
	// Outcome is what gets compared.
	Outcome outcome
	// Detail is what a reader needs to act on it, never compared.
	Detail string
	// Unreached means the vendor had no capacity for the call, so the row says
	// nothing either way and is reported as not checked.
	Unreached bool
}

// gap is something this run did not check.
type gap struct {
	// Check is the comparison that has the hole.
	Check check
	// Name is the catalog, page, row or wire.
	Name string
	// Reason says why, so the reader can repair it.
	Reason string
}

// observation is everything one run saw. A name missing from a map was not
// checked, which is never the same as being empty.
type observation struct {
	catalogs   map[string][]string
	changelogs map[string][]section
	live       map[string]liveRow
	members    map[string][]string
	// skippedLive holds live row prefixes this run could not reach, so an
	// accepted row under one stays accepted rather than reading as gone.
	skippedLive []string
	gaps        []gap
	spend       spend
}

// knowledge is what the repository itself says, which no baseline can override.
type knowledge struct {
	// pins are the models internal/livemodel names.
	pins []livemodel.Pin
	// wires names every wire whose responses should have been compared.
	wires []string
	// recorded holds each wire's member paths found in the recorded fixtures.
	recorded map[string]map[string]bool
	// modeled holds each wire's member names found on its wire structs.
	modeled map[string]map[string]bool
}

// finding is one thing that changed.
type finding struct {
	// Check is the comparison that found it.
	Check check
	// Name is what changed, the pin, heading, row or member path.
	Name string
	// What says how it changed.
	What string
	// Detail quotes the source, untrusted when it comes from a vendor.
	Detail string
}

// report is one run's result.
type report struct {
	findings []finding
	gaps     []gap
	spend    spend
	// covered says what the run looked at.
	covered []string
}
