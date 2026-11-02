# Conformance fixtures

Every file here is recorded, by

    go run ./scripts/record-fixtures -provider conformance -env .env

on **2026-11-02**, with one exchange per backend and form: `typesafe` on `jev-1.13.0`, `openrouter` on `openai/gpt-6-luna-decisions`, `openai` on `gpt-6-luna`, and `lmstudio` with no model named, answered by `julia-1` on LM Studio 0.4.28+2.

| Form | What it asks |
|---|---|
| `object` | The suite's questions over the object state. `Tool` and `AuthoredTool` must reproduce this request. |
| `text` | The same questions over the same complaint as a string. `Route` must reproduce this request. |
| `refused` | The same questions of a model that does not exist, so the server's own error body is on disk. The status is in the suite's table, since a fixture holds no status line. |

Each response has a `.request.json` beside it, indented for reading with its member order untouched, and the suite's replay answers only a request matching one of them. The bytes are one model's judgment on one day, and models disagree: `julia-1` sends the text form to `technical` where Luna says `billing`. So the suite asserts structure and accessors and never an answer.
