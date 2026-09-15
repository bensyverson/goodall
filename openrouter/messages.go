package openrouter

import (
	"encoding/base64"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"strings"

	"github.com/bensyverson/goodall"
)

// toolErrorPrefix marks a failed tool result. The Chat Completions format has
// no is_error flag on a tool message, so the failure has to be conveyed in the
// text the model reads; a leading "Error: " is what the model is used to
// seeing and what it acts on.
const toolErrorPrefix = "Error: "

// defaultDocumentFilename names a document whose block carried no title.
// OpenRouter's file part wants a filename and the model reads it.
const defaultDocumentFilename = "document"

// messages builds the wire message list: the standing system prompt first,
// then the conversation.
func (t *translator) messages(req *goodall.Request) ([]chatMessage, error) {
	var out []chatMessage
	if len(req.System) > 0 {
		out = append(out, t.systemMessage(req.System))
	}
	for i, m := range req.Messages {
		switch m.Role {
		case goodall.RoleAssistant:
			out = append(out, t.assistantMessage(m))
		case goodall.RoleSystem:
			parts, err := t.parts(m.Content)
			if err != nil {
				return nil, fmt.Errorf("message %d: %w", i, err)
			}
			out = append(out, chatMessage{Role: roleSystem, Content: partsContent(parts)})
		default:
			tools, parts, err := t.userMessages(m)
			if err != nil {
				return nil, fmt.Errorf("message %d: %w", i, err)
			}
			out = append(out, tools...)
			if len(parts) > 0 {
				out = append(out, chatMessage{Role: roleUser, Content: partsContent(parts)})
			}
		}
	}
	return out, nil
}

// systemMessage is the standing instruction as one leading message whose
// content is an array of text parts, so that a cache breakpoint can sit on
// the last of them.
func (t *translator) systemMessage(system []goodall.Text) chatMessage {
	parts := make([]contentPart, 0, len(system))
	for _, text := range system {
		parts = append(parts, contentPart{Type: partText, Text: text.Text, CacheControl: t.marker(text.Cache)})
	}
	if t.policy == goodall.CacheAuto {
		parts[len(parts)-1].CacheControl = &cacheControl{Type: cacheEphemeral}
		t.markers++
	}
	return chatMessage{Role: roleSystem, Content: partsContent(parts)}
}

// userMessages splits one neutral user turn into the tool messages it
// carries and the content parts that remain. The tool messages are emitted
// first, in block order: the wire answers each tool call with its own
// message, and the model expects them before any new user content.
func (t *translator) userMessages(m goodall.Message) ([]chatMessage, []contentPart, error) {
	var tools []chatMessage
	var parts []contentPart
	for _, blk := range m.Content {
		if result, ok := blk.(goodall.ToolResult); ok {
			tools = append(tools, chatMessage{
				Role:       roleTool,
				Content:    stringContent(toolResultText(result)),
				ToolCallID: result.ToolUseID,
			})
			continue
		}
		part, ok, err := t.part(blk)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			parts = append(parts, part)
		}
	}
	return tools, parts, nil
}

// parts converts a block list to content parts, leaving out the blocks the
// wire has no part type for.
func (t *translator) parts(blocks goodall.Blocks) ([]contentPart, error) {
	var parts []contentPart
	for _, blk := range blocks {
		part, ok, err := t.part(blk)
		if err != nil {
			return nil, err
		}
		if ok {
			parts = append(parts, part)
		}
	}
	return parts, nil
}

// part converts one block to a content part. The second result is false for a
// block the wire has no part type for, which is dropped.
func (t *translator) part(blk goodall.Block) (contentPart, bool, error) {
	switch b := blk.(type) {
	case goodall.Text:
		return contentPart{Type: partText, Text: b.Text, CacheControl: t.marker(b.Cache)}, true, nil
	case goodall.Image:
		url, err := imageSourceURL(b.Source)
		if err != nil {
			return contentPart{}, false, err
		}
		return contentPart{
			Type:         partImageURL,
			ImageURL:     &imageURL{URL: url},
			CacheControl: t.marker(b.Cache),
		}, true, nil
	case goodall.Document:
		data, err := documentSourceData(b.Source)
		if err != nil {
			return contentPart{}, false, err
		}
		return contentPart{
			Type:         partFile,
			File:         &filePart{Filename: documentFilename(b), FileData: data},
			CacheControl: t.marker(b.Cache),
		}, true, nil
	default:
		return contentPart{}, false, nil
	}
}

// imageSourceURL renders an image source as the wire's image_url: inline
// bytes become a data: URL and a URL is passed through. A provider file id
// names an upload this package never made, so it is refused rather than sent
// as a URL the provider would try to fetch.
func imageSourceURL(s goodall.Source) (string, error) {
	switch s.Type {
	case goodall.SourceBytes:
		return dataURL(s.MediaType, s.Data), nil
	case goodall.SourceURL:
		return s.URL, nil
	default:
		return "", fmt.Errorf("openrouter: %w: an image block carrying a provider file id cannot be sent; send it inline or by URL",
			goodall.KindInvalidRequest)
	}
}

// documentSourceData renders a document source as the wire's file_data.
// OpenRouter accepts a publicly fetchable URL there as well as a data: URL,
// verified against its PDF documentation on 2026-09-15.
func documentSourceData(s goodall.Source) (string, error) {
	switch s.Type {
	case goodall.SourceBytes:
		return dataURL(s.MediaType, s.Data), nil
	case goodall.SourceURL:
		return s.URL, nil
	default:
		return "", fmt.Errorf("openrouter: %w: a document block carrying a provider file id cannot be sent; send it inline or by URL",
			goodall.KindInvalidRequest)
	}
}

// dataURL renders inline bytes as a base64 data: URL.
func dataURL(mediaType string, data []byte) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// documentFilename is what the model sees the document called.
func documentFilename(d goodall.Document) string {
	if d.Title != "" {
		return d.Title
	}
	return defaultDocumentFilename
}

// toolResultText renders a tool result as the text a tool message carries.
// The result's text blocks are concatenated; a block of any other kind
// becomes a placeholder naming its type, because the tool role takes a string
// and an image inside a result would otherwise vanish without trace. A result
// marked IsError is prefixed, since the wire has no is_error flag.
func toolResultText(r goodall.ToolResult) string {
	var b strings.Builder
	if r.IsError {
		b.WriteString(toolErrorPrefix)
	}
	for _, blk := range r.Content {
		if text, ok := blk.(goodall.Text); ok {
			b.WriteString(text.Text)
			continue
		}
		b.WriteString(blockPlaceholder(blk))
	}
	return b.String()
}

// blockPlaceholder names a block the tool role cannot carry.
func blockPlaceholder(blk goodall.Block) string {
	switch b := blk.(type) {
	case goodall.Image:
		return "[" + string(goodall.BlockImage) + "]"
	case goodall.Document:
		return "[" + string(goodall.BlockDocument) + "]"
	case goodall.ToolUse:
		return "[" + string(goodall.BlockToolUse) + "]"
	case goodall.ToolResult:
		return "[" + string(goodall.BlockToolResult) + "]"
	case goodall.Thinking:
		return "[" + string(goodall.BlockThinking) + "]"
	case goodall.RedactedThinking:
		return "[" + string(goodall.BlockRedactedThinking) + "]"
	case goodall.Unknown:
		return "[" + string(b.Type) + "]"
	default:
		return "[block]"
	}
}

// assistantMessage renders a model turn. Text blocks concatenate into the
// message's string content, tool uses become tool calls whose arguments are a
// JSON string, and thinking blocks become reasoning_details entries in block
// order — re-emitted byte for byte, because a thinking signature binds the
// block to the prefix that produced it.
func (t *translator) assistantMessage(m goodall.Message) chatMessage {
	msg := chatMessage{Role: roleAssistant}
	var text strings.Builder
	for _, blk := range m.Content {
		switch b := blk.(type) {
		case goodall.Text:
			text.WriteString(b.Text)
		case goodall.ToolUse:
			msg.ToolCalls = append(msg.ToolCalls, toolCall{
				ID:       b.ID,
				Type:     functionTool,
				Function: toolCallFunction{Name: b.Name, Arguments: toolArguments(b.Input)},
			})
		case goodall.Thinking:
			t.appendReasoning(&msg, thinkingEntry(b))
		case goodall.RedactedThinking:
			t.appendReasoning(&msg, redactedThinkingEntry(b))
		}
	}
	if text.Len() > 0 {
		msg.Content = stringContent(text.String())
	}
	return msg
}

// appendReasoning adds one reasoning_details entry, on the dialects that
// carry them.
func (t *translator) appendReasoning(msg *chatMessage, entry jsontext.Value) {
	if !t.quirks.ReasoningDetails || len(entry) == 0 {
		return
	}
	msg.ReasoningDetails = append(msg.ReasoningDetails, entry)
}

// toolArguments renders a tool call's input as the JSON string the wire
// wants. An input the model never filled in is sent as an empty object, which
// is what a no-parameter tool call looks like.
func toolArguments(input jsontext.Value) string {
	if len(input) == 0 {
		return "{}"
	}
	return string(input)
}

// thinkingEntry is the reasoning_details entry for a thinking block: the
// provider's own bytes when the block carries them, and a rebuilt
// reasoning.text entry otherwise. A block with nothing in it at all produces
// no entry.
func thinkingEntry(b goodall.Thinking) jsontext.Value {
	if len(b.Raw) > 0 {
		return jsontext.Value(b.Raw)
	}
	if b.Text == "" && b.Signature == "" {
		return nil
	}
	return marshalDetail(reasoningDetail{Type: reasoningText, Text: b.Text, Signature: b.Signature})
}

// redactedThinkingEntry is the reasoning_details entry for an encrypted
// thinking block.
func redactedThinkingEntry(b goodall.RedactedThinking) jsontext.Value {
	if len(b.Raw) > 0 {
		return jsontext.Value(b.Raw)
	}
	if b.Data == "" {
		return nil
	}
	return marshalDetail(reasoningDetail{Type: reasoningEncrypted, Data: b.Data})
}

// marshalDetail renders a rebuilt reasoning entry. The struct holds only
// strings, so encoding cannot fail on bad input and a failure would be a bug
// rather than something a caller could cause.
func marshalDetail(d reasoningDetail) jsontext.Value {
	out, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	return out
}
