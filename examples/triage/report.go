package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/bensyverson/goodall"
)

// The report is the product of a run: what the agent proposes, printed after
// the streamed transcript so the person has the conclusion in one place. It is
// built from what the tools recorded rather than from what the model said
// about them, so a draft's status is a fact about the run.

// How a draft came out. They are typed constants because the four statuses are
// the cascade's whole vocabulary — the cheap model wrote it, the judge passed
// it, the strong model repaired it, or nobody could settle it — and a report
// that spelled one of them differently in two places would read as two
// different things.
const (
	// statusReady is a draft no inspector flagged.
	statusReady = "ready"
	// statusRedrafted is a draft the strong model repaired and the
	// inspectors then passed.
	statusRedrafted = "ready after a redraft"
	// statusEscalated is a draft still flagged after its one redraft: the
	// person decides.
	statusEscalated = "escalated to you"
	// statusFlagged is a draft that was flagged and never redrafted, which
	// is the run ending before the cascade did.
	statusFlagged = "flagged, and not redrafted before the run ended"
)

// writeReport prints the whole report: what each message is, what was drafted
// for it, what the reader could not read, and what the run spent.
func writeReport(out io.Writer, o options, in *inbox, st *store) {
	fmt.Fprintf(out, "\n%s\n", strings.Repeat("=", 72))
	fmt.Fprintf(out, "Triage report: %s, %s, nothing moved or sent\n", in.Source, count(len(in.Records), "message"))
	if cause, message, stopped := st.stop(); stopped {
		fmt.Fprintf(out, "The run ended early (%s): %s\n", cause, message)
	}
	writeMessages(out, in, st)
	writeDrafts(out, in, st)
	writeSkipped(out, in)
	writeSpend(out, o, st)
}

// writeMessages prints one block per message: who it is from, what it is, and
// the triage with the judge's own probabilities beside each decision, so a
// close call reads as one.
func writeMessages(out io.Writer, in *inbox, st *store) {
	fmt.Fprintf(out, "\nMESSAGES\n")
	for _, record := range in.Records {
		fmt.Fprintf(out, "  %s\n", record.ID)
		fmt.Fprintf(out, "    from    %s\n", addressText(record.From.Name, record.From.Address))
		fmt.Fprintf(out, "    subject %s\n", record.Subject)
		verdict, judged := st.triageOf(record.ID)
		if !judged {
			reason, failed := st.failureOf(record.ID)
			if !failed {
				reason = "it was not among the messages this run triaged"
			}
			fmt.Fprintf(out, "    triage  not judged: %s\n", reason)
			continue
		}
		fmt.Fprintf(out, "    triage  %s%s · needs a reply %s %s · urgent %s %s\n",
			verdict.Department, unsureNote(verdict),
			yesNo(verdict.NeedsReply), probability(verdict.NeedsReplyProbability),
			yesNo(verdict.Urgent), probability(verdict.UrgentProbability))
	}
}

// unsureNote is how a close department call is shown: the probability, and the
// word for what it means, rather than a number the reader has to interpret.
func unsureNote(verdict triageVerdict) string {
	if verdict.Unsure {
		return fmt.Sprintf(" %s (a close call)", probability(verdict.DepartmentProbability))
	}
	return " " + probability(verdict.DepartmentProbability)
}

// writeDrafts prints each draft with what the inspectors made of it and what
// happens to it next.
func writeDrafts(out io.Writer, in *inbox, st *store) {
	drafted := st.drafted()
	fmt.Fprintf(out, "\nDRAFTS\n")
	if len(drafted) == 0 {
		fmt.Fprintf(out, "  none: no reply was drafted and checked in this run\n")
		return
	}
	for _, id := range drafted {
		checks := st.checksOf(id)
		last := checks[len(checks)-1]
		fmt.Fprintf(out, "  %s — %s\n", id, statusOf(checks))
		if record, ok := in.lookup(id); ok {
			fmt.Fprintf(out, "    replying to %s, %q\n", addressText(record.From.Name, record.From.Address), record.Subject)
		}
		if len(checks) > 1 {
			fmt.Fprintf(out, "    drafts      %d: the first by the cheaper model, then one rewrite by the stronger one\n", len(checks))
		}
		for _, ins := range last.Fired {
			fmt.Fprintf(out, "    flagged     %s %s\n", ins.Label, probability(last.Probabilities[ins.ID]))
		}
		for line := range strings.SplitSeq(strings.TrimSpace(last.Draft), "\n") {
			fmt.Fprintf(out, "    | %s\n", line)
		}
	}
}

// statusOf is what happened to one message's drafts, read from the checks
// themselves: a clean first check is ready, a clean second is a repair that
// worked, and a flagged last check after the cascade's one rewrite is the
// person's call.
func statusOf(checks []checkAttempt) string {
	last := checks[len(checks)-1]
	switch {
	case len(last.Fired) == 0 && len(checks) == 1:
		return statusReady
	case len(last.Fired) == 0:
		return statusRedrafted
	case len(checks) >= maxDraftAttempts:
		return statusEscalated
	}
	return statusFlagged
}

// writeSkipped prints the files the reader could not read. A listing that
// quietly lost four messages is worse than one that says so, which is why this
// section prints even when the agent never mentioned it.
func writeSkipped(out io.Writer, in *inbox) {
	if len(in.Skipped) == 0 {
		return
	}
	fmt.Fprintf(out, "\nSKIPPED FILES (%d not read, so they were not triaged)\n", len(in.Skipped))
	for _, skip := range in.Skipped {
		fmt.Fprintf(out, "  %s: %v\n", skip.Path, skip.Err)
	}
}

// writeSpend prints what the run cost, per role rather than as one number:
// tokens on different models are not the same thing, and the point of the
// cascade is which work went where.
func writeSpend(out io.Writer, o options, st *store) {
	judge := spend{}
	for _, tool := range []string{toolTriage, toolCheck} {
		entry := st.spentOn(tool)
		judge.Calls += entry.Calls
		judge.Usage = judge.Usage.Add(entry.Usage)
	}
	// The routing judgment is a hook rather than a tool call, so it
	// reaches no ToolCallEnd and is not in these figures. It is one small
	// call per run.
	fmt.Fprintf(out, "\nSPEND (the routing judgment is a hook rather than a call, so it is not counted here)\n")
	fmt.Fprintf(out, "  %-20s %s in %s, %s\n", "judge ("+o.JudgeModel+")",
		count(st.judgmentCount(), "judgment"), count(judge.Calls, "tool call"), tokens(judge.Usage))
	writeDelegateSpend(out, st, toolDraft, "drafts")
	writeDelegateSpend(out, st, toolRedraft, "redrafts")
	fmt.Fprintf(out, "  %-20s %s\n", "this agent", tokens(st.parentUsage()))
}

// writeDelegateSpend prints one delegate's line, which is what its child runs
// declared on each call rather than anything rolled into the parent's totals.
func writeDelegateSpend(out io.Writer, st *store, tool, what string) {
	entry := st.spentOn(tool)
	if entry.Calls == 0 {
		return
	}
	fmt.Fprintf(out, "  %-20s %s, %s\n", what, count(entry.Calls, "call"), tokens(entry.Usage))
}

// count writes a number with its noun, pluralized, because "1 calls" reads as
// a bug to the person holding the report.
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// tokens writes a usage figure, saying plainly when nothing was reported: a
// zero is "nobody said", never "free".
func tokens(u goodall.Usage) string {
	if u == (goodall.Usage{}) {
		return "no token counts reported"
	}
	return fmt.Sprintf("%d input tokens, %d output", u.TotalInput(), u.Output)
}

// probability writes a judge's figure the way a report should read it: two
// decimal places, because a third is precision the judgment does not have.
func probability(p float64) string {
	return fmt.Sprintf("%.2f", p)
}

// yesNo is a decided noul in the words the report uses everywhere.
func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
