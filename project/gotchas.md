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

## 2026-09-14 Inside `synctest.Test`, derive contexts from `context.Background()`, not `t.Context()`

`synctest.Test` waits for every bubbled goroutine to exit before it returns, and `t.Context()` is only cancelled when the test function returns; a goroutine parked on that context (the transport's body watcher, anything selecting on `ctx.Done()`) deadlocks the bubble. A nil `Done` channel is the mirror trap: a `select` over one is not durably blocked, so the bubble never goes idle and fake time never advances. Found by the transport leaf (`internal/transport/retry_test.go` shows the working pattern).

## 2026-09-14 A second `json.WithUnmarshalers` replaces the first instead of adding to it

The root package now has two interface dispatches, `Block` and `Event`. Building the option set as `json.JoinOptions(json.WithUnmarshalers(evFn), blockUnmarshalers)` compiles and runs, and the *event* dispatch silently never fires: json/v2 treats a later option of the same kind as an override, so only the block unmarshalers survive. The failure reads as "cannot derive concrete type for nil interface with finite type set" on the outer type, which points at the interface rather than at the options. Join the funcs, not the options: `json.WithUnmarshalers(json.JoinUnmarshalers(json.UnmarshalFromFunc(a), json.UnmarshalFromFunc(b)))`. Any leaf adding a third dispatch does the same.

## 2026-09-15 `gofmt -w .` and `gofmt -l .` from the main checkout recurse into `.claude/worktrees/`

Unlike the Go toolchain's `./...`, gofmt walks every directory including dot-directories, so a formatting pass in the main checkout rewrites files in every running agent's worktree and `gofmt -l .` reports their half-written files as yours. Format only what git tracks: `gofmt -l -w $(git ls-files '*.go')`. The same applies to any `find`-style tool run from the root.

## 2026-09-15 A `jsontext.Token` is voided by the next decoder call, and the panic names the wrong line

Reading a JSON object member by member with `jsontext.Decoder` — `tok, _ := dec.ReadToken()` for the name, then `dec.ReadValue()` for the value — and only calling `tok.String()` *after* the `ReadValue` panics with "invalid jsontext.Token; it has been voided by a subsequent json.Decoder call". Copy the name out of the token the moment you have it. The trap is that the panic points at the `String()` call, not at the `ReadValue` that invalidated it, and the same code with the two lines swapped looks identical at a glance. `ReadValue`'s result is borrowed the same way, so `Clone()` it before the next call. Hit while merging streamed `reasoning_details` fragments in `openrouter/stream_blocks.go`.

## 2026-09-15 A service test that stops a run right after starting it may stop it before the provider is ever called

`chat.Service.Send` returns once the run is registered, not once it has sent anything, so a test that calls `Stop` (or `Shutdown`) on the next line can cancel the run at the top of the loop, before `Provider.Stream`. The run still ends and persists correctly — but the fake provider's script cursor has not advanced, so the *next* run in the same test gets `Script[0]` again. The symptom is a later `Stalled` turn hanging where an `Answer` was expected, ten seconds of polling and a failure that names the wrong thing. Drive a subscription to a provider event (`sub.until(goodall.EventBlockStop)`) before stopping, or gate on the tool actually starting.

## 2026-09-15 In encoding/json/v2 a nil slice marshals as `[]`, so a round trip turns `nil` into an empty slice

Unlike v1, json/v2 writes a nil slice as an empty array unless the field asks for `format:emitnull`, so `Blocks(nil)` comes back from a decode as `Blocks{}` and `reflect.DeepEqual` fails on a difference nothing in the payload records. It bit the wire writers' round-trip test through a `TurnEnd` whose `Response.Message` had no content — the failure names the two `goodall.Blocks` values in a wall of `%#v` and reads like an encoder bug rather than a fixture one. Any fixture compared with `DeepEqual` across an encode should carry non-nil slices, or the comparison should be over the JSON.

## 2026-09-15 A hand-written SSE fixture needs a blank line after `data: [DONE]`

An authored OpenRouter stream that ends `data: [DONE]\n` instead of `data: [DONE]\n\n` decodes as a stream that never terminated: the framer dispatches an event only on the blank line, so the terminal frame is never delivered and the run fails with "the stream ended before message_stop, so the message is incomplete" — an error that points at the accumulator rather than at the missing newline. Every recorded fixture ends in `\n\n`; check the tail bytes (`tail -c 30 file | xxd`) before blaming the decoder.

## 2026-09-15 Recording a provider fixture from a *refused* request leaves a JSON error body in a `.sse` file

`scripts/record-fixtures` saved whatever came back, whether or not the call succeeded — the right call for a stream that broke halfway, the wrong one for an HTTP 400, whose body is a JSON error envelope. The run aborts and prints the error, but the file stays on disk, and the next `go test` reads it as a recording of an answer: the failure surfaces as `the stream ended before message_stop` on a fixture nobody remembers writing. Fixed at the source (`recorder.refused` discards a capture whose status is not 200), so this is only a warning to anyone who finds an old one: delete the fixture as well as re-running the recorder.
