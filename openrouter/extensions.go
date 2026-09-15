package openrouter

import (
	"encoding/json/jsontext"

	"github.com/bensyverson/goodall"
)

// Extensions is the OpenRouter-specific part of a request: where it may be
// routed, which plugins run, how it is grouped for sticky routing, and
// whether OpenRouter echoes what it sent upstream. Put one on
// goodall.Request.Extensions; a request carrying another provider's
// extensions is rejected before it is sent.
//
// Every field is ignored on a dialect whose [Quirks] say the server does not
// understand it, so the same request can be pointed at a local LM Studio
// without editing it.
type Extensions struct {
	// Routing constrains which upstream providers may serve the request.
	// The field is not called Provider because the goodall.Extension
	// interface needs that name for the method below.
	Routing *ProviderRouting
	// Models is the fallback list: OpenRouter tries the request's model
	// first and these in order after it.
	Models []string
	// Plugins are the plugins to run for this request, such as the PDF
	// file parser.
	Plugins []Plugin
	// SessionID groups related requests so they route to the same upstream
	// provider, which is what keeps a prompt cache warm across the turns
	// of a run whose opening messages vary. At most 256 characters.
	SessionID string
	// Debug asks OpenRouter to report what it sent upstream, which is how
	// a cross-provider translation is checked without a second live
	// provider.
	Debug Debug
}

// Provider names the provider that understands these options.
func (Extensions) Provider() string { return ProviderName }

// ProviderSort is the strategy OpenRouter uses to pick an upstream provider
// when no explicit order is given. The zero value leaves the choice to
// OpenRouter, which load-balances.
type ProviderSort string

const (
	// SortDefault is the zero value: OpenRouter's own load balancing.
	SortDefault ProviderSort = ""
	// SortPrice prefers the cheapest endpoint.
	SortPrice ProviderSort = "price"
	// SortThroughput prefers the fastest endpoint.
	SortThroughput ProviderSort = "throughput"
	// SortLatency prefers the endpoint that answers soonest.
	SortLatency ProviderSort = "latency"
	// SortExacto prefers the endpoint that best reproduces the model's
	// reference behavior.
	SortExacto ProviderSort = "exacto"
)

// DataCollection says whether providers that may store and train on the
// request are acceptable. The zero value allows them, which is OpenRouter's
// default.
type DataCollection string

const (
	// DataCollectionDefault is the zero value: providers that retain data
	// are allowed.
	DataCollectionDefault DataCollection = ""
	// DataCollectionAllow allows providers that retain data, said
	// explicitly.
	DataCollectionAllow DataCollection = "allow"
	// DataCollectionDeny routes only to providers that do not collect
	// user data, failing the request if none qualifies.
	DataCollectionDeny DataCollection = "deny"
)

// ProviderRouting constrains which upstream providers may serve a request.
// The zero value constrains nothing.
type ProviderRouting struct {
	// Order is the provider slugs to try, in order.
	Order []string `json:"order,omitzero"`
	// AllowFallbacks is whether a provider outside Order may serve the
	// request. It is a pointer because OpenRouter's default is true, so
	// "false" has to be distinguishable from "unset".
	AllowFallbacks *bool `json:"allow_fallbacks,omitzero"`
	// RequireParameters restricts routing to providers that support every
	// parameter on the request, rather than silently dropping the ones
	// they do not.
	RequireParameters bool `json:"require_parameters,omitzero"`
	// Only is the provider slugs that may serve the request.
	Only []string `json:"only,omitzero"`
	// Ignore is the provider slugs that may not.
	Ignore []string `json:"ignore,omitzero"`
	// Quantizations restricts routing to endpoints at these quantization
	// levels, such as "fp8".
	Quantizations []string `json:"quantizations,omitzero"`
	// Sort is the strategy used when Order is empty.
	Sort ProviderSort `json:"sort,omitzero"`
	// DataCollection says whether providers that retain data are allowed.
	DataCollection DataCollection `json:"data_collection,omitzero"`
	// ZDR restricts routing to zero-data-retention endpoints.
	ZDR bool `json:"zdr,omitzero"`
	// MaxPrice caps what the request may cost.
	MaxPrice *MaxPrice `json:"max_price,omitzero"`
}

// MaxPrice caps the price of a request. Prompt and Completion are USD per
// million tokens; Request and Image are USD per request and per image. A zero
// field is not sent, so it places no cap.
//
// OpenRouter reads these as decimal strings, so they are written as strings
// rather than as JSON numbers, which would round a price away.
type MaxPrice struct {
	// Prompt caps the price per million prompt tokens, in USD.
	Prompt goodall.Decimal
	// Completion caps the price per million completion tokens, in USD.
	Completion goodall.Decimal
	// Request caps the price per request, in USD.
	Request goodall.Decimal
	// Image caps the price per image, in USD.
	Image goodall.Decimal
}

// MarshalJSONTo writes the caps that are set, in declaration order, as the
// decimal strings OpenRouter expects.
func (p MaxPrice) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value goodall.Decimal
	}{
		{"prompt", p.Prompt},
		{"completion", p.Completion},
		{"request", p.Request},
		{"image", p.Image},
	} {
		if field.value.IsZero() {
			continue
		}
		if err := enc.WriteToken(jsontext.String(field.name)); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.String(field.value.String())); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// PluginID names one of OpenRouter's request plugins.
type PluginID string

const (
	// PluginFileParser parses PDFs and other files before the model sees
	// them; its PDF engine is chosen through [PDFOptions].
	PluginFileParser PluginID = "file-parser"
	// PluginContextCompression shortens an over-long prompt. It is on by
	// default for endpoints with a small context window.
	PluginContextCompression PluginID = "context-compression"
	// PluginModeration runs OpenRouter's moderation pass.
	PluginModeration PluginID = "moderation"
	// PluginWebSearch adds web search as a server-side tool.
	PluginWebSearch PluginID = "web"
)

// PDFEngine is how the file-parser plugin turns a PDF into something a model
// can read. The zero value lets OpenRouter choose: the model's own file
// support where it has any, and OCR otherwise.
type PDFEngine string

const (
	// PDFEngineDefault is the zero value.
	PDFEngineDefault PDFEngine = ""
	// PDFEngineNative passes the file to a model that reads files itself,
	// billed as input tokens.
	PDFEngineNative PDFEngine = "native"
	// PDFEngineMistralOCR runs OCR, which is what a scanned document
	// needs. It is billed per page.
	PDFEngineMistralOCR PDFEngine = "mistral-ocr"
	// PDFEngineCloudflareAI converts the PDF to Markdown, free of charge.
	PDFEngineCloudflareAI PDFEngine = "cloudflare-ai"
)

// PDFOptions configures the file-parser plugin's PDF handling.
type PDFOptions struct {
	// Engine is how the PDF is turned into model input.
	Engine PDFEngine `json:"engine,omitzero"`
}

// Plugin is one request plugin and its settings. Only the members the named
// plugin defines are meaningful; the rest are omitted.
type Plugin struct {
	// ID names the plugin.
	ID PluginID `json:"id"`
	// Enabled turns a default-on plugin off for this request. It is a
	// pointer so that "off" is distinguishable from "unset".
	Enabled *bool `json:"enabled,omitzero"`
	// PDF configures the file-parser plugin.
	PDF *PDFOptions `json:"pdf,omitzero"`
	// Engine names the context-compression engine.
	Engine string `json:"engine,omitzero"`
}

// Debug asks OpenRouter for a look at its own work.
type Debug struct {
	// EchoUpstreamBody returns the exact body OpenRouter sent upstream, in
	// a debug frame at the start of the stream. It works on streaming
	// requests only.
	EchoUpstreamBody bool `json:"echo_upstream_body,omitzero"`
}

// The header names OpenRouter reads a request's attribution from.
const (
	// HeaderReferer carries the app's URL and is what OpenRouter's
	// leaderboards attribute the request to.
	HeaderReferer = "HTTP-Referer"
	// HeaderTitle carries the app's name.
	HeaderTitle = "X-OpenRouter-Title"
)

// Header is one HTTP header name and value.
type Header struct {
	// Name is the HTTP header's name.
	Name string
	// Value is the HTTP header's value.
	Value string
}

// Attribution is how an app names itself to OpenRouter, which shows the name
// and the link on its public leaderboards. It travels as headers rather than
// in the body, so it is a client option rather than a request extension.
type Attribution struct {
	// Referer is the app's public URL.
	Referer string
	// Title is the app's name.
	Title string
}

// Headers is the attribution as HTTP headers, in a fixed order and leaving
// out whichever half was not given. An empty Attribution yields none.
func (a Attribution) Headers() []Header {
	var out []Header
	if a.Referer != "" {
		out = append(out, Header{HeaderReferer, a.Referer})
	}
	if a.Title != "" {
		out = append(out, Header{HeaderTitle, a.Title})
	}
	return out
}
