# OpenRouter fixtures

Two sets live here, and the file names say which is which.

- **`live_*` — recorded**, by `go run ./scripts/record-fixtures -provider openrouter` and
  `-provider openai`, from the live API on **2026-09-15**. Every byte of a `live_*.sse` or
  `live_*.json` came off the wire; the `live_*.request.json` beside each one is the body
  goodall sent to get it, indented for reading but otherwise unchanged. No headers are
  captured, so no key can be in one.
- **everything else — hand-authored**, written on 2026-09-15 from the shapes OpenRouter
  documents and checked against one live exchange by hand. They stay because the existing
  unit tests read them, and because they are short enough to read as illustrations of the
  format where a real recording is not.

## The models

| Recording | Endpoint | Model |
|---|---|---|
| `live_*` (no prefix) | `openrouter.ai/api/v1` | `anthropic/claude-opus-5` |
| `live_encrypted_reasoning_tool_use*` | `openrouter.ai/api/v1` | `openai/gpt-6-astra` |
| `live_openai_*` | `api.openai.com/v1`, `Dialect: OpenAI` | `gpt-5.6-terra` |

The constants and the reasons for each choice are in `internal/livemodel`.

## Recorded exchanges

| File | What it is |
|---|---|
| `live_text.sse` / `live_text_blocking.json` | A one-line answer, streamed and blocking. The two are separate calls, so only the stop reason, model and block shape are comparable. |
| `live_tool_use.sse` / `live_tool_use_blocking.json` | A forced tool call, arguments streamed as fragments keyed by index. |
| `live_thinking_tool_use.sse` + `_turn2` | Claude thinks, calls the tool, and the second turn replays the signed `reasoning.text` entry with the tool result. The second turn answering is the proof that goodall's replay is accepted. |
| `live_encrypted_reasoning_tool_use.sse` + `_turn2`, `_turn3` | The same shape on an OpenAI model, whose entries are `reasoning.summary` and `reasoning.encrypted` in `openai-responses-v1` format. Three turns, because this model reasons on the turn that *answers* rather than the turn that calls the tool — see below. |
| `live_image.sse` | A generated orange PNG the model is asked to name the colour of. |
| `live_pdf.sse` | A generated one-page PDF, read through the `file-parser` plugin's `native` engine. |
| `live_debug_echo.sse` | The same question with `debug.echo_upstream_body`, so the Anthropic-shaped body OpenRouter sent upstream is on disk. |
| `live_error.json` | An unrecognised model id: HTTP 400, not 404. |
| `live_openai_text.sse`, `live_openai_tool_use.sse`, `live_openai_reasoning.sse` | The OpenAI dialect against api.openai.com: text, a forced tool call, and a reasoning answer. |

## What the recordings confirmed, and what they corrected

The two differences the hand-authored files predicted both hold:

- a recorded blocking response does carry more members than the wire structs model, which
  they ignore: `provider`, `service_tier` and `system_fingerprint` at the top, `logprobs` and
  `native_finish_reason` on the choice, `cost_details` and `is_byok` in the usage; and
- a merged `reasoning_details` entry does follow fragment arrival order. The Claude stream
  sends `{type,text,format,index}` fragments and then `{type,signature,format,index}`, and the
  entry goodall replays on the next turn is `{type,text,format,index,signature}`.

Four things the recordings **corrected**, each of which was a bug fixed before these files were
committed (the regression tests are in `dialect_test.go`):

1. **`api.openai.com` refuses `max_tokens`** on a reasoning model — HTTP 400, "Unsupported
   parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens'
   instead." The OpenAI dialect now sends `max_completion_tokens`; OpenRouter still takes
   `max_tokens`.
2. **`api.openai.com` streams no usage unless asked.** Every streamed OpenAI-dialect answer
   reported zero tokens. The OpenAI and LM Studio dialects now send
   `stream_options: {include_usage: true}`; OpenRouter does not, because it documents the
   option as a deprecated no-op and always reports usage.
3. **OpenAI's Chat Completions endpoint will not serve function tools together with
   reasoning.** "Function tools with `reasoning_effort` are not supported for … in
   `/v1/chat/completions`. To use function tools, use `/v1/responses` or set
   `reasoning_effort` to `'none'`." It is refused on `gpt-6-astra` and `gpt-5.6-terra` alike,
   so there is no OpenAI-dialect reasoning *round trip* here: `live_openai_tool_use.sse`
   turns reasoning off, and `live_openai_reasoning.sse` offers no tool. The encrypted
   reasoning round trip is recorded through OpenRouter instead, which routes upstream to
   `/v1/responses`.
4. **An OpenAI model reasons on the turn that answers, not on the turn that calls a tool.**
   At `effort: "high"`, `openai/gpt-6-astra` returned `reasoning_tokens: 0` and no
   `reasoning_details` on the tool-calling turn and 98 `reasoning_details` fragments on the
   turn that answered. That is why that exchange has a third turn: it is the one whose
   request replays an entry goodall cannot read, and it was accepted.

Two smaller facts worth keeping:

- OpenRouter **adds to the upstream body**. `live_debug_echo.sse` shows it sending Anthropic a
  `thinking: {type: "adaptive", display: "summarized"}` object and a `cache_control` breakpoint
  that goodall's request did not set, and a `metadata.user_id` of `<redacted>`.
- **`anthropic/claude-fable-5.1` is newer than the model recorded here** but its
  `supported_parameters` name no `tool_choice` and its reasoning is mandatory. OpenRouter drops
  a parameter the endpoint does not support rather than refusing it, so a forced-tool recording
  against it would have been silently unforced.

## Hand-authored files

| File | What it is |
|---|---|
| `stream-reasoning-tools.txt` | A stream with a thinking entry (text fragments then a signature-only fragment), a text block, two tool calls interleaved by index, a `finish_reason` frame, a usage frame that repeats `finish_reason`, a comment keep-alive and `[DONE]`. |
| `blocking-reasoning-tools.json` | The same exchange as a blocking response. `Collect` over the stream must equal `Complete` over this. |
| `stream-error-only.txt` | An HTTP 200 stream whose only frame is an error object — the "200 is not success" case. |
| `models.json` | A two-model excerpt of `GET /api/v1/models`, one multimodal and one text-only, with the price strings the model tests assert on. |
