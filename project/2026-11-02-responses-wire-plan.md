# The Responses wire for direct OpenAI

Plan, 2026-11-02, for `goodall v1.7` (`Uz2EPX`). **The OpenAI dialect moves from Chat Completions to `/v1/responses`, inside `goodall/openrouter`, so a developer with only an OpenAI key can run GPT-6 as a thinking agent with tools.** OpenRouter and local servers stay on Chat Completions. Why this is needed is in the [model currency findings](2026-11-02-model-currency-findings.md).

## Rulings

The owner ruled on all three on 2026-11-02, each as recommended.

1. **The wire lives behind a dialect switch in `goodall/openrouter`.** A new `Quirks` field picks the conversation wire, the same way `Quirks.DecisionWire` already picks the decision wire, with the code in `openrouter/responses_*.go`. Direct OpenAI then keeps one client for conversation, decisions, the catalog and errors. The cost, accepted: the package named `openrouter` holds more OpenAI code. A new `goodall/openai` package would have given direct OpenAI users two clients, and an `internal/responses` layer would have one caller.
2. **The OpenAI dialect moves entirely.** One dialect speaks one conversation wire. There is no way to choose Chat Completions against api.openai.com any more. This is a breaking change, which the BUILD stage allows. `live_openai_*` chat fixtures are replaced.
3. **OpenRouter's `/responses` does not share the wire now.** Its Chat Completions carries the same models, and no feature was found only on `/responses`. The [backlog entry](backlog.md) stays, with the differences below recorded so a later switch is cheap.

Also decided in this plan, within the brief the leaf gave it:

- **`store: false` on every request, and never `previous_response_id`.** goodall owns the conversation, and a pause can outlive the process with no state on the server.
- **Async tool calling, mid-turn steering and `configuration_update` are out of scope**, and now parked in the backlog. All three are opt-in by OpenAI's own [description](https://developers.openai.com/api/docs/guides/latest-model), and no recording here used them. Steering needs a WebSocket transport, async changes what a run's turn means, and goodall already states the effort on every request.

## What was observed

Everything in this section comes from one command, which writes each request and response under `scripts/probe-responses/testdata/`:

```
go run ./scripts/probe-responses -env /abs/path/.env
```

**Each row is one run unless it says otherwise.** That is enough to show what the API accepts, and not enough to show how a model tends to behave.

| Exchange | Asked | Observed |
|---|---|---|
| `two_turn_stream_*`, Astra, Sol, Terra | Tool call with reasoning, items replayed verbatim | 200 on both turns, `output=[reasoning,function_call]` then `[message]` |
| `two_turn_stream_gpt-6-luna` | The same | 200 on both turns, but **no reasoning item at low effort**, so this run tests no reasoning round trip. `tool_result_image` on Luna does: `[reasoning,function_call]`, replayed, 200 |
| `no_include` | No `include` member, `store: false` | `encrypted_content` arrives anyway |
| `strip_reasoning` | Follow-up with the reasoning item dropped | **200, no complaint.** Input 129 tokens against 186 to 239 for a full replay, and 52 reasoning tokens spent on the follow-up against 0 |
| `strip_encrypted` | Reasoning item kept, `encrypted_content` removed | **200, no complaint.** Input 129, 29 reasoning tokens |
| `minimal_call` | `function_call` rebuilt from `call_id`, `name`, `arguments` | 200. Input 188, 0 reasoning tokens, so the reasoning still counted |
| `three_turn_minimal` | The same, plus the assistant message rebuilt from its text and `phase` dropped | 200 on all three turns |
| `effort_max` | `max` on all four models | 200 with reasoning, all four |
| `effort_none` | `none` on all four models | 400 on Astra and Sol, naming the supported values. 200 on Terra and Luna |
| `incomplete` | `max_output_tokens: 16`, streamed | `response.incomplete`, `reason: max_output_tokens`, output holding only a reasoning item |
| `strict_absent` | Loose schema, no `strict` member | 200, and **the schema was rewritten**: the response's tool has `strict: true`, `additionalProperties: false`, and optional `units` moved into `required` |
| `strict_false` | Loose schema, `strict: false` | 200, schema unchanged |
| `strict_true` | Loose schema, `strict: true` | 400 `invalid_function_parameters` |
| `media` | `input_image` and `input_file` as data URLs | 200, named the color and quoted the PDF. A first attempt was a 503 with a non-JSON body |
| `tool_result_image` | `input_image` inside a `function_call_output` | 200 and 141 input tokens on 3 of 3 runs. One named the color, "Dark orange"; two said it had an image with no weather in it. **Only the third is saved.** An earlier primed prompt, "a picture of the sky", drew "light blue" |
| `members` | `metadata`, `safety_identifier`, `prompt_cache_key`, `text.format` | 200, all echoed, and the answer matched the schema |
| `instructions` | System prompt in `instructions` | 200, obeyed |
| `unknown_call_id` | Answer to no call, streamed | HTTP 400 with `code: null`, before any stream began |
| `parallel` | Two calls in a turn | Two `function_call` items, answered together, 200 |

**A first run proved nothing, and it is worth knowing why.** With the prompt "What is the weather in Paris?", no GPT-6 model produced a reasoning item at low effort, so that run's replay and strip tests passed over zero cases. The prompt now makes the model do arithmetic first. It may also explain the [findings](2026-11-02-model-currency-findings.md)' `reasoning=0`, though that was not tested: the responses here report non-zero `reasoning_tokens`, such as 58, when the model does think, and the last leaf checks this through goodall.

### What this means for the wire

- **Nothing on the server catches lost reasoning.** Dropping it is a 200, and in these runs the model spent reasoning tokens again on the follow-up. So invariant 2 rests entirely on goodall's offline tests. This is the most important fact in the plan.
- **A tool's `strict` is always stated.** Omitting it lets OpenAI change the schema a developer wrote, including what is required, with no error. Non-strict tools send `strict: false`.
- **Take a reasoning item from `response.output_item.done`, never `.added`.** The two carry different `encrypted_content`: 1,228 characters on `added` and 2,316 on `done` in `two_turn_stream_gpt-6-astra.turn1`.
- **`Thinking.Raw` holds the whole reasoning item.** `Thinking.Text` is its summaries. `function_call` and `message` items can be rebuilt from neutral blocks, so no new carrier is needed on `ToolUse` or `Text`.
- **`include` is not sent.** The docs call `reasoning.encrypted_content` there a legacy value, and `no_include` shows it is unneeded. A live test asserts a non-empty `encrypted_content`, so a change is loud.
- **`EffortMax` is reachable,** and `EffortOff` becomes `none`, which is a loud 400 on Astra and Sol unless the catalog says `ThinkingOffNever`.
- **Images can travel in tool results,** which the Chat path turns into placeholders. On one run in three the model described the image, so the bytes arrive, and how well models use them is not established.

### Not observed

Documented or inferred only, so a build leaf records them or says it could not:

- **`response.failed` and the stream's `error` event.** Neither could be provoked.
- **Refusals.** The docs name refusal delta events and nothing here drew one.
- **`content_filter` as an incomplete reason.** Seen only in a docs sample.
- **Whether `phase` matters.** Dropping `phase: final_answer` was accepted once, and the fetched guides do not mention it. A `commentary` phase never appeared.
- **`tool_choice`, `parallel_tool_calls`, and any form of stop sequence.** No Responses page fetched mentions stop sequences, and goodall has `Request.StopSequences`.
- **Default `reasoning.summary`.** Every request here set `auto`, which the response reports as `detailed`.

### OpenRouter's `/responses`, for the backlog

Two turns passed on `openai/gpt-6-astra`, `anthropic/claude-sonnet-5.5` and `google/gemini-4-argon` (`or_*`). It differs from OpenAI's: item ids are temporary (`fc_tmp_…`), the stream ends in `data: [DONE]`, no `response.reasoning_summary_*` events appeared, reasoning items carry `format`, and usage carries `cost`. Claude returned no reasoning item and 0 reasoning tokens, so nothing here shows how a Claude signature round-trips on that wire.

## The mapping

| Neutral | Responses |
|---|---|
| `Request.System` | `instructions` |
| `Request.MaxTokens` | `max_output_tokens` |
| `Thinking.Effort`, `Thinking.Display` | `reasoning.effort`, `reasoning.summary` |
| `Metadata`, its `user_id` | `metadata`, `safety_identifier` |
| `Text` in a user turn | `message` with `input_text` |
| `Image`, `Document` | `input_image`, `input_file` |
| `Thinking` | `Raw`, the whole `reasoning` item, verbatim and in order |
| `Text` in an assistant turn | `message` with `output_text` |
| `ToolUse` | `function_call`, whose `call_id` is the block's ID |
| `ToolResult` | `function_call_output`, with `output` as a string or a part list |

| Stream event | Neutral |
|---|---|
| `response.output_item.added` | block start, for `message` parts and `function_call` |
| `response.reasoning_summary_text.delta` | thinking delta |
| `response.output_text.delta` | text delta |
| `response.function_call_arguments.delta` | tool input delta |
| `response.output_item.done` | block stop, carrying `Raw` for reasoning |
| `response.completed`, `response.incomplete` | stop reason and usage |
| `response.failed`, `error` | a typed `APIError` |
| anything else | surfaced as unknown, never dropped (invariant 6) |

The events `response.created`, `in_progress`, `content_part.*`, `*.done` and `reasoning_summary_part.*` add nothing the deltas lack. `obfuscation` and `sequence_number` are ignored. Stop reasons: `completed` with a `function_call` is tool use, `completed` without is end of turn, `incomplete` maps its `reason`, and an unknown value is surfaced.

## Order

The wire goes first because everything else exercises it. The request and blocking paths come before streaming, because streaming reuses both and `Collect` over a stream must equal `Complete`. The move of the live pins comes after, since until the wire exists the old pin is the only one that works. The leaves form a chain, so they suit one agent in order, as far as a session allows.

## Tasks

Imported into `job` on 2026-11-02 under `Uz2EPX`. The tracker is the plan from here; change a leaf with `job edit`, not this block.

```yaml
tasks:
  - title: Translate requests and blocking responses on the Responses wire
    ref: blocking
    desc: |
      Why: api.openai.com refuses function tools with reasoning on Chat Completions and accepts them on /v1/responses, so goodall cannot run GPT-6 directly as a thinking agent. This leaf gives the OpenAI dialect a working Client.Complete on Responses, and the next leaf adds streaming on top of it.

      What: a Client with WithDialect(OpenAI) posts to /responses and reads the answer into neutral blocks, stop reason and usage. OpenRouter, LMStudio and Generic send exactly the bytes they send today.

      Already decided by the owner (project/2026-11-02-responses-wire-plan.md, "Rulings"): a new Quirks field picks the conversation wire, as Quirks.DecisionWire does, with the code in openrouter/responses_*.go. OpenAI moves entirely, with no way to choose Chat Completions against api.openai.com. store is false on every request and previous_response_id is never sent. include is not sent, because encrypted_content arrives without it.

      Observed, and these bind (recordings in scripts/probe-responses/testdata, plan section "What this means for the wire"). Dropping a reasoning item is a 200 with no complaint, so the server will not catch a lost one, and invariant 2 rests on this package's tests: Thinking.Raw holds the entire reasoning item and is re-emitted verbatim and in order. Omitting strict lets OpenAI rewrite a loose schema, moving optional properties into required, so every tool states strict, true or false. A function_call and a message can be rebuilt from neutral blocks, with call_id as the block's ID. HTTP 400 can carry code null, which errors.go already handles, and a 503 can have a non-JSON body.

      Seams: openrouter/dialect.go (and its table comment), client.go, errors.go, a new wire struct file with no maps (invariants 7 and 8; messages last or otherwise keep a byte-stable prefix). The plan's "The mapping" tables are the intended shape. Tool results carry images as a part list here, where the Chat path uses a placeholder. Dead Chat quirks, such as CapMaxCompletionTokens if nothing uses it, go.

      Out of scope: streaming (next leaf), re-pinning live models and the recorder (the leaf after), OpenRouter's /responses, async tools, steering, configuration_update.

      Open, your call: how to treat a Thinking block with no reasoning item in Raw, such as a Claude history resumed on OpenAI. OpenAI's docs say incompatible reasoning is omitted, and goodall's history must stay sendable. Whether a request should carry Thinking.Display default as no summary member; nothing recorded this.

      Open, must be recorded rather than assumed: tool_choice forms including named and any, parallel_tool_calls false, and StopSequences. No fetched page mentions stop sequences on Responses. If there is no member, refuse with a typed error rather than drop the caller's stops silently. Add these to scripts/probe-responses. Also check phase: dropping it was accepted once, and a commentary phase never appeared.
    criteria:
      - Offline tests over recorded bytes in scripts/probe-responses/testdata show a two-turn exchange replaying a reasoning item byte for byte, and a mutation that drops Raw fails them
      - A test shows every tool sent by the OpenAI dialect states strict, and tests show the other three dialects' request bytes unchanged
      - A test shows two consecutive requests share a byte-identical prefix (invariant 8)
      - tool_choice, parallel_tool_calls and StopSequences behavior is recorded by a named scripts/probe-responses exchange and tested, including a typed refusal if stops have no member
      - go test -short ./... passes
  - title: Stream the Responses wire into neutral events
    ref: streaming
    blockedBy: [blocking]
    desc: |
      Why: streaming is goodall's default path (invariant 5), and goodall.Agent runs on it, so without this GPT-6 still cannot run the loop directly.

      What: Client.Stream on the OpenAI dialect turns Responses events into neutral events, so Collect over a stream equals Complete over the same exchange, block for block.

      Observed, and these bind (scripts/probe-responses/testdata). The reasoning item on response.output_item.added and on .done carry different encrypted_content, 1,228 against 2,316 characters in two_turn_stream_gpt-6-astra.turn1, so Raw comes only from .done. OpenAI's stream has no data: [DONE]; the terminal events are response.completed and response.incomplete. An incomplete stream may hold only a reasoning item. A request refused outright is an HTTP 400 before any stream begins. Deltas carry obfuscation and sequence_number, which are ignored. Parallel calls interleave by output_index, so address blocks by it.

      Seams: openrouter/stream.go's neutralizer shape, internal/sse, goodall's accumulator (accumulate.go). The event table is in the plan's "The mapping". Unknown event types are surfaced, never dropped (invariant 6).

      Risks: response.failed, the stream's error event and refusal parts were never observed. Test them with fixtures that say in their names or a comment that they are authored from documentation. Multiple summary parts on one reasoning item need a join that keeps Thinking.Text readable, while Raw stays untouched.

      Out of scope: anything needing a WebSocket; OpenRouter's stream, which differs in small ways listed in the plan.
    criteria:
      - A test replaying each recorded stream shows Collect equals Complete's blocks, stop reason and usage on the same model's exchanges, including Raw bytes
      - A test over the recorded two_turn_stream Astra fixture shows Raw holds the output_item.done bytes and not the added ones, and a mutation reading added fails it
      - Tests cover parallel calls, response.incomplete with only reasoning, an authored response.failed, an authored error event, an authored refusal and an unknown event type
      - go test -short ./... passes
  - title: Move the OpenAI recorder, pins and drift check onto Responses
    ref: pins
    blockedBy: [streaming]
    desc: |
      Why: livemodel.OpenAI pins gpt-5.6-terra for a reason that is about Chat Completions, and scripts/record-fixtures and scripts/check-drift still describe the old wire, so after the move they would report on nothing or fail for the wrong reason.

      What: scripts/record-fixtures -provider openai records Responses exchanges into openrouter/testdata, replacing chat live_openai_* fixtures, with no decision fixtures touched. internal/livemodel.OpenAI is re-pinned with its comment rewritten. check-drift's openai rows, surfaces and baseline reflect the new wire.

      Recommendation on the pin, your call after a look: gpt-6.1-terra, because it took none, low and max on /v1/responses (the plan's table), so one model can serve the off and thinking live paths. Astra refuses none.

      Seams: scripts/record-fixtures/openrouter.go, internal/livemodel/livemodel.go, scripts/check-drift/sources_table.go and baseline.json, openrouter/live_test.go. livemodel.OpenAIDecision and the /decisions wire do not change. Keep scripts/probe-responses, since the plan cites it, and leave scripts/probe-gpt6 as the record of the original measurement.

      Risks: check-drift's -accept is a user-visible baseline change, so report what moved rather than accepting blindly. The launchd job on the owner's machine runs on Mondays and reads the same baseline.
    criteria:
      - go run ./scripts/record-fixtures -provider openai -list names only Responses exchanges for conversation, and a run writes fixtures the offline suite reads
      - A live test with OPENAI_API_KEY runs on the new pin, asserts non-empty encrypted_content, and is reported with its real result rather than a skip
      - go run ./scripts/check-drift -env .env reports no finding caused by the wire move, with its output quoted in the leaf's note
      - go test -short ./... passes
  - title: Prove GPT-6 runs the agent loop directly, and close the findings
    ref: proof
    blockedBy: [pins]
    desc: |
      Why: this is v1.7's definition of done. The finding that started it is a table of 400s from goodall.Agent on direct OpenAI, so the proof is that table turning into passes through the production path, not a raw probe.

      What: go run ./scripts/probe-agent-loop -env /abs/.env -dialect openai -models gpt-6-astra,gpt-6.1-sol,gpt-6.1-terra,gpt-6-luna at effort low, and again at off, and report each row. Use a prompt that makes the model think: at low effort a bare weather question draws no reasoning item, and the run would then pass over zero cases (plan, "What was observed"). Report reasoning_tokens per row to show thinking happened.

      Then correct the docs: project/2026-11-02-model-currency-findings.md in place with a marked block quote, including its reasoning=0 limit; the OpenAI dialect's mention in the architecture plan as a marked correction; README only if new users must know, which a direct-GPT-6 path probably means. Write a findings note on anything the earlier leaves changed from this plan.

      Expected: Astra and Sol refuse off with a loud 400 unless the catalog says ThinkingOffNever, which is not a failure. Say which happened.

      Out of scope: new features. A failure here goes back to the leaf that owns it.
    criteria:
      - The four GPT-6 models pass goodall.Agent at low effort through scripts/probe-agent-loop with non-zero reasoning_tokens on at least one turn each, the rows quoted in the findings with the command
      - The effort off rows are recorded, with Astra and Sol's outcome explained
      - The model currency findings carry a dated correction naming this result
      - go test ./... passes with the live OpenAI tests having run, and the run's own report says they ran
```
