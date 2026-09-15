package goodall

import (
	"context"
	"log/slog"
	"slices"
	"strings"
)

// The pre-flight check: what a request asks of a model, measured against what
// the provider's catalogue says the model accepts.
//
// It refuses only what a model is *known* to reject (invariant 11): a fact
// nobody published lets the request through, because a catalogue goodall
// cannot read must never block a call that would have worked. The check is
// worth making because the alternative is a round trip that ends in a 400 —
// paid for in latency, and in a run that has already committed its turn.

// mediaTypePDF is the media type a Document must carry for the check to read
// it as a PDF. A document with any other media type, or none at all, asks
// nothing of the model that goodall knows how to name.
const mediaTypePDF = "application/pdf"

// lookupModel reads the catalogue entry for the run's model, once, before the
// first turn. A provider that lists no models, and a catalogue that cannot
// answer, both leave the run with no facts and no pre-flight: the catalogue
// can lag the API, so a model it has never heard of is a warning rather than
// a failed run.
func (r *run) lookupModel(ctx context.Context) *ModelInfo {
	lister, ok := r.agent.Provider.(ModelLister)
	if !ok {
		return nil
	}
	info, err := lister.Model(ctx, r.agent.Model)
	if err != nil {
		r.agent.log(ctx, slog.LevelWarn, "goodall: the model catalogue could not be read, so the run sends without checking capabilities",
			"model", r.agent.Model, "error", err.Error())
		return nil
	}
	return info
}

// checkCapabilities reports the first fact a request asks of its model that
// the model is known to reject, or nil when there is nothing to object to. It
// is a pure function of the Request, so a consumer who built one by hand can
// ask the same question the loop asks.
//
// It runs on every turn rather than once per run, because what a request
// carries changes as the run goes: a tool result can carry an image that the
// caller's first message did not.
func checkCapabilities(req *Request) *CapabilityError {
	info := req.ModelInfo
	if info == nil {
		return nil
	}
	for _, want := range requiredCapabilities(req) {
		if info.Capabilities.Get(want) == Unsupported {
			return &CapabilityError{
				Provider:   info.Provider,
				Model:      req.Model,
				Capability: want,
			}
		}
	}
	return nil
}

// requiredCapabilities is every fact a request asks of the model, deduplicated
// and in a stable order: the parameters of the turn first, then what the
// messages carry, oldest first. The order decides which fact a refusal names
// when a request would fail several checks, so it is fixed rather than
// incidental.
func requiredCapabilities(req *Request) []Capability {
	var required []Capability
	need := func(c Capability) {
		if !slices.Contains(required, c) {
			required = append(required, c)
		}
	}

	if len(req.Tools) > 0 {
		need(CapTools)
	}
	// EffortOff asks the model not to think, which no model needs a
	// thinking capability to obey; the default asks for nothing at all.
	if e := req.Thinking.Effort; e != EffortDefault && e != EffortOff {
		need(CapThinking)
	}
	// Only CacheManual sends the caller's own markers. Under CacheAuto a
	// marker on a block is ignored, so it asks nothing of the model, and
	// the breakpoint the policy adds is the provider's business.
	manual := req.Cache == CacheManual
	for _, text := range req.System {
		if manual && text.Cache != nil {
			need(CapCacheControl)
		}
	}
	for _, msg := range req.Messages {
		scanBlocks(msg.Content, manual, need)
	}
	return required
}

// scanBlocks names what one block list asks for, following tool results into
// their own content: a tool that answers with an image asks as much of the
// model as a caller who sent one.
//
// Tool use and tool result blocks name no capability of their own. They are
// history, and the request that carries them carries the tool definitions
// too, which is what the check reads; a conversation replayed without its
// tools is a shape the loop never builds.
func scanBlocks(blocks Blocks, manual bool, need func(Capability)) {
	for _, block := range blocks {
		var marker *CacheControl
		switch b := block.(type) {
		case Text:
			marker = b.Cache
		case Image:
			need(CapImageInput)
			marker = b.Cache
		case Document:
			if isPDF(b.Source.MediaType) {
				need(CapPDFInput)
			}
			marker = b.Cache
		case ToolUse:
			marker = b.Cache
		case ToolResult:
			marker = b.Cache
			scanBlocks(b.Content, manual, need)
		}
		if manual && marker != nil {
			need(CapCacheControl)
		}
	}
}

// isPDF reads a media type as a PDF, ignoring case and any parameters after
// it, which is how the header it came from is defined.
func isPDF(mediaType string) bool {
	base, _, _ := strings.Cut(mediaType, ";")
	return strings.EqualFold(strings.TrimSpace(base), mediaTypePDF)
}
