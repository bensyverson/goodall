package goodall

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"strings"
)

// accBlock is one content block being assembled. It keeps the block as the
// provider opened it, so the id and name of a tool call, the payload of a
// redacted thinking block and the bytes of an unrecognised one survive
// without a second representation of each.
type accBlock struct {
	start    Block // the block as BlockStart gave it
	text     strings.Builder
	sig      strings.Builder
	input    []byte // tool-call input fragments, concatenated
	finished Block  // set at BlockStop, nil while the block is open
}

// Accumulator folds a stream of events into the message the model produced.
// The zero value is ready to use, and one accumulator serves one message, so
// the streaming and blocking paths share the whole of this logic.
//
// It is deliberately strict: a stream it cannot fold is a *ProtocolError
// rather than a plausible-looking wrong message, because the loop acts on what
// comes out of here and a truncated tool call must never run.
//
// An Accumulator is not safe for concurrent use; a stream has one consumer.
type Accumulator struct {
	started      bool
	done         bool
	id           string
	model        string
	stopReason   StopReason
	stopSequence string
	nativeStop   string
	usage        Usage
	cost         Cost

	blocks       []*accBlock       // in the order their BlockStart arrived
	open         map[int]*accBlock // the blocks not yet stopped, by index
	justFinished Block             // the block the most recent Apply closed
	hasFinished  bool
}

// Apply folds one event into the accumulator. Events the agent loop emits are
// ignored, because a run stream carries both kinds and the message is built
// only from the provider's.
//
// Every Apply clears the JustFinished edge before doing anything else, so the
// edge belongs to exactly one call.
func (a *Accumulator) Apply(ev Event) error {
	a.justFinished, a.hasFinished = nil, false
	if ev == nil {
		return &ProtocolError{Reason: "a nil event"}
	}
	if ev.Type().FromLoop() {
		return nil
	}
	if a.done {
		return &ProtocolError{
			Event:  string(ev.Type()),
			Reason: "the event arrived after message_stop closed the message",
		}
	}

	switch e := ev.(type) {
	case MessageStart:
		if a.started {
			return &ProtocolError{
				Event:  string(EventMessageStart),
				Reason: "a second message_start on one message",
			}
		}
		a.started = true
		a.id, a.model = e.ID, e.Model
		a.usage = laterUsage(a.usage, e.Usage)

	case BlockStart:
		if _, ok := a.open[e.Index]; ok {
			return &ProtocolError{
				Event:  string(EventBlockStart),
				Index:  e.Index,
				Reason: "a second block_start on an index that is still open",
			}
		}
		if a.open == nil {
			a.open = make(map[int]*accBlock)
		}
		b := &accBlock{start: e.Block}
		a.open[e.Index] = b
		a.blocks = append(a.blocks, b)

	case TextDelta:
		b, err := a.openAt(EventTextDelta, e.Index)
		if err != nil {
			return err
		}
		if _, ok := b.start.(Text); !ok {
			return mismatch(EventTextDelta, e.Index, b)
		}
		b.text.WriteString(e.Text)

	case ThinkingDelta:
		b, err := a.openAt(EventThinkingDelta, e.Index)
		if err != nil {
			return err
		}
		if _, ok := b.start.(Thinking); !ok {
			return mismatch(EventThinkingDelta, e.Index, b)
		}
		b.text.WriteString(e.Text)

	case SignatureDelta:
		b, err := a.openAt(EventSignatureDelta, e.Index)
		if err != nil {
			return err
		}
		if _, ok := b.start.(Thinking); !ok {
			return mismatch(EventSignatureDelta, e.Index, b)
		}
		b.sig.WriteString(e.Signature)

	case ToolInputDelta:
		b, err := a.openAt(EventToolInputDelta, e.Index)
		if err != nil {
			return err
		}
		if _, ok := b.start.(ToolUse); !ok {
			return mismatch(EventToolInputDelta, e.Index, b)
		}
		b.input = append(b.input, e.PartialJSON...)

	case BlockStop:
		b, err := a.openAt(EventBlockStop, e.Index)
		if err != nil {
			return err
		}
		blk, err := b.finish(e.Index, e.Raw)
		if err != nil {
			return err
		}
		b.finished = blk
		delete(a.open, e.Index)
		a.justFinished, a.hasFinished = blk, true

	case MessageDelta:
		if e.StopReason != StopNone {
			a.stopReason = e.StopReason
		}
		if e.StopSequence != "" {
			a.stopSequence = e.StopSequence
		}
		if e.NativeStopReason != "" {
			a.nativeStop = e.NativeStopReason
		}
		a.usage = laterUsage(a.usage, e.Usage)
		if e.Cost != (Cost{}) {
			a.cost = e.Cost
		}

	case MessageStop:
		a.done = true

	case UnknownEvent:
		// Surfaced to the consumer by the stream, but it says nothing
		// about the message, so there is nothing to fold in.
	}
	return nil
}

// openAt is the block open at index, or the error for an event addressing an
// index nobody opened — which is also what a delta arriving after its block
// closed produces, since the block is no longer open.
func (a *Accumulator) openAt(t EventType, index int) (*accBlock, error) {
	b, ok := a.open[index]
	if !ok {
		return nil, &ProtocolError{
			Event:  string(t),
			Index:  index,
			Reason: "no block is open at this index",
		}
	}
	return b, nil
}

// mismatch is the error for a delta of the wrong kind for the open block, such
// as text arriving into a tool call.
func mismatch(t EventType, index int, b *accBlock) error {
	return &ProtocolError{
		Event:  string(t),
		Index:  index,
		Reason: fmt.Sprintf("the block open at this index is a %T, which takes no %s", b.start, t),
	}
}

// finish builds the completed block. Raw is the provider's own finished block,
// which only the two thinking kinds and an unrecognised block can carry; a
// text block has nowhere to put it, and rebuilding one from its text is exact.
func (b *accBlock) finish(index int, raw jsontext.Value) (Block, error) {
	switch v := b.start.(type) {
	case Text:
		v.Text = b.text.String()
		return v, nil
	case Thinking:
		v.Text = b.text.String()
		v.Signature = b.sig.String()
		if len(raw) > 0 {
			v.Raw = raw
		}
		return v, nil
	case RedactedThinking:
		if len(raw) > 0 {
			v.Raw = raw
		}
		return v, nil
	case ToolUse:
		in := bytes.TrimSpace(b.input)
		if len(in) == 0 {
			// A tool that takes no arguments streams no fragments,
			// and its input still has to be an object.
			in = []byte("{}")
		}
		// Compact validates as it normalises: a provider streams the
		// fragments with whatever whitespace it likes and the blocking
		// path compacts, so the two paths must agree byte for byte.
		value := jsontext.Value(bytes.Clone(in))
		if err := value.Compact(); err != nil {
			return nil, &ProtocolError{
				Event:  string(EventBlockStop),
				Index:  index,
				Reason: "the tool call's input is not a complete JSON value, so the call was cut short and must not run",
			}
		}
		v.Input = value
		return v, nil
	case Unknown:
		if len(raw) > 0 {
			v.Raw = raw
		}
		return v, nil
	default:
		// A block kind that carries no deltas — an image a provider
		// echoes back, say — is complete as it was opened.
		return b.start, nil
	}
}

// snapshot is the open block as far as it has arrived. A tool call's input is
// left out: half a JSON document is not a JSON value, and a Message holding
// one would fail to marshal just when a UI wanted to draw it. A consumer that
// wants the fragments reads the ToolInputDelta events.
func (b *accBlock) snapshot() Block {
	switch v := b.start.(type) {
	case Text:
		v.Text = b.text.String()
		return v
	case Thinking:
		v.Text = b.text.String()
		v.Signature = b.sig.String()
		return v
	default:
		return b.start
	}
}

// Message is the assistant message so far: every finished block, then the
// text or thinking of a block still open. Partial is true until MessageStop,
// so a consumer can tell a finished answer from one still arriving or cut off.
//
// Each call builds a fresh list, so a message handed out earlier never changes
// as more events arrive and a caller editing one cannot reach in here.
func (a *Accumulator) Message() Message {
	var content Blocks
	if len(a.blocks) > 0 {
		content = make(Blocks, 0, len(a.blocks))
		for _, b := range a.blocks {
			if b.finished != nil {
				content = append(content, b.finished)
			} else {
				content = append(content, b.snapshot())
			}
		}
	}
	return Message{Role: RoleAssistant, Content: content, Partial: !a.done}
}

// Response is the response so far: the message, the stop reason and the usage
// as the provider has reported them.
//
// It is never nil and never withheld until the message finishes, because the
// moment a caller most needs it is the moment a stream failed; Message.Partial
// already says whether the answer is complete, so a second way of saying so
// would only give every caller two paths to write.
func (a *Accumulator) Response() *Response {
	return &Response{
		ID:               a.id,
		Model:            a.model,
		Message:          a.Message(),
		StopReason:       a.stopReason,
		StopSequence:     a.stopSequence,
		NativeStopReason: a.nativeStop,
		Usage:            a.usage,
		Cost:             a.cost,
	}
}

// Done reports whether MessageStop has arrived, which is the only thing that
// makes a message complete.
func (a *Accumulator) Done() bool { return a.done }

// JustFinished is the block the most recent Apply completed, and false when
// that call completed none. It is how the loop starts a tool the moment its
// block closes rather than waiting for the message to end.
func (a *Accumulator) JustFinished() (Block, bool) { return a.justFinished, a.hasFinished }

// Prefix is the run of finished blocks from the start of the message, which is
// what can be replayed to a provider after a dropped stream. It stops at the
// first block still open: a half-arrived block cannot be replayed, and a
// thinking signature binds the blocks after it to everything before.
func (a *Accumulator) Prefix() []Block {
	var out []Block
	for _, b := range a.blocks {
		if b.finished == nil {
			break
		}
		out = append(out, b.finished)
	}
	return out
}

// laterUsage folds a newer usage report over an older one.
//
// Anthropic puts the input side of the ledger on message_start and the
// cumulative counts on message_delta, and OpenRouter sends one trailing usage
// frame; in both the later report supersedes the earlier rather than adding to
// it, so the counts are replaced and not summed. A count the later report
// leaves at zero is one it did not mention: token counts only grow within a
// message, so a zero there means "not reported here", never "reset", and the
// earlier figure stands.
func laterUsage(old, next Usage) Usage {
	pick := func(o, n int) int {
		if n != 0 {
			return n
		}
		return o
	}
	return Usage{
		Input:      pick(old.Input, next.Input),
		Output:     pick(old.Output, next.Output),
		CacheRead:  pick(old.CacheRead, next.CacheRead),
		CacheWrite: pick(old.CacheWrite, next.CacheWrite),
		Reasoning:  pick(old.Reasoning, next.Reasoning),
	}
}
