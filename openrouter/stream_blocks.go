// The block state machine behind the stream: OpenRouter numbers no content
// blocks and marks no boundaries, so this file decides where one block ends
// and the next begins, and merges the reasoning_details fragments back into
// the entries the blocking path receives whole.

package openrouter

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"strings"

	"github.com/bensyverson/goodall"
)

// noEntryIndex is the key of a reasoning fragment that carries no index of its
// own: it belongs to whichever entry is open, and opens one if none is.
const noEntryIndex = -1

// flatEntryIndex is the key of the thinking block built from the flat
// `reasoning` string, which a server that sends no reasoning_details uses.
const flatEntryIndex = -2

// blockState tracks the content blocks a stream has opened. Block indexes are
// assigned from zero in order of first appearance, because the root
// Accumulator addresses blocks by index and OpenRouter supplies none.
//
// One thinking block and one text block are open at a time, and each tool call
// keeps its own block open until the message ends: a model emits its reasoning,
// then its text, then its calls, and a delta of a different kind closes what
// the previous kind had open.
type blockState struct {
	next    int
	text    *openText
	reason  *openReasoning
	tools   []*openTool
	toolKey map[int]*openTool
}

// openText is a text block being filled.
type openText struct{ index int }

// openReasoning is one reasoning_details entry being reassembled. The entry
// arrives as fragments — the text in pieces and the signature in a fragment of
// its own — so the raw bytes are merged member by member until the entry
// closes, which is what lets the finished block re-send verbatim (invariant 2).
type openReasoning struct {
	index int
	key   int
	kind  reasoningType
	raw   jsontext.Value
	// data is the encrypted payload, accumulated because a redacted
	// thinking block carries it on the block itself rather than in deltas.
	data strings.Builder
	// pending is set while the block's start has not been emitted, which
	// is the case for an encrypted entry: its payload is only complete at
	// the close, and there is nothing to display in the meantime.
	pending bool
}

// openTool is one tool call being assembled. Its block index is goodall's; the
// wire's own index is the map key that finds it.
type openTool struct{ index int }

// reasoningKey reads the members that say which entry a fragment belongs to.
// It is separate from reasoningDetail because a fragment is identified before
// it is understood.
type reasoningKey struct {
	Type  reasoningType `json:"type,omitzero"`
	Index *int          `json:"index,omitzero"`
}

// textFragment adds a text fragment, opening a text block if none is open. A
// text fragment closes every other open block: the model has moved on.
func (b *blockState) textFragment(fragment string) []goodall.Event {
	var events []goodall.Event
	if b.text == nil {
		events = append(events, b.closeAll()...)
		b.text = &openText{index: b.take()}
		events = append(events, goodall.BlockStart{Index: b.text.index, Block: goodall.Text{}})
	}
	return append(events, goodall.TextDelta{Index: b.text.index, Text: fragment})
}

// flatReasoning adds a fragment of the flat `reasoning` string, for a server
// that sends no reasoning_details. The block carries no raw entry, so it
// re-sends as a rebuilt reasoning.text rather than verbatim.
func (b *blockState) flatReasoning(fragment string) []goodall.Event {
	events := b.openReasoningIfNeeded(flatEntryIndex, reasoningText)
	return append(events, goodall.ThinkingDelta{Index: b.reason.index, Text: fragment})
}

// reasoningFragment folds one reasoning_details fragment into the entry it
// belongs to, opening a block when the entry is new.
func (b *blockState) reasoningFragment(entry jsontext.Value) []goodall.Event {
	if len(entry) == 0 {
		return nil
	}
	var key reasoningKey
	if err := json.Unmarshal(entry, &key); err != nil {
		// A fragment goodall cannot even key still belongs to the
		// thinking that is arriving; fold it into the open entry.
		key = reasoningKey{}
	}
	index := noEntryIndex
	if key.Index != nil {
		index = *key.Index
	}

	events := b.openReasoningIfNeeded(index, key.Type)
	open := b.reason
	open.raw = mergeReasoningEntry(open.raw, entry)

	var detail reasoningDetail
	if err := json.Unmarshal(entry, &detail); err != nil {
		return events
	}
	switch open.kind {
	case reasoningEncrypted:
		open.data.WriteString(detail.Data)
	default:
		if text := detail.Text + detail.Summary; text != "" {
			events = append(events, goodall.ThinkingDelta{Index: open.index, Text: text})
		}
	}
	if detail.Signature != "" {
		events = append(events, goodall.SignatureDelta{Index: open.index, Signature: detail.Signature})
	}
	return events
}

// openReasoningIfNeeded returns the events that make a reasoning block current
// for this entry: none when the open block is already it, and a close plus an
// open when the entry changed.
//
// A fragment with no index of its own folds into whatever entry is open, which
// is how a server that omits the member still produces one block per entry.
func (b *blockState) openReasoningIfNeeded(key int, kind reasoningType) []goodall.Event {
	if b.reason != nil && (key == noEntryIndex || b.reason.key == key) {
		return nil
	}
	events := b.closeAll()
	open := &openReasoning{index: b.take(), key: key, kind: kind}
	b.reason = open
	if kind == reasoningEncrypted {
		// The payload is only whole at the close, and a redacted block
		// has nothing to show meanwhile, so the block opens then.
		open.pending = true
		return events
	}
	return append(events, goodall.BlockStart{Index: open.index, Block: goodall.Thinking{}})
}

// toolFragment folds one tool-call fragment into the call its index names. It
// closes the text and thinking blocks — the model has stopped writing prose —
// but leaves the other calls open, because a turn's calls are siblings.
func (b *blockState) toolFragment(call toolCallDelta) []goodall.Event {
	var events []goodall.Event
	open, ok := b.toolKey[call.Index]
	if !ok {
		events = append(events, b.closeText()...)
		events = append(events, b.closeReasoning()...)
		open = &openTool{index: b.take()}
		if b.toolKey == nil {
			b.toolKey = make(map[int]*openTool)
		}
		b.toolKey[call.Index] = open
		b.tools = append(b.tools, open)
		events = append(events, goodall.BlockStart{
			Index: open.index,
			Block: goodall.ToolUse{ID: call.ID, Name: call.Function.Name},
		})
	}
	if call.Function.Arguments != "" {
		events = append(events, goodall.ToolInputDelta{Index: open.index, PartialJSON: call.Function.Arguments})
	}
	return events
}

// closeAll closes every open block, in index order, which is what a finish
// reason, the usage frame and [DONE] each do before anything else.
func (b *blockState) closeAll() []goodall.Event {
	type closer struct {
		index  int
		events []goodall.Event
	}
	var pending []closer
	// Each index is read before the close that clears the field: the order
	// in which a composite literal evaluates a field access and a call
	// beside it is not specified.
	if b.reason != nil {
		index := b.reason.index
		pending = append(pending, closer{index, b.closeReasoning()})
	}
	if b.text != nil {
		index := b.text.index
		pending = append(pending, closer{index, b.closeText()})
	}
	for _, tool := range b.tools {
		pending = append(pending, closer{tool.index, []goodall.Event{goodall.BlockStop{Index: tool.index}}})
	}
	b.tools, b.toolKey = nil, nil

	var events []goodall.Event
	for len(pending) > 0 {
		lowest := 0
		for i, p := range pending {
			if p.index < pending[lowest].index {
				lowest = i
			}
		}
		events = append(events, pending[lowest].events...)
		pending = append(pending[:lowest], pending[lowest+1:]...)
	}
	return events
}

// closeText closes the open text block, if there is one.
func (b *blockState) closeText() []goodall.Event {
	if b.text == nil {
		return nil
	}
	index := b.text.index
	b.text = nil
	return []goodall.Event{goodall.BlockStop{Index: index}}
}

// closeReasoning closes the open thinking block, handing the merged entry to
// BlockStop so the finished block carries the provider's own bytes. An
// encrypted entry opens here too, because only now is its payload whole.
func (b *blockState) closeReasoning() []goodall.Event {
	open := b.reason
	if open == nil {
		return nil
	}
	b.reason = nil
	var events []goodall.Event
	if open.pending {
		events = append(events, goodall.BlockStart{
			Index: open.index,
			Block: goodall.RedactedThinking{Data: open.data.String()},
		})
	}
	return append(events, goodall.BlockStop{Index: open.index, Raw: open.raw})
}

// take assigns the next block index.
func (b *blockState) take() int {
	index := b.next
	b.next++
	return index
}

// accumulatingMembers are the members of a reasoning_details entry that arrive
// in pieces. Every other member is a fact about the entry that each fragment
// repeats, so the later fragment simply replaces it.
var accumulatingMembers = map[string]bool{
	"text":      true,
	"summary":   true,
	"data":      true,
	"signature": true,
}

// mergeReasoningEntry folds a fragment into the entry being reassembled,
// keeping the members in the order they were first seen. Merging at the JSON
// level rather than through a struct is what preserves the members goodall
// does not model — id, format, and whatever OpenRouter adds next — which
// invariant 2 needs re-sent verbatim.
//
// A fragment that is not an object, or bytes that will not re-encode, leave
// the last fragment standing rather than dropping the entry.
func mergeReasoningEntry(dst, src jsontext.Value) jsontext.Value {
	if len(dst) == 0 {
		return jsontext.Value(src.Clone())
	}
	into, err := objectMembers(dst)
	if err != nil {
		return jsontext.Value(src.Clone())
	}
	from, err := objectMembers(src)
	if err != nil {
		return dst
	}
	for _, member := range from {
		at := -1
		for i := range into {
			if into[i].name == member.name {
				at = i
				break
			}
		}
		switch {
		case at < 0:
			into = append(into, member)
		case accumulatingMembers[member.name]:
			into[at].value = concatJSONStrings(into[at].value, member.value)
		default:
			into[at].value = member.value
		}
	}
	merged, err := encodeMembers(into)
	if err != nil {
		return dst
	}
	return merged
}

// jsonMember is one member of a JSON object, in the order it was written.
type jsonMember struct {
	name  string
	value jsontext.Value
}

// objectMembers reads a JSON object into its members, preserving order.
func objectMembers(v jsontext.Value) ([]jsonMember, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(v))
	if _, err := dec.ReadToken(); err != nil { // the opening brace
		return nil, err
	}
	var out []jsonMember
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		if tok.Kind() == '}' {
			return out, nil
		}
		// The token is only valid until the next decoder call, so the
		// name is copied out before the value is read.
		name := tok.String()
		value, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		out = append(out, jsonMember{name: name, value: jsontext.Value(value.Clone())})
	}
}

// encodeMembers writes members back as a compact JSON object.
func encodeMembers(members []jsonMember) (jsontext.Value, error) {
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return nil, err
	}
	for _, member := range members {
		if err := enc.WriteToken(jsontext.String(member.name)); err != nil {
			return nil, err
		}
		if err := enc.WriteValue(member.value); err != nil {
			return nil, err
		}
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return nil, err
	}
	out := jsontext.Value(bytes.TrimSpace(buf.Bytes()))
	if err := out.Compact(); err != nil {
		return nil, err
	}
	return out, nil
}

// concatJSONStrings joins two JSON string values. Anything that is not a pair
// of strings leaves the later value standing, since there is nothing sensible
// to concatenate.
func concatJSONStrings(a, b jsontext.Value) jsontext.Value {
	var left, right string
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return b
	}
	joined, err := json.Marshal(left + right)
	if err != nil {
		return b
	}
	return jsontext.Value(joined)
}
