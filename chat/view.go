package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/bensyverson/goodall"
)

// ToolResultStatus is how a tool call turned out, as much as a front end is
// told: the result's content stays with the back end, so "it failed" is the
// whole of what travels. It is a typed constant rather than a bool because a
// result that is neither — refused, still running — is a shape this will grow,
// and a bool named for one outcome would have to be renamed to admit it.
type ToolResultStatus string

const (
	// ToolResultOK is a tool that returned a result.
	ToolResultOK ToolResultStatus = "ok"
	// ToolResultError is a tool that failed, refused or was cut short. The
	// model saw why; the front end is told only that it did.
	ToolResultError ToolResultStatus = "error"
)

// BlockViewKind is what a redacted block is. It is the front end's whole
// vocabulary for content: a switch over these renders a thread, and nothing
// else in the view needs interpreting.
type BlockViewKind string

const (
	// BlockViewText is a run of text, from the person or the model.
	BlockViewText BlockViewKind = "text"
	// BlockViewImage is an image the person sent.
	BlockViewImage BlockViewKind = "image"
	// BlockViewDocument is a document the person sent.
	BlockViewDocument BlockViewKind = "document"
	// BlockViewToolCall is the model asking for a tool, by name. Its
	// arguments are behind a placeholder.
	BlockViewToolCall BlockViewKind = "tool_call"
	// BlockViewToolResult is a tool's outcome: a status and a placeholder,
	// never the content the tool produced.
	BlockViewToolResult BlockViewKind = "tool_result"
	// BlockViewThinking is the model's reasoning, with its text when the
	// agent displays thinking and without when it does not.
	BlockViewThinking BlockViewKind = "thinking"
	// BlockViewRedactedThinking is reasoning the provider encrypted: a
	// marker that the model thought, with nothing to read.
	BlockViewRedactedThinking BlockViewKind = "redacted_thinking"
	// BlockViewUnknown is a block this version of goodall does not model,
	// named by the provider's own tag so a UI can say so rather than
	// showing a gap.
	BlockViewUnknown BlockViewKind = "unknown"
)

// MediaView is where a picture or a document in the view comes from. A source
// the front end can fetch — a URL, or a file already uploaded to the provider
// — passes through; bytes do not, because the front end is the side that sent
// them and already has them, and re-serving them through the back end would
// put megabytes of base64 into every read of the thread.
type MediaView struct {
	// Source says which of the fields below is meaningful.
	Source goodall.SourceType `json:"source"`
	// MediaType is the IANA type, where the block carried one.
	MediaType string `json:"media_type,omitzero"`
	// URL is the fetchable URL of a URL source.
	URL string `json:"url,omitzero"`
	// FileID names a file uploaded to the provider.
	FileID string `json:"file_id,omitzero"`
	// Title is a document's title, which is a label the sender chose
	// rather than content the back end owns.
	Title string `json:"title,omitzero"`
	// Placeholder is the opaque id standing in for inline bytes, empty for
	// every other source.
	Placeholder string `json:"placeholder,omitzero"`
}

// BlockView is one block of a thread as a front end may see it. Kind says
// which of the fields below carry anything: a text block has Text, a tool call
// has ToolName and Placeholder, a tool result has Status and Placeholder, and
// so on. It is one struct with a typed kind rather than an interface because
// the view is a wire shape first — a front end decodes it in whatever language
// it is written in, and a tagged union with one member set is the shape that
// travels.
type BlockView struct {
	// Kind is what this block is.
	Kind BlockViewKind `json:"kind"`
	// Text is the readable text of a text or thinking block.
	Text string `json:"text,omitzero"`
	// Media is where an image or document block's content comes from.
	Media *MediaView `json:"media,omitzero"`
	// ToolUseID is the model's own id for a tool call, carried on the call
	// and on its result so a UI can pair them. It identifies the call and
	// says nothing about what was passed or returned.
	ToolUseID string `json:"tool_use_id,omitzero"`
	// ToolName is the tool the model asked for.
	ToolName string `json:"tool_name,omitzero"`
	// Status is how a tool call turned out, on a tool result block.
	Status ToolResultStatus `json:"status,omitzero"`
	// BlockType is the provider's own tag on an unknown block.
	BlockType goodall.BlockType `json:"block_type,omitzero"`
	// Placeholder is the opaque id standing in for content the back end
	// keeps: a tool call's input, a tool result's content.
	Placeholder string `json:"placeholder,omitzero"`
}

// MessageView is one turn of a thread as a front end may see it.
type MessageView struct {
	// Role is who the turn is from. A turn carrying tool results is a user
	// turn, as it is in the conversation itself.
	Role goodall.Role `json:"role"`
	// Content is the turn's blocks, in order. It is never null, so a front
	// end iterates without testing.
	Content []BlockView `json:"content"`
	// Partial marks a model turn that was interrupted, so a UI renders it
	// as cut off rather than finished.
	Partial bool `json:"partial,omitzero"`
}

// ThreadView is a thread as a front end may see it: the conversation with
// everything the back end owns taken out, plus the thread's identity, its
// ledger and its timestamps. It is computed from the canonical thread and is
// never written back — nothing in it can be turned into a thread, which is the
// point.
//
// What it withholds is the system prompt, every tool call's input, every tool
// result's content, the bytes of inline media, thinking signatures and the
// provider bytes of blocks goodall does not model. Each of those is announced
// by an opaque placeholder id, so a UI can render "a tool ran" or "your
// picture" in the right place without being handed the thing itself.
type ThreadView struct {
	// ID is the thread's id, which is what a front end sends back.
	ID string `json:"id"`
	// System is the placeholder id standing in for the agent's system
	// prompt, empty when the agent has none. The prompt's text is never in
	// the view, and never reaches the function that computes it.
	System string `json:"system,omitzero"`
	// Messages is the redacted history, in order and never null.
	Messages []MessageView `json:"messages"`
	// Usage is the thread's token total over every run.
	Usage goodall.Usage `json:"usage,omitzero"`
	// Cost is the thread's money total over every run, where the provider
	// reported one.
	Cost goodall.Cost `json:"cost,omitzero"`
	// Version is the version of the thread this view was computed from,
	// which is what a caller quotes when it writes.
	Version int `json:"version"`
	// CreatedAt is when the thread was first stored.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when it was last stored.
	UpdatedAt time.Time `json:"updated_at"`
}

// ViewOptions are the two facts a view needs that a thread does not carry,
// because they belong to the agent rather than to the history. [Service.View]
// fills them from its own agent; a consumer computing a view by hand supplies
// them itself.
type ViewOptions struct {
	// HasSystemPrompt is whether the agent has a standing instruction, and
	// so whether the view announces a system placeholder. The prompt
	// itself is deliberately not a field: what cannot be passed in cannot
	// be leaked out.
	HasSystemPrompt bool
	// ThinkingDisplay is the agent's thinking display setting. Under
	// [goodall.DisplayOmitted] the thinking blocks are still shown as
	// blocks — the model did think — but carry no text.
	ThinkingDisplay goodall.ThinkingDisplay
}

// NewThreadView computes the redacted view of a thread. It is a pure function:
// it reads the thread and the agent's two facts and returns a fresh value,
// touching neither the thread nor any store. A nil thread has no view, which
// mirrors [Thread.Clone].
func NewThreadView(thread *Thread, opts ViewOptions) *ThreadView {
	if thread == nil {
		return nil
	}
	view := &ThreadView{
		ID:        thread.ID,
		Messages:  make([]MessageView, 0, thread.Conversation.Len()),
		Usage:     thread.Usage,
		Cost:      thread.Cost,
		Version:   thread.Version,
		CreatedAt: thread.CreatedAt,
		UpdatedAt: thread.UpdatedAt,
	}
	if opts.HasSystemPrompt {
		view.System = placeholderID(thread.ID, systemPosition)
	}
	for i, msg := range thread.Conversation.Messages() {
		mv := MessageView{
			Role:    msg.Role,
			Content: make([]BlockView, 0, len(msg.Content)),
			Partial: msg.Partial,
		}
		for j, blk := range msg.Content {
			mv.Content = append(mv.Content, blockView(blk, placeholderID(thread.ID, blockPosition(i, j)), opts))
		}
		view.Messages = append(view.Messages, mv)
	}
	return view
}

// View is the redacted view of a stored thread, which is what a front end is
// handed. It reads the thread as [Service.Get] does, so it reflects what is
// stored: a run in flight is not in it, because the service persists once the
// run ends and [Service.Subscribe] is how a caller watches the answer arrive.
//
// The two facts the thread does not carry come from the service's own agent:
// whether it has a system prompt, and how much of its thinking it displays.
func (s *Service) View(ctx context.Context, threadID string) (*ThreadView, error) {
	thread, err := s.store.Get(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return NewThreadView(thread, ViewOptions{
		HasSystemPrompt: s.agent.System != "",
		ThinkingDisplay: s.agent.Thinking.Display,
	}), nil
}

// blockView redacts one block. id is the placeholder this block's position
// earned, spent only on the kinds that withhold something.
func blockView(blk goodall.Block, id string, opts ViewOptions) BlockView {
	switch b := blk.(type) {
	case goodall.Text:
		return BlockView{Kind: BlockViewText, Text: b.Text}
	case goodall.Image:
		return BlockView{Kind: BlockViewImage, Media: mediaView(b.Source, "", id)}
	case goodall.Document:
		return BlockView{Kind: BlockViewDocument, Media: mediaView(b.Source, b.Title, id)}
	case goodall.ToolUse:
		return BlockView{Kind: BlockViewToolCall, ToolUseID: b.ID, ToolName: b.Name, Placeholder: id}
	case goodall.ToolResult:
		return BlockView{Kind: BlockViewToolResult, ToolUseID: b.ToolUseID, Status: resultStatus(b), Placeholder: id}
	case goodall.Thinking:
		view := BlockView{Kind: BlockViewThinking}
		if opts.ThinkingDisplay != goodall.DisplayOmitted {
			view.Text = b.Text
		}
		return view
	case goodall.RedactedThinking:
		return BlockView{Kind: BlockViewRedactedThinking}
	case goodall.Unknown:
		return BlockView{Kind: BlockViewUnknown, BlockType: b.Type}
	default:
		// The Block interface is sealed, so this is reachable only if a
		// block type is added to the core without being redacted here.
		// Naming it beats rendering it.
		return BlockView{Kind: BlockViewUnknown}
	}
}

// mediaView redacts a source: bytes become a placeholder, and a source the
// front end can fetch for itself passes through.
func mediaView(src goodall.Source, title, id string) *MediaView {
	view := &MediaView{Source: src.Type, MediaType: src.MediaType, Title: title}
	switch src.Type {
	case goodall.SourceURL:
		view.URL = src.URL
	case goodall.SourceFile:
		view.FileID = src.FileID
	default:
		view.Placeholder = id
	}
	return view
}

// resultStatus is how a tool call turned out.
func resultStatus(r goodall.ToolResult) ToolResultStatus {
	if r.IsError {
		return ToolResultError
	}
	return ToolResultOK
}

// systemPosition is the position of the system prompt in the placeholder
// scheme. It is a word rather than a number, so it can never collide with a
// block's position.
const systemPosition = "system"

// placeholderIDBytes is how much of the digest a placeholder id keeps. Sixteen
// bytes is 32 hex characters: far past any collision a thread could reach, and
// short enough to read in a log.
const placeholderIDBytes = 16

// placeholderID is the opaque id standing in for content the view withholds.
//
// It is a digest of the thread's id and the content's *position*, never of the
// content itself: hashing the secret would make the id a verifier for it —
// anyone holding a guess could confirm it, and two threads carrying the same
// tool result would advertise the fact by sharing an id. Position is stable
// across computations because history is append-only, so two views of one
// stored thread agree, which is what lets a front end keep its own state keyed
// by these ids.
func placeholderID(threadID, position string) string {
	sum := sha256.Sum256([]byte(threadID + "\x00" + position))
	return hex.EncodeToString(sum[:placeholderIDBytes])
}

// blockPosition names a block by its place in the history. The separator is a
// byte that appears in neither number, so no two positions share a spelling.
func blockPosition(message, block int) string {
	return strconv.Itoa(message) + "\x00" + strconv.Itoa(block)
}
