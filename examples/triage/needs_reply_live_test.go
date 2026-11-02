package main

import (
	"strings"
	"testing"

	"github.com/bensyverson/goodall/decision"
)

// TestLiveNeedsReplyOnTheBundledInbox asks the judge the shipping questions about
// the bundled messages whose answer is not a matter of taste. The appeal and the
// survey are the shape that passed on real mail: a company's call to action, written
// as if a person were waiting. It runs under the plain go test, on one pinned model.
func TestLiveNeedsReplyOnTheBundledInbox(t *testing.T) {
	if testing.Short() {
		t.Skip("spends real tokens, and -short is the offline suite")
	}
	judge, err := newJudgeClient(judgeTypeSafe)
	if err != nil {
		t.Skipf("no judge: %v; set %s in .env", err, typeSafeKeyEnv)
	}
	want := map[string]bool{
		"Will you stand with us before Friday?":                     false,
		"Reminder: we would still love your feedback":               false,
		"Your receipt from Orchard Coffee":                          false,
		"Nightly backup completed":                                  false,
		"Field Notes: five things about small teams this week":      false,
		"Can Harbour export a ledger as CSV?":                       true,
		"Invoice 2214 - which purchase order should we bill it to?": true,
		"Decision needed: which vendor for the Q3 print run":        true,
		"Interested in Harbour for about 40 seats":                  true,
	}
	asked := 0
	for _, record := range sampleInbox(t).Records {
		expected, known := want[strings.TrimSpace(record.Subject)]
		if !known {
			continue
		}
		asked++
		t.Run(record.Subject, func(t *testing.T) {
			answers, err := judge.Decide(t.Context(), &decision.Request{Model: defaultJudgeModel, State: stateOf(record), Questions: triageQuestions()})
			if err != nil {
				t.Fatal(err)
			}
			verdict, err := readTriage(answers)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("needs_reply %.2f", verdict.NeedsReplyProbability)
			if verdict.NeedsReply != expected {
				t.Errorf("needs_reply = %v at %.2f, want %v", verdict.NeedsReply, verdict.NeedsReplyProbability, expected)
			}
		})
	}
	if asked != len(want) {
		t.Errorf("asked about %d messages of %d; a subject in this table has left the bundled inbox", asked, len(want))
	}
}
