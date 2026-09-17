package main

import (
	"bytes"
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
	// nestedIndent starts every line of a delegated run, one level in, so
	// a delegate's work reads as work under the call that asked for it
	// rather than as part of the answer.
	nestedIndent = "    "
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
	// children is a printer per tool call that reports events of its own,
	// keyed by the call's id. A delegated run is a run: it opens blocks and
	// streams deltas, so it needs the same state this printer keeps, kept
	// separately and written one level in.
	children map[string]*printer
}

// newPrinter is a printer that writes to out, at the start of a line.
func newPrinter(out io.Writer) *printer {
	return &printer{
		out:         out,
		atLineStart: true,
		thinking:    make(map[int]bool),
		children:    make(map[string]*printer),
	}
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
	case goodall.ToolEvent:
		p.nested(e)
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

// nested renders one event a running tool reported, one level in, through a
// printer of its own: a delegated run streams text and calls tools exactly as
// the parent does, so it gets the same renderer rather than a second set of
// rules.
//
// The child writes through an indenting writer, so this printer never sees
// those bytes and cannot know whether a line was left half-written; it takes
// that back from the child afterwards, which is what lets its own next label
// start on a line of its own.
func (p *printer) nested(e goodall.ToolEvent) {
	if e.Event == nil {
		return
	}
	child, ok := p.children[e.ToolUseID]
	if !ok {
		p.newline()
		child = newPrinter(&indentWriter{out: p.out, prefix: nestedIndent, atLineStart: true})
		p.children[e.ToolUseID] = child
	}
	child.event(e.Event)
	p.atLineStart = child.atLineStart
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

// indentWriter prefixes every line it writes, which is how a delegated run's
// output is set one level in without the renderer above it knowing anything
// about indentation.
type indentWriter struct {
	// out is where the prefixed lines go.
	out io.Writer
	// prefix starts every line.
	prefix string
	// atLineStart is whether the next byte begins a line, so the prefix is
	// written once per line however the writes fall.
	atLineStart bool
}

// Write writes p to the underlying writer, starting each line with the prefix.
// It reports the bytes of p it consumed, never counting the prefixes, as
// io.Writer requires.
func (w *indentWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if w.atLineStart {
			if _, err := io.WriteString(w.out, w.prefix); err != nil {
				return written, err
			}
			w.atLineStart = false
		}
		line := p
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			line, p = p[:i+1], p[i+1:]
			w.atLineStart = true
		} else {
			p = nil
		}
		n, err := w.out.Write(line)
		written += n
		if err != nil {
			return written, err
		}
	}
	return written, nil
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
