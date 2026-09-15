// Package anthropic talks to the Anthropic Messages API.
//
// It is one of goodall's two providers: it translates a goodall.Request into
// the Messages API body, sends it, and turns what comes back into goodall's
// neutral blocks, stop reasons and usage. Nothing here leaks into the core —
// the core imports no provider — so a consumer chooses a provider by
// constructing one and handing it to an agent.
package anthropic

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"

	"github.com/bensyverson/goodall"
)

// ProviderName is how this package names itself on errors and on its
// Extensions, matching goodall.Extension.Provider.
const ProviderName = "anthropic"

// The wire structs below are the Messages API's own shapes, not goodall's.
// Each one lists its members in the order Anthropic documents them, because
// encoding/json/v2 writes them in declaration order and invariant 8 says the
// bytes of a request must be reproducible: a member that moves invalidates
// the prompt cache and every thinking signature bound to the prefix.
//
// They are unexported. The package's API is the provider, its options and its
// betas; the body is an implementation detail that must be free to follow
// Anthropic's changes.

// blockType is the "type" tag of a content block on the Anthropic wire.
type blockType string

const (
	blockText             blockType = "text"
	blockImage            blockType = "image"
	blockDocument         blockType = "document"
	blockToolUse          blockType = "tool_use"
	blockToolResult       blockType = "tool_result"
	blockThinking         blockType = "thinking"
	blockRedactedThinking blockType = "redacted_thinking"
)

// sourceType is the "type" tag of an image or document source.
type sourceType string

const (
	sourceBase64 sourceType = "base64"
	sourceURL    sourceType = "url"
	sourceFile   sourceType = "file"
)

// cacheType is the "type" tag of a cache_control marker. Anthropic has only
// ever defined one, and the lifetime is carried by ttl beside it.
type cacheType string

const cacheEphemeral cacheType = "ephemeral"

// wireCacheControl is a cache breakpoint. An absent ttl takes Anthropic's
// default of five minutes.
type wireCacheControl struct {
	Type cacheType        `json:"type"`
	TTL  goodall.CacheTTL `json:"ttl,omitzero"`
}

// wireSource is where the bytes of an image or a document come from. Data is
// []byte, which encoding/json/v2 writes as base64, so the field matches
// Anthropic's base64 source without a conversion step.
type wireSource struct {
	Type      sourceType `json:"type"`
	MediaType string     `json:"media_type,omitzero"`
	Data      []byte     `json:"data,omitzero"`
	URL       string     `json:"url,omitzero"`
	FileID    string     `json:"file_id,omitzero"`
}

// wireBlock is one content block. The interface is sealed to this package's
// block structs, plus wireUnknown for a type goodall does not model.
type wireBlock interface {
	isWireBlock()
}

// wireBlocks is a list of content blocks. Like goodall.Blocks it is a named
// type because encoding/json/v2 cannot choose a concrete type for an
// interface without the dispatch this type installs on decode.
type wireBlocks []wireBlock

// wireText is a run of text.
type wireText struct {
	Text  string            `json:"text"`
	Cache *wireCacheControl `json:"cache_control,omitzero"`
}

// wireImage is an image the model can see.
type wireImage struct {
	Source wireSource        `json:"source"`
	Cache  *wireCacheControl `json:"cache_control,omitzero"`
}

// wireDocument is a document the model can read, with the optional title and
// context that help it cite the document.
type wireDocument struct {
	Source  wireSource        `json:"source"`
	Title   string            `json:"title,omitzero"`
	Context string            `json:"context,omitzero"`
	Cache   *wireCacheControl `json:"cache_control,omitzero"`
}

// wireToolUse is the model's request to call a tool. Input is always written,
// as {} when the call takes no arguments, because Anthropic requires the
// member.
type wireToolUse struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Input jsontext.Value    `json:"input"`
	Cache *wireCacheControl `json:"cache_control,omitzero"`
}

// wireToolResult is the outcome of a tool call. Content is always the block
// list rather than Anthropic's alternative bare string, so one shape covers a
// text result and a result carrying an image.
type wireToolResult struct {
	ToolUseID string            `json:"tool_use_id"`
	IsError   bool              `json:"is_error,omitzero"`
	Content   wireBlocks        `json:"content"`
	Cache     *wireCacheControl `json:"cache_control,omitzero"`
}

// wireThinking is a reasoning block. It is rebuilt from goodall's neutral
// fields on the way out and read back into them on the way in: on Anthropic
// the text and the signature are the whole block, so nothing is lost.
type wireThinking struct {
	Thinking  string            `json:"thinking"`
	Signature string            `json:"signature,omitzero"`
	Cache     *wireCacheControl `json:"cache_control,omitzero"`
}

// wireRedactedThinking is a reasoning block Anthropic encrypted.
type wireRedactedThinking struct {
	Data  string            `json:"data"`
	Cache *wireCacheControl `json:"cache_control,omitzero"`
}

// wireUnknown is a block this version does not model — a server tool use, a
// container upload, whatever Anthropic ships next. It keeps the original
// bytes and writes them back verbatim, which is invariant 6 at this seam.
type wireUnknown struct {
	Type blockType
	Raw  jsontext.Value
}

func (wireText) isWireBlock()             {}
func (wireImage) isWireBlock()            {}
func (wireDocument) isWireBlock()         {}
func (wireToolUse) isWireBlock()          {}
func (wireToolResult) isWireBlock()       {}
func (wireThinking) isWireBlock()         {}
func (wireRedactedThinking) isWireBlock() {}
func (wireUnknown) isWireBlock()          {}

// tagged writes a block's members after its type tag. The body is a defined
// type without marshal methods, so embedding a block here cannot recurse.
type tagged[T any] struct {
	Type blockType `json:"type"`
	Body T         `json:",embed"`
}

// Each body type strips the marshal method from its block so the block can be
// encoded through tagged without calling itself.
type (
	textBody             wireText
	imageBody            wireImage
	documentBody         wireDocument
	toolUseBody          wireToolUse
	toolResultBody       wireToolResult
	thinkingBody         wireThinking
	redactedThinkingBody wireRedactedThinking
)

// MarshalJSONTo writes the block as a "text" object.
func (t wireText) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[textBody]{blockText, textBody(t)})
}

// MarshalJSONTo writes the block as an "image" object.
func (i wireImage) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[imageBody]{blockImage, imageBody(i)})
}

// MarshalJSONTo writes the block as a "document" object.
func (d wireDocument) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[documentBody]{blockDocument, documentBody(d)})
}

// MarshalJSONTo writes the block as a "tool_use" object.
func (t wireToolUse) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[toolUseBody]{blockToolUse, toolUseBody(t)})
}

// MarshalJSONTo writes the block as a "tool_result" object.
func (t wireToolResult) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[toolResultBody]{blockToolResult, toolResultBody(t)})
}

// MarshalJSONTo writes the block as a "thinking" object.
func (t wireThinking) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[thinkingBody]{blockThinking, thinkingBody(t)})
}

// MarshalJSONTo writes the block as a "redacted_thinking" object.
func (r wireRedactedThinking) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, tagged[redactedThinkingBody]{blockRedactedThinking, redactedThinkingBody(r)})
}

// MarshalJSONTo writes the provider's original bytes, so a block goodall does
// not model reaches Anthropic exactly as it arrived. A block built without
// them falls back to its type tag alone.
func (u wireUnknown) MarshalJSONTo(enc *jsontext.Encoder) error {
	if len(u.Raw) == 0 {
		return json.MarshalEncode(enc, struct {
			Type blockType `json:"type"`
		}{u.Type})
	}
	return enc.WriteValue(u.Raw)
}

// blockUnmarshalers dispatches on the "type" member. It is a package-level
// value because building the option set on every decode would allocate on the
// streaming path. Any further interface dispatch in this package must join
// the funcs rather than the options, or one of them silently stops firing.
var blockUnmarshalers = json.WithUnmarshalers(json.UnmarshalFromFunc(unmarshalWireBlock))

// unmarshalWireBlock reads one block and picks its concrete type from the
// "type" member, keeping the bytes of anything it does not recognise.
func unmarshalWireBlock(dec *jsontext.Decoder, b *wireBlock) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	// ReadValue's buffer is only valid until the next read, and wireUnknown
	// keeps these bytes.
	raw = jsontext.Value(append([]byte(nil), raw...))

	var probe struct {
		Type blockType `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		*b = wireUnknown{Raw: raw}
		return nil
	}
	switch probe.Type {
	case blockText:
		return decodeWireBlock(b, raw, func(v textBody) wireBlock { return wireText(v) })
	case blockImage:
		return decodeWireBlock(b, raw, func(v imageBody) wireBlock { return wireImage(v) })
	case blockDocument:
		return decodeWireBlock(b, raw, func(v documentBody) wireBlock { return wireDocument(v) })
	case blockToolUse:
		return decodeWireBlock(b, raw, func(v toolUseBody) wireBlock { return wireToolUse(v) })
	case blockToolResult:
		return decodeWireBlock(b, raw, func(v toolResultBody) wireBlock { return wireToolResult(v) })
	case blockThinking:
		return decodeWireBlock(b, raw, func(v thinkingBody) wireBlock { return wireThinking(v) })
	case blockRedactedThinking:
		return decodeWireBlock(b, raw, func(v redactedThinkingBody) wireBlock { return wireRedactedThinking(v) })
	default:
		*b = wireUnknown{Type: probe.Type, Raw: raw}
		return nil
	}
}

// decodeWireBlock decodes raw into a block's body type, which carries no
// marshal method of its own, and converts it back to the block.
func decodeWireBlock[Body any](out *wireBlock, raw jsontext.Value, convert func(Body) wireBlock) error {
	var v Body
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("anthropic: decoding a %T block: %w", convert(v), err)
	}
	*out = convert(v)
	return nil
}

// UnmarshalJSONFrom decodes a JSON array of content blocks, choosing each
// block's concrete type from its "type" member.
func (b *wireBlocks) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	return json.UnmarshalDecode(dec, (*[]wireBlock)(b), blockUnmarshalers)
}

// wireBlockValue is a single content block standing on its own, as
// content_block_start carries it. It exists for the same reason wireBlocks
// does: the dispatch has to hang off a named type.
type wireBlockValue struct {
	Block wireBlock
}

// MarshalJSONTo writes the block it holds.
func (v wireBlockValue) MarshalJSONTo(enc *jsontext.Encoder) error {
	return json.MarshalEncode(enc, v.Block)
}

// UnmarshalJSONFrom decodes one content block.
func (v *wireBlockValue) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	return json.UnmarshalDecode(dec, &v.Block, blockUnmarshalers)
}

// wireMessage is one turn. The role is goodall's own, because Anthropic's
// three roles are spelled the same and a mid-conversation system message
// passes straight through.
type wireMessage struct {
	Role    goodall.Role `json:"role"`
	Content wireBlocks   `json:"content"`
}

// wireTool is a tool definition. InputSchema is goodall's Schema, which
// marshals its members in declaration order, so the bytes of a tool are
// stable between turns and the cached prefix survives.
type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitzero"`
	InputSchema *goodall.Schema `json:"input_schema"`
}

// toolChoiceType is the "type" tag of tool_choice.
type toolChoiceType string

const (
	toolChoiceAuto toolChoiceType = "auto"
	toolChoiceAny  toolChoiceType = "any"
	toolChoiceTool toolChoiceType = "tool"
	toolChoiceNone toolChoiceType = "none"
)

// wireToolChoice constrains the model's tool use for one turn.
type wireToolChoice struct {
	Type                   toolChoiceType `json:"type"`
	Name                   string         `json:"name,omitzero"`
	DisableParallelToolUse bool           `json:"disable_parallel_tool_use,omitzero"`
}

// thinkingType is the "type" tag of the thinking configuration.
type thinkingType string

const (
	thinkingAdaptive thinkingType = "adaptive"
	thinkingEnabled  thinkingType = "enabled"
	thinkingDisabled thinkingType = "disabled"
)

// wireThinkingConfig is the request's thinking setting. Display belongs to
// adaptive thinking and BudgetTokens to the older enabled form; thinking.go
// decides which one a model gets.
type wireThinkingConfig struct {
	Type         thinkingType            `json:"type"`
	Display      goodall.ThinkingDisplay `json:"display,omitzero"`
	BudgetTokens int                     `json:"budget_tokens,omitzero"`
}

// wireOutputConfig carries the effort rung on models with adaptive thinking.
type wireOutputConfig struct {
	Effort goodall.Effort `json:"effort,omitzero"`
}

// wireMetadata is Anthropic's per-request metadata, which is one member: an
// opaque end-user identifier used for abuse monitoring.
type wireMetadata struct {
	UserID string `json:"user_id,omitzero"`
}

// wireRequest is the body of POST /v1/messages.
//
// Messages comes last so that everything above it — the model, the
// parameters, the system prompt and the tools — forms a byte-stable prefix
// that appending a turn cannot disturb (invariant 8). Stream is set by the
// client after translation, since the same body serves both paths.
type wireRequest struct {
	Model         string              `json:"model"`
	MaxTokens     int                 `json:"max_tokens"`
	System        wireBlocks          `json:"system,omitzero"`
	Tools         []wireTool          `json:"tools,omitzero"`
	ToolChoice    *wireToolChoice     `json:"tool_choice,omitzero"`
	Thinking      *wireThinkingConfig `json:"thinking,omitzero"`
	OutputConfig  *wireOutputConfig   `json:"output_config,omitzero"`
	StopSequences []string            `json:"stop_sequences,omitzero"`
	Metadata      *wireMetadata       `json:"metadata,omitzero"`
	ServiceTier   ServiceTier         `json:"service_tier,omitzero"`
	CacheControl  *wireCacheControl   `json:"cache_control,omitzero"`
	Stream        bool                `json:"stream,omitzero"`
	Messages      []wireMessage       `json:"messages"`
}

// wireUsage is Anthropic's token ledger. The two cache members are absolute
// counts, not a share of input_tokens, so the prompt size is the sum of the
// three input members.
type wireUsage struct {
	InputTokens              int `json:"input_tokens,omitzero"`
	OutputTokens             int `json:"output_tokens,omitzero"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitzero"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitzero"`
}

// wireResponse is a whole message: the body of a non-streaming response and
// the payload of a message_start event, which carries the same shape with an
// empty content list and only the input side of the usage filled in.
type wireResponse struct {
	ID           string             `json:"id,omitzero"`
	Type         string             `json:"type,omitzero"`
	Role         goodall.Role       `json:"role,omitzero"`
	Model        string             `json:"model,omitzero"`
	Content      wireBlocks         `json:"content,omitzero"`
	StopReason   goodall.StopReason `json:"stop_reason,omitzero"`
	StopSequence string             `json:"stop_sequence,omitzero"`
	Usage        wireUsage          `json:"usage,omitzero"`
}
