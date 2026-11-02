// A neutral request as a body for OpenAI's /responses. See
// project/2026-11-02-responses-wire-plan.md for what was recorded behind each
// choice here.

package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"strings"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/claudename"
)

// instructionSeparator joins the blocks of a system prompt, since instructions is
// one string where the Chat path sends parts.
const instructionSeparator = "\n\n"

// translateResponsesRequest turns a neutral request into a /responses body.
//
// Stop sequences are refused because the wire has no member for them: stop and
// stop_sequences are both a 400 unknown_parameter (the stop_member and
// stop_sequences_member recordings), and dropping them would change what the
// caller's run does with no word said.
//
// Cache markers have nowhere to go, as on the Chat path to OpenAI: the server
// caches a prefix by itself.
func translateResponsesRequest(req *goodall.Request, d Dialect, stream bool) (*responsesRequest, error) {
	if err := req.ToolChoice.Validate(); err != nil {
		return nil, err
	}
	if err := claudename.RefuseForcing(ProviderName, req); err != nil {
		return nil, err
	}
	if _, err := extensionsOf(req); err != nil {
		return nil, err
	}
	if _, err := checkStrict(req, d); err != nil {
		return nil, err
	}
	if len(req.StopSequences) > 0 {
		return nil, &goodall.CapabilityError{
			Provider:   ProviderName,
			Model:      req.Model,
			Capability: goodall.CapStopSequences,
			Message:    "OpenAI's Responses API has no member for stop sequences",
		}
	}

	input, err := responsesInput(req.Messages)
	if err != nil {
		return nil, err
	}
	out := &responsesRequest{
		Model:           req.Model,
		Instructions:    responsesInstructions(req.System),
		Tools:           responsesTools(req.Tools),
		ToolChoice:      responsesChoice(req.ToolChoice),
		Reasoning:       responsesThinking(req.Thinking, req.ModelInfo),
		MaxOutputTokens: req.MaxTokens,
		Stream:          stream,
		Input:           input,
	}
	if req.ToolChoice.NoParallel {
		serial := false
		out.ParallelToolCalls = &serial
	}
	if d.Quirks().Metadata {
		out.Metadata, out.SafetyIdentifier = responsesMetadata(req.Metadata)
	}
	return out, nil
}

// responsesInstructions is the standing system prompt as one string.
func responsesInstructions(system []goodall.Text) string {
	parts := make([]string, 0, len(system))
	for _, text := range system {
		parts = append(parts, text.Text)
	}
	return strings.Join(parts, instructionSeparator)
}

// responsesTools renders the tools. Order is preserved, because it is part of the
// cached prefix.
func responsesTools(tools []goodall.Tool) []responsesTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]responsesTool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, responsesTool{
			Type:        functionTool,
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Schema(),
			Strict:      goodall.StrictnessOf(tool) == goodall.Strict,
		})
	}
	return out
}

// responsesChoice renders the tool choice, sending nothing for the zero value. A
// mode goodall does not define is passed through rather than becoming "auto".
func responsesChoice(c goodall.ToolChoice) *responsesToolChoice {
	switch c.Mode {
	case goodall.ToolChoiceAuto:
		return nil
	case goodall.ToolChoiceAny:
		return &responsesToolChoice{Mode: "required"}
	case goodall.ToolChoiceNamed:
		return &responsesToolChoice{Name: c.Name}
	default:
		return &responsesToolChoice{Mode: string(c.Mode)}
	}
}

// responsesThinking is the reasoning member, or nil when there is nothing to say.
//
// Only an explicit [goodall.DisplaySummarized] asks for a summary. The default
// display means whatever the provider defaults to, and summary_absent records that
// as no summary at all; omitted is the same request. Effort off asks for neither,
// and a model whose catalog entry says its reasoning cannot be turned off gets no
// member, as [goodall.EffortOff] promises.
func responsesThinking(cfg goodall.ThinkingConfig, info *goodall.ModelInfo) *responsesReasoning {
	if cfg.Effort == goodall.EffortOff {
		if info != nil && info.Capabilities.ThinkingOff == goodall.ThinkingOffNever {
			return nil
		}
		return &responsesReasoning{Effort: effortString(cfg.Effort)}
	}
	out := responsesReasoning{Effort: effortString(cfg.Effort)}
	if cfg.Display == goodall.DisplaySummarized {
		out.Summary = reasoningSummaryAuto
	}
	if out == (responsesReasoning{}) {
		return nil
	}
	return &out
}

// responsesMetadata splits the request's metadata: the key "user_id" becomes the
// safety identifier and the rest travels as metadata.
func responsesMetadata(m map[string]string) (metadata, string) {
	var user string
	var rest metadata
	for k, v := range m {
		if k == metadataUserKey {
			user = v
			continue
		}
		if rest == nil {
			rest = make(metadata, len(m))
		}
		rest[k] = v
	}
	return rest, user
}

// responsesInput builds the item list, one neutral message at a time.
func responsesInput(messages []goodall.Message) ([]jsontext.Value, error) {
	var out []jsontext.Value
	for i, m := range messages {
		var items []jsontext.Value
		var err error
		switch m.Role {
		case goodall.RoleAssistant:
			items, err = responsesAssistantItems(m)
		case goodall.RoleSystem:
			items, err = responsesTurnItems(m, roleSystem)
		default:
			items, err = responsesTurnItems(m, roleUser)
		}
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		out = append(out, items...)
	}
	return out, nil
}

// responsesTurnItems renders a user or system turn: its tool results first, in
// block order, since each answers a call the model is waiting on, then a message
// holding what remains.
func responsesTurnItems(m goodall.Message, r role) ([]jsontext.Value, error) {
	var items []jsontext.Value
	var parts []responsesPart
	for _, blk := range m.Content {
		if result, ok := blk.(goodall.ToolResult); ok {
			item, err := responsesToolOutput(result)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
			continue
		}
		part, ok, err := responsesInputPart(blk)
		if err != nil {
			return nil, err
		}
		if ok {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return items, nil
	}
	message, err := marshalItem(responsesMessage{Type: itemMessage, Role: r, Content: parts})
	if err != nil {
		return nil, err
	}
	return append(items, message), nil
}

// responsesInputPart converts one block to an input part. The second result is
// false for a block with no part type, and for empty text, which says nothing and
// which the wire has no way to carry in a part whose text is required.
func responsesInputPart(blk goodall.Block) (responsesPart, bool, error) {
	switch b := blk.(type) {
	case goodall.Text:
		return responsesPart{Type: partInputText, Text: b.Text}, b.Text != "", nil
	case goodall.Image:
		url, err := imageSourceURL(b.Source)
		if err != nil {
			return responsesPart{}, false, err
		}
		return responsesPart{Type: partInputImage, ImageURL: url}, true, nil
	case goodall.Document:
		part, err := responsesFilePart(b)
		return part, err == nil, err
	default:
		return responsesPart{}, false, nil
	}
}

// responsesFilePart renders a document: inline bytes as a named data URL, as the
// media recording shows, and a URL as file_url, as the file_url recording shows
// with no filename beside it. A provider file id names an upload goodall never
// made, so documentSourceData refuses it.
func responsesFilePart(d goodall.Document) (responsesPart, error) {
	data, err := documentSourceData(d.Source)
	if err != nil {
		return responsesPart{}, err
	}
	if d.Source.Type == goodall.SourceURL {
		return responsesPart{Type: partInputFile, FileURL: data}, nil
	}
	return responsesPart{Type: partInputFile, Filename: documentFilename(d), FileData: data}, nil
}

// responsesToolOutput renders a tool result: a string when it holds only text, and
// a list of parts when it holds more, which the tool_result_image recording shows
// reaches the model. The wire has no is_error flag, so a failure is told in the
// text the model reads, as on the Chat path.
func responsesToolOutput(r goodall.ToolResult) (jsontext.Value, error) {
	output, err := responsesOutputValue(r)
	if err != nil {
		return nil, err
	}
	return marshalItem(responsesFunctionOutput{Type: itemFunctionCallOutput, CallID: r.ToolUseID, Output: output})
}

// responsesOutputValue is the output member of a tool result.
func responsesOutputValue(r goodall.ToolResult) (jsontext.Value, error) {
	onlyText := true
	for _, blk := range r.Content {
		if _, ok := blk.(goodall.Text); !ok {
			onlyText = false
		}
	}
	if onlyText {
		return marshalItem(toolResultText(r))
	}
	var parts []responsesPart
	if r.IsError {
		parts = append(parts, responsesPart{Type: partInputText, Text: toolErrorPrefix})
	}
	for _, blk := range r.Content {
		part, ok, err := responsesInputPart(blk)
		if err != nil {
			return nil, err
		}
		if !ok {
			if _, isText := blk.(goodall.Text); isText {
				continue
			}
			part = responsesPart{Type: partInputText, Text: blockPlaceholder(blk)}
		}
		parts = append(parts, part)
	}
	return marshalItem(parts)
}

// responsesAssistantItems renders a model turn, keeping its blocks in order. A
// run of text blocks is one message; a tool use is a function_call whose call_id
// is the block's ID; a reasoning item goes back as the bytes it arrived in
// (invariant 2).
//
// Thinking with no reasoning item in Raw and redacted thinking are left out, which
// keeps a history from another provider sendable, as OpenAI's own documentation
// describes for incompatible reasoning.
//
// An unknown block goes back as its bytes, in order. It may be an output item this
// version of goodall does not recognize, and the server accepts a lost item with a
// 200, so leaving it out could lose it silently. If it is another provider's, the
// server's 400 is the failure to prefer. With no valid bytes there is nothing to send.
func responsesAssistantItems(m goodall.Message) ([]jsontext.Value, error) {
	var items []jsontext.Value
	var text []responsesPart
	flush := func() error {
		if len(text) == 0 {
			return nil
		}
		item, err := marshalItem(responsesMessage{Type: itemMessage, Role: roleAssistant, Content: text})
		if err != nil {
			return err
		}
		items, text = append(items, item), nil
		return nil
	}
	for _, blk := range m.Content {
		switch b := blk.(type) {
		case goodall.Text:
			if b.Text != "" {
				text = append(text, responsesPart{Type: partOutputText, Text: b.Text})
			}
		case goodall.Thinking:
			if isReasoningItem(b.Raw) {
				if err := flush(); err != nil {
					return nil, err
				}
				items = append(items, b.Raw)
			}
		case goodall.Unknown:
			if len(b.Raw) > 0 && b.Raw.IsValid() {
				if err := flush(); err != nil {
					return nil, err
				}
				items = append(items, b.Raw)
			}
		case goodall.ToolUse:
			if err := flush(); err != nil {
				return nil, err
			}
			item, err := marshalItem(responsesFunctionCall{
				Type:      itemFunctionCall,
				CallID:    b.ID,
				Name:      b.Name,
				Arguments: toolArguments(b.Input),
			})
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return items, nil
}

// isReasoningItem reports whether a thinking block's bytes are this wire's. A
// Claude block has no Raw, and an OpenRouter reasoning_details entry has a type of
// its own, such as reasoning.text.
func isReasoningItem(raw jsontext.Value) bool {
	if len(raw) == 0 || !raw.IsValid() {
		return false
	}
	var head responsesOutputItem
	return json.Unmarshal(raw, &head) == nil && head.Type == itemReasoning
}

// marshalItem encodes one built item. A jsontext.Value in it travels as its bytes.
func marshalItem(v any) (jsontext.Value, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("openrouter: encoding an input item: %w", err)
	}
	return out, nil
}
