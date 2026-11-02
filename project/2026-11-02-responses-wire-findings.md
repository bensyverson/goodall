# GPT-6 runs the agent loop directly

Findings, 2026-11-02, closing `goodall v1.7` (`Uz2EPX`). **All four GPT-6 models now pass `goodall.Agent` on api.openai.com with thinking and a tool, and each accepted a request replaying its encrypted reasoning.** This turns the table of 400s in the [model currency findings](2026-11-02-model-currency-findings.md) into passes.

## The agent loop

`internal/liveprobe` now asks for arithmetic before the tool, since a bare weather question drew no reasoning. Each row is one run.

```
go run ./scripts/probe-agent-loop -env /abs/.env -dialect openai -models gpt-6-astra,gpt-6.1-sol,gpt-6.1-terra,gpt-6-luna

gpt-6-astra      ok turns=2 tool_calls=1 in=258 out=87 reasoning=38 by_turn=[0 38]
gpt-6.1-sol      ok turns=2 tool_calls=1 in=258 out=99 reasoning=50 by_turn=[0 50]
gpt-6.1-terra    ok turns=2 tool_calls=1 in=267 out=98 reasoning=53 by_turn=[7 46]
gpt-6-luna       ok turns=2 tool_calls=1 in=258 out=115 reasoning=71 by_turn=[0 71]
```

The same with `-effort off`. `gpt-6-astra` and `gpt-6.1-sol` refuse with a loud 400, as the plan expected, and the refusal lists the values they do take. The probe attaches no catalog entry, so this run did not exercise `ThinkingOffNever`:

```
gpt-6-astra      FAIL turns=0 kind=invalid_request (400 unsupported_value): Unsupported value: 'none' is not supported with the 'gpt-6-astra' model. Supported values are: 'low', 'medium', 'high', 'xhigh', and 'max'.
gpt-6.1-sol      FAIL turns=0 kind=invalid_request (400 unsupported_value): Unsupported value: 'none' is not supported with the 'gpt-6.1-sol' model. Supported values are: 'low', 'medium', 'high', 'xhigh', and 'max'.
gpt-6.1-terra    ok turns=2 tool_calls=1 in=258 out=44 reasoning=0 by_turn=[0 0]
gpt-6-luna       ok turns=2 tool_calls=1 in=258 out=43 reasoning=0 by_turn=[0 0]
```

## A two-turn pass often replays nothing

Only reasoning from before the last turn travels back to the server, and a total could not show that, so rows now print `by_turn`. The rows above show three of four models replaying nothing at low effort. Effort does not settle it:

```
go run ./scripts/probe-agent-loop -env /abs/.env -dialect openai -effort high -models gpt-6-astra,gpt-6.1-sol,gpt-6.1-terra,gpt-6-luna

gpt-6-astra      by_turn=[0 154]
gpt-6.1-sol      by_turn=[0 169]
gpt-6.1-terra    by_turn=[65 0]
gpt-6-luna       by_turn=[86 55]
```

Astra and Sol reported 0 reasoning tokens on the tool-calling turn in 6 of 6 further runs at high effort, 3 per model, through `go run ./scripts/record-fixtures -provider openai -only openai_thinking_tool_use -model <id> -out <scratch>`, with the exchange at two turns. That fits what the OpenRouter recordings showed for Astra in 2026-09, and it is a tendency on this prompt, not a rule: `two_turn_stream_*` in the plan drew a reasoning item before the call from both.

So the exchange now records three turns. One run each, same command:

| Model | Turn 3 replayed | Result |
|---|---|---|
| `gpt-6-astra` | 1 reasoning item, 4,536 characters of ciphertext | `end_turn`, input 638 |
| `gpt-6.1-sol` | 1 reasoning item, 4,556 characters | `end_turn`, input 630 |

A 200 here shows only that the request was accepted, since the plan records that the server also takes a request with the reasoning lost. That it was carried is held by `TestTheRecordedResponsesTurnReplaysItsReasoningByteForByte`, over `gpt-6.1-terra`'s committed recording.

**Worth knowing for a future reader of the drift check:** a passing `loop/openai/*` row says nothing about reasoning replay. At low effort it usually replays nothing, and a row that did replay would still pass with the reasoning lost, since the server does not complain. Only the offline tests hold it.

## What changed from the plan

- **The pin is `gpt-6.1-terra`,** as recommended. `TestLiveOpenAIDialectRoundTrip` takes a thinking tool call through two turns on it and asserts non-empty `encrypted_content`; one run read turn 1 `reasoning=80`, 2,232 characters of ciphertext, and turn 2 `end_turn`.
- **More was recorded than the leaf asked:** blocking, image, PDF and error exchanges, because the probe's requests were hand-built and these go through the production client. OpenAI answers an unknown model with a 404 `model_not_found`, where OpenRouter answers 400.
- **The drift check gained `gpt-6-luna` at low.** The baseline took 7 changes, approved by the owner: Astra, Sol and Terra at low now pass, Luna low and Terra off are new passes, and the two `gpt-5.6-terra` rows are gone.
- **A drift run now costs about two cents,** not one: USD 0.0179 reported, against 0.0085 before, from `go run ./scripts/check-drift -env .env` with the arithmetic prompt, which every agent-loop row now asks.

## Still open

- **`decide/openrouter/nace-ai/drex-max` answered a 429 "Model busy, retry later" on 2 of the 5 drift runs on this date,** once for each state form. A row refused for capacity (`rate_limited`, `overloaded`, `server`) is now `NOT CHECKED` rather than drift, and the baseline keeps what it had.
- **The error prefix.** The rows above read `openrouter:` on OpenAI's refusals. Ruled the same day and fixed: `APIError.Provider`, `CapabilityError.Provider` and `ModelInfo.Provider` now name the client's dialect, so they read `openai:`. This breaks anyone comparing against `ProviderName` outside the OpenRouter dialect. `Extensions.Provider` keeps the package's name.
- **Still never observed,** so only authored fixtures cover them: `response.failed`, the stream's `error` event, refusals and `content_filter`.
