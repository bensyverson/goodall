package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// printAll renders a list of events to a fresh printer and returns the
// transcript, which is what a person reading the terminal sees.
func printAll(events ...goodall.Event) string {
	out := &bytes.Buffer{}
	p := newPrinter(out)
	for _, ev := range events {
		p.event(ev)
	}
	return out.String()
}

// TestPrinterIndentsAToolsOwnEvents is the preview of the nested-event state: a
// delegated run's text and tool calls are rendered under the call that started
// them, one level in, so a reader can tell the delegate's work from the
// answer.
func TestPrinterIndentsAToolsOwnEvents(t *testing.T) {
	use := goodall.ToolUse{ID: "tu_1", Name: "research"}
	nested := func(ev goodall.Event) goodall.ToolEvent {
		return goodall.ToolEvent{ToolUseID: "tu_1", Name: "research", Event: ev}
	}
	got := printAll(
		goodall.TextDelta{Text: "asking the specialist"},
		goodall.ToolCallStart{ToolUse: use},
		nested(goodall.TextDelta{Text: "checking the sources"}),
		nested(goodall.ToolCallStart{ToolUse: goodall.ToolUse{ID: "tu_c1", Name: "search"}}),
		nested(goodall.ToolCallEnd{
			ToolUse: goodall.ToolUse{ID: "tu_c1", Name: "search"},
			Result:  goodall.TextResult("three papers"),
		}),
		goodall.ToolCallEnd{ToolUse: use, Result: goodall.TextResult("1960")},
	)
	t.Logf("transcript:\n%s", got)

	for _, want := range []string{
		nestedIndent + "checking the sources",
		nestedIndent + toolLabel + " search",
		nestedIndent + toolLabel + " search " + string(statusOK) + ": three papers",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the transcript does not contain %q; it was:\n%s", want, got)
		}
	}
	for line := range strings.SplitSeq(strings.TrimRight(got, "\n"), "\n") {
		if strings.Contains(line, "research") && strings.HasPrefix(line, nestedIndent) {
			t.Errorf("the parent's own line %q is indented as a child's", line)
		}
	}
	if !strings.Contains(got, toolLabel+" research "+string(statusOK)+": 1960") {
		t.Errorf("the parent's tool result is missing or indented; the transcript was:\n%s", got)
	}
}

// TestPrinterKeepsTheParentsLineStartAfterANestedLine is the mechanical half of
// the nesting: the child writes through a writer of its own, so the parent has
// to learn whether a line was left half-written before it prints its own.
func TestPrinterKeepsTheParentsLineStartAfterANestedLine(t *testing.T) {
	got := printAll(
		goodall.ToolEvent{ToolUseID: "tu_1", Name: "research", Event: goodall.TextDelta{Text: "half a line"}},
		goodall.ToolCallEnd{ToolUse: goodall.ToolUse{ID: "tu_1", Name: "research"}, Result: goodall.TextResult("done")},
	)
	t.Logf("transcript:\n%s", got)

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("the transcript is %d lines, want the child's and the parent's: %q", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], nestedIndent+"half a line") {
		t.Errorf("the first line is %q, want the child's indented text", lines[0])
	}
	if strings.HasPrefix(lines[1], nestedIndent) {
		t.Errorf("the second line is %q, want the parent's own line unindented", lines[1])
	}
}
