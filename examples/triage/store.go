package main

import (
	"sync"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/examples/triage/mailbox"
)

// This file holds the run's two pieces of state: the inbox, read once before
// the agent starts and never written again, and the store the tools write
// their results into so the report can be built from what actually happened
// rather than from what the model said happened.

// inbox is what one run was given: the filtered records of the messages that
// read, and the files the listing passed over. It is the only view of the mail
// anything downstream gets — no tool, no model and no judge ever sees a whole
// message — and it is read-only once built.
type inbox struct {
	// Source names where the mail came from, for the report's first line.
	Source string
	// Records are the messages, newest first.
	Records []mailbox.Record
	// Skipped names the files the reader could not parse. A listing that
	// quietly lost four messages is worse than one that says so, so this
	// travels all the way to the report.
	Skipped []mailbox.Skip

	byID map[string]mailbox.Record
}

// newInbox builds the inbox from one listing, cutting each snippet to
// maxSnippet runes.
func newInbox(source string, listing mailbox.Listing, maxSnippet int) *inbox {
	in := &inbox{
		Source:  source,
		Skipped: listing.Skipped,
		byID:    make(map[string]mailbox.Record, len(listing.Messages)),
	}
	for _, msg := range listing.Messages {
		record := msg.Record(maxSnippet)
		in.Records = append(in.Records, record)
		in.byID[record.ID] = record
	}
	return in
}

// lookup is the record with this id, and whether the inbox holds one.
func (in *inbox) lookup(id string) (mailbox.Record, bool) {
	record, ok := in.byID[id]
	return record, ok
}

// ids is every message's id, newest first.
func (in *inbox) ids() []string {
	out := make([]string, 0, len(in.Records))
	for _, record := range in.Records {
		out = append(out, record.ID)
	}
	return out
}

// checkAttempt is one pass of a draft through the inspectors: what was
// checked, and what fired. Attempts are kept in order, so the second one for a
// message is by definition the redraft.
type checkAttempt struct {
	// Draft is the text that was checked.
	Draft string
	// Fired is the inspectors that flagged it, empty when it is clean.
	Fired []inspector
	// Probabilities is each fired inspector's own probability, for the
	// report: a draft that only just tripped a threshold reads differently
	// from one that tripped it hard.
	Probabilities map[questionID]float64
}

// spend is what one tool spent over a whole run: the calls it made and the
// tokens they cost. It is filled from each call's ToolCallEnd rather than by
// the tools themselves, so the judge's tokens and a delegate's arrive by one
// path and the report says who spent what.
type spend struct {
	// Calls is how many times the tool ran.
	Calls int
	// Usage is the sum of what those calls declared.
	Usage goodall.Usage
}

// store is what the run produced, in memory, for the report to read. It is
// written from tools that may run in parallel, so every method locks.
type store struct {
	mu       sync.Mutex
	triage   map[string]triageVerdict
	failures map[string]string
	checks   map[string][]checkAttempt
	// draftOrder is the message ids that have been checked, in the order
	// the run first checked them, so the report lists drafts in the order
	// they were written rather than in map order.
	draftOrder []string
	spend      map[string]spend
	judgments  int
	result     goodall.Result
	stopCause  goodall.StopCause
	stopReason string
	stopped    bool
}

// newStore is an empty store.
func newStore() *store {
	return &store{
		triage:   map[string]triageVerdict{},
		failures: map[string]string{},
		checks:   map[string][]checkAttempt{},
		spend:    map[string]spend{},
	}
}

// recordTriage keeps one message's verdict.
func (s *store) recordTriage(id string, verdict triageVerdict) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.triage[id] = verdict
	delete(s.failures, id)
}

// recordTriageFailure keeps why one message could not be judged, so the report
// can say "not judged" rather than leaving the row blank.
func (s *store) recordTriageFailure(id, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[id] = reason
}

// triageOf is one message's verdict, and whether it was judged.
func (s *store) triageOf(id string) (triageVerdict, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	verdict, ok := s.triage[id]
	return verdict, ok
}

// failureOf is why one message was not judged, and whether it failed.
func (s *store) failureOf(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reason, ok := s.failures[id]
	return reason, ok
}

// recordCheck appends one check of a message's draft and returns which attempt
// it was, counting from one. The attempt number is the cascade's own record:
// the first is the cheap model's draft, and a second is the strong model's
// redraft of it.
func (s *store) recordCheck(id string, attempt checkAttempt) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.checks[id]) == 0 {
		s.draftOrder = append(s.draftOrder, id)
	}
	s.checks[id] = append(s.checks[id], attempt)
	return len(s.checks[id])
}

// checksOf is every check of one message's drafts, in order.
func (s *store) checksOf(id string) []checkAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]checkAttempt, len(s.checks[id]))
	copy(out, s.checks[id])
	return out
}

// drafted is every message id that has a draft, in the order they were first
// checked, so the report lists drafts in the order the run wrote them.
func (s *store) drafted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.draftOrder...)
}

// observe records what the run's events say a call spent. Every reporting tool
// declares its own tokens on its ToolCallEnd — the judge's on triage_inbox and
// check_draft, a delegate's whole child run on draft_reply and redraft_reply —
// so watching the stream is one path to all of it, and it needs no accounting
// code inside the tools.
func (s *store) observe(ev goodall.Event) {
	end, ok := ev.(goodall.ToolCallEnd)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.spend[end.ToolUse.Name]
	entry.Calls++
	entry.Usage = entry.Usage.Add(end.Usage)
	s.spend[end.ToolUse.Name] = entry
}

// finish keeps the run's own result, whose usage is the parent model's tokens
// alone: what a delegated call spent is deliberately not rolled into it.
func (s *store) finish(result goodall.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result = result
}

// recordJudgments counts the questions sets sent to the judge. It is recorded
// by the tools rather than read off the stream because a call's ToolCallEnd
// carries what it spent and not how many judgments it made, and the difference
// between the two is the point of batching: one tool call can be fifty
// judgments.
func (s *store) recordJudgments(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.judgments += n
}

// judgmentCount is how many judgments the run asked for.
func (s *store) judgmentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.judgments
}

// recordStop keeps why a run ended early, so the report says so rather than
// presenting a half-finished triage as a finished one.
func (s *store) recordStop(cause goodall.StopCause, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopCause, s.stopReason, s.stopped = cause, message, true
}

// stop is why the run ended early, and whether it did.
func (s *store) stop() (goodall.StopCause, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopCause, s.stopReason, s.stopped
}

// parentUsage is what the orchestrating model itself spent. A delegated call's
// tokens are not in it: they are on their own calls, because tokens on
// different models are not the same unit.
func (s *store) parentUsage() goodall.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result.Usage
}

// spentOn is what one tool's calls cost over the run.
func (s *store) spentOn(tool string) spend {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spend[tool]
}
