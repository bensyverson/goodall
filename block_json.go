package goodall

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
)

// goodall's JSON shape for a block is an object whose first member is "type"
// and whose remaining members are the block's fields in declaration order. It
// resembles Anthropic's shape by design, but it is goodall's own format —
// what a thread is persisted as and what a front end receives — and providers
// translate to and from their wire formats.
//
// Marshalling needs no options: each block type carries a MarshalJSONTo that
// writes its tag. Unmarshalling does, because encoding/json/v2 cannot pick a
// concrete type for an interface on its own; Blocks.UnmarshalJSONFrom applies
// them, so plain json.Unmarshal works on anything holding a Blocks.
//
// A jsontext.Value member such as Thinking.Raw comes back byte-for-byte as
// long as it was compact, which provider wire bytes are: the encoder strips
// insignificant whitespace and normalises escape sequences in strings, but it
// preserves member order, numbers verbatim, and every member. Verified on
// go1.27.0.

// tagged writes a block's fields after its type tag. The body is a defined
// type without marshal methods, so embedding it here cannot recurse.
type tagged[T any] struct {
	Type BlockType `json:"type"`
	Body T         `json:",embed"`
}

// Each body type strips the marshal method from its block so the block can be
// encoded through tagged without calling itself.
type (
	textBody             Text
	imageBody            Image
	documentBody         Document
	toolUseBody          ToolUse
	toolResultBody       ToolResult
	thinkingBody         Thinking
	redactedThinkingBody RedactedThinking
)

// MarshalJSONTo writes the block as a "text" object.
func (t Text) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[textBody]{BlockText, textBody(t)})
}

// MarshalJSONTo writes the block as an "image" object.
func (i Image) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[imageBody]{BlockImage, imageBody(i)})
}

// MarshalJSONTo writes the block as a "document" object.
func (d Document) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[documentBody]{BlockDocument, documentBody(d)})
}

// MarshalJSONTo writes the block as a "tool_use" object.
func (t ToolUse) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[toolUseBody]{BlockToolUse, toolUseBody(t)})
}

// MarshalJSONTo writes the block as a "tool_result" object.
func (t ToolResult) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[toolResultBody]{BlockToolResult, toolResultBody(t)})
}

// MarshalJSONTo writes the block as a "thinking" object.
func (t Thinking) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[thinkingBody]{BlockThinking, thinkingBody(t)})
}

// MarshalJSONTo writes the block as a "redacted_thinking" object.
func (r RedactedThinking) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[redactedThinkingBody]{BlockRedactedThinking, redactedThinkingBody(r)})
}

// MarshalJSONTo writes the provider's original bytes, so a block goodall does
// not understand reaches its destination as it arrived. A block built by hand
// without them falls back to just its type tag.
func (u Unknown) MarshalJSONTo(enc *jsontext.Encoder) error {
	if len(u.Raw) == 0 {
		return json.MarshalEncode(enc, struct {
			Type BlockType `json:"type"`
		}{u.Type})
	}
	return enc.WriteValue(u.Raw)
}

// blockUnmarshalers dispatches on the "type" member. It is a package-level
// value because building the option set on every decode would allocate on a
// hot path.
var blockUnmarshalers = json.WithUnmarshalers(json.UnmarshalFromFunc(unmarshalBlock))

// unmarshalBlock reads one block and picks its concrete type from the "type"
// member. An unrecognised or missing tag yields an Unknown holding the
// original bytes rather than an error: invariant 6 says unknown values are
// surfaced, never dropped.
func unmarshalBlock(dec *jsontext.Decoder, b *Block) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	// ReadValue's buffer is only valid until the next read, and Unknown and
	// Thinking keep these bytes.
	raw = bytes.Clone(raw)

	var probe struct {
		Type BlockType `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		*b = Unknown{Raw: raw}
		return nil
	}
	switch probe.Type {
	case BlockText:
		return decodeBlock(b, raw, func(v textBody) Block { return Text(v) })
	case BlockImage:
		return decodeBlock(b, raw, func(v imageBody) Block { return Image(v) })
	case BlockDocument:
		return decodeBlock(b, raw, func(v documentBody) Block { return Document(v) })
	case BlockToolUse:
		return decodeBlock(b, raw, func(v toolUseBody) Block { return ToolUse(v) })
	case BlockToolResult:
		return decodeBlock(b, raw, func(v toolResultBody) Block { return ToolResult(v) })
	case BlockThinking:
		return decodeBlock(b, raw, func(v thinkingBody) Block { return Thinking(v) })
	case BlockRedactedThinking:
		return decodeBlock(b, raw, func(v redactedThinkingBody) Block { return RedactedThinking(v) })
	default:
		*b = Unknown{Type: probe.Type, Raw: raw}
		return nil
	}
}

// decodeBlock decodes raw into a block's body type, which has no unmarshal
// method of its own, and converts it back to the block. The "type" member is
// an unknown member to the body, and json/v2 ignores unknown members.
func decodeBlock[Body any](out *Block, raw jsontext.Value, convert func(Body) Block) error {
	var v Body
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("goodall: decoding a %T block: %w", convert(v), err)
	}
	*out = convert(v)
	return nil
}

// UnmarshalJSONFrom decodes a JSON array of blocks, choosing each block's
// concrete type from its "type" member. It is what makes plain
// json.Unmarshal work on a Message, a Conversation or any other value with a
// Blocks field.
func (b *Blocks) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	return json.UnmarshalDecode(dec, (*[]Block)(b), blockUnmarshalers)
}
