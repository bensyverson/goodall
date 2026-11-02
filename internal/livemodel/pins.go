package livemodel

// Pin is one model this package names, so scripts/check-drift can look for it
// in its vendor's catalog.
type Pin struct {
	// Const is the constant's name.
	Const string
	// Model is the model id the constant holds.
	Model string
	// Catalog names the vendor's list to find it in. Empty means no catalog can
	// say: TypeSafe's lists its aliases while accepting versioned ids it omits.
	Catalog string
}

// Catalog names, as scripts/check-drift reads them.
const (
	catalogAnthropic  = "anthropic"
	catalogOpenAI     = "openai"
	catalogOpenRouter = "openrouter"
	catalogDecisions  = "openrouter-decisions"
)

// Pins lists every model constant in livemodel.go; a test holds it to them.
var Pins = []Pin{
	{"Anthropic", Anthropic, catalogAnthropic},
	{"AnthropicForcing", AnthropicForcing, catalogAnthropic},
	{"OpenRouterClaude", OpenRouterClaude, catalogOpenRouter},
	{"OpenRouterClaudeForcing", OpenRouterClaudeForcing, catalogOpenRouter},
	{"OpenRouterOpenAI", OpenRouterOpenAI, catalogOpenRouter},
	{"OpenAI", OpenAI, catalogOpenAI},
	{"OpenRouterDecision", OpenRouterDecision, catalogDecisions},
	{"OpenAIDecision", OpenAIDecision, catalogOpenAI},
	{"OpenRouterDecisionLimited", OpenRouterDecisionLimited, catalogDecisions},
	{"TypeSafe", TypeSafe, ""},
}
