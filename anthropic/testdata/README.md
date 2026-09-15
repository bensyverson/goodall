# Anthropic fixtures

Two kinds of file live here.

**Recorded** files are named `live_*`. They were captured from the live
Messages API by `scripts/record-fixtures` on **2026-09-15** against
**`claude-sonnet-5`**, and they are exactly the bytes the API returned: no
headers, no status line, no key. Refresh them with

    go run ./scripts/record-fixtures -env .env -out anthropic/testdata

Each recording has a `.request.json` beside it holding the body goodall sent,
so a reader can see what produced the answer. Those request files are indented
for reading; member order is untouched, so they still show the byte-stable
prefix the translation builds. They carry message ids, tool-call ids and
thinking signatures, which are not secrets.

**Hand-authored** files have no prefix. They are written from the documented
event and body shapes in `project/2026-09-14-research-findings.md` section 1
and cover the cases a recording cannot reach: an overload mid-stream, an event
type goodall does not model, and a model whose catalog leaves capability
leaves absent.

| File | Source | What it is |
| --- | --- | --- |
| `live_text.sse` | recorded | A one-line answer, streamed. |
| `live_text_blocking.json` | recorded | The same question through `Complete`, so the two paths can be compared. |
| `live_tool_use.sse` | recorded | A forced `tool_choice`, streamed as `input_json_delta` fragments. |
| `live_tool_use_blocking.json` | recorded | The same forced tool call through `Complete`. |
| `live_thinking_tool_use.sse` | recorded | Adaptive thinking at effort low with summarized display, producing a signed thinking block and a tool call. |
| `live_thinking_tool_use_turn2.sse` | recorded | The next turn, sent with that thinking block replayed and the tool result appended. The answer, rather than an error, is the proof that goodall's replay is accepted. |
| `live_image.sse` | recorded | A generated 8×8 PNG the model was asked to name the color of. |
| `live_pdf.sse` | recorded | A generated one-page PDF the model was asked to quote. |
| `live_error.json` | recorded | A real 404 envelope, from a model identifier Anthropic does not serve. |
| `thinking_tool_use.sse` | authored | A full stream: a thinking block with a signature, a text block, a tool call streamed as `input_json_delta` fragments, a `ping`, then `message_delta` and `message_stop`. |
| `thinking_tool_use.json` | authored | The same message as a non-streaming response body. Collecting the stream above must produce exactly this response. |
| `error_overloaded.sse` | authored | HTTP 200 whose stream carries an `overloaded_error` after `message_start`, which is how an overload mid-generation arrives. |
| `unknown_events.sse` | authored | An SSE event name and two content-block delta types this version of goodall does not model, so the "surface, never drop" rule has something to surface. |
| `model.json` | authored | A `GET /v1/models/{id}` body with the capability tree, including leaves (`effort.max`, `structured_outputs` on the second file) deliberately absent so the tri-state stays honest. |
| `model_image_only.json` | authored | A model that reads images and refuses PDFs, which is what makes the two facts separate rather than one "vision" flag. Its `thinking` branch carries no `supported` member, so the fallback to the thinking *types* is exercised. |

## Why the hand-authored thinking pair stays

`thinking_tool_use.sse` and `thinking_tool_use.json` are the proof that
collecting a stream produces exactly the response the blocking path returns.
That proof needs two files describing **one** message, and two live calls can
never provide it: every call gets a fresh message id and a fresh thinking
signature, so a recorded pair would differ in bytes for reasons that have
nothing to do with the accumulator. The pair is therefore authored and edited
together, and the recorded siblings (`live_text*`, `live_tool_use*`) are
compared on stop reason, model and block count instead.

The catalog tree's shape — `capabilities.{image_input,pdf_input,structured_outputs}`,
`capabilities.thinking.{supported,types.{adaptive,enabled}}` and
`capabilities.effort.{supported,low..max}`, each a `{"supported": bool}` leaf —
was confirmed against a live `GET /v1/models/claude-sonnet-4-5-20250929` on
2026-09-15. The values in the authored files are still invented; only the
shape is verified.

Nothing here is a secret and nothing here needs a key to read.
