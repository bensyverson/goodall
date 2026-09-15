package anthropic

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"maps"
	"slices"

	"github.com/bensyverson/goodall"
)

// maxCacheBreakpoints is the number of explicit cache_control markers
// Anthropic accepts on one request. Under goodall.CacheManual the caller's
// markers are counted here and a fifth is refused before the call, because
// the alternative is a 400 with a whole request's tokens already spent.
const maxCacheBreakpoints = 4

// emptyToolInput is what a tool call with no arguments sends: Anthropic
// requires the input member, and the empty object is the honest spelling.
var emptyToolInput = jsontext.Value(`{}`)

// translateRequest turns a neutral request into the Messages API body and the
// beta features the call needs in its anthropic-beta header.
//
// Stream is left unset: the client sets it, because the same body serves the
// streaming and the blocking path.
func translateRequest(req *goodall.Request) (*wireRequest, []Beta, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("anthropic: %w: a nil request", goodall.KindInvalidRequest)
	}
	ext, err := extensionsFor(req.Extensions)
	if err != nil {
		return nil, nil, err
	}
	if err := req.ToolChoice.Validate(); err != nil {
		return nil, nil, err
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}

	tr := &translator{policy: req.Cache}
	system, err := tr.system(req.System)
	if err != nil {
		return nil, nil, err
	}
	messages, err := tr.messages(req.Messages)
	if err != nil {
		return nil, nil, err
	}
	if req.Cache == goodall.CacheManual && tr.markers > maxCacheBreakpoints {
		return nil, nil, fmt.Errorf("anthropic: %w: the request carries %d cache markers and Anthropic accepts at most %d",
			goodall.KindInvalidRequest, tr.markers, maxCacheBreakpoints)
	}
	if req.Cache == goodall.CacheAuto {
		markLastSystemBlock(system)
	}

	tools, err := translateTools(req.Tools)
	if err != nil {
		return nil, nil, err
	}
	thinking, output, err := thinkingFor(req.Model, req.Thinking, maxTokens)
	if err != nil {
		return nil, nil, err
	}
	metadata, err := translateMetadata(req.Metadata)
	if err != nil {
		return nil, nil, err
	}

	body := &wireRequest{
		Model:         req.Model,
		MaxTokens:     maxTokens,
		System:        system,
		Tools:         tools,
		ToolChoice:    translateToolChoice(req.ToolChoice),
		Thinking:      thinking,
		OutputConfig:  output,
		StopSequences: req.StopSequences,
		Metadata:      metadata,
		ServiceTier:   ext.ServiceTier,
		Messages:      messages,
	}
	if req.Cache == goodall.CacheAuto {
		body.CacheControl = &wireCacheControl{Type: cacheEphemeral}
	}
	return body, slices.Clone(ext.Betas), nil
}

// translator carries the cache policy down through nested blocks and counts
// the markers it emits, so the four-breakpoint limit is checked once over the
// whole request rather than per message.
type translator struct {
	policy  goodall.CachePolicy
	markers int
}

// cache translates a caller's marker under the request's policy: CacheManual
// sends it, CacheAuto and CacheOff drop it, since under CacheAuto the
// breakpoints are goodall's to place.
func (t *translator) cache(c *goodall.CacheControl) *wireCacheControl {
	if c == nil || t.policy != goodall.CacheManual {
		return nil
	}
	t.markers++
	return &wireCacheControl{Type: cacheEphemeral, TTL: c.TTL}
}

// system translates the standing instruction. It is an array of text blocks
// rather than a bare string so that a cache breakpoint can sit on it.
func (t *translator) system(text []goodall.Text) (wireBlocks, error) {
	if len(text) == 0 {
		return nil, nil
	}
	out := make(wireBlocks, 0, len(text))
	for _, blk := range text {
		out = append(out, wireText{Text: blk.Text, Cache: t.cache(blk.Cache)})
	}
	return out, nil
}

// messages translates the conversation. A mid-conversation system message
// keeps its role: both current model families accept one, and folding it into
// the system prompt would rewrite the cached prefix.
// The result is never nil, so a request built with no conversation sends an
// empty array rather than null and is refused for the reason it deserves.
func (t *translator) messages(msgs []goodall.Message) ([]wireMessage, error) {
	out := make([]wireMessage, 0, len(msgs))
	for i, m := range msgs {
		content, err := t.blocks(m.Content)
		if err != nil {
			return nil, fmt.Errorf("anthropic: message %d: %w", i, err)
		}
		out = append(out, wireMessage{Role: m.Role, Content: content})
	}
	return out, nil
}

// blocks translates a content list. The result is never nil, so an empty tool
// result sends "content": [] rather than null.
func (t *translator) blocks(blocks goodall.Blocks) (wireBlocks, error) {
	out := make(wireBlocks, 0, len(blocks))
	for _, b := range blocks {
		w, err := t.block(b)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// block translates one neutral block.
//
// A thinking block is rebuilt from its text and signature rather than replayed
// from Raw: on Anthropic those two members are the whole block, so the rebuilt
// bytes are the bytes that arrived, and goodall's Raw is reserved for
// providers whose blocks carry more (OpenRouter's reasoning_details). An
// Unknown block is the opposite case and goes back exactly as it came.
func (t *translator) block(b goodall.Block) (wireBlock, error) {
	switch v := b.(type) {
	case goodall.Text:
		return wireText{Text: v.Text, Cache: t.cache(v.Cache)}, nil
	case goodall.Image:
		return wireImage{Source: translateSource(v.Source), Cache: t.cache(v.Cache)}, nil
	case goodall.Document:
		return wireDocument{
			Source:  translateSource(v.Source),
			Title:   v.Title,
			Context: v.Context,
			Cache:   t.cache(v.Cache),
		}, nil
	case goodall.ToolUse:
		return wireToolUse{ID: v.ID, Name: v.Name, Input: toolInput(v.Input), Cache: t.cache(v.Cache)}, nil
	case goodall.ToolResult:
		// The marker on the result itself is counted before the markers
		// inside it, so the error names the earlier breakpoint first.
		cache := t.cache(v.Cache)
		content, err := t.blocks(v.Content)
		if err != nil {
			return nil, err
		}
		return wireToolResult{ToolUseID: v.ToolUseID, IsError: v.IsError, Content: content, Cache: cache}, nil
	case goodall.Thinking:
		return wireThinking{Thinking: v.Text, Signature: v.Signature}, nil
	case goodall.RedactedThinking:
		return wireRedactedThinking{Data: v.Data}, nil
	case goodall.Unknown:
		return wireUnknown{Type: blockType(v.Type), Raw: v.Raw}, nil
	case nil:
		return nil, fmt.Errorf("anthropic: %w: a nil content block", goodall.KindInvalidRequest)
	default:
		return nil, fmt.Errorf("anthropic: %w: a %T content block", goodall.KindInvalidRequest, b)
	}
}

// translateSource maps a neutral source onto Anthropic's three input sources.
// A source kind goodall adds later passes through under its own name rather
// than being forced into one of the three: Anthropic refuses what it cannot
// read, which is a clearer failure than silently sending the wrong source.
func translateSource(s goodall.Source) wireSource {
	w := wireSource{
		MediaType: s.MediaType,
		Data:      s.Data,
		URL:       s.URL,
		FileID:    s.FileID,
	}
	switch s.Type {
	case goodall.SourceBytes:
		w.Type = sourceBase64
	case goodall.SourceURL:
		w.Type = sourceURL
	case goodall.SourceFile:
		w.Type = sourceFile
	default:
		w.Type = sourceType(s.Type)
	}
	return w
}

// toolInput reads an absent input as the empty object, which is what a call
// to a tool with no parameters means.
func toolInput(input jsontext.Value) jsontext.Value {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return emptyToolInput
	}
	return input
}

// markLastSystemBlock places the one explicit breakpoint the auto policy asks
// for. Anthropic's top-level marker caches the tools and the messages; the
// explicit one pins the static system prompt, which is the shape its guidance
// recommends for an agent loop.
func markLastSystemBlock(system wireBlocks) {
	n := len(system)
	if n == 0 {
		return
	}
	if text, ok := system[n-1].(wireText); ok {
		text.Cache = &wireCacheControl{Type: cacheEphemeral}
		system[n-1] = text
	}
}

// translateTools sends the tools in the order given: the order is part of the
// cached prefix and of what a thinking signature binds to.
func translateTools(tools []goodall.Tool) ([]wireTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]wireTool, 0, len(tools))
	for i, tool := range tools {
		if tool == nil {
			return nil, fmt.Errorf("anthropic: %w: tool %d is nil", goodall.KindInvalidRequest, i)
		}
		name := tool.Name()
		if name == "" {
			return nil, fmt.Errorf("anthropic: %w: tool %d has no name", goodall.KindInvalidRequest, i)
		}
		schema := tool.Schema()
		if schema == nil {
			return nil, fmt.Errorf("anthropic: %w: tool %q has no input schema", goodall.KindInvalidRequest, name)
		}
		out = append(out, wireTool{Name: name, Description: tool.Description(), InputSchema: schema})
	}
	return out, nil
}

// translateToolChoice maps the neutral choice onto Anthropic's object. The
// default — the model decides, parallel calls allowed — sends nothing, so a
// caller who never thinks about tool choice adds no bytes to the prefix. A
// mode goodall does not define passes through verbatim rather than being
// flattened into "auto".
func translateToolChoice(c goodall.ToolChoice) *wireToolChoice {
	switch c.Mode {
	case goodall.ToolChoiceAuto:
		if !c.NoParallel {
			return nil
		}
		return &wireToolChoice{Type: toolChoiceAuto, DisableParallelToolUse: true}
	case goodall.ToolChoiceAny:
		return &wireToolChoice{Type: toolChoiceAny, DisableParallelToolUse: c.NoParallel}
	case goodall.ToolChoiceNamed:
		return &wireToolChoice{Type: toolChoiceTool, Name: c.Name, DisableParallelToolUse: c.NoParallel}
	case goodall.ToolChoiceNone:
		// Anthropic documents disable_parallel_tool_use on auto, any and
		// tool only, and "none" forbids every call anyway.
		return &wireToolChoice{Type: toolChoiceNone}
	default:
		return &wireToolChoice{Type: toolChoiceType(c.Mode), Name: c.Name, DisableParallelToolUse: c.NoParallel}
	}
}

// metadataUserID is the one metadata key Anthropic defines: an opaque
// end-user identifier used for abuse monitoring.
const metadataUserID = "user_id"

// translateMetadata maps the neutral metadata map onto Anthropic's one
// member. A key Anthropic does not define is an error rather than a silent
// drop: metadata a caller believed was sent and was not is worse than a
// refusal they can see.
func translateMetadata(md map[string]string) (*wireMetadata, error) {
	if len(md) == 0 {
		return nil, nil
	}
	for _, key := range slices.Sorted(maps.Keys(md)) {
		if key != metadataUserID {
			return nil, fmt.Errorf("anthropic: %w: request metadata %q is not a key Anthropic accepts; it takes %q alone",
				goodall.KindInvalidRequest, key, metadataUserID)
		}
	}
	id := md[metadataUserID]
	if id == "" {
		return nil, nil
	}
	return &wireMetadata{UserID: id}, nil
}

// translateResponse turns a decoded message into the neutral response. Cost
// stays at its zero value — Anthropic reports tokens and no money — and so
// does NativeStopReason, because Anthropic's stop string is the StopReason
// rather than a router's normalisation of one.
func translateResponse(w *wireResponse) *goodall.Response {
	role := w.Role
	if role == "" {
		role = goodall.RoleAssistant
	}
	return &goodall.Response{
		ID:    w.ID,
		Model: w.Model,
		Message: goodall.Message{
			Role:    role,
			Content: neutralBlocks(w.Content),
		},
		StopReason:   w.StopReason,
		StopSequence: w.StopSequence,
		Usage:        w.Usage.neutral(),
	}
}

// neutral maps Anthropic's token ledger onto goodall's. The thinking share of
// the output is reported under output_tokens_details, and a response that
// carries no such member leaves Reasoning at zero — which is what a turn that
// did not think reports in any case.
func (u wireUsage) neutral() goodall.Usage {
	usage := goodall.Usage{
		Input:      u.InputTokens,
		Output:     u.OutputTokens,
		CacheRead:  u.CacheReadInputTokens,
		CacheWrite: u.CacheCreationInputTokens,
	}
	if d := u.OutputTokensDetails; d != nil {
		usage.Reasoning = d.ThinkingTokens
	}
	return usage
}

// neutralBlocks translates a decoded content list into neutral blocks.
func neutralBlocks(blocks wireBlocks) goodall.Blocks {
	if len(blocks) == 0 {
		return nil
	}
	out := make(goodall.Blocks, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, neutralBlock(b))
	}
	return out
}

// neutralBlock translates one decoded block. A thinking block decodes with an
// empty Raw: the neutral text and signature rebuild it exactly, and carrying
// the bytes as well would mean two sources of truth for one block.
func neutralBlock(b wireBlock) goodall.Block {
	switch v := b.(type) {
	case wireText:
		return goodall.Text{Text: v.Text}
	case wireImage:
		return goodall.Image{Source: neutralSource(v.Source)}
	case wireDocument:
		return goodall.Document{Source: neutralSource(v.Source), Title: v.Title, Context: v.Context}
	case wireToolUse:
		return goodall.ToolUse{ID: v.ID, Name: v.Name, Input: v.Input}
	case wireToolResult:
		return goodall.ToolResult{ToolUseID: v.ToolUseID, IsError: v.IsError, Content: neutralBlocks(v.Content)}
	case wireThinking:
		return goodall.Thinking{Text: v.Thinking, Signature: v.Signature}
	case wireRedactedThinking:
		return goodall.RedactedThinking{Data: v.Data}
	case wireUnknown:
		return goodall.Unknown{Type: goodall.BlockType(v.Type), Raw: v.Raw}
	default:
		// Unreachable while wireBlock is sealed to this package, and a
		// surfaced value rather than a panic if it ever is not.
		return goodall.Unknown{}
	}
}

// neutralSource maps an Anthropic source back onto the neutral one, keeping
// a kind goodall does not model rather than dropping it.
func neutralSource(s wireSource) goodall.Source {
	out := goodall.Source{
		MediaType: s.MediaType,
		Data:      s.Data,
		URL:       s.URL,
		FileID:    s.FileID,
	}
	switch s.Type {
	case sourceBase64:
		out.Type = goodall.SourceBytes
	case sourceURL:
		out.Type = goodall.SourceURL
	case sourceFile:
		out.Type = goodall.SourceFile
	default:
		out.Type = goodall.SourceType(s.Type)
	}
	return out
}
