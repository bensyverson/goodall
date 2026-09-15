package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/bensyverson/goodall"
)

// The marks the answer is rendered with. They are plain text on purpose: an
// example that reached for a terminal library would be teaching the library
// rather than the event stream, and a transcript that is piped to a file
// should still read.
const (
	// thinkingLabel opens a thinking block.
	thinkingLabel = "[thinking]"
	// thinkingPrefix starts every line of thinking, so the model's
	// reasoning cannot be mistaken for its answer however it wraps.
	thinkingPrefix = "  | "
	// toolLabel opens a tool line, both when the call starts and when it
	// ends.
	toolLabel = "[tool]"
	// usageLabel opens the one-line summary of what a turn cost.
	usageLabel = "[usage]"
	// stoppedLabel opens the line that says a run ended early.
	stoppedLabel = "[stopped]"
	// errorLabel opens the line that says the stream itself failed.
	errorLabel = "[error]"
	// cutShort is what the user is told when they stopped the answer. The
	// partial message is kept, so the sentence says so: the next turn
	// continues from it.
	cutShort = "the answer was cut short; what had arrived is kept in the thread"
)

// toolStatus is how a tool call ended. It is a typed constant rather than a
// bool because a status line that says "ok" or "error" is what the reader
// needs, and a bool named for one of them gets read as the other.
type toolStatus string

const (
	// statusOK is a result the model can use.
	statusOK toolStatus = "ok"
	// statusError is a result that carries a failure for the model to act
	// on.
	statusError toolStatus = "error"
)

// printer renders one run's events to a writer as they arrive. It exists
// because the interesting part of a streaming chat client is exactly this:
// which events are shown, and how a delta that arrives mid-line is kept from
// running into the next thing printed.
//
// A printer serves one run and is used from one goroutine.
type printer struct {
	out io.Writer
	// atLineStart is whether the last thing written ended a line, which is
	// what lets a labeled line interrupt a half-written answer cleanly.
	atLineStart bool
	// thinking is the indexes of the blocks that are thinking blocks, so a
	// delta is rendered by what its block is rather than by what arrived
	// last.
	thinking map[int]bool
}

// newPrinter is a printer that writes to out, at the start of a line.
func newPrinter(out io.Writer) *printer {
	return &printer{out: out, atLineStart: true, thinking: make(map[int]bool)}
}

// event renders one event of a run. Events it does not render — the turn and
// message boundaries a terminal reader has no use for — are dropped here
// rather than upstream, so the loop hands over everything the run produced.
func (p *printer) event(ev goodall.Event) {
	switch e := ev.(type) {
	case goodall.BlockStart:
		if _, ok := e.Block.(goodall.Thinking); ok {
			p.thinking[e.Index] = true
			p.line("%s", thinkingLabel)
		}
	case goodall.ThinkingDelta:
		p.prefixed(e.Text, thinkingPrefix)
	case goodall.TextDelta:
		p.write(e.Text)
	case goodall.BlockStop:
		if p.thinking[e.Index] {
			delete(p.thinking, e.Index)
			p.newline()
		}
	case goodall.ToolCallStart:
		p.line("%s %s %s", toolLabel, e.ToolUse.Name, string(e.ToolUse.Input))
	case goodall.ToolCallEnd:
		status := statusOK
		if e.Result.IsError {
			status = statusError
		}
		p.line("%s %s %s: %s", toolLabel, e.ToolUse.Name, status, oneLine(e.Result.Text()))
	case goodall.TurnEnd:
		p.usage(e.Turn, e.Response.Usage, e.Response.Cost)
	case goodall.Done:
		p.newline()
	case goodall.Stopped:
		p.newline()
		if e.Cause == goodall.StopCauseCanceled {
			p.line("%s %s", stoppedLabel, cutShort)
			return
		}
		p.line("%s %s: %s", stoppedLabel, e.Cause, e.Message)
	}
}

// fail reports a stream that ended in an error, which for a subscription
// means the reader fell too far behind the run.
func (p *printer) fail(err error) {
	p.line("%s %v", errorLabel, err)
}

// usage is the one-line summary of what a turn cost. The cost is shown only
// when the provider reported one: a zero that is not a figure must not read
// as "free".
func (p *printer) usage(turn int, u goodall.Usage, c goodall.Cost) {
	line := fmt.Sprintf("%s turn %d: %d in, %d out", usageLabel, turn, u.TotalInput(), u.Output)
	if u.Reasoning > 0 {
		line += fmt.Sprintf(" (%d thinking)", u.Reasoning)
	}
	if c.Reported {
		line += fmt.Sprintf(", %s %s", c.Amount, c.Currency)
	}
	p.line("%s", line)
}

// line writes a whole line, starting a new one first if the answer was left
// mid-line.
func (p *printer) line(format string, args ...any) {
	p.newline()
	fmt.Fprintf(p.out, format+"\n", args...)
	p.atLineStart = true
}

// newline ends the current line unless one has just ended.
func (p *printer) newline() {
	if p.atLineStart {
		return
	}
	fmt.Fprintln(p.out)
	p.atLineStart = true
}

// write puts text out verbatim, remembering whether it ended a line.
func (p *printer) write(text string) {
	if text == "" {
		return
	}
	fmt.Fprint(p.out, text)
	p.atLineStart = strings.HasSuffix(text, "\n")
}

// prefixed writes text with prefix at the start of every line, including a
// line the previous delta left half-written. Thinking arrives in fragments
// that break anywhere, so the prefix is applied by the writer rather than by
// the sender.
func (p *printer) prefixed(text, prefix string) {
	for text != "" {
		if p.atLineStart {
			fmt.Fprint(p.out, prefix)
			p.atLineStart = false
		}
		before, after, found := strings.Cut(text, "\n")
		fmt.Fprint(p.out, before)
		if !found {
			return
		}
		fmt.Fprintln(p.out)
		p.atLineStart = true
		text = after
	}
}

// oneLine folds a tool result onto the status line, so a long result does not
// bury the conversation. The whole result is in the thread either way.
func oneLine(text string) string {
	const limit = 120
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}
