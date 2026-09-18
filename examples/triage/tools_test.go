package main

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/examples/triage/mailbox"
)

// The ids of the sample inbox's messages that the tests name.
const (
	customerID  = "m03-customer@northwind.example"
	invoiceID   = "m04-invoice@brightlane.example"
	colleagueID = "m05-colleague@harbourworks.example"
	phishingID  = "m07-phishing@harbour-secure-verify.test"
	sampleCount = 12
)

// sampleInbox reads the bundled sample the way the command does with no flags.
func sampleInbox(t *testing.T) *inbox {
	t.Helper()
	opts, err := parseOptions(nil, io.Discard)
	if err != nil {
		t.Fatalf("parsing the default flags: %v", err)
	}
	in, err := opts.open(t.Context())
	if err != nil {
		t.Fatalf("opening the sample inbox: %v", err)
	}
	return in
}

// runTool executes one tool the way the loop does, with its reports collected.
func runTool(t *testing.T, tool goodall.Tool, input string) (goodall.ToolOutcome, []goodall.Event) {
	t.Helper()
	reporter, ok := tool.(goodall.Reporter)
	if !ok {
		result, err := tool.Execute(t.Context(), jsontext.Value(input))
		if err != nil {
			t.Fatalf("running %s: %v", tool.Name(), err)
		}
		return goodall.ToolOutcome{Result: result}, nil
	}
	var reported []goodall.Event
	outcome, err := reporter.ExecuteReporting(t.Context(), jsontext.Value(input), func(ev goodall.Event) {
		reported = append(reported, ev)
	})
	if err != nil {
		t.Fatalf("running %s: %v", tool.Name(), err)
	}
	return outcome, reported
}

// decodeResult reads a tool result's JSON into v.
func decodeResult[T any](t *testing.T, result goodall.ToolResult) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(result.Text()), &v); err != nil {
		t.Fatalf("decoding the tool result %q: %v", result.Text(), err)
	}
	return v
}

func TestListInboxCarriesEveryRecordAndTheFilesTheListingSkipped(t *testing.T) {
	in := sampleInbox(t)
	in.Skipped = append(in.Skipped, mailbox.Skip{Path: "sample.mbox:9999", Err: errors.New("the header would not parse")})

	tool, err := listInboxTool(in)
	if err != nil {
		t.Fatalf("building list_inbox: %v", err)
	}
	outcome, _ := runTool(t, tool, `{}`)
	listing := decodeResult[inboxListing](t, outcome.Result)

	if len(listing.Messages) != sampleCount {
		t.Errorf("listed %d messages, want %d", len(listing.Messages), sampleCount)
	}
	if listing.SkippedFiles != 1 {
		t.Errorf("reported %d skipped files, want 1", listing.SkippedFiles)
	}
	var customer recordView
	for _, msg := range listing.Messages {
		if msg.ID == customerID {
			customer = msg
		}
	}
	if customer.ID == "" {
		t.Fatalf("the customer message is not in the listing")
	}
	if !strings.Contains(customer.Subject, "CSV") {
		t.Errorf("subject is %q", customer.Subject)
	}
	if !strings.Contains(customer.Snippet, "finance team") {
		t.Errorf("snippet is %q", customer.Snippet)
	}
	if outcome.Usage != (goodall.Usage{}) {
		t.Errorf("a deterministic tool declared usage: %+v", outcome.Usage)
	}
}

func TestTriageInboxAsksOneJudgmentPerMessageAndSumsWhatTheySpent(t *testing.T) {
	in := sampleInbox(t)
	st := newStore()
	j := newJudge(t)

	tool, err := triageInboxTool(j.client(), testJudgeModel, in, st)
	if err != nil {
		t.Fatalf("building triage_inbox: %v", err)
	}
	outcome, _ := runTool(t, tool, `{"message_ids":["all"]}`)
	report := decodeResult[triageReport](t, outcome.Result)

	if len(report.Judged) != sampleCount {
		t.Fatalf("judged %d messages, want %d", len(report.Judged), sampleCount)
	}
	if len(j.seen()) != sampleCount {
		t.Errorf("made %d judgment calls, want one per message (%d)", len(j.seen()), sampleCount)
	}
	for _, req := range j.seen() {
		if len(req.IDs) != 3 {
			t.Errorf("a triage call asked %d questions, want the three of them: %v", len(req.IDs), req.IDs)
		}
		if req.Model != testJudgeModel {
			t.Errorf("a triage call asked %q, want the pinned %q", req.Model, testJudgeModel)
		}
	}
	want := goodall.Usage{Input: judgeInputTokens * sampleCount, Output: judgeOutputTokens * sampleCount}
	if outcome.Usage != want {
		t.Errorf("declared usage %+v, want the sum over every call %+v", outcome.Usage, want)
	}
	if outcome.Cost.Reported {
		t.Error("the judge reports no money, so the cost must stay unreported")
	}

	byID := map[string]judgedMessage{}
	for _, judged := range report.Judged {
		byID[judged.ID] = judged
	}
	if got := byID[invoiceID].Department; got != departmentBilling {
		t.Errorf("the invoice went to %q, want %q", got, departmentBilling)
	}
	if !byID[customerID].NeedsReply {
		t.Error("the customer question was not marked as needing a reply")
	}
	if byID[phishingID].Urgent {
		t.Error("the phishing message's own countdown made it urgent; the question decides, not the message")
	}
	if verdict, ok := st.triageOf(customerID); !ok || !verdict.NeedsReply {
		t.Error("the triage was not recorded for the report")
	}
}

func TestTriageInboxJudgesOnlyTheMessagesItWasNamed(t *testing.T) {
	in := sampleInbox(t)
	j := newJudge(t)
	tool, err := triageInboxTool(j.client(), testJudgeModel, in, newStore())
	if err != nil {
		t.Fatalf("building triage_inbox: %v", err)
	}
	outcome, _ := runTool(t, tool, `{"message_ids":["`+customerID+`","`+invoiceID+`"]}`)
	report := decodeResult[triageReport](t, outcome.Result)
	if len(report.Judged) != 2 {
		t.Fatalf("judged %d messages, want 2", len(report.Judged))
	}
	if len(j.seen()) != 2 {
		t.Errorf("made %d judgment calls, want 2", len(j.seen()))
	}
}

func TestTriageInboxReportsOneMessageTheJudgeRefusedWithoutLosingTheRest(t *testing.T) {
	in := sampleInbox(t)
	j := newJudge(t)
	j.refuse = func(req judgeRequest) bool { return strings.Contains(req.State, "Orchard Coffee") }

	tool, err := triageInboxTool(j.client(), testJudgeModel, in, newStore())
	if err != nil {
		t.Fatalf("building triage_inbox: %v", err)
	}
	outcome, _ := runTool(t, tool, `{"message_ids":["all"]}`)
	report := decodeResult[triageReport](t, outcome.Result)

	if len(report.Judged) != sampleCount-1 {
		t.Errorf("judged %d messages, want %d", len(report.Judged), sampleCount-1)
	}
	if len(report.Failed) != 1 {
		t.Fatalf("reported %d failures, want 1: %+v", len(report.Failed), report.Failed)
	}
	if report.Failed[0].ID == "" || report.Failed[0].Error == "" {
		t.Errorf("a failure that names neither the message nor the reason: %+v", report.Failed[0])
	}
}

func TestTriageInboxRefusesAnIDItDoesNotKnow(t *testing.T) {
	in := sampleInbox(t)
	j := newJudge(t)
	tool, err := triageInboxTool(j.client(), testJudgeModel, in, newStore())
	if err != nil {
		t.Fatalf("building triage_inbox: %v", err)
	}
	outcome, _ := runTool(t, tool, `{"message_ids":["not-a-message"]}`)
	if !outcome.Result.IsError {
		t.Fatalf("an unknown id was accepted: %q", outcome.Result.Text())
	}
	if len(j.seen()) != 0 {
		t.Error("the judge was asked about a message that is not in the inbox")
	}
}

func TestCheckDraftCombinesTheInspectorsInCodeAndSaysWhatToDoNext(t *testing.T) {
	in := sampleInbox(t)
	st := newStore()
	j := newJudge(t)
	tool, err := checkDraftTool(j.client(), testJudgeModel, in, st)
	if err != nil {
		t.Fatalf("building check_draft: %v", err)
	}

	clean, _ := runTool(t, tool, `{"message_id":"`+customerID+`","draft":"A plain answer about the CSV export."}`)
	verdict := decodeResult[checkVerdict](t, clean.Result)
	if !verdict.Ready || len(verdict.Flagged) != 0 {
		t.Errorf("a clean draft was flagged: %+v", verdict)
	}
	if verdict.Attempt != 1 {
		t.Errorf("the first check of a message is attempt %d", verdict.Attempt)
	}
	if verdict.NextStep != nextStepReady {
		t.Errorf("next step is %q, want %q", verdict.NextStep, nextStepReady)
	}

	dirty, _ := runTool(t, tool, `{"message_id":"`+invoiceID+`","draft":"INVENTED figures and a PROMISED refund."}`)
	verdict = decodeResult[checkVerdict](t, dirty.Result)
	if verdict.Ready {
		t.Error("a draft that invents a fact was called ready")
	}
	if len(verdict.Flagged) != 2 {
		t.Fatalf("flagged %+v, want the two inspectors that fired", verdict.Flagged)
	}
	if verdict.Flagged[0].ID != string(qInventsAFact) || verdict.Flagged[1].ID != string(qUnaskedPromise) {
		t.Errorf("flagged the wrong inspectors: %+v", verdict.Flagged)
	}
	if verdict.NextStep != nextStepRedraft {
		t.Errorf("a first flagged draft's next step is %q, want %q", verdict.NextStep, nextStepRedraft)
	}
	if outcome := dirty.Usage; outcome.Input != judgeInputTokens {
		t.Errorf("declared usage %+v, want the one call it made", outcome)
	}

	again, _ := runTool(t, tool, `{"message_id":"`+invoiceID+`","draft":"Still INVENTED after a redraft."}`)
	verdict = decodeResult[checkVerdict](t, again.Result)
	if verdict.Attempt != 2 {
		t.Errorf("the second check of a message is attempt %d", verdict.Attempt)
	}
	if verdict.NextStep != nextStepEscalate {
		t.Errorf("a flagged redraft's next step is %q, want %q", verdict.NextStep, nextStepEscalate)
	}
	if got := st.checksOf(invoiceID); len(got) != 2 {
		t.Errorf("the store holds %d checks for the invoice, want 2", len(got))
	}
}

func TestCheckDraftRefusesAnIDItDoesNotKnow(t *testing.T) {
	j := newJudge(t)
	tool, err := checkDraftTool(j.client(), testJudgeModel, sampleInbox(t), newStore())
	if err != nil {
		t.Fatalf("building check_draft: %v", err)
	}
	outcome, _ := runTool(t, tool, `{"message_id":"nope","draft":"anything"}`)
	if !outcome.Result.IsError {
		t.Fatalf("an unknown id was accepted: %q", outcome.Result.Text())
	}
	if len(j.seen()) != 0 {
		t.Error("the judge was asked about a message that is not in the inbox")
	}
}

// TestTheJudgeIsShownRecordsRatherThanMessages is the privacy line the plan
// draws: the sample inbox carries a long body and an attachment, and neither
// may appear in a request to a third party.
func TestTheJudgeIsShownRecordsRatherThanMessages(t *testing.T) {
	in := sampleInbox(t)
	j := newJudge(t)
	triage, err := triageInboxTool(j.client(), testJudgeModel, in, newStore())
	if err != nil {
		t.Fatalf("building triage_inbox: %v", err)
	}
	runTool(t, triage, `{"message_ids":["all"]}`)
	check, err := checkDraftTool(j.client(), testJudgeModel, in, newStore())
	if err != nil {
		t.Fatalf("building check_draft: %v", err)
	}
	runTool(t, check, `{"message_id":"`+colleagueID+`","draft":"Castlebridge, then."}`)

	if len(j.seen()) == 0 {
		t.Fatal("no request reached the judge, so this test proves nothing")
	}
	for _, req := range j.seen() {
		for _, secret := range []string{bodyMarker, attachmentMarker, attachmentName} {
			if strings.Contains(req.Body, secret) {
				t.Errorf("a judge request carried %q:\n%s", secret, req.Body)
			}
		}
	}
}

// The markers planted in the sample inbox's longest message: one deep in the
// body, past the snippet limit, and one inside an attachment that is never
// decoded.
const (
	bodyMarker       = "ZENITHMARKER"
	attachmentMarker = "QVRUQUNITUVOVFBBWUxPQUQ"
	attachmentName   = "q3-print-quotes.pdf"
)

// testJudgeModel is the pinned judge version the tests assert travels with
// every call.
const testJudgeModel = "jev-1.13.0"
