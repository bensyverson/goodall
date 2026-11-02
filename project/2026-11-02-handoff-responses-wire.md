# Handoff, 2026-11-02: the Responses wire is in, its tooling is not

`goodall v1.7` (`Uz2EPX`) is two leaves from done. This replaces [the v1.6 handoff](2026-11-02-handoff-v1.6-done.md) as the starting point. No agent is running and no worktree is left.

## Done this session

| Commit | Leaf | What |
|---|---|---|
| `61571f1` | `yEETo1` | [The plan](2026-11-02-responses-wire-plan.md), `scripts/probe-responses` and its recordings |
| `26043da` | `dxKI0t`, `Ssp3XD` | The OpenAI dialect on `/responses`, blocking and streamed |

## Next

`IhZuY4` moves the recorder, the pin and the drift check onto the new wire, then `KgGLdl` proves all four GPT-6 models through `goodall.Agent`. Read both leaves' notes first. They form a chain, so one agent can take both, except for the baseline step below.

**The tree is between states until `IhZuY4` lands.** The offline suite passes. These do not yet match the wire:

- `internal/livemodel.OpenAI` still pins `gpt-5.6-terra`, and `openrouter/live_test.go` and `scripts/record-fixtures` still describe the old wire. A plain `go test ./...` was **not** run after the move, so the live OpenAI tests are unverified.
- `scripts/record-fixtures -provider openai` still writes `live_openai_*.sse` into `openrouter/testdata`. The chat fixture tests glob `live_*.sse`, and the skip that protected them is gone, so a re-record would make them misread those files. Fix this in the same leaf.
- The chat `live_openai_*` fixtures are deleted, so `check-drift`'s glob for the `openai` wire finds only the conformance ones.

## Rulings from the owner

- **The pin is `gpt-6.1-terra`** (note on `IhZuY4`).
- **The drift baseline:** the main thread sorts the `check-drift` report into changes from the wire move and changes vendors made on their own, brings it to the owner, and runs `-accept` in a commit only once they approve that run. The `/drift-check` Skill and the scheduled job still never run it.

## Worth knowing

- **The Monday job runs next on November 9,** and until the baseline is accepted, its report will include the OpenAI changes.
- **The smoke run had no reasoning.** `probe-agent-loop` on Astra at low effort passed with `reasoning=0`, because its prompt draws none. `KgGLdl` needs a prompt that makes the model think, or it passes over zero cases.
- **`ME7` on `Ssp3XD` passed as restated, not as written.** See the leaf's closing note and the correction in the plan.
- **Never observed,** so only authored fixtures cover them: `response.failed`, the stream's `error` event, refusals and `content_filter`.

## Open issues

Unchanged: `HHn1fo`, `bA7105` and `QVt82X`.
