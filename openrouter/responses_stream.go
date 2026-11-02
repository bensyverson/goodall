// A /responses stream as neutral events. Each frame's data says its own type, so
// the event: line is not read, and the stream has no [DONE]: its terminal events
// are response.completed and response.incomplete.

package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"slices"
	"strings"

	"github.com/bensyverson/goodall"
)

// The stream's event types goodall acts on.
const (
	responsesEventCreated      = "response.created"
	responsesEventItemAdded    = "response.output_item.added"
	responsesEventItemDone     = "response.output_item.done"
	responsesEventSummaryDelta = "response.reasoning_summary_text.delta"
	responsesEventTextDelta    = "response.output_text.delta"
	responsesEventRefusalDelta = "response.refusal.delta"
	responsesEventArgsDelta    = "response.function_call_arguments.delta"
	responsesEventCompleted    = "response.completed"
	responsesEventIncomplete   = "response.incomplete"
	responsesEventFailed       = "response.failed"
	responsesEventError        = "error"
)

// responsesRedundantEvents repeat what the deltas and items above already said, so
// they are read and set aside. Any type not named in this file is surfaced.
var responsesRedundantEvents = []string{
	"response.in_progress",
	"response.content_part.added",
	"response.content_part.done",
	"response.output_text.done",
	"response.refusal.done",
	"response.function_call_arguments.done",
	"response.reasoning_summary_part.added",
	"response.reasoning_summary_part.done",
	"response.reasoning_summary_text.done",
}

// responsesEvent is the part of one frame goodall reads: each event type fills the
// members of its own. Code and Message are where the documentation puts an error
// event's; Error is where every other error here puts them.
type responsesEvent struct {
	Type         string         `json:"type,omitzero"`
	Response     *responsesBody `json:"response,omitzero"`
	Item         jsontext.Value `json:"item,omitzero"`
	OutputIndex  int            `json:"output_index,omitzero"`
	ContentIndex int            `json:"content_index,omitzero"`
	SummaryIndex int            `json:"summary_index,omitzero"`
	Delta        string         `json:"delta,omitzero"`
	Code         jsontext.Value `json:"code,omitzero"`
	Message      string         `json:"message,omitzero"`
	Error        *wireError     `json:"error,omitzero"`
}

// responsesOpenItem is an output item whose blocks are not all closed. A message
// opens one text block per part with something in it, so an empty part makes no
// block, as on the blocking path; every other item is one block from its start.
type responsesOpenItem struct {
	kind  responsesItemType
	block int
	// parts maps a message's content index to its text block.
	parts map[int]int
	// summary and wrote say which summary part is arriving and whether any text
	// has, which is what decides when a join is due.
	summary int
	wrote   bool
	// argued is whether a call has had an arguments delta.
	argued bool
}

// responsesNeutralizer turns this wire's frames into goodall events. Blocks are
// addressed by output_index, because parallel calls may interleave.
//
// It is not safe for concurrent use; one lives inside one stream.
type responsesNeutralizer struct {
	header  http.Header
	started bool
	next    int
	open    map[int]*responsesOpenItem
	refused bool
	called  bool
}

// frame turns one SSE data payload into events. It reports the error that ends the
// stream, and whether this frame was terminal.
func (n *responsesNeutralizer) frame(data string) ([]goodall.Event, *goodall.APIError, bool) {
	payload := strings.TrimSpace(data)
	if payload == "" {
		return nil, nil, false
	}
	raw := []byte(payload)
	var ev responsesEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return []goodall.Event{unknownEvent(eventMalformedChunk, raw)}, nil, false
	}
	switch ev.Type {
	case responsesEventCompleted, responsesEventIncomplete:
		return n.terminal(&ev), nil, true
	case responsesEventFailed:
		var we *wireError
		if ev.Response != nil {
			we = ev.Response.Error
		}
		return nil, responsesFailure(we, n.header, raw), true
	case responsesEventError:
		we := ev.Error
		if !we.said() {
			we = &wireError{Code: ev.Code, Message: ev.Message}
		}
		return nil, responsesFailure(we, n.header, raw), true
	}

	events := n.start(ev.Response)
	switch ev.Type {
	case responsesEventCreated:
		return events, nil, false
	case responsesEventItemAdded:
		var item responsesOutputItem
		if json.Unmarshal(ev.Item, &item) != nil {
			return append(events, unknownEvent(goodall.EventType(ev.Type), raw)), nil, false
		}
		_, opened := n.item(ev.OutputIndex, item.Type, &item)
		return append(events, opened...), nil, false
	case responsesEventItemDone:
		return append(events, n.itemDone(&ev)...), nil, false
	case responsesEventSummaryDelta:
		return append(events, n.summaryDelta(&ev, raw)...), nil, false
	case responsesEventTextDelta:
		return append(events, n.textDelta(&ev, raw)...), nil, false
	case responsesEventRefusalDelta:
		n.refused = true
		return append(events, n.textDelta(&ev, raw)...), nil, false
	case responsesEventArgsDelta:
		return append(events, n.argsDelta(&ev, raw)...), nil, false
	}
	if slices.Contains(responsesRedundantEvents, ev.Type) {
		return events, nil, false
	}
	return append(events, unknownEvent(goodall.EventType(ev.Type), raw)), nil, false
}

// start opens the message on the first event that can. response.created carries
// the id and the model, and a stream that skipped it still gets a start, because
// the Accumulator's order is strict.
func (n *responsesNeutralizer) start(body *responsesBody) []goodall.Event {
	if n.started {
		return nil
	}
	n.started = true
	start := goodall.MessageStart{}
	if body != nil {
		start.ID, start.Model = body.ID, body.Model
	}
	return []goodall.Event{start}
}

// item finds the item at an output index, opening it when this is the first anyone
// has heard of it. A message opens no block yet: its parts do, as they fill.
//
// The reasoning item on an added event is never kept. Its encrypted_content is not
// the one that replays: replay_added read as lost reasoning with no error.
func (n *responsesNeutralizer) item(index int, kind responsesItemType, item *responsesOutputItem) (*responsesOpenItem, []goodall.Event) {
	if open, ok := n.open[index]; ok {
		return open, nil
	}
	if n.open == nil {
		n.open = make(map[int]*responsesOpenItem)
	}
	open := &responsesOpenItem{kind: kind}
	n.open[index] = open
	var block goodall.Block
	switch kind {
	case itemMessage:
		open.parts = make(map[int]int)
		return open, nil
	case itemReasoning:
		block = goodall.Thinking{}
	case itemFunctionCall:
		n.called = true
		block = goodall.ToolUse{ID: item.CallID, Name: item.Name}
	default:
		block = goodall.Unknown{Type: goodall.BlockType(kind)}
	}
	open.block = n.take()
	return open, []goodall.Event{goodall.BlockStart{Index: open.block, Block: block}}
}

// take assigns the next block index.
func (n *responsesNeutralizer) take() int {
	index := n.next
	n.next++
	return index
}

// summaryDelta adds thinking text, with a paragraph break where a new summary part
// follows one that said something, as on the blocking path.
func (n *responsesNeutralizer) summaryDelta(ev *responsesEvent, raw []byte) []goodall.Event {
	open, events := n.item(ev.OutputIndex, itemReasoning, nil)
	if open.kind != itemReasoning {
		return append(events, unknownEvent(goodall.EventType(ev.Type), raw))
	}
	if ev.Delta == "" {
		return events
	}
	if open.wrote && ev.SummaryIndex != open.summary {
		events = append(events, goodall.ThinkingDelta{Index: open.block, Text: summarySeparator})
	}
	open.summary, open.wrote = ev.SummaryIndex, true
	return append(events, goodall.ThinkingDelta{Index: open.block, Text: ev.Delta})
}

// textDelta adds text to a message part, opening the part's block on its first
// text. A refusal part reads the same way, since a refusal is an answer.
func (n *responsesNeutralizer) textDelta(ev *responsesEvent, raw []byte) []goodall.Event {
	open, events := n.item(ev.OutputIndex, itemMessage, nil)
	if open.kind != itemMessage {
		return append(events, unknownEvent(goodall.EventType(ev.Type), raw))
	}
	if ev.Delta == "" {
		return events
	}
	block, ok := open.parts[ev.ContentIndex]
	if !ok {
		block = n.take()
		open.parts[ev.ContentIndex] = block
		events = append(events, goodall.BlockStart{Index: block, Block: goodall.Text{}})
	}
	return append(events, goodall.TextDelta{Index: block, Text: ev.Delta})
}

// argsDelta adds a fragment of a call's arguments. A delta for a call nobody
// announced has no id or name to run under, so it is surfaced rather than opened:
// the done item that follows carries all three.
func (n *responsesNeutralizer) argsDelta(ev *responsesEvent, raw []byte) []goodall.Event {
	open, ok := n.open[ev.OutputIndex]
	if !ok || open.kind != itemFunctionCall {
		return []goodall.Event{unknownEvent(goodall.EventType(ev.Type), raw)}
	}
	if ev.Delta == "" {
		return nil
	}
	open.argued = true
	return []goodall.Event{goodall.ToolInputDelta{Index: open.block, PartialJSON: ev.Delta}}
}

// itemDone closes an item's blocks. Raw comes from here and not from the added
// event: the two carry different encrypted_content, and only this one replays.
func (n *responsesNeutralizer) itemDone(ev *responsesEvent) []goodall.Event {
	var item responsesOutputItem
	if err := json.Unmarshal(ev.Item, &item); err != nil {
		return []goodall.Event{unknownEvent(responsesEventItemDone, ev.Item)}
	}
	open, events := n.item(ev.OutputIndex, item.Type, &item)
	events = append(events, n.backfill(open, &item)...)
	events = append(events, open.close(compactItem(ev.Item))...)
	delete(n.open, ev.OutputIndex)
	return events
}

// backfill supplies what a finished item holds and its deltas never said, so Collect
// reads what Complete reads: a message part's text in content order, a refusal, a
// summary, and a call's arguments, without which a tool would run on an empty input.
// A part that had deltas is not repeated, even if the finished text differs.
func (n *responsesNeutralizer) backfill(open *responsesOpenItem, item *responsesOutputItem) []goodall.Event {
	if item.Type != open.kind {
		return nil
	}
	switch open.kind {
	case itemMessage:
		var events []goodall.Event
		for i, part := range item.Content {
			text := part.Text
			if part.Type == partRefusal {
				n.refused = true
				text = part.Refusal
			}
			if _, had := open.parts[i]; had || text == "" {
				continue
			}
			block := n.take()
			open.parts[i] = block
			events = append(events,
				goodall.BlockStart{Index: block, Block: goodall.Text{}},
				goodall.TextDelta{Index: block, Text: text})
		}
		return events
	case itemReasoning:
		if text := summaryText(item.Summary); !open.wrote && text != "" {
			open.wrote = true
			return []goodall.Event{goodall.ThinkingDelta{Index: open.block, Text: text}}
		}
	case itemFunctionCall:
		if !open.argued && item.Arguments != "" {
			open.argued = true
			return []goodall.Event{goodall.ToolInputDelta{Index: open.block, PartialJSON: item.Arguments}}
		}
	}
	return nil
}

// close stops an item's blocks, handing raw to the blocks that keep it.
func (o *responsesOpenItem) close(raw jsontext.Value) []goodall.Event {
	switch o.kind {
	case itemMessage:
		blocks := make([]int, 0, len(o.parts))
		for _, block := range o.parts {
			blocks = append(blocks, block)
		}
		slices.Sort(blocks)
		events := make([]goodall.Event, 0, len(blocks))
		for _, block := range blocks {
			events = append(events, goodall.BlockStop{Index: block})
		}
		return events
	case itemFunctionCall:
		return []goodall.Event{goodall.BlockStop{Index: o.block}}
	default:
		return []goodall.Event{goodall.BlockStop{Index: o.block, Raw: raw}}
	}
}

// terminal ends the message: anything still open closes, the stop reason and usage
// come from the same code the blocking path uses, and the message stops.
//
// An item with no done event takes its content and bytes from the terminal response,
// whose reasoning items the two_turn recordings replayed, so a cut-off item loses
// nothing.
func (n *responsesNeutralizer) terminal(ev *responsesEvent) []goodall.Event {
	body := ev.Response
	if body == nil {
		body = &responsesBody{Status: strings.TrimPrefix(ev.Type, "response.")}
	}
	events := n.start(body)
	indexes := make([]int, 0, len(n.open))
	for index := range n.open {
		indexes = append(indexes, index)
	}
	slices.Sort(indexes)
	for _, index := range indexes {
		var raw jsontext.Value
		if index < len(body.Output) {
			raw = compactItem(body.Output[index])
			var item responsesOutputItem
			if json.Unmarshal(raw, &item) == nil {
				events = append(events, n.backfill(n.open[index], &item)...)
			}
		}
		events = append(events, n.open[index].close(raw)...)
	}
	n.open = nil

	stop, native := responsesStop(body, n.refused, n.called)
	return append(events, goodall.MessageDelta{
		StopReason:       stop,
		NativeStopReason: native,
		Usage:            responsesUsageOf(body.Usage),
	}, goodall.MessageStop{})
}
