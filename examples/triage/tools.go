package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/examples/triage/mailbox"
	"github.com/bensyverson/goodall/typesafe"
)

// The tools the agent reads the inbox with. Two of them ask the judge; none of
// them writes, moves or sends anything, which is what the descriptions tell
// the model as well.
const (
	toolListInbox = "list_inbox"
	toolTriage    = "triage_inbox"
	toolCheck     = "check_draft"
)

// judgeConcurrency is how many messages are judged at once. One request per
// message is what keeps each judgment about one small state — addressing
// records by reference inside one big array is the indirection Jev degrades on
// — and a bounded pool is what keeps a thousand-message inbox from opening a
// thousand connections.
const judgeConcurrency = 8

// allMessages is what the model sends instead of naming every id, which is the
// habit the tool's description asks for: one call triages the whole inbox.
const allMessages = "all"

// recordView is one message as the model reads it: the filtered record and its
// id. The id is what every other tool takes, so the model names messages
// rather than quoting them back.
type recordView struct {
	// ID addresses this message in every other tool.
	ID string `json:"id"`
	// From is the sender, display name and address.
	From string `json:"from"`
	// Subject is the decoded subject.
	Subject string `json:"subject"`
	// Date is when the message arrived, RFC 3339.
	Date string `json:"date"`
	// Snippet is the start of the text body, cleaned and bounded.
	Snippet string `json:"snippet"`
}

// viewOf is the record as the model reads it.
func viewOf(record mailbox.Record) recordView {
	return recordView{
		ID:      record.ID,
		From:    addressText(record.From.Name, record.From.Address),
		Subject: record.Subject,
		Date:    record.Date.Format(time.RFC3339),
		Snippet: record.Snippet,
	}
}

// addressText writes a sender the way a mail client shows one.
func addressText(name, address string) string {
	if name == "" {
		return address
	}
	return name + " <" + address + ">"
}

// messageState is what the judge is shown about a message, and all it is ever
// shown: no id, because the judge is asked about one message at a time and an
// id is detail no question uses, and no body beyond the bounded snippet the
// record already cut.
type messageState struct {
	// From is the sender.
	From string `json:"from"`
	// Subject is the subject.
	Subject string `json:"subject"`
	// Body is the cleaned, bounded snippet.
	Body string `json:"body_snippet"`
}

// stateOf is the judge's view of one record.
func stateOf(record mailbox.Record) messageState {
	return messageState{
		From:    addressText(record.From.Name, record.From.Address),
		Subject: record.Subject,
		Body:    record.Snippet,
	}
}

// draftState is what the inspectors are shown: a draft and the message it
// answers, so "does this answer what was asked" is a question about two things
// the judge can see.
type draftState struct {
	// Message is the message being answered.
	Message messageState `json:"message"`
	// Draft is the proposed reply in full.
	Draft string `json:"draft"`
}

// inboxListing is what list_inbox returns.
type inboxListing struct {
	// Messages are the records, newest first.
	Messages []recordView `json:"messages"`
	// SkippedFiles is how many files the reader could not parse, so the
	// model knows the listing is short before it counts anything.
	SkippedFiles int `json:"skipped_files"`
}

// listInboxTool reads the inbox that was loaded before the run started. It
// involves no model and no judge: the mail is already in the process, and a
// tool that asked anybody for it would be spending money to learn what it
// knows.
func listInboxTool(in *inbox) (goodall.Tool, error) {
	return goodall.NewTool(toolListInbox,
		"List the messages in the inbox, newest first, as filtered records: id, sender, subject, date and a short snippet of the text. "+
			"This is the whole inbox as anything in this run can see it — no message body, no attachment — and the ids are what every other tool takes.",
		func(ctx context.Context, _ struct{}) (goodall.ToolResult, error) {
			listing := inboxListing{SkippedFiles: len(in.Skipped)}
			for _, record := range in.Records {
				listing.Messages = append(listing.Messages, viewOf(record))
			}
			return goodall.JSONResult(listing)
		})
}

// triageRequest is what the model sends triage_inbox.
type triageRequest struct {
	// MessageIDs names the messages to judge.
	MessageIDs []string `json:"message_ids" desc:"the ids of the messages to triage, or the single value \"all\" for the whole inbox. Prefer one call for everything: each message is judged in its own request and they run in parallel, so triaging fifty messages at once costs the same wall clock as triaging eight"`
}

// judgedMessage is one message's triage as the model reads it back.
type judgedMessage struct {
	// ID is the message this verdict is about.
	ID string `json:"id"`
	// triageVerdict is the judgment after the thresholds in questions.go.
	triageVerdict
}

// judgeFailure is one message the judge could not answer about. A failure is
// reported per message and not for the batch, because eleven judgments are
// worth having when the twelfth did not arrive.
type judgeFailure struct {
	// ID is the message that was not judged.
	ID string `json:"id"`
	// Error is what went wrong, in the API's own words where it had any.
	Error string `json:"error"`
}

// triageReport is what triage_inbox returns.
type triageReport struct {
	// Judged is one entry per message that was judged, in the order asked.
	Judged []judgedMessage `json:"judged"`
	// Failed is the messages the judge could not answer about.
	Failed []judgeFailure `json:"failed,omitzero"`
}

// triageInboxTool judges every named message: where it belongs, whether it is
// waiting on an answer, and whether it is waiting today.
//
// It is built by hand rather than with typesafe.Tool because the state is
// already in this process: the model names records and the tool builds the
// judge's state from the records it holds, so fifty messages are never echoed
// through the model's own output to reach the judge. One call per message, run
// in parallel, is the shape the judge answers best.
func triageInboxTool(client *typesafe.Client, model string, in *inbox, st *store) (goodall.Tool, error) {
	return goodall.NewReportingTool(toolTriage,
		"Sort messages: which part of the business each belongs to, whether it is waiting on a written answer, and whether it is waiting today. "+
			"A judgment model answers each message on its own record, so this is cheap enough to run over the whole inbox in one call, which is the habit to keep.",
		func(ctx context.Context, req triageRequest, _ func(goodall.Event)) (goodall.ToolOutcome, error) {
			return triageMessages(ctx, client, model, in, st, req.MessageIDs)
		})
}

// triageMessages judges the named messages in parallel and sums what the calls
// spent onto the one outcome the parent's ToolCallEnd carries.
func triageMessages(ctx context.Context, client *typesafe.Client, model string, in *inbox, st *store, named []string) (goodall.ToolOutcome, error) {
	ids, err := resolveIDs(in, named)
	if err != nil {
		return goodall.ToolOutcome{Result: goodall.ErrorResult(err.Error())}, nil
	}

	type outcome struct {
		verdict triageVerdict
		usage   goodall.Usage
		err     error
	}
	results := make([]outcome, len(ids))
	questions := triageQuestions()
	var wg sync.WaitGroup
	slots := make(chan struct{}, judgeConcurrency)
	for i, id := range ids {
		record, _ := in.lookup(id)
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			answers, err := client.Ask(ctx, stateOf(record), questions, typesafe.WithModel(model))
			if err != nil {
				results[i] = outcome{err: err}
				return
			}
			verdict, err := readTriage(answers)
			results[i] = outcome{verdict: verdict, usage: answers.Usage, err: err}
		})
	}
	wg.Wait()

	st.recordJudgments(len(ids))
	report := triageReport{}
	var total goodall.Usage
	for i, id := range ids {
		total = total.Add(results[i].usage)
		if err := results[i].err; err != nil {
			st.recordTriageFailure(id, err.Error())
			report.Failed = append(report.Failed, judgeFailure{ID: id, Error: err.Error()})
			continue
		}
		st.recordTriage(id, results[i].verdict)
		report.Judged = append(report.Judged, judgedMessage{ID: id, triageVerdict: results[i].verdict})
	}
	result, err := goodall.JSONResult(report)
	if err != nil {
		return goodall.ToolOutcome{}, err
	}
	// The cost stays unreported: TypeSafe publishes its prices on its
	// website rather than on the response, and a zero would read as free.
	return goodall.ToolOutcome{Result: result, Usage: total}, nil
}

// resolveIDs turns what the model named into message ids, refusing an id the
// inbox does not hold rather than judging a subset and saying nothing.
func resolveIDs(in *inbox, named []string) ([]string, error) {
	if len(named) == 0 || (len(named) == 1 && strings.EqualFold(named[0], allMessages)) {
		return in.ids(), nil
	}
	var unknown []string
	for _, id := range named {
		if _, ok := in.lookup(id); !ok {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("this inbox holds no message with the id %s; the ids are the ones %s returned, or send [%q] for all of them",
			strings.Join(quoteAll(unknown), ", "), toolListInbox, allMessages)
	}
	return named, nil
}

// quoteAll quotes each id so a message naming several of them reads cleanly.
func quoteAll(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, fmt.Sprintf("%q", id))
	}
	return out
}

// checkRequest is what the model sends check_draft.
type checkRequest struct {
	// MessageID is the message the draft answers.
	MessageID string `json:"message_id" desc:"the id of the message this draft answers, as list_inbox gave it"`
	// Draft is the proposed reply.
	Draft string `json:"draft" desc:"the draft reply in full, exactly as it would be sent, with no commentary around it"`
}

// The next step the code hands back with a verdict. The cascade is policy, so
// it is decided here and stated in the words the model acts on, rather than
// left for the model to infer from four probabilities.
const (
	nextStepReady    = "This draft is ready. Report it to the person as it stands."
	nextStepRedraft  = "Hand this to redraft_reply once, naming the problems above so the stronger model knows what to fix, then check the redraft."
	nextStepEscalate = "Leave this one to the person: report it as escalated, with the problems above and the draft as it stands."
)

// maxDraftAttempts is how many times a draft is written before the person sees
// it: the cheap model writes, the judge checks, the strong model gets one
// rewrite, and a draft still flagged after that is the person's call. The
// judge is the bouncer in front of the expensive model, and then in front of
// the person.
const maxDraftAttempts = 2

// flaggedInspector is one inspector that fired, as the model and the report
// read it.
type flaggedInspector struct {
	// ID is the inspector's question id.
	ID string `json:"id"`
	// Problem is what fired, in words.
	Problem string `json:"problem"`
	// Probability is the judge's own figure behind it.
	Probability float64 `json:"probability"`
}

// checkVerdict is what check_draft returns: what fired, whether the draft is
// ready, and what to do about it.
type checkVerdict struct {
	// MessageID is the message the draft answers.
	MessageID string `json:"message_id"`
	// Attempt is which draft of this message was checked, counting from
	// one.
	Attempt int `json:"attempt"`
	// Ready is whether nothing fired.
	Ready bool `json:"ready"`
	// Flagged is the inspectors that fired, in the order they are asked.
	Flagged []flaggedInspector `json:"flagged,omitzero"`
	// NextStep is what the cascade says happens now.
	NextStep string `json:"next_step"`
}

// checkDraftTool puts one draft past four inspectors — does it answer what was
// asked, does it state a fact the message does not contain, does it promise
// something nobody asked for, does its tone fit — and combines their answers
// in code.
//
// The four questions are deliberately small and specific: a judge asked "is
// this draft good" gives a number nobody can act on, while a named defect
// tells the redraft what to fix. The state is built here from the record this
// process already holds, so the judge sees the same filtered view triage saw
// plus the draft.
func checkDraftTool(client *typesafe.Client, model string, in *inbox, st *store) (goodall.Tool, error) {
	return goodall.NewReportingTool(toolCheck,
		"Check a draft reply before anyone sees it: four inspectors ask whether it answers what was asked, whether it states a fact the message does not contain, "+
			"whether it promises something nobody asked for, and whether its tone fits the sender's. The answer says which of them fired and what to do next.",
		func(ctx context.Context, req checkRequest, _ func(goodall.Event)) (goodall.ToolOutcome, error) {
			return checkDraft(ctx, client, model, in, st, req)
		})
}

// checkDraft asks the inspectors about one draft and applies the cascade.
func checkDraft(ctx context.Context, client *typesafe.Client, model string, in *inbox, st *store, req checkRequest) (goodall.ToolOutcome, error) {
	record, ok := in.lookup(req.MessageID)
	if !ok {
		return goodall.ToolOutcome{Result: goodall.ErrorResult(fmt.Sprintf(
			"this inbox holds no message with the id %q; the ids are the ones %s returned", req.MessageID, toolListInbox))}, nil
	}
	if strings.TrimSpace(req.Draft) == "" {
		return goodall.ToolOutcome{Result: goodall.ErrorResult(
			"there is no draft to check; send the reply text itself in \"draft\"")}, nil
	}

	st.recordJudgments(1)
	answers, err := client.Ask(ctx, draftState{Message: stateOf(record), Draft: req.Draft}, inspectorQuestions(), typesafe.WithModel(model))
	if err != nil {
		return goodall.ToolOutcome{}, err
	}
	fired, err := flagged(answers)
	if err != nil {
		return goodall.ToolOutcome{Usage: answers.Usage}, err
	}

	attempt := checkAttempt{Draft: req.Draft, Fired: fired, Probabilities: map[questionID]float64{}}
	verdict := checkVerdict{MessageID: req.MessageID, Ready: len(fired) == 0}
	for _, ins := range fired {
		answer, _ := answers.Noul(string(ins.ID))
		attempt.Probabilities[ins.ID] = answer.Noul
		verdict.Flagged = append(verdict.Flagged, flaggedInspector{ID: string(ins.ID), Problem: ins.Label, Probability: answer.Noul})
	}
	verdict.Attempt = st.recordCheck(req.MessageID, attempt)
	switch {
	case verdict.Ready:
		verdict.NextStep = nextStepReady
	case verdict.Attempt < maxDraftAttempts:
		verdict.NextStep = nextStepRedraft
	default:
		verdict.NextStep = nextStepEscalate
	}

	result, err := goodall.JSONResult(verdict)
	if err != nil {
		return goodall.ToolOutcome{}, err
	}
	return goodall.ToolOutcome{Result: result, Usage: answers.Usage}, nil
}
