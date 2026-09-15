package anthropic

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/url"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/transport"
)

// wireSupported is one leaf of the capability tree. Every leaf is an object
// with a "supported" member, so the pointer is what carries the third state:
// a leaf that is absent is a fact Anthropic has not published, which is not
// the same as one it published as false (invariant 11).
type wireSupported struct {
	Supported bool `json:"supported,omitzero"`
}

// support reads a leaf as goodall's tri-state.
func (s *wireSupported) support() goodall.Support {
	switch {
	case s == nil:
		return goodall.SupportUnknown
	case s.Supported:
		return goodall.Supported
	default:
		return goodall.Unsupported
	}
}

// wireThinkingTypes is which thinking configurations a model accepts:
// "adaptive" is the effort-driven form on 4.6 and above, "enabled" the older
// budget form.
type wireThinkingTypes struct {
	Adaptive *wireSupported `json:"adaptive,omitzero"`
	Enabled  *wireSupported `json:"enabled,omitzero"`
}

// wireThinkingCapability is the thinking branch: a "supported" member of its
// own beside the map of thinking types, which is what a live
// GET /v1/models/{id} returns.
type wireThinkingCapability struct {
	Supported *bool             `json:"supported,omitzero"`
	Types     wireThinkingTypes `json:"types,omitzero"`
}

// wireEffortCapability is the effort ladder, rung by rung, under the same kind
// of branch-level "supported" member. A rung a model does not offer is absent
// or false; goodall reports only the ones it offers.
type wireEffortCapability struct {
	Supported *bool          `json:"supported,omitzero"`
	Low       *wireSupported `json:"low,omitzero"`
	Medium    *wireSupported `json:"medium,omitzero"`
	High      *wireSupported `json:"high,omitzero"`
	XHigh     *wireSupported `json:"xhigh,omitzero"`
	Max       *wireSupported `json:"max,omitzero"`
}

// wireCapabilities is the part of the capability tree goodall models. The
// tree carries more — batch, citations, code execution, context management —
// and the members left out here are ignored rather than being an error: a
// catalogue that grows a leaf must not stop a model being usable.
type wireCapabilities struct {
	ImageInput        *wireSupported          `json:"image_input,omitzero"`
	PDFInput          *wireSupported          `json:"pdf_input,omitzero"`
	Thinking          *wireThinkingCapability `json:"thinking,omitzero"`
	Effort            *wireEffortCapability   `json:"effort,omitzero"`
	StructuredOutputs *wireSupported          `json:"structured_outputs,omitzero"`
}

// wireModel is the body of GET /v1/models/{id}.
type wireModel struct {
	ID             string            `json:"id,omitzero"`
	Type           string            `json:"type,omitzero"`
	DisplayName    string            `json:"display_name,omitzero"`
	MaxInputTokens int               `json:"max_input_tokens,omitzero"`
	MaxTokens      int               `json:"max_tokens,omitzero"`
	Capabilities   *wireCapabilities `json:"capabilities,omitzero"`
}

// Model describes one model by its Anthropic identifier, which is what
// goodall.ModelLister asks for. The agent caches the answer and uses it to
// refuse, before the network call, a request carrying an input the model is
// known to reject.
//
// An identifier Anthropic does not know is a *goodall.APIError with
// KindNotFound, not an empty result: "no such model" is an answer worth
// acting on.
func (c *Client) Model(ctx context.Context, id string) (*goodall.ModelInfo, error) {
	resp, err := c.http.Do(ctx, transport.Request{
		Method: http.MethodGet,
		URL:    c.endpoint(modelsPath + "/" + url.PathEscape(id)),
		Header: c.headers(nil),
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := readBody(resp)
	if err != nil {
		return nil, err
	}
	var w wireModel
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, &goodall.ProtocolError{Reason: "the model body could not be decoded: " + err.Error()}
	}
	return neutralModel(&w), nil
}

// neutralModel maps the catalogue entry onto goodall's facts.
//
// Tools and cache control are reported as supported without asking the
// catalogue: every model the Messages API serves takes tool definitions and
// cache_control breakpoints, and Anthropic publishes no leaf for either, so
// the honest answer is the one every model gives. Pricing stays nil —
// Anthropic publishes prices on its website, not on this endpoint, and a
// price of zero would read as "free".
func neutralModel(w *wireModel) *goodall.ModelInfo {
	caps := goodall.Capabilities{
		Tools:         goodall.Supported,
		CacheControl:  goodall.Supported,
		ContextWindow: w.MaxInputTokens,
		MaxOutput:     w.MaxTokens,
	}
	if c := w.Capabilities; c != nil {
		caps.ImageInput = c.ImageInput.support()
		caps.PDFInput = c.PDFInput.support()
		caps.StructuredOutput = c.StructuredOutputs.support()
		caps.Thinking = thinkingSupport(c.Thinking)
		caps.ThinkingStyle = catalogueThinkingStyle(c.Thinking)
		caps.ThinkingEfforts = supportedEfforts(c.Effort)
	}
	return &goodall.ModelInfo{
		ID:           w.ID,
		Provider:     ProviderName,
		DisplayName:  w.DisplayName,
		Capabilities: caps,
	}
}

// catalogueThinkingStyle reads which thinking form the model takes from the two
// types. Adaptive wins when both are offered, since it is the form goodall
// prefers to send; a model offering neither, or a branch that was not
// published, leaves the style unknown and the name heuristic in charge.
func catalogueThinkingStyle(t *wireThinkingCapability) goodall.ThinkingStyle {
	if t == nil {
		return goodall.ThinkingStyleUnknown
	}
	switch {
	case t.Types.Adaptive.support() == goodall.Supported:
		return goodall.ThinkingAdaptive
	case t.Types.Enabled.support() == goodall.Supported:
		return goodall.ThinkingBudget
	}
	return goodall.ThinkingStyleUnknown
}

// thinkingSupport reads whether the model thinks. The branch's own "supported"
// member wins where Anthropic sent one: a model offering a thinking type this
// version does not model would otherwise read as unsupported from the types
// alone, and the agent would refuse a request the model accepts.
//
// Falling back to the types, a model thinks if either form is offered and is
// known not to think only when it published both as unsupported; neither leaf
// present means nobody said.
func thinkingSupport(t *wireThinkingCapability) goodall.Support {
	if t == nil {
		return goodall.SupportUnknown
	}
	if t.Supported != nil {
		if *t.Supported {
			return goodall.Supported
		}
		return goodall.Unsupported
	}
	adaptive, enabled := t.Types.Adaptive.support(), t.Types.Enabled.support()
	if adaptive == goodall.Supported || enabled == goodall.Supported {
		return goodall.Supported
	}
	if adaptive == goodall.Unsupported || enabled == goodall.Unsupported {
		return goodall.Unsupported
	}
	return goodall.SupportUnknown
}

// supportedEfforts lists the rungs a model offers, in ladder order, so a
// caller that wants "the hardest this model will think" reads the last one.
func supportedEfforts(e *wireEffortCapability) []goodall.Effort {
	if e == nil {
		return nil
	}
	ladder := []struct {
		effort goodall.Effort
		leaf   *wireSupported
	}{
		{goodall.EffortLow, e.Low},
		{goodall.EffortMedium, e.Medium},
		{goodall.EffortHigh, e.High},
		{goodall.EffortXHigh, e.XHigh},
		{goodall.EffortMax, e.Max},
	}
	var out []goodall.Effort
	for _, rung := range ladder {
		if rung.leaf.support() == goodall.Supported {
			out = append(out, rung.effort)
		}
	}
	return out
}
