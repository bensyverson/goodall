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
