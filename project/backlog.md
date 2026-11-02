# Backlog

Work decided against, parked rather than dropped in silence. Nothing here is scheduled or blocking — active work lives in `job`. Read it before proposing something that sounds novel; it may already have been weighed and parked.

- **One dated H2 per item:** what it is, why it's parked, and *what would un-park it*.
- **Delete an item when it lands, or when it stops being plausible.** A long list is one nobody reads.
- **Something parked that turns out to be needed becomes a task in `job`** — move it, don't work it from here.

Format: one dated H2 per entry, a headline, then what it is, why it's parked, and the trigger that would revive it.

---

## 2026-09-14 Model tiering (fast/standard/flagship × direct/reasoning)

The Swift LLM library resolved a tier pair to a concrete model per provider so call sites stayed portable. Parked because the ladder keeps changing shape (Haiku, Sonnet, Opus, Fable, Mythos) and adaptive thinking made "reasoning" a per-request effort knob rather than a model choice; a tier table goes stale like a price list. A model is a string the consumer aliases. Revive if a consumer needs provider-portable model selection in library code rather than in their own config.

## 2026-09-14 MCP client and server support

Operator compiled the MCP SDK into its core target. Ruled out of scope for goodall by the owner. Revive only on an explicit decision; it would be a separate `goodall/mcp` package, never a core dependency.

## 2026-09-14 Pricing Anthropic usage

Anthropic reports tokens but no cost, so Anthropic threads report usage and a cost marked not reported. A static price table needs upkeep; OpenRouter's models endpoint lists every Claude model's per-token prices and could price Anthropic tokens at runtime through a `Pricer`. Revive if a consumer needs a dollar figure for direct Anthropic traffic.

## 2026-09-14 Image preprocessing (resize, describe for non-vision models)

The Swift library resized images before sending and could replace media with a description for text-only models. Anthropic downsizes server-side and the standard library has no good resampler, so the core only fails fast on a known-unsupported input. Revive as an injectable preprocessor hook if a consumer hits size limits or tool-result image rules (computer use rejects oversized screenshots).

## 2026-09-14 OpenRouter Responses and Messages endpoints

OpenRouter also serves `/responses` (GA, stateless) and an Anthropic-shaped `/messages`. Chat Completions is the v1 target because it is the compatibility standard for local servers too. Revive if a feature lands only on one of the other endpoints; the Messages skin may already work through the Anthropic client with a base-URL override.

> **Trigger fired 2026-11-02, for OpenAI's Responses.** Tools with reasoning on GPT-6 now work only on `/v1/responses` when calling OpenAI directly ([findings](2026-11-02-model-currency-findings.md)). OpenRouter's Chat Completions still carries them, so the OpenRouter part of this entry stands. The owner ruled the same day that it needs its own plan, after the decision work; that is `yEETo1` in `job`.

> **Ruled 2026-11-02: OpenRouter's part stays parked.** OpenAI's Responses is being built for direct OpenAI only. OpenRouter's `/responses` passed two-turn tool calls on three vendors, with its small differences recorded in [the plan](2026-11-02-responses-wire-plan.md). The trigger is unchanged: a feature that lands only on one of its other endpoints.

## 2026-09-14 Client-side rate limiting and concurrency caps

Retries honor `Retry-After`; a token-bucket limiter is not built in because Anthropic's limits are token-based and a naive semaphore does not model them. Consumers can wrap the injected HTTP client. Revive if a consumer needs a shared limiter across many threads.

## 2026-09-14 Full CommonMark rendering

The chat layer ships a zero-dependency subset. `yuin/goldmark/v2` (zero dependencies, MIT, CommonMark 0.31.2) plugs into the `Renderer` interface in a few lines and the README shows how. Revive as a built-in only if the subset proves insufficient for the chat consumer.

## 2026-09-14 Conversation compaction

Preserved thinking allows only "summary as a fresh first message" client-side, or server-side compaction on Anthropic. Not needed until threads outgrow the 1M context. Revive as a hook-driven summarizer plus the Anthropic `compact` beta when a consumer hits context limits.

## 2026-09-14 Audio input

OpenRouter carries `input_audio`; Anthropic does not accept audio blocks. Not in v1 so the block set has no case a provider silently drops. Revive by adding an `Audio` block with a capability fact when a consumer needs it through OpenRouter.

## 2026-09-15 Strict tool schemas and structured output on OpenRouter

The plan says the OpenRouter dialect decides whether `strict: true` tools need the `x-anthropic-beta: structured-outputs-2025-11-13` header, and `response_format{json_schema}` is the structured-output path. Neither is built: `goodall.Tool` carries no strict flag and `Request` has no output-format field, so there is nothing neutral to translate from. Parked because no consumer needs it for v1 and it is a root-package decision (a `Strict` fact on tools, an output schema on the request) before it is a provider one. Revive when a consumer needs schema-enforced tool arguments or JSON-schema output; it would be one leaf across the root and both providers.

> **Strict tools un-parked 2026-10-29.** The 5.5 models reject a forced tool choice, so strict is now the only schema guarantee on the default models; it is a `job` leaf under goodall v1.4, ruled in the [architecture plan](2026-09-14-architecture-plan.md) beside the OpenRouter "Not built" note. Structured output is still parked here; revive it when a consumer needs JSON back without a tool call.

## 2026-09-17 A "delegate" or "judge" seam in the provider layer

A second interface beside `Provider` that a judgment model (TypeSafe's Jev) and a nested agent would both satisfy, so the loop could call either through one seam. Parked in the [TypeSafe and subagents findings](2026-09-17-typesafe-jev-findings.md): nothing would call it but the two adapters that already exist as a `Tool` and a `BeforeSend` helper, and it is the second abstraction layer the plan rules out. Revive if a third kind of delegate appears that neither a tool nor a hook can express.

## 2026-09-17 An explicit union type on a judgment tool's `state` schema

`decision.Tool` and `decision.AuthoredTool` take the state as a `jsontext.Value`, which `goodall.NewTool` infers to a required, described member with no `type`, so a model may write a string, an object or an array there. Spelling that as an explicit union needs `goodall.Schema` to gain `anyOf`, a root-package change. Parked because both providers accept the typeless member and the live run (`typesafe/live_authored_test.go`) shows a model filling it correctly without one. Revive if a model is seen writing the wrong shape, or if a provider starts refusing a property with no type.

## 2026-09-17 Approval inside a delegated run

A hook in an `AgentTool`'s child that defers a tool call for approval ends the child with `StopCauseDeferred`, and the parent receives an error result saying approval inside a delegated run is not supported; the parent's caller is never asked. Parked because a nested pause needs a path from the child's pending calls to the parent's `Result.Pending` and a `Resume` that knows which level to resume, which is a loop design of its own. Revive when a consumer wants a delegate to run tools that need a person's approval; the shape to consider is the child's `Pending` traveling on the parent's `Stopped` with the call that owns it.

## 2026-09-17 Discouraging indirection in the authored judgment tool's guidance

The one live run of `decision.AuthoredTool` (findings doc, "Observed 2026-09-17 by the adapters leaf") showed claude-sonnet-5 asking all ten questions in one call, as the description asks, but addressing each ticket by reference over the whole array as state, which is the indirection TypeSafe's jaggedness page says degrades Jev. Jev resolved all ten. Parked because one run is not evidence and every sentence in `DefaultAuthoredDescription` competes with the four already there. Revive when a second run shows a wrong answer traceable to indirection; the fix is a sentence in the description or in the `state` field's `desc`, measured over repeated runs.

## 2026-09-17 Structured instructions and criteria on a typesafe question

The API accepts a JSON object or array wherever a question takes text: instructions, an option's description, a score level, a predicate's true and false. `goodall/decision` sends strings only, which every documented example uses; 2389's typesafe-go types the slot as `any` ([comparison](2026-09-17-typesafe-go-comparison.md)). Parked because no consumer has asked for a rubric object and a `string` field cannot become `any` without touching every question type. Revive when a consumer needs a structured rubric; the shape is a new field beside `Instructions`, such as `Rubric any`, marshaled in its place when set, so the string form stays the simple one.

## 2026-09-17 Typed question handles with a generic Choice

typesafe-go's `Choice[T ~string]` returns a `ChoiceAnswer[T]` keyed by the caller's own type, and every question is a handle whose `From(res)` reads its own answer, so an id appears once and a mismatched read is a compile error ([comparison](2026-09-17-typesafe-go-comparison.md)). Parked because the two adapters that make goodall's package different cannot use it: `AuthoredTool`'s ids are written by the model at call time and `Answers` must round-trip through JSON as a tool result, both of which need ids as data. Revive if the fixed-questions path gets enough consumers to want compile-time matching; it would be a thin layer above `Questions` and `Answers`, not a replacement.

## 2026-10-29 Rewriting optional parameters for OpenAI's strict mode

OpenAI's strict mode needs every property listed in `required`, with an optional one spelled as a `null` type option (`"type": ["string", "null"]`); a strict tool with an optional parameter is a 400 there (verified live 2026-10-29, see the architecture plan's "Built 2026-10-29" block). goodall refuses such a request before sending instead of rewriting the schema, because `goodall.Schema` has one `Type` and no null form, and because a rewrite changes what the handler receives: an absent field would arrive as JSON `null`, and the missing-required check and the input decode would both have to read it as absent. Parked as the user's call. Revive when a consumer needs strict tools with optional parameters on an OpenAI model; the work is a nullable form on `Schema`, a translation-time rewrite on the OpenAI rule set, and a decode path that reads `null` as absent.

## 2026-11-02 Images and Box questions on decision models

Most decision models take images, Jev 1.14 answers a `box` question, and the surfaces disagree: `assets` with references on TypeSafe, `image_url` parts in the `state` array on OpenRouter, `input_image` parts on OpenAI, which also takes them per question ([findings](2026-11-02-decision-models-findings.md)). Parked because no consumer needs them yet and three syntaxes want a neutral image type of their own; a Box answer already arrives as an `UnknownAnswer`, and a caller can already put OpenRouter's parts in the `state`. Revive when a consumer has an image to judge. `goodall.Block` already has a neutral image to start from.

Facts for whoever builds it, read 2026-11-02 from [Images](https://docs.typesafe.ai/images.md), [Box](https://docs.typesafe.ai/primitives/box.md) and the [1.14 post](https://typesafe.ai/blog/jev-1-14):

- **TypeSafe gates images and Box behind `TypeSafe-Danger-Zone-Images: true`, per request.** Without it the API answers 400, "Images are experimental. Send the header TypeSafe-Danger-Zone-Images: true to use them." goodall's TypeSafe client does not send it.
- **TypeSafe does not vouch for this in production.** It calls these features "Dreamer-ready": the format, answers and limits can change between releases, and its post warns that models do worse on images than on text. So goodall should send the header only when a caller opts in, by a name that says what they are opting into, and never because a request happens to hold an image.
- **Box has no "not found".** It returns a box even when the target is absent, so a caller needs a yes/no question beside it. Box needs `jev-1.14.0`, where images work on 1.13 too.

Observed the same day, one raw POST per row to `api.typesafe.ai/v1/systemone`, no script kept, on one generated 400×300 PNG holding an orange rectangle. So this shows the mechanics and says nothing about accuracy on real images:

| Model, header | Asked | Answer |
|---|---|---|
| 1.14, none | Yes/no, Box, Choice | 400, the sentence above |
| 1.14, on | Box on the rectangle | 200, every edge within 0.02, confidence 0.93 |
| 1.14, on | Box on a blue circle that is not there | 200, a box, confidence 0.2 |
| 1.14, on | Yes/no on the rectangle, then on the circle | 0.95, then 0.02 |
| 1.13, on | Yes/no and Choice, no Box | 200, 0.86 and 0.00, Choice correct |
| 1.13, on | The same with Box | 400, "Invalid request." |

TypeSafe's [models page](https://docs.typesafe.ai/models.md) still gives Jev's input as "Text", which these calls contradict.

## 2026-11-02 OpenAI's conversation input and prompt-cache controls on decisions

`/v1/decisions` takes messages, tool calls and tool outputs as `input`, which could let a `goodall.Conversation` be judged directly, and it has explicit cache breakpoints, a cache key and a TTL. Neither has an equivalent on the System One wire, and neither was exercised. Parked because `decision` starts from what every wire shares. Revive when an agent wants a decision over its own history and rendering that history into the `state` proves worse, or when a consumer's repeated evidence is large enough for the cache discount to matter.

## 2026-11-02 Decision vendors' own direct APIs

Cloudflare, Perplexity, Microsoft and the rest were exercised only through OpenRouter, and whether each has a direct API, and in which shape, was not checked. Parked because OpenRouter already reaches them with one key. Revive when a consumer needs one directly for data handling, price or availability; a server speaking System One needs only a base URL, and one with its own wire is a new translation behind `Decider`.

## 2026-11-02 The Jev Router

`typesafe/jev-router` is documented as an ordinary model on Chat Completions that picks a model and effort per request, so `goodall/openrouter` may already drive it. Not run. It is a conversational model rather than a decision model, so it is outside the decision work. Revive when a consumer wants routing without writing a `Route`; the first step is one live call and a look at what the response's `model` reports.

## 2026-11-02 Direct Gemini

Google's API directly, rather than through OpenRouter. Gemini 4 Argon and the current Flash models already run goodall's full agent loop through OpenRouter ([findings](2026-11-02-model-currency-findings.md)), and the owner ruled on 2026-11-02 that this is enough for now. The `Generic` dialect could reach Google's OpenAI-compatible route, but goodall has no `extra_content` member for thought signatures, so it probably fails on the second turn; no Google key was available to test that. Revive when a consumer needs Google directly for data handling, price or a feature OpenRouter does not carry. The first step is a key and `scripts/probe-agent-loop`.

## 2026-11-02 Rewriting git history to remove an OpenRouter account id

`openrouter/testdata/live_error.json` carried OpenRouter's `user_id` in `968b9b6`, and that commit is public. The recorder now redacts it and the current file is clean as of `f93e8fc`. The owner ruled on 2026-11-02 to leave history as it is: the value is an account identifier and not a key, and a rewrite would change the hashes of every later commit while existing clones and GitHub could still hold the old one. What would un-park it: evidence that the id can be used against the account, or a real key found in history, which would need revoking first in any case.

## 2026-11-02 Local servers in scripts/check-drift

The drift check leaves out LM Studio, llama.cpp, Ollama and LocalAI, and its report says so. They are not vendor APIs a key can reach, so a scheduled run on a machine with no server would report "not checked" every time, teaching a reader to ignore that line, and the model a server has loaded varies by machine. `go test ./...` already runs the LM Studio tests when a server answers. Un-park this if a local server's update breaks goodall without the live tests noticing, or if a consumer depends on one.

## 2026-11-02 GPT-6's async tool calling, mid-turn steering and `configuration_update`

Optional Responses features, parked by [the Responses plan](2026-11-02-responses-wire-plan.md). Async tools (`async: true`, a result returned later by `call_id`) change what one turn of a run means, steering needs a WebSocket transport goodall does not have, and `configuration_update` changes effort mid-history where goodall already states it on every request. None was recorded. Revive async or steering when a consumer needs a long tool or a mid-run instruction that a pause with `Await` cannot serve, and `configuration_update` if keeping request-level effort steady is shown to save real cache money.
