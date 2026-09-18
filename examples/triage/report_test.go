package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/examples/triage/mailbox"
)

// TestTheDraftStatusIsReadFromTheChecksThemselves covers the cascade's whole
// vocabulary, including the two endings a scripted run does not reach: a
// redraft the inspectors then passed, and a flagged draft the run ended before
// redrafting.
func TestTheDraftStatusIsReadFromTheChecksThemselves(t *testing.T) {
	fired := []inspector{inspectors[1]}
	cases := []struct {
		name   string
		checks []checkAttempt
		want   string
	}{
		{"a clean first draft", []checkAttempt{{}}, statusReady},
		{"a repair that worked", []checkAttempt{{Fired: fired}, {}}, statusRedrafted},
		{"a repair that did not", []checkAttempt{{Fired: fired}, {Fired: fired}}, statusEscalated},
		{"a flagged draft nobody redrafted", []checkAttempt{{Fired: fired}}, statusFlagged},
	}
	for _, c := range cases {
		if got := statusOf(c.checks); got != c.want {
			t.Errorf("%s is %q, want %q", c.name, got, c.want)
		}
	}
}

// TestTheReportSaysWhatItCouldNotJudge keeps a message the judge refused
// visible: a row that went blank would read as a message with nothing to say
// about it.
func TestTheReportSaysWhatItCouldNotJudge(t *testing.T) {
	in := sampleInbox(t)
	st := newStore()
	st.recordTriageFailure(customerID, "the judge was rate limited")

	var out bytes.Buffer
	opts, _ := parseOptions(nil, io.Discard)
	writeReport(&out, opts, in, st)
	report := out.String()

	if !strings.Contains(report, "not judged: the judge was rate limited") {
		t.Errorf("the report hides a message the judge refused:\n%s", report)
	}
	if !strings.Contains(report, "not judged: it was not among the messages this run triaged") {
		t.Errorf("the report hides a message nothing was asked about:\n%s", report)
	}
	if !strings.Contains(report, "none: no reply was drafted") {
		t.Errorf("a run with no drafts says nothing about it:\n%s", report)
	}
}

// TestTheReportSaysWhenTheRunEndedEarly keeps a half-finished triage from
// reading as a finished one.
func TestTheReportSaysWhenTheRunEndedEarly(t *testing.T) {
	st := newStore()
	st.recordStop(goodall.StopCauseTurnLimit, "the run reached its 40 turns")
	var out bytes.Buffer
	opts, _ := parseOptions(nil, io.Discard)
	writeReport(&out, opts, newInbox("a source", mailbox.Listing{}, defaultSnippet), st)
	if !strings.Contains(out.String(), "ended early (turn_limit)") {
		t.Errorf("a run that stopped short did not say so:\n%s", out.String())
	}
}
