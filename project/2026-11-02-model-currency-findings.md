# Does goodall keep up with current models?

Findings, 2026-11-02, on GPT-6 and Gemini 4 as conversational models. **Through OpenRouter and through Anthropic's API, current models run goodall's full agent loop today. Directly against OpenAI, all four GPT-6 models now do too, since the dialect moved to Responses the same day; when this was first written it could not, because the dialect spoke Chat Completions and OpenAI only offers tools with reasoning on Responses.** Direct Gemini is not supported and could not be tested. Decision models are covered separately in the [decision models findings](2026-11-02-decision-models-findings.md).

> **Corrected 2026-11-02 (`KgGLdl`).** The OpenAI dialect now speaks `/v1/responses`. All four GPT-6 models pass `goodall.Agent` at low effort with reasoning reported, `go run ./scripts/probe-agent-loop -env /abs/.env -dialect openai -models gpt-6-astra,gpt-6.1-sol,gpt-6.1-terra,gpt-6-luna`, and the OpenAI rows below describe the old wire. Off is unchanged: Astra and Sol refuse it, Terra and Luna pass. The rows and what they do not show are in [the Responses findings](2026-11-02-responses-wire-findings.md).

## What was run

`go run ./scripts/probe-agent-loop -env /abs/.env -models <ids>` drives `goodall.Agent` to a terminal event with one tool, through the production path. A pass means the model called the tool, took the result back on a second turn and finished; the second turn is where a provider refuses a history that lost its signed reasoning. `go run ./scripts/probe-gpt6 -env /abs/.env` sends raw requests to compare the endpoints. Each model and setting ran once.

| Route | Effort | Models | Result |
|---|---|---|---|
| OpenRouter | low | `google/gemini-4-argon`, `google/gemini-3.8-flash`, `google/gemini-3.5-flash-lite` | all pass |
| OpenRouter | low | `openai/gpt-6-astra`, `gpt-6.1-sol`, `gpt-6.1-terra`, `gpt-6-luna` | all pass |
| OpenRouter | low | `anthropic/claude-sonnet-5.5`, `anthropic/claude-fable-5.1` | all pass |
| OpenAI dialect | low | `gpt-6-astra`, `gpt-6.1-sol`, `gpt-6.1-terra`, `gpt-6-luna`, `gpt-5.6-terra` | all 400: "Function tools with reasoning_effort are not supported … use /v1/responses" |
| OpenAI dialect | off | `gpt-6-astra`, `gpt-6.1-sol` | 400: `none` is not supported |
| OpenAI dialect | off | `gpt-6.1-terra`, `gpt-6-luna` | pass, with no thinking |

The raw probe got 200 and a function call from `/v1/responses` on all four GPT-6 models at low effort, and 200 from OpenRouter's Chat Completions.

**One limit on the passes:** every run reported `reasoning=0` in usage, so nothing here shows how much thinking happened or that goodall read a reasoning count. A pass shows the loop completes with thinking requested. Older recordings noted the same zero for `gpt-6-astra` through OpenRouter (`openrouter/fixtures_test.go`).

> **Corrected 2026-11-02 (`KgGLdl`).** On the OpenAI dialect the zero came from the prompt, and goodall does read the count. A prompt with arithmetic in it drew non-zero `reasoning` on all four models over the same probe. Whether the same holds for the OpenRouter rows above was not checked by that run.

## GPT-6

OpenAI's [guide](https://developers.openai.com/api/docs/guides/latest-model) states the rule: "GPT-6 Astra, GPT-6.1 Sol, and GPT-6.1 Terra support Chat Completions, but tool calling requires Responses", while Sol and Luna take function calling there only with `reasoning_effort: "none"`. The probe found a narrower split than that sentence: Terra did take tools with effort off. Its [migration guide](https://developers.openai.com/api/docs/guides/migrate-to-responses) says Chat Completions "remains supported" and that Responses is recommended, so this is a closed door for one feature rather than a shutdown.

Consequences for goodall:

- **The gap is direct OpenAI only.** The brief's minimum is Anthropic and OpenRouter, and both work. A developer with only an OpenAI key cannot run Astra or Sol with tools at all.
- **The failure is loud and clear,** in OpenAI's own words, so nobody is silently misled.
- **The backlog's trigger has fired.** The [Responses entry](backlog.md) says to revive it "if a feature lands only on one of the other endpoints". Tools with reasoning now has.
- **`livemodel.OpenAI` is stale in its reasoning.** It pins `gpt-5.6-terra` as a model that "serves all three of text, a tool call and a reasoning answer", and at low effort with tools it now returns the same 400. Its own comment says so on the same page, so the pin works only because the live tests do not combine the two. Worth reading when this gets built.
- **`EffortMax`:** the Chat Completions 400 lists efforts up to `xhigh`, while Responses lists `max` as well. Not tested through goodall.

GPT-6's other additions (async tool calling, mid-turn steering, `configuration_update`) are all Responses features. They are optional, and none was assessed here.

## Gemini

Google's current flagship is `gemini-4-argon`, GA on October 13 ([changelog](https://ai.google.dev/gemini-api/docs/changelog), [guide](https://ai.google.dev/gemini-api/docs/latest-model)), with thinking levels `low`, `medium` and `high` and a 1M-token window shared by input, thinking and output. Its quickstart uses a newer Interactions API rather than `generateContent`. Argon passed through OpenRouter, signed reasoning and all, with no goodall change.

Direct Gemini is a different matter, and **none of it was run, because `.env` has no Google key**:

- Google offers an OpenAI-compatible route at `generativelanguage.googleapis.com/v1beta/openai/`, which the `Generic` dialect could reach.
- [Its docs](https://ai.google.dev/gemini-api/docs/openai) say Gemini 3 supports thought signatures there. Third-party reports say they travel in `extra_content` on tool calls and that a missing one is a 400 on the follow-up.
- `openrouter/` has no `extra_content` member, so goodall probably drops it and would fail on the second turn. That is inferred from reading the code and third-party reports.

Direct Gemini was never in the brief, so this is a gap only if someone wants it.

## A defect the run exposed

The OpenAI dialect reported the tools-with-reasoning 400 as `kind=unknown`, though the unsupported-value 400 came back as `invalid_request`. So a caller branching on `KindInvalidRequest` misses it. Filed in `job` under "Issues found on the way".

> **Fixed 2026-11-02 (`1JOSCO`).** The cause: this body's `error.code` is JSON `null`, which `errorCode` decoded into an int as 0 with no error and called numeric, so `KindForStatus(0)` set the kind to unknown, and the transport only fills a kind that is empty. A body with no `code` member at all was never affected. Regression test: `openrouter/errors_null_code_test.go`.

## Why this went unnoticed

> **Landed 2026-11-02:** `scripts/check-drift` now looks outward; see [its findings](2026-11-02-check-drift-findings.md).

Nothing in the repo looks outward between sessions. `go test ./...` replays against the pinned models, so a new model family, a stale pin or a moved feature stays invisible until someone asks, as happened here. On this one date, a look found GPT-6.1, Gemini 4, Jev 1.14 with a fourth question type, decision models on five local servers, and the Responses trigger firing.
