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
// It is claude-sonnet-5 because that is the newest model the live catalog
// confirms: GET /v1/models/claude-sonnet-5 on 2026-09-15 answered "Claude
// Sonnet 5", adaptive thinking, the effort ladder from low to max, a
// 1,000,000-token context and a 128,000-token output limit
// (TestLiveModelReportsImageAndPDFSupport makes the call and logs it). It is
// also the style the provider prefers to send: a budget-only model such as
// claude-sonnet-4-5-20250929 has no wire slot for the thinking display
// setting, so a recording from one would not exercise the adaptive path at
// all. Override it on the recorder with -model.
const Anthropic = "claude-sonnet-5"

// OpenRouterClaude is the Claude model the OpenRouter live tests and
// recordings run against, named the way OpenRouter names it.
//
// It is anthropic/claude-opus-5 because that is the newest Claude in the
// OpenRouter catalog that advertises tool_choice. GET
// https://openrouter.ai/api/v1/models on 2026-09-15 listed 446 models, of
// which the three newest Claudes were anthropic/claude-fable-5.1 (created
// 2026-08-31), anthropic/claude-opus-5 (2026-07-24) and
// anthropic/claude-sonnet-5 (2026-06-30). claude-fable-5.1 is newer still but
// its supported_parameters name neither tool_choice nor temperature and its
// reasoning is mandatory, so the forced-tool recordings would silently not be
// forced and the plain recordings could not turn thinking off; OpenRouter
// drops a parameter the endpoint does not support rather than refusing it, so
// that failure would be invisible in the fixture. claude-opus-5 reads text,
// images and files, supports tools, tool_choice and the effort ladder from low
// to max, and reports a 1,000,000-token context with a 128,000-token output
// limit.
const OpenRouterClaude = "anthropic/claude-opus-5"

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
