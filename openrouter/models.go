// The models catalog: the wire shape of GET /models and its mapping onto
// goodall's tri-state capabilities and exact per-token prices.

package openrouter

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/transport"
)

// modality is one of the input kinds architecture.input_modalities lists.
type modality string

const (
	modalityText  modality = "text"
	modalityImage modality = "image"
	modalityFile  modality = "file"
	modalityAudio modality = "audio"
	modalityVideo modality = "video"
)

// parameter is one of the names supported_parameters lists. Only the ones a
// goodall capability asks about are named.
type parameter string

const (
	// paramTools is tool definitions and tool calls.
	paramTools parameter = "tools"
	// paramReasoning is OpenRouter's reasoning object.
	paramReasoning parameter = "reasoning"
	// paramReasoningEffort is OpenAI's top-level reasoning_effort string,
	// which some endpoints advertise instead.
	paramReasoningEffort parameter = "reasoning_effort"
	// paramStructuredOutputs is a schema-constrained answer.
	paramStructuredOutputs parameter = "structured_outputs"
)

// catalog is the body of GET /models.
type catalog struct {
	Data       []catalogModel `json:"data,omitzero"`
	TotalCount int            `json:"total_count,omitzero"`
}

// catalogModel is one entry. Members goodall has no use for — the
// description, the benchmarks, the default parameters — are left out; json/v2
// ignores what a struct does not name.
type catalogModel struct {
	ID                  string          `json:"id,omitzero"`
	CanonicalSlug       string          `json:"canonical_slug,omitzero"`
	Name                string          `json:"name,omitzero"`
	ContextLength       int             `json:"context_length,omitzero"`
	Architecture        *architecture   `json:"architecture,omitzero"`
	Pricing             *modelPricing   `json:"pricing,omitzero"`
	TopProvider         *topProvider    `json:"top_provider,omitzero"`
	SupportedParameters []parameter     `json:"supported_parameters,omitzero"`
	Reasoning           *modelReasoning `json:"reasoning,omitzero"`
}

// architecture is what a model reads and writes.
type architecture struct {
	Modality         string     `json:"modality,omitzero"`
	InputModalities  []modality `json:"input_modalities,omitzero"`
	OutputModalities []modality `json:"output_modalities,omitzero"`
	Tokenizer        string     `json:"tokenizer,omitzero"`
}

// modelPricing is the published price per token, as decimal strings. They stay
// strings on the wire and become [goodall.Decimal] here: a price of
// "0.00000015" cannot survive a float64 and be printed back.
type modelPricing struct {
	Prompt            string `json:"prompt,omitzero"`
	Completion        string `json:"completion,omitzero"`
	InputCacheRead    string `json:"input_cache_read,omitzero"`
	InputCacheWrite   string `json:"input_cache_write,omitzero"`
	Image             string `json:"image,omitzero"`
	Audio             string `json:"audio,omitzero"`
	WebSearch         string `json:"web_search,omitzero"`
	InternalReasoning string `json:"internal_reasoning,omitzero"`
}

// topProvider is what the endpoint OpenRouter would route to accepts.
type topProvider struct {
	ContextLength       int  `json:"context_length,omitzero"`
	MaxCompletionTokens int  `json:"max_completion_tokens,omitzero"`
	IsModerated         bool `json:"is_moderated,omitzero"`
}

// modelReasoning is how the model thinks, where it thinks at all.
type modelReasoning struct {
	Mandatory         bool             `json:"mandatory,omitzero"`
	SupportedEfforts  []goodall.Effort `json:"supported_efforts,omitzero"`
	DefaultEffort     goodall.Effort   `json:"default_effort,omitzero"`
	DefaultEnabled    bool             `json:"default_enabled,omitzero"`
	SupportsMaxTokens bool             `json:"supports_max_tokens,omitzero"`
}

// Model describes one model by its OpenRouter identifier.
//
// OpenRouter publishes no per-model catalog endpoint — /models/{id} is a 404
// and /models/{id}/endpoints returns a routing view with none of the facts
// below — so a lookup reads the whole catalog and finds the id. The client
// memoizes what it read, and since one fetch carries every model, the first
// lookup answers for all of them; the agent asks at the start of every run,
// so that is what keeps a run's pre-flight free.
//
// A model the catalog does not list is a *[goodall.APIError] with
// [goodall.KindNotFound], and is not memoized: OpenRouter adds models
// continuously, so a miss is worth asking about again. The result is shared
// between callers and must not be modified.
func (c *Client) Model(ctx context.Context, id string) (*goodall.ModelInfo, error) {
	if info, ok := c.cachedModel(id); ok {
		return info, nil
	}
	read, err := c.fetchCatalog(ctx)
	if err != nil {
		return nil, err
	}
	return c.cacheCatalog(read, id)
}

// catalogRead is one read of GET /models: the entries, and the facts about
// the response that a "no such model" error names.
type catalogRead struct {
	list      catalog
	status    int
	requestID string
}

// cachedModel reads the memo.
func (c *Client) cachedModel(id string) (*goodall.ModelInfo, bool) {
	c.modelsMu.Lock()
	defer c.modelsMu.Unlock()
	info, ok := c.models[id]
	return info, ok
}

// cacheCatalog stores every entry of a freshly read catalog, under both
// the id and the canonical slug a caller may name it by, and returns the one
// that was asked for. An id the catalog does not carry is a not-found,
// which is not stored.
func (c *Client) cacheCatalog(read *catalogRead, id string) (*goodall.ModelInfo, error) {
	c.modelsMu.Lock()
	defer c.modelsMu.Unlock()
	if c.models == nil {
		c.models = make(map[string]*goodall.ModelInfo, len(read.list.Data))
	}
	var found *goodall.ModelInfo
	for _, entry := range read.list.Data {
		info := modelInfo(&entry)
		c.models[entry.ID] = info
		if entry.CanonicalSlug != "" && entry.CanonicalSlug != entry.ID {
			c.models[entry.CanonicalSlug] = info
		}
		if entry.ID == id || entry.CanonicalSlug == id {
			found = info
		}
	}
	if found == nil {
		return nil, &goodall.APIError{
			Provider:  ProviderName,
			Kind:      goodall.KindNotFound,
			Status:    read.status,
			Message:   fmt.Sprintf("the catalog lists no model %q", id),
			RequestID: read.requestID,
		}
	}
	return found, nil
}

// fetchCatalog reads GET /models. The lock is never held across the call:
// a slow catalog must not block a lookup that the memo could have answered.
func (c *Client) fetchCatalog(ctx context.Context) (*catalogRead, error) {
	resp, err := c.http.Do(ctx, transport.Request{
		Method: http.MethodGet,
		URL:    c.baseURL + pathModels,
		Header: c.header(),
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	read := &catalogRead{status: resp.StatusCode, requestID: requestID(resp.Header)}
	if err := json.Unmarshal(raw, &read.list); err != nil {
		if apiErr := decodeError(0, resp.Header, raw); apiErr != nil {
			return nil, apiErr
		}
		return nil, fmt.Errorf("openrouter: decoding the models catalog: %w", err)
	}
	return read, nil
}

// modelInfo maps one catalog entry onto the neutral description.
//
// A list the catalog did not publish leaves the capabilities it would have
// answered at [goodall.SupportUnknown], never Unsupported: unknown means try,
// and a catalog goodall cannot read must not block a call that would have
// worked. CacheControl stays unknown for every model, because the catalog
// says nothing about prompt-cache breakpoints — only the price list hints at
// them, and a price is not a promise.
func modelInfo(entry *catalogModel) *goodall.ModelInfo {
	caps := goodall.Capabilities{ContextWindow: entry.ContextLength}
	if arch := entry.Architecture; arch != nil && len(arch.InputModalities) > 0 {
		caps.ImageInput = supportFor(arch.InputModalities, modalityImage)
		caps.PDFInput = supportFor(arch.InputModalities, modalityFile)
		caps.AudioInput = supportFor(arch.InputModalities, modalityAudio)
	}
	if params := entry.SupportedParameters; len(params) > 0 {
		caps.Tools = supportFor(params, paramTools)
		caps.Thinking = supportFor(params, paramReasoning, paramReasoningEffort)
		caps.StructuredOutput = supportFor(params, paramStructuredOutputs)
	}
	if top := entry.TopProvider; top != nil {
		caps.MaxOutput = top.MaxCompletionTokens
	}
	if r := entry.Reasoning; r != nil && len(r.SupportedEfforts) > 0 {
		caps.ThinkingEfforts = efforts(r.SupportedEfforts)
	}
	return &goodall.ModelInfo{
		ID:           entry.ID,
		Provider:     ProviderName,
		Capabilities: caps,
		Pricing:      pricing(entry.Pricing),
	}
}

// supportFor reports whether a published list names any of the wanted values.
// The caller has already established that the list exists, so an absence here
// really is a refusal.
func supportFor[T comparable](list []T, wanted ...T) goodall.Support {
	for _, have := range list {
		if slices.Contains(wanted, have) {
			return goodall.Supported
		}
	}
	return goodall.Unsupported
}

// efforts maps the catalog's effort rungs onto goodall's. The two ladders
// share their words except that OpenRouter spells "off" as "none"; a rung
// goodall does not define travels on verbatim, since the ladder keeps growing.
func efforts(published []goodall.Effort) []goodall.Effort {
	out := make([]goodall.Effort, 0, len(published))
	for _, rung := range published {
		if rung == wireEffortNone {
			rung = goodall.EffortOff
		}
		out = append(out, rung)
	}
	return out
}

// wireEffortNone is how the wire spells [goodall.EffortOff].
const wireEffortNone goodall.Effort = "none"

// pricing parses the published decimal strings exactly. A price the catalog
// omits reads as zero, which for a cache price means the model has no separate
// one; a model with no price list at all yields nil, because "no published
// price" is not the same fact as "free".
func pricing(p *modelPricing) *goodall.Pricing {
	if p == nil {
		return nil
	}
	return &goodall.Pricing{
		Input:      decimalOrZero(p.Prompt),
		Output:     decimalOrZero(p.Completion),
		CacheRead:  decimalOrZero(p.InputCacheRead),
		CacheWrite: decimalOrZero(p.InputCacheWrite),
		Currency:   costCurrency,
	}
}

// decimalOrZero parses one price, reading anything unparseable as zero: a
// price goodall cannot read must not fail a capability lookup the caller made
// for some other reason.
func decimalOrZero(s string) goodall.Decimal {
	if s == "" {
		return goodall.Decimal{}
	}
	d, err := goodall.ParseDecimal(s)
	if err != nil {
		return goodall.Decimal{}
	}
	return d
}
