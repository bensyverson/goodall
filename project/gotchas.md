# Gotchas

Project-specific traps that cost real time and that no general rule predicts. Read at session start; append when you hit one.

- **Fix at the source when you can.** A gotcha is a bug report on our tooling, not a permanent fact — if it can be fixed in code, file it in `job` and fix it instead of recording it here.
- **Delete anything that becomes obvious, gets fixed, or stops recurring.** Keep this list short; a long list is one nobody reads.
- **Feedback about `AGENTS.md` itself** — a rule that was wrong, misread, or cost time — goes here too, prefixed `rule:`. It is harvested when the shared rules are reviewed.

Format: one dated H2 headline, then one paragraph. If an entry needs more than that, it's a finding — write it as a dated doc under `project/` and link it from the paragraph.

---

## 2026-09-14 A `job import` criterion containing ": " must be quoted

YAML parses `- A javascript: link renders as text` as a map, and `job import` reports only "criteria entries must be strings". Quote any criterion or title that contains a colon followed by a space. `awk '/^```yaml/{f=1;next}/^```/{f=0}f' plan.md | grep '^ *- .*: '` finds the offenders.

## 2026-09-14 The `claude-api` skill's "use the official SDK" rule does not apply here

The bundled skill fires whenever Anthropic is named and insists code call Claude through `anthropic-sdk-go`. goodall's zero-dependency core talks to the wire itself by design (see the architecture plan). Use the skill for wire facts, model ids and the preserved-thinking rules; ignore its SDK mandate.

## 2026-09-14 A literal BOM in a Go string literal breaks the *source file*, not just the test

Writing a test case for BOM-stripping with `"﻿" + "data: ..."` fails the build with `illegal byte order mark` — Go rejects an actual U+FEFF byte sequence anywhere in a source file except as the file's own leading bytes, and a string-writing tool that emits the literal UTF-8 bytes for `﻿` (rather than the six ASCII characters `\`, `u`, `F`, `E`, `F`, `F`) triggers this even though the *text* looks like a harmless escape. Use `"\xEF\xBB\xBF"` (raw hex-byte escapes) instead: it stays plain ASCII in the source and only becomes the BOM at runtime.
