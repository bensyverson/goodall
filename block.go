package goodall

import "encoding/json/jsontext"

// BlockType is the tag that identifies a content block on the wire. The
// constants below are the block types goodall understands; any other value
// decodes to an Unknown block rather than being dropped.
type BlockType string

const (
	// BlockText is a run of plain text.
	BlockText BlockType = "text"
	// BlockImage is an image the model can see.
	BlockImage BlockType = "image"
	// BlockDocument is a document, typically a PDF or plain text.
	BlockDocument BlockType = "document"
	// BlockToolUse is the model's request to call a tool.
	BlockToolUse BlockType = "tool_use"
	// BlockToolResult is the outcome of a tool call, sent back to the model.
	BlockToolResult BlockType = "tool_result"
	// BlockThinking is a reasoning block the model produced.
	BlockThinking BlockType = "thinking"
	// BlockRedactedThinking is a reasoning block the provider encrypted.
	BlockRedactedThinking BlockType = "redacted_thinking"
)

// Block is one piece of content in a message. The interface is sealed: only
// the types in this package implement it, so a type switch over a Block is
// exhaustive once it handles Unknown, which carries anything a newer provider
// sends. Blocks are values, so a []Block holds Text{…} rather than &Text{…}.
type Block interface {
	isBlock()
}

// Blocks is a list of content blocks. It is a named type because decoding an
// interface needs the type-tag dispatch this package installs; a plain
// []Block encodes correctly but cannot be decoded by encoding/json/v2 on its
// own. Any struct with a Blocks field marshals and unmarshals with no options.
type Blocks []Block

// Text is a run of plain text.
type Text struct {
	Text  string        `json:"text"`
	Cache *CacheControl `json:"cache,omitzero"`
}

// Image is an image for the model to look at.
type Image struct {
	Source Source        `json:"source"`
	Cache  *CacheControl `json:"cache,omitzero"`
}

// Document is a document for the model to read, such as a PDF. Title and
// Context are optional hints that help the model cite it.
type Document struct {
	Source  Source        `json:"source"`
	Title   string        `json:"title,omitzero"`
	Context string        `json:"context,omitzero"`
	Cache   *CacheControl `json:"cache,omitzero"`
}

// ToolUse is the model's request to call a tool. Input is the argument object
// exactly as the model produced it, left unparsed so the tool decodes it into
// its own type.
type ToolUse struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input jsontext.Value `json:"input,omitzero"`
	Cache *CacheControl  `json:"cache,omitzero"`
}

// ToolResult is the outcome of a tool call. It travels in a user message, as
// on Anthropic; providers whose wire format has a separate tool role
// translate it. Content holds Text, Image and Document blocks.
type ToolResult struct {
	ToolUseID string        `json:"tool_use_id"`
	IsError   bool          `json:"is_error,omitzero"`
	Content   Blocks        `json:"content"`
	Cache     *CacheControl `json:"cache,omitzero"`
}

// Thinking is a reasoning block. Text is what a UI displays and may be empty
// when the provider omits it. Raw is the provider's block exactly as
// received: providers re-emit it untouched, because a thinking signature
// binds the block to the conversation prefix that produced it.
type Thinking struct {
	Text      string         `json:"text,omitzero"`
	Signature string         `json:"signature,omitzero"`
	Raw       jsontext.Value `json:"raw,omitzero"`
}

// RedactedThinking is a reasoning block the provider encrypted. Data is the
// opaque payload, and Raw is the provider's block as received.
type RedactedThinking struct {
	Data string         `json:"data,omitzero"`
	Raw  jsontext.Value `json:"raw,omitzero"`
}

// Unknown is a block whose type tag this version of goodall does not
// recognise. It keeps the provider's bytes so the block survives a round trip
// through storage and back to the provider unchanged.
type Unknown struct {
	Type BlockType      `json:"type"`
	Raw  jsontext.Value `json:"raw,omitzero"`
}

func (Text) isBlock()             {}
func (Image) isBlock()            {}
func (Document) isBlock()         {}
func (ToolUse) isBlock()          {}
func (ToolResult) isBlock()       {}
func (Thinking) isBlock()         {}
func (RedactedThinking) isBlock() {}
func (Unknown) isBlock()          {}

// clone returns a copy that shares no slice with b. Blocks are values, so
// only the nested block list of a tool result needs copying; the raw JSON of
// a thinking or unknown block is shared, as nothing in the library writes
// through it.
func (b Blocks) clone() Blocks {
	if b == nil {
		return nil
	}
	out := make(Blocks, len(b))
	for i, blk := range b {
		if tr, ok := blk.(ToolResult); ok {
			tr.Content = tr.Content.clone()
			blk = tr
		}
		out[i] = blk
	}
	return out
}
