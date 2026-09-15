# OpenRouter fixtures

These files are **hand-authored**, not recorded. They were written from the shapes OpenRouter
documents, then checked against one live streamed exchange and one live blocking exchange on
`anthropic/claude-haiku-4.5` (upstream Amazon Bedrock) made by hand on 2026-09-15 — the
fragment structure, the member order inside a `reasoning_details` entry, the empty
`"content": ""` fillers, the repeated `finish_reason` on the usage frame and the
`x-generation-id` header all match what the live call returned. The text, ids, signatures and
token counts are shortened or invented so the files stay readable and carry nothing that needs
a key.

`scripts/record-fixtures` (the fixture leaf) will replace them with real recordings. When it
does, expect two differences that the tests already tolerate:

- a recorded blocking response carries more members per entry (`id`, `service_tier`,
  `cost_details`, `system_fingerprint`, `logprobs`, …), which the wire structs ignore; and
- member *order* inside a merged `reasoning_details` entry follows the order the fragments
  arrived in, which happened to match the blocking response's order on the live call but is
  not a promise. The round-trip test compares JSON after compaction, not raw bytes.

| File | What it is |
|---|---|
| `stream-reasoning-tools.txt` | A stream with a thinking entry (text fragments then a signature-only fragment), a text block, two tool calls interleaved by index, a `finish_reason` frame, a usage frame that repeats `finish_reason`, a comment keep-alive and `[DONE]`. |
| `blocking-reasoning-tools.json` | The same exchange as a blocking response. `Collect` over the stream must equal `Complete` over this. |
| `stream-error-only.txt` | An HTTP 200 stream whose only frame is an error object — the "200 is not success" case. |
| `models.json` | A two-model excerpt of `GET /api/v1/models`, one multimodal and one text-only, with the price strings criterion 3 asserts on. |
