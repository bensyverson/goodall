package main

import (
	"net/http"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
	"github.com/bensyverson/goodall/internal/livemodel"
	"github.com/bensyverson/goodall/openrouter"
)

// page is one vendor page to watch.
type page struct {
	// Name is what the report and baseline call it.
	Name string
	// URL serves Markdown.
	URL string
}

// pages is every vendor page this watches. TypeSafe publishes no changelog, so
// its models page stands in.
var pages = []page{
	{"anthropic", "https://platform.claude.com/docs/en/release-notes/overview.md"},
	{"openai", "https://developers.openai.com/api/docs/changelog.md"},
	{"openai-deprecations", "https://developers.openai.com/api/docs/deprecations.md"},
	{"openrouter", "https://openrouter.ai/docs/changelog.md"},
	{"google", "https://ai.google.dev/gemini-api/docs/changelog.md.txt"},
	{"typesafe-models", "https://docs.typesafe.ai/models.md"},
}

// catalogSpec is one vendor's model list.
type catalogSpec struct {
	// Name is what the report, baseline and pins call it.
	Name string
	// URL is the list's address.
	URL string
	// KeyEnv names the key it needs, empty for a public list.
	KeyEnv string
	// Header builds the request's headers from the key.
	Header func(key string) http.Header
	// Prefixes narrows the list, since OpenRouter's churns through vendors goodall
	// has no stake in. Empty keeps everything.
	Prefixes []string
}

// bearer is the header most vendors take.
func bearer(key string) http.Header {
	return http.Header{"Authorization": {"Bearer " + key}}
}

// catalogDecisions names the catalog whose concrete models the decision sweep asks.
const catalogDecisions = "openrouter-decisions"

// catalogs is every model list this watches.
var catalogs = []catalogSpec{
	{Name: "anthropic", URL: "https://api.anthropic.com/v1/models?limit=1000", KeyEnv: "ANTHROPIC_API_KEY",
		Header: func(key string) http.Header {
			return http.Header{"X-Api-Key": {key}, "Anthropic-Version": {"2023-06-01"}}
		}},
	{Name: "openai", URL: "https://api.openai.com/v1/models", KeyEnv: "OPENAI_API_KEY", Header: bearer},
	{Name: "openrouter", URL: "https://openrouter.ai/api/v1/models", Prefixes: []string{"anthropic/", "google/", "openai/", "typesafe/"}},
	{Name: catalogDecisions, URL: "https://openrouter.ai/api/v1/models?output_modalities=decisions"},
	{Name: "typesafe", URL: "https://api.typesafe.ai/v1/models", KeyEnv: "TYPESAFE_API_KEY", Header: bearer},
}

// wire is one API surface whose responses are compared.
type wire struct {
	// Name is what the report and baseline call it, and the first part of its
	// live rows.
	Name string
	// Fixtures are globs, from the repository root, of its recordings.
	Fixtures []string
	// Source lists the packages holding its wire structs.
	Source []string
}

// wires is every surface compared. live_[^o]* is every OpenRouter recording but
// the live_openai_* ones, which came from api.openai.com.
var wires = []wire{
	{"anthropic", []string{"anthropic/testdata/live_*"}, []string{"anthropic"}},
	{"openrouter",
		[]string{"openrouter/testdata/live_[^o]*", "internal/conformance/testdata/live_openrouter_*"},
		[]string{"openrouter", "internal/systemone", "decision"}},
	{"openai",
		[]string{"openrouter/testdata/live_openai_*", "internal/conformance/testdata/live_openai_*"},
		[]string{"openrouter", "decision"}},
	{"typesafe",
		[]string{"typesafe/testdata/live_*", "internal/conformance/testdata/live_typesafe_*"},
		[]string{"typesafe", "internal/systemone", "decision"}},
}

// dataMembers hold the caller's own data, so what lies beneath them is not API
// shape: input is a tool call's arguments on Anthropic's wire, and probabilities
// and legend are keyed by a question's own options.
var dataMembers = map[string]bool{"input": true, "probabilities": true, "legend": true}

// keyedMembers are objects whose member names are the caller's, with an API shape
// beneath each: System One's answers, keyed by question id.
var keyedMembers = map[string]bool{"answers": true}

// loopTarget is one model the agent loop is run against.
type loopTarget struct {
	// Wire names the surface, and so the key and client.
	Wire string
	// Model names the model.
	Model string
	// Effort is the thinking asked for. A row that is known to be refused stays
	// in the table, so its recovery is noticed too.
	Effort goodall.Effort
}

// loopTargets are the current models of each family, by hand: a new family shows
// up in the catalogs, and a person decides whether it earns a row here.
var loopTargets = []loopTarget{
	{"anthropic", livemodel.Anthropic, goodall.EffortLow},
	{"anthropic", livemodel.AnthropicForcing, goodall.EffortLow},
	{"anthropic", "claude-opus-5-5", goodall.EffortLow},
	{"anthropic", "claude-fable-5-1", goodall.EffortLow},
	{"openrouter", livemodel.OpenRouterClaude, goodall.EffortLow},
	{"openrouter", livemodel.OpenRouterOpenAI, goodall.EffortLow},
	{"openrouter", "openai/gpt-6.1-sol", goodall.EffortLow},
	{"openrouter", "google/gemini-4-argon", goodall.EffortLow},
	{"openrouter", "google/gemini-3.8-flash", goodall.EffortLow},
	{"openai", livemodel.OpenAI, goodall.EffortOff},
	{"openai", livemodel.OpenAI, goodall.EffortLow},
	{"openai", "gpt-6-astra", goodall.EffortLow},
	{"openai", "gpt-6.1-sol", goodall.EffortLow},
	{"openai", "gpt-6.1-terra", goodall.EffortLow},
	{"openai", "gpt-6-luna", goodall.EffortOff},
}

// wireKeys names each wire's key.
var wireKeys = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"typesafe":   "TYPESAFE_API_KEY",
}

// newProvider builds a wire's conversational client over the tapped HTTP client.
func newProvider(wireName, key string, client *http.Client) goodall.Provider {
	switch wireName {
	case "anthropic":
		return anthropic.New(key, anthropic.WithHTTPClient(client))
	case "openai":
		return openrouter.New(openrouter.WithAPIKey(key), openrouter.WithHTTPClient(client),
			openrouter.WithBaseURL("https://api.openai.com/v1"), openrouter.WithDialect(openrouter.OpenAI))
	default:
		return openrouter.New(openrouter.WithAPIKey(key), openrouter.WithHTTPClient(client))
	}
}
