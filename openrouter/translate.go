package openrouter

import (
	"fmt"
	"slices"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/claudename"
	"github.com/bensyverson/goodall/internal/strict"
)

// maxCacheBreakpoints is how many explicit prompt-cache breakpoints one
// request may carry. It is the providers' shared limit, checked here so the
// caller hears about it instead of the model's answer arriving uncached.
const maxCacheBreakpoints = 4

// metadataUserKey is the goodall.Request.Metadata key that becomes the wire's
// top-level end-user identifier. It is spelled the way Anthropic's metadata
// spells it, so one request carries an end-user id to either provider.
const metadataUserKey = "user_id"

// translator carries the decisions that apply to a whole request: which
// members the dialect accepts, and where cache breakpoints may go. It also
// counts the breakpoints it wrote, so the limit is checked once at the end
// rather than guessed at each block.
type translator struct {
	quirks  Quirks
	policy  goodall.CachePolicy
	markers int
}

// translateRequest turns a neutral request into a Chat Completions body for
// the given dialect. Streaming is the caller's choice rather than a property
// of the request, so it is a parameter.
//
// Blocks the wire has no place for are left out: an Unknown block in an
// assistant turn (its bytes are another provider's, and OpenRouter would
// reject them), and an image, document or tool result in an assistant turn
// (the model never produces one, and the assistant role takes a plain
// string). A cache marker on a block that becomes no content part — a tool
// result, a tool call, an assistant text — has nowhere on the wire to sit and
// is ignored.
func translateRequest(req *goodall.Request, d Dialect, stream bool) (*chatRequest, error) {
	if err := req.ToolChoice.Validate(); err != nil {
		return nil, err
	}
	if err := claudename.RefuseForcing(ProviderName, req); err != nil {
		return nil, err
	}
	ext, err := extensionsOf(req)
	if err != nil {
		return nil, err
	}
	strictTools, err := checkStrict(req, d)
	if err != nil {
		return nil, err
	}

	t := &translator{quirks: d.Quirks(), policy: req.Cache}
	if !t.quirks.CacheControl {
		t.policy = goodall.CacheOff
	}

	out := &chatRequest{
		Model:      req.Model,
		Tools:      wireTools(req.Tools),
		ToolChoice: wireToolChoice(req.ToolChoice),
		Stop:       req.StopSequences,
		Stream:     stream,
	}
	if strictTools && t.quirks.StrictTools == StrictToolsWithAnthropicBeta {
		out.anthropicBeta = anthropicStructuredOutputsBeta
	}
	t.applyOutputCap(req.MaxTokens, out)
	if stream && t.quirks.StreamUsage {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if req.ToolChoice.NoParallel {
		serial := false
		out.ParallelToolCalls = &serial
	}
	if out.Messages, err = t.messages(req); err != nil {
		return nil, err
	}
	t.applyReasoning(req.Thinking, req.ModelInfo, out)
	t.applyMetadata(req.Metadata, out)
	t.applyExtensions(ext, out)
	if t.policy == goodall.CacheAuto {
		out.CacheControl = &cacheControl{Type: cacheEphemeral}
	}
	if t.markers > maxCacheBreakpoints {
		return nil, fmt.Errorf("openrouter: %w: the request sets %d cache breakpoints and at most %d may be sent",
			goodall.KindInvalidRequest, t.markers, maxCacheBreakpoints)
	}
	return out, nil
}

// extensionsOf reads this package's extensions off the request, rejecting
// another provider's rather than dropping the options it carries.
func extensionsOf(req *goodall.Request) (Extensions, error) {
	switch ext := req.Extensions.(type) {
	case nil:
		return Extensions{}, nil
	case Extensions:
		return ext, nil
	case *Extensions:
		if ext == nil {
			return Extensions{}, nil
		}
		return *ext, nil
	default:
		return Extensions{}, fmt.Errorf("openrouter: %w: the request carries %s extensions",
			goodall.KindInvalidRequest, ext.Provider())
	}
}

// checkStrict refuses, before sending, a request whose strict tools the
// dialect cannot carry or the upstream model would refuse, and reports
// whether the request carries any. The rules are the model family's:
// api.openai.com serves only OpenAI's, and through OpenRouter the family is
// read from the model name, so a model whose rules goodall has not measured
// is sent as it is.
func checkStrict(req *goodall.Request, d Dialect) (bool, error) {
	if !slices.ContainsFunc(req.Tools, func(t goodall.Tool) bool {
		return t != nil && goodall.StrictnessOf(t) == goodall.Strict
	}) {
		return false, nil
	}
	rules := strict.RulesFor(req.Model)
	switch d.Quirks().StrictTools {
	case StrictToolsRefused:
		return true, &goodall.CapabilityError{
			Provider:   ProviderName,
			Model:      req.Model,
			Capability: goodall.CapStrictTools,
			Message:    "the " + d.String() + " dialect carries no strict flag; build the tool without goodall.Strict",
		}
	case StrictToolsMember:
		if d == OpenAI {
			rules = strict.RulesOpenAI
		}
	}
	return true, strict.Check(ProviderName, req, rules)
}

// wireTools renders the request's tools. Order is preserved, because it is
// part of the cached prefix, and each tool's schema marshals its properties
// in declaration order for the same reason.
func wireTools(tools []goodall.Tool) []wireTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]wireTool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, wireTool{
			Type: functionTool,
			Function: toolFunction{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Schema(),
				Strict:      goodall.StrictnessOf(tool) == goodall.Strict,
			},
		})
	}
	return out
}

// wireToolChoice renders the tool choice, sending nothing for the zero value:
// "auto" is the wire's own default, and a request that never thought about
// tool choice should send no member at all. A mode goodall does not define is
// passed through verbatim rather than silently becoming "auto".
func wireToolChoice(c goodall.ToolChoice) *toolChoice {
	switch c.Mode {
	case goodall.ToolChoiceAuto:
		return nil
	case goodall.ToolChoiceNone:
		return &toolChoice{Mode: "none"}
	case goodall.ToolChoiceAny:
		return &toolChoice{Mode: "required"}
	case goodall.ToolChoiceNamed:
		return &toolChoice{Name: c.Name}
	default:
		return &toolChoice{Mode: string(c.Mode)}
	}
}

// marker renders a caller's cache marker, but only under CacheManual on a
// dialect that sends them: CacheAuto places its own single breakpoint and
// ignores the caller's, and CacheOff places none.
func (t *translator) marker(c *goodall.CacheControl) *cacheControl {
	if c == nil || t.policy != goodall.CacheManual {
		return nil
	}
	t.markers++
	return &cacheControl{Type: cacheEphemeral, TTL: c.TTL}
}

// applyOutputCap writes the output-token cap into the member the dialect reads
// it from. The two names are not interchangeable: api.openai.com refuses
// max_tokens on a reasoning model rather than ignoring it, and a request that
// sent both would be refused for the one it should not have sent.
func (t *translator) applyOutputCap(max int, out *chatRequest) {
	if max == 0 {
		return
	}
	if t.quirks.OutputCap == CapMaxCompletionTokens {
		out.MaxCompletion = max
		return
	}
	out.MaxTokens = max
}

// applyReasoning writes the thinking configuration in the shape the dialect
// takes. goodall's effort rungs are the wire's own strings, so the mapping is
// the identity except that EffortDefault sends nothing and EffortOff sends
// "none", which OpenRouter documents as disabling reasoning entirely. A rung
// goodall does not define is passed through: the ladder keeps growing, and a
// clamped value would quietly buy less thinking than the caller asked for.
//
// A model whose catalog entry says its reasoning cannot be turned off gets no
// effort for EffortOff, as [goodall.EffortOff] promises: OpenRouter answers
// "none" on such a model with a 400.
//
// The exclude flag has no equivalent in OpenAI's top-level reasoning_effort
// string, so a display setting is ignored on those dialects.
func (t *translator) applyReasoning(cfg goodall.ThinkingConfig, info *goodall.ModelInfo, out *chatRequest) {
	effort := effortString(cfg.Effort)
	if cfg.Effort == goodall.EffortOff && info != nil && info.Capabilities.ThinkingOff == goodall.ThinkingOffNever {
		effort = ""
	}
	switch t.quirks.Reasoning {
	case ReasoningObject:
		exclude := cfg.Display == goodall.DisplayOmitted
		if effort == "" && !exclude {
			return
		}
		out.Reasoning = &reasoningConfig{Effort: effort, Exclude: exclude}
	case ReasoningEffortField:
		out.ReasoningEffort = effort
	}
}

// effortString is the wire spelling of an effort rung.
func effortString(e goodall.Effort) string {
	switch e {
	case goodall.EffortDefault:
		return ""
	case goodall.EffortOff:
		return "none"
	default:
		return string(e)
	}
}

// applyMetadata writes the request's metadata. The key "user_id" is lifted to
// the wire's top-level end-user identifier, which is the slot OpenRouter
// documents for abuse isolation and the thing goodall.Request.Metadata names
// as its example; the rest travels in the metadata object, sorted by key so
// that two identical requests produce identical bytes.
func (t *translator) applyMetadata(m map[string]string, out *chatRequest) {
	if !t.quirks.Metadata || len(m) == 0 {
		return
	}
	rest := make(metadata, len(m))
	for k, v := range m {
		if k == metadataUserKey {
			out.User = v
			continue
		}
		rest[k] = v
	}
	if len(rest) > 0 {
		out.Metadata = rest
	}
}

// applyExtensions writes the OpenRouter-only options, each gated on the
// dialect understanding it.
func (t *translator) applyExtensions(ext Extensions, out *chatRequest) {
	if t.quirks.Routing {
		out.Provider = ext.Routing
		out.Models = ext.Models
	}
	if t.quirks.Plugins {
		out.Plugins = ext.Plugins
	}
	if t.quirks.SessionID {
		out.SessionID = ext.SessionID
	}
	if t.quirks.Debug && ext.Debug != (Debug{}) {
		debug := ext.Debug
		out.Debug = &debug
	}
}
