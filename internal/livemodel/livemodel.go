// Package livemodel names the model each provider's live tests and fixture
// recordings run against.
//
// It exists so the recorder and the live tests cannot drift: a fixture
// recorded from one model and a live test asserting against another would
// disagree about thinking style, capabilities and stop reasons, and the
// disagreement would read as a bug in the provider. One constant per
// provider, changed in one place when the newest model changes.
//
// It is internal because the choice is a test detail, not an API: a consumer
// names their own model on goodall.Request.
package livemodel

// Anthropic is the model the Anthropic live tests and
// scripts/record-fixtures use by default.
//
// It is claude-sonnet-5-5, the current Sonnet: GET /v1/models/claude-sonnet-5-5
// on 2026-10-28 answered "Claude Sonnet 5.5", adaptive thinking, the effort
// ladder from low to max, a 1,000,000-token context and a 128,000-token output
// limit. Recording from a 5.5 model is the point: it thinks by default, ties
// each thinking block to the conversation before it, and refuses a replay
// that has drifted, so a recorded second turn proves goodall's replay against
// the rules the current models enforce. It refuses a forced tool_choice, which
// is why the forced recordings use [AnthropicForcing]. Override it on the
// recorder with -model.
const Anthropic = "claude-sonnet-5-5"

// AnthropicForcing is the model the forced tool_choice recordings use.
//
// It is claude-haiku-5-5 because it is the current model that still accepts a
// forced tool_choice: on 2026-10-28 claude-opus-5-5 and claude-sonnet-5-5
// answered tool_choice "tool" and "any" with a 400, and claude-haiku-5-5
// answered both with a tool call.
const AnthropicForcing = "claude-haiku-5-5"

// OpenRouterClaude is the Claude model the OpenRouter live tests and
// recordings run against, named the way OpenRouter names it.
//
// It is anthropic/claude-sonnet-5.5, the same model as [Anthropic], so the two
// providers' recordings answer the same correspondent. GET
// https://openrouter.ai/api/v1/models on 2026-10-28 listed it with tools,
// tool_choice, reasoning and the effort ladder from low to max, a
// 1,000,000-token context, and reasoning that is mandatory: effort "none" is a
// 400 ("Reasoning is mandatory for this endpoint and cannot be disabled"), and
// goodall's translation leaves the effort out instead when the catalog entry
// is attached. Like the Anthropic model it refuses a forced tool_choice,
// relayed as a 400, so the forced recordings use [OpenRouterClaudeForcing].
const OpenRouterClaude = "anthropic/claude-sonnet-5.5"

// OpenRouterClaudeForcing is the Claude model the OpenRouter forced
// tool_choice recordings use: anthropic/claude-haiku-5.5, for the reason
// [AnthropicForcing] gives, confirmed through OpenRouter on 2026-10-28.
const OpenRouterClaudeForcing = "anthropic/claude-haiku-5.5"

// OpenRouterOpenAI is the OpenAI reasoning model the OpenRouter recordings use
// for the encrypted-reasoning round trip, which is the other shape a
// reasoning_details entry takes.
//
// It is openai/gpt-6-astra because that is the newest OpenAI model the same
// catalog read lists (created 2026-09-03) whose reasoning object is present:
// mandatory reasoning, the effort ladder from low to max, tools and
// tool_choice, text, image and file input, and a 1,050,000-token context.
const OpenRouterOpenAI = "openai/gpt-6-astra"

// OpenAI is the model the OpenAI-dialect recordings and live test use against
// api.openai.com itself, rather than through OpenRouter.
//
// It is gpt-5.6-terra because that is the newest model GET
// https://api.openai.com/v1/models offered this project's key on 2026-09-15
// that can use function tools on /v1/chat/completions, which is the only
// format this package speaks. The two newer reasoning models cannot: observed
// the same day, /v1/chat/completions refuses function tools alongside any
// reasoning effort above "none" (HTTP 400, "Function tools with
// reasoning_effort are not supported … use /v1/responses or set
// reasoning_effort to 'none'"), and gpt-6-astra's reasoning cannot be turned
// off at all (HTTP 400, "'reasoning_effort' does not support 'none' with this
// model"), so it can never carry a tool on this endpoint. gpt-5.6-terra's
// effort ladder runs from none to max, so it serves all three of text, a tool
// call and a reasoning answer.
const OpenAI = "gpt-5.6-terra"

// TypeSafe is the Jev model the TypeSafe live test and
// scripts/record-fixtures use by default.
//
// It is the versioned id jev-1.13.0 rather than the jev-latest alias, which is
// what a caller's code would normally send: TypeSafe tunes each version's
// confidence figures separately and says so on its models page, so a fixture
// recorded behind an alias would start disagreeing with the thresholds a test
// asserts the moment a release ships, and the disagreement would read as a bug
// in the client. The response reports the versioned id that answered either
// way, so pinning it here costs nothing. Override it on the recorder with
// -model.
const TypeSafe = "jev-1.13.0"
