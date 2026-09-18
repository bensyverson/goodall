package main

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/fake"
)

// The drafts the scripted delegates write. The markers are what the fake judge
// answers from: the invoice draft invents a figure, and the redraft of it
// still does, which is the path that ends with the person.
const (
	cleanDraft   = "Hello Priya, a CSV export is on the reports screen next to the PDF one. Tell me if it is not there for your plan and I will check."
	dirtyDraft   = "Hello, invoice 2214 belongs to purchase order INVENTED-4471 and we will credit you by Friday, PROMISED."
	redraftDraft = "Hello, invoice 2214 belongs to purchase order INVENTED-4471; we are checking which of the two it should be."
	finalAnswer  = "Twelve messages triaged. Two drafts written: one is ready and one needs you."
)

// scriptedRun is the whole cascade as three scripted providers: the parent
// lists, triages, drafts, checks, redrafts, checks again and answers; the
// cheap delegate writes two drafts; the strong one rewrites the flagged draft.
func scriptedRun(t *testing.T) (options, deps, *judge) {
	t.Helper()
	opts, err := parseOptions(nil, io.Discard)
	if err != nil {
		t.Fatalf("parsing the default flags: %v", err)
	}
	j := newJudge(t)

	parent := &fake.Provider{Script: []fake.Turn{
		fake.Answer(goodall.StopToolUse, fake.Use("t1", toolListInbox, `{}`)),
		fake.Answer(goodall.StopToolUse, fake.Use("t2", toolTriage, `{"message_ids":["all"]}`)),
		fake.Answer(goodall.StopToolUse, fake.Use("t3", toolDraft,
			`{"prompt":"Answer Priya Raman, who asked whether a ledger can be exported as CSV."}`)),
		fake.Answer(goodall.StopToolUse, fake.Use("t4", toolCheck,
			`{"message_id":"`+customerID+`","draft":`+quoted(cleanDraft)+`}`)),
		fake.Answer(goodall.StopToolUse, fake.Use("t5", toolDraft,
			`{"prompt":"Answer Brightlane Accounts, who asked which purchase order invoice 2214 belongs to."}`)),
		fake.Answer(goodall.StopToolUse, fake.Use("t6", toolCheck,
			`{"message_id":"`+invoiceID+`","draft":`+quoted(dirtyDraft)+`}`)),
		fake.Answer(goodall.StopToolUse, fake.Use("t7", toolRedraft,
			`{"prompt":"Rewrite the invoice draft: it states a fact the message does not contain and promises something nobody asked for."}`)),
		fake.Answer(goodall.StopToolUse, fake.Use("t8", toolCheck,
			`{"message_id":"`+invoiceID+`","draft":`+quoted(redraftDraft)+`}`)),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: finalAnswer}),
	}}
	cheap := &fake.Provider{Script: []fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: cleanDraft}).Using(goodall.Usage{Input: 300, Output: 40}),
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: dirtyDraft}).Using(goodall.Usage{Input: 310, Output: 45}),
	}}
	strong := &fake.Provider{Script: []fake.Turn{
		fake.Answer(goodall.StopEndTurn, goodall.Text{Text: redraftDraft}).Using(goodall.Usage{Input: 500, Output: 60}),
	}}

	return opts, deps{
		Parent:     parent,
		Strong:     strong,
		Cheap:      cheap,
		Models:     anthropicModels,
		Judge:      j.client(),
		JudgeModel: opts.JudgeModel,
	}, j
}

// quoted writes a Go string as a JSON string, for the scripted tool inputs.
func quoted(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func TestAWholeRunOverTheSampleInboxPrintsTheReport(t *testing.T) {
	opts, d, j := scriptedRun(t)
	var out bytes.Buffer
	if err := execute(t.Context(), opts, d, &out); err != nil {
		t.Fatalf("running the example: %v", err)
	}
	report := out.String()

	for _, want := range []string{
		"the bundled sample inbox",
		customerID,
		string(departmentBilling),
		string(departmentTechnical),
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
	// The newsletter's body asks to have everything marked urgent. The
	// questions decide, so it is neither urgent nor waiting on a reply.
	if strings.Count(report, "urgent yes") > 1 {
		t.Errorf("more than one message came out urgent, which is what the newsletter asked for:\n%s", report)
	}
	if !strings.Contains(report, statusReady) {
		t.Errorf("the clean draft is not reported as ready:\n%s", report)
	}
	if !strings.Contains(report, statusEscalated) {
		t.Errorf("the twice-flagged draft was not escalated to the person:\n%s", report)
	}
	if !strings.Contains(report, "it states a fact the message does not contain") {
		t.Errorf("the report does not say which inspector fired:\n%s", report)
	}
	if !strings.Contains(report, redraftDraft) {
		t.Errorf("the escalated draft's own text is missing:\n%s", report)
	}
	// Every judgment is one call: twelve triage calls, three checks and
	// the routing judgment.
	if got, want := j.asked(qDepartment), sampleCount; got != want {
		t.Errorf("%d triage calls, want %d", got, want)
	}
	if got := j.asked(qInventsAFact); got != 3 {
		t.Errorf("%d draft checks, want 3", got)
	}
	if got := j.asked(qIntent); got != 1 {
		t.Errorf("%d routing calls, want one for the person's turn", got)
	}
	// The spend section is built from what each call declared on its
	// ToolCallEnd, so it is the accounting end to end.
	wantJudged := (sampleCount + 3) * judgeInputTokens
	if !strings.Contains(report, strconv.Itoa(wantJudged)) {
		t.Errorf("the report does not carry the judge's %d input tokens:\n%s", wantJudged, report)
	}
}

func TestTheRunPrintsTheFilesTheListingSkipped(t *testing.T) {
	opts, d, _ := scriptedRun(t)
	opts.Source = sourceMbox
	opts.Path = "testdata/truncated.mbox"
	var out bytes.Buffer
	if err := execute(t.Context(), opts, d, &out); err != nil {
		t.Fatalf("running the example: %v", err)
	}
	if !strings.Contains(out.String(), "SKIPPED FILES") || !strings.Contains(out.String(), "malformed header") {
		t.Errorf("a listing that lost a message said nothing about it:\n%s", out.String())
	}
}

func TestTheDelegateRunsUnderItsCallAndDeclaresWhatItSpent(t *testing.T) {
	opts, d, _ := scriptedRun(t)
	in, err := opts.open(t.Context())
	if err != nil {
		t.Fatalf("opening the sample inbox: %v", err)
	}
	st := newStore()
	agent, err := newAgent(d, in, st)
	if err != nil {
		t.Fatalf("building the agent: %v", err)
	}

	var nested []goodall.ToolEvent
	ends := map[string]goodall.ToolCallEnd{}
	for ev, err := range agent.Run(t.Context(), goodall.Conversation{}, goodall.Text{Text: opts.Instruction}) {
		if err != nil {
			t.Fatalf("the run failed: %v", err)
		}
		switch e := ev.(type) {
		case goodall.ToolEvent:
			nested = append(nested, e)
		case goodall.ToolCallEnd:
			ends[e.ToolUse.Name] = e
		}
	}

	if len(nested) == 0 {
		t.Fatal("no delegate event arrived nested under its call")
	}
	var sawDraftText bool
	for _, ev := range nested {
		if ev.Name != toolDraft && ev.Name != toolRedraft {
			t.Errorf("an event nested under %q, want only the delegates", ev.Name)
		}
		if ev.ToolUseID == "" {
			t.Error("a nested event does not name the call it came from")
		}
		if done, ok := ev.Event.(goodall.Done); ok && done.Result.Response != nil {
			sawDraftText = true
		}
	}
	if !sawDraftText {
		t.Error("the delegate's own run never reached the parent's stream")
	}

	triage := ends[toolTriage]
	if want := (goodall.Usage{Input: judgeInputTokens * sampleCount, Output: judgeOutputTokens * sampleCount}); triage.Usage != want {
		t.Errorf("triage_inbox declared %+v, want the sum over its twelve calls %+v", triage.Usage, want)
	}
	draft := ends[toolDraft]
	if draft.Usage.Input != 310 {
		t.Errorf("draft_reply declared %+v, want the child run's own usage", draft.Usage)
	}
	redraft := ends[toolRedraft]
	if redraft.Usage.Input != 500 {
		t.Errorf("redraft_reply declared %+v, want the strong model's usage", redraft.Usage)
	}
}

func TestARunThatAsksForASummaryKeepsTheDraftingToolsAway(t *testing.T) {
	opts, d, _ := scriptedRun(t)
	opts.Instruction = "What is in my inbox this morning and what is waiting on me?"
	in, err := opts.open(t.Context())
	if err != nil {
		t.Fatalf("opening the sample inbox: %v", err)
	}
	agent, err := newAgent(d, in, newStore())
	if err != nil {
		t.Fatalf("building the agent: %v", err)
	}
	drain(t, agent, opts.Instruction)
	parent, ok := d.Parent.(*fake.Provider)
	if !ok {
		t.Fatalf("the parent provider is a %T", d.Parent)
	}
	requests := parent.Requests()
	if len(requests) == 0 {
		t.Fatal("nothing was sent")
	}
	var names []string
	for _, tool := range requests[0].Tools {
		names = append(names, tool.Name())
	}
	if len(names) != 2 || names[0] != toolListInbox || names[1] != toolTriage {
		t.Errorf("a summary turn carried the tools %v, want just the two that read", names)
	}
	if requests[0].Model != anthropicModels.Parent {
		t.Errorf("a summary turn went to %q, want %q", requests[0].Model, anthropicModels.Parent)
	}
}

func TestARunThatAsksForDraftsGoesToTheStrongModelWithEveryTool(t *testing.T) {
	opts, d, _ := scriptedRun(t)
	in, err := opts.open(t.Context())
	if err != nil {
		t.Fatalf("opening the sample inbox: %v", err)
	}
	agent, err := newAgent(d, in, newStore())
	if err != nil {
		t.Fatalf("building the agent: %v", err)
	}
	drain(t, agent, opts.Instruction)
	requests := d.Parent.(*fake.Provider).Requests()
	if len(requests) == 0 {
		t.Fatal("nothing was sent")
	}
	if requests[0].Model != anthropicModels.Strong {
		t.Errorf("a drafting turn went to %q, want %q", requests[0].Model, anthropicModels.Strong)
	}
	if len(requests[0].Tools) != 5 {
		t.Errorf("a drafting turn carried %d tools, want all five", len(requests[0].Tools))
	}
}

// drain runs an agent to its terminal event, which is what a test that only
// wants to see what was sent needs: the request is recorded when the turn is
// sent, not when the run's first event arrives.
func drain(t *testing.T, agent *goodall.Agent, instruction string) {
	t.Helper()
	for _, err := range agent.Run(t.Context(), goodall.Conversation{}, goodall.Text{Text: instruction}) {
		if err != nil {
			t.Fatalf("the run failed: %v", err)
		}
	}
}
