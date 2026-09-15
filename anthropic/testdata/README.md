# Anthropic fixtures

Every file here is **hand-authored from the documented event and body shapes**
in `project/2026-09-14-research-findings.md` section 1, not recorded from the
live API. They exist so the streaming, blocking, error and catalogue paths have
something byte-shaped to parse offline; `scripts/record-fixtures` will replace
them with real recordings when the fixture leaf lands, and the tests that read
them should keep passing unchanged when it does.

| File | What it is |
| --- | --- |
| `thinking_tool_use.sse` | A full stream: a thinking block with a signature, a text block, a tool call streamed as `input_json_delta` fragments, a `ping`, then `message_delta` and `message_stop`. |
| `thinking_tool_use.json` | The same message as a non-streaming response body. Collecting the stream above must produce exactly this response — that equality is the point of the pair, so the two files are edited together. |
| `error_overloaded.sse` | HTTP 200 whose stream carries an `overloaded_error` after `message_start`, which is how an overload mid-generation arrives. |
| `unknown_events.sse` | An SSE event name and two content-block delta types this version of goodall does not model, so the "surface, never drop" rule has something to surface. |
| `model.json` | A `GET /v1/models/{id}` body with the capability tree, including leaves (`effort.max`, `structured_outputs` on the second file) deliberately absent so the tri-state stays honest. |
| `model_image_only.json` | A model that reads images and refuses PDFs, which is what makes the two facts separate rather than one "vision" flag. Its `thinking` branch carries no `supported` member, so the fallback to the thinking *types* is exercised. |

The catalogue tree's shape here — `capabilities.{image_input,pdf_input,structured_outputs}`,
`capabilities.thinking.{supported,types.{adaptive,enabled}}` and
`capabilities.effort.{supported,low..max}`, each a `{"supported": bool}` leaf —
was confirmed against a live `GET /v1/models/claude-sonnet-4-5-20250929` on
2026-09-15. The values below are still invented; only the shape is verified.

The identifiers, signatures and token counts are invented. Nothing here is a
secret and nothing here needs a key to read.
