# TypeSafe's Jev and the question of subagents

Findings, 2026-09-17, on two enhancements the owner raised together: adopting TypeSafe's Jev as a provider, and letting the core agent call out to other model types rather than only running tools on its own context. This document records the verified facts about the TypeSafe API, weighs where each enhancement fits goodall's architecture, and names the decisions that are the owner's to make. Nothing here is decided; the decisions go into the [architecture plan](2026-09-14-architecture-plan.md) as a dated block quote once ruled, and the work into `job`.

## Sources

Read on 2026-09-17. Every page below is served as Markdown by appending `.md` to the path, and the whole index is `https://docs.typesafe.ai/llms.txt`. Reproduce with `curl -sL https://docs.typesafe.ai/<path>.md`.

- The agent skill: `agent-skill` and its `SKILL.md` at `https://raw.githubusercontent.com/typesafe-ai/skills/main/skills/typesafe-ai/SKILL.md`. The skill is guidance for a coding agent that is *building on* TypeSafe; it points at the docs and carries no API facts of its own.
- The API: `api`, `models`, `concepts/system-one`, `concepts/state`, `primitives`, `confidence`, `model-jaggedness/jev-1.13`, and the Python SDK's `sdk/python/api/retries`, `sdk/python/api/constants`, `sdk/python/api/exceptions`.
- The patterns that bear on an agent: `patterns/fan-out`, `patterns/intent-routing`, `cookbooks/function_calling`, `cookbooks/skill_suggestion`.

No live call was made; there is no TypeSafe key in `.env`. Every fact below is documentation, not observation, and the repo's fixture practice (`scripts/record-fixtures`) would need a key before any of it is asserted in a test.

## What Jev is

Jev is not a conversational model and it does not call tools. It is a **judgment service**: one request carries a `state` (text, a JSON object or an array) and a map of typed **questions**, and the answer is one typed value per question, all evaluated in parallel over the same state. There is no text output, no reasoning trace, no image input and no streaming; the response is one JSON body.

The framing "pure tool calls, several in parallel" maps onto TypeSafe like this: the parallelism is *many questions in one request*, and "function calling" is a cookbook pattern the *caller* builds in code, a `choice` question over the function names plus a `choice` per closed-set argument, not a capability of the model. The model never sees a tool schema and never produces a call.

### The wire, verified against `api.md`

```
POST https://api.typesafe.ai/v1/systemone
Authorization: Bearer <TYPESAFE_API_KEY>
{ "state": <string|object|array>, "model": "jev-latest", "questions": { "<id>": <Question>, … } }
```

| Question `type` | Request members | Answer members |
|---|---|---|
| `noul` (yes or no) | `instructions`; optional `criteria{true, false}` | `noul` in [0, 1], the probability of yes; **no confidence** |
| `choice` (one of a set) | `instructions`; `criteria` map of option to description, `null` allowed | `choice`, `probabilities` per option summing to 1, `confidence` |
| `score` (a level on a rubric) | `instructions`; `criteria` ordered array of at least two level descriptions | `score` (probability-weighted, may fall between levels), `legend`, `probabilities` per level index, `confidence` |

`instructions` and `criteria` values may be strings or JSON structure (`primitives/advanced`). Question ids are for the caller and are not shown to the model. The response is `{model, answers, usage{input_tokens, output_tokens}}`; `model` reports the versioned id that answered even when an alias was sent.

- **Errors:** 401, 422 (validation, body names the field), 429 and 529, the last two with a `retry-after` header the SDKs honor. The Python SDK's default policy retries 408, 429 and 5xx with jittered backoff and a 30-second request timeout.
- **Models:** `jev-1.13.0`; aliases `jev-latest` and `jev-preview`, both pointing at it today. `GET /v1/models` lists `{name, description, release_date}` per entry and nothing about capability. Pinning a version matters because confidence thresholds are tuned per version.
- **Price and limits, from `models.md` on 2026-09-17:** $0.042 per million input tokens; output tokens are free. Rate limits 250,000 tokens per second and 1,200 requests per minute, stated to be adjusting dynamically. There is no per-response cost member, so cost is "not reported" under the plan's cost ruling, the same as Anthropic.
- **Context, from `model-jaggedness/jev-1.13.md`:** 64k tokens for state plus all questions together, 32k for state plus the longest question. Text only.
- **Configuration the SDKs read:** `TYPESAFE_API_KEY`, `TYPESAFE_BASE_URL` (default `https://api.typesafe.ai`), `TYPESAFE_DEFAULT_MODEL` (default `jev-latest`).

### What "confidence" means

`confidence` on a `choice` or `score` answer is a statistic of the probability distribution the same answer already carries: concentrated means high, spread means low. It says nothing about the correctness of an individual answer; TypeSafe's own pages say calibration is measured across groups of predictions and that "typed output guarantees the interface, not truth". A `noul` has no confidence at all, and a `noul` near 0.5 means "yes and no are equally likely", not "medium". The `function_calling` cookbook reports a call's confidence as the *minimum* over its judgments, on the reasoning that one wrong argument spoils the call.

### The jagged edges that shape an integration

From `model-jaggedness/jev-1.13.md`, reviewed by TypeSafe on 2026-09-16: it reads instructions literally; it cannot count, do arithmetic or compare dates; it degrades on indirection and on state full of irrelevant detail ("context rot"); it does not treat adversarial content in the state as hostile; and it cannot generate. The recommended shape is always "filter and compute in code, ask the model only the semantic part, and thresholds live in one reviewable place".

## Where Jev fits goodall

### It is not a `Provider`

`goodall.Provider` is `Stream(ctx, *Request) Stream`: a conversation of messages and tools in, a stream of block events out. Jev has no messages, no blocks, no tools, no stop reason and no stream. An adapter that fabricated a conversation around a judgment request, or emitted a fake `tool_use` block per answer so the loop could "run" it, would be an adapter holding a decision, which the principles call a bug. The research findings' invariant on "just enough abstraction" applies: one layer around an LLM provider is prudent, and a `TextGenerationProvider` above it is not. A "judgment provider" interface that both kinds satisfy would be that second layer.

### It is its own client package

The fit is `goodall/typesafe`, a sibling of `anthropic` and `openrouter` that imports the root and `internal/transport` and nothing else. Sketch, to be settled in code:

```go
c := typesafe.New(os.Getenv("TYPESAFE_API_KEY"))            // WithBaseURL, WithHTTPClient, WithMaxRetries
ans, err := c.Ask(ctx, state, typesafe.Questions{
    {ID: "department", Question: typesafe.Choice{Instructions: "...", Options: []typesafe.Option{{Key: "billing", Description: "..."}, …}}},
    {ID: "is_urgent",  Question: typesafe.Noul{Instructions: "..."}},
}, typesafe.WithModel("jev-1.13.0"))
dept := ans.Choice("department")   // Choice, Probabilities, Confidence
```

What the repo's rules already decide about it:

- **Ordered typed structs, never maps** (invariant 7) and **deterministic request bytes** (invariant 8). A `choice`'s options are a map on the wire; in Go they are an ordered `[]Option` marshaled as an object in slice order, the pattern `goodall.Properties` already uses. `Questions` is likewise an ordered slice with ids, marshaled as the `questions` object.
- **Errors** map onto `*goodall.APIError` with the existing kinds: 401 → `KindUnauthorized`, 422 → `KindInvalidRequest`, 429 → `KindRateLimited`, 529 → `KindOverloaded`; `internal/transport` already retries those and honors `Retry-After`. The provider name on the error is `typesafe`.
- **Usage** maps onto `goodall.Usage{Input, Output}`; **cost** is not reported. A cost would need a price table, which the backlog already parks for Anthropic.
- **State is untrusted data.** The state type is `any` marshaled as JSON, and the package documents that a conversation placed in it is data the model does not defend against (the jaggedness page says so).
- **Fixtures and live tests** follow `scripts/record-fixtures`, which needs a `TYPESAFE_API_KEY` in `.env`; `-provider typesafe` writes into `typesafe/testdata`.

`GET /v1/models` carries no capability facts, so the package would not implement `ModelLister`; a `Models(ctx)` returning the typed list is enough.

### It composes with the conversational agent in two ways, neither needing a core change

1. **As a `Tool`.** A `typesafe.Tool(client, name, description, questions)` gives a conversational model one tool whose input is the state to judge and whose result is the typed answers as JSON (`goodall.JSONResult`). The questions are fixed by the developer, which is what TypeSafe recommends ("questions and thresholds in one reviewable place"). A second constructor could let the model author the questions at call time, with the schema inferred from the question structs; that is the real "delegate a batch of judgments to a cheaper model" shape, and the literalness caveat above is the reason to keep it a separate, opt-in constructor.
2. **As a routing hook.** `Hooks.BeforeSend(ctx, req, newTurn)` already sees the new user turn before it is sent and may shape the request. A `typesafe.Route` helper that asks Jev about the turn and returns typed answers lets a consumer choose the model, narrow the tool list, add a one-line hint to the system prompt or refuse the turn, which is exactly the `intent-routing` and `skill_suggestion` patterns. The skill-suggestion cookbook measured that shape on a 182-skill roster: wrong loads fell from 16.8% to 7.3% over 488 requests against `claude-haiku-4-5-20251001` (its own figure, `cookbooks/skill_suggestion.md`, not reproduced here).

## Subagents: the core agent calling other model types

Three shapes were considered.

**A. Agent as a `Tool`.** `goodall.AgentTool(child *Agent, name, description)` wraps any agent, on any provider, as a tool: the parent calls it with a prompt, the child runs a fresh conversation to `Done`, and the final text comes back as the tool result. Every existing mechanism already carries through: `Execute` receives the parent's context so cancellation propagates; the child has its own `Budget`; `chat.Redact` already hides tool result content, so a child's transcript never reaches a front end. Cost is one file in the root package and no change to the loop. Two things it does *not* do: the parent's stream shows nothing while the child works, and the child's tokens and cost appear nowhere in the parent's `Result`.

**B. Nested events and accounting in the loop.** The one core change that would make delegated work first-class: a way for a running tool to emit events into the parent's run stream, and a place to report what it spent. Sketch: an optional interface a tool may implement, `Reporter{Execute(ctx, input, report func(Event))}`, with the loop wrapping each reported event as `ToolEvent{ToolUseID, Event}` so a front end can render a child's progress under the call that started it. Accounting is the harder half: tokens on different providers are not commensurable, so summing a child's `Usage` into the parent's would make `TokensSpent` meaningless for the budget. The honest shape is per-call: `ToolCallEnd` gains an optional `Usage` and `Cost` for what the tool itself spent, and `Result` gains a roll-up of *cost only*, since money is commensurable, reported only when every contributor reported (the existing `Cost` rule). Budgets stay on the parent's own tokens.

**C. A generic "delegate" seam in the provider layer.** A `Judge` or `Delegate` interface beside `Provider` that Jev and a nested agent both satisfy. Rejected: it is the second abstraction layer the plan rules out, and nothing calls it but the two adapters above.

## Recommendation

Build **the client package** (`goodall/typesafe`) with **the Tool and hook adapters**, and **A** (agent as a tool) as its own leaf in the root package. Do **B** as a separate, later leaf once A and the Jev tool exist, because both are consumers of it and would show what the events and accounting really need to carry; designing B first would be guessing. Keep C parked in the backlog with the trigger "a third kind of delegate that neither a Tool nor a hook can express".

The reasons, in order: Jev's shape is far from a conversation and forcing it through `Provider` would corrupt the seam that the two real providers share; every composition Jev offers an agent is already expressible as a tool or a hook, so the zero-dependency core needs no change to adopt it; and a nested agent is the same kind of tool, so one design serves both enhancements.

## Decisions

> **Ruled 2026-09-17 by the owner**, recorded in the [architecture plan](2026-09-14-architecture-plan.md) as a dated block quote under Decisions.
>
> 1. **Package name:** `goodall/typesafe`.
> 2. **Scope:** all three — the client, the Tool and hook adapters, and agent-as-tool (A) — with B filed as a leaf blocked by the two adapters so it is designed against them.
> 3. **API key:** `TYPESAFE_API_KEY` is in `.env`, so fixtures are recorded and the live tests run.
> 4. **Model-authored questions:** shipped, as a separate opt-in constructor beside the developer-fixed one.
> 5. **Accounting in B:** deferred to that leaf; the recommendation stands.

## When to reach for a judgment model

A working rule, with Woodcase as the example. A `.pen` document is a tree of typed nodes that `woodcase tree --json` and `woodcase find` already query by structure: type, name, font size, position. Jev adds the query the structure cannot answer, the *semantic* one, and nothing else.

- **Reach for it when the question is a snap judgment a person makes in a second given the right context, over many items, and the answer is one of a set you can name.** "Which of these 300 text nodes is placeholder copy?" "Is this frame a card, a toolbar, a dialog or a page?" "Which of these five layers is the primary action?" "Does this label read as an error state?" Each is a Noul or a Choice per node, all in one request, and the answers come back as typed values with probabilities the code thresholds. Filter the rows in code first (the model degrades on irrelevant state), send the fields the question needs, and keep the thresholds in one file.
- **Do not reach for it for anything the tree already knows or code can compute.** Overlap, alignment, contrast ratios, whether a font size is under 12, how many children a frame has: `find` and `lint` answer those exactly, and Jev cannot count, compare numbers or dates, or read hex colors. Convert a number into a named bucket in code if a judgment needs it.
- **It does not generate**, so it does not design. It can sit beside a model that does: a conversational agent proposes nodes or edits, and Jev verifies each candidate against a rubric ("does this proposed heading fit the copy voice of its siblings?", "is this node a plausible child of that container?") cheaply enough to check every one, with the uncertain ones escalated to the flagship or a person. That is the cascade shape from TypeSafe's cookbooks: generate with a large model, select and verify with a small one.
- **State is text only.** A rendered PNG cannot go to Jev; the node tree can. A question about how a design *looks* still needs a vision model, and Jev can judge the tree that produced the pixels.

## Task tree

Imported into `job` under a new root; the leaves carry their files so agents do not collide. `internal/transport` is shared and not modified.

```yaml
tasks:
  - title: "goodall v1.1: TypeSafe and delegation"
    desc: |
      Adopt TypeSafe's Jev as a judgment client beside the providers and let the
      conversational agent delegate: to Jev through a tool and a hook, and to another
      agent through a tool. Ruled 2026-09-17 in project/2026-09-17-typesafe-jev-findings.md
      and the block quote in the architecture plan. The API facts in the findings are
      documentation, not observation; the client leaf records fixtures first.
    children:
      - title: TypeSafe client package
        ref: client
        desc: |
          goodall/typesafe: a blocking client over POST https://api.typesafe.ai/v1/systemone
          and GET /v1/models, importing the root and internal/transport only.
          New(apiKey, opts...) with WithBaseURL, WithHTTPClient and WithMaxRetries;
          Ask(ctx, state any, questions Questions, opts...) (*Answers, error) with WithModel
          (default jev-latest); Models(ctx). Questions is an ordered slice of {ID, Question}
          marshaled as the questions object; a Choice's options are an ordered []Option
          marshaled as an object in slice order (invariants 7 and 8, the goodall.Properties
          pattern). Answers carry the model that answered, goodall.Usage{Input, Output},
          a cost of not reported, and typed accessors per question type; an answer of the
          wrong type, or a missing one, is an error naming the id. Errors are *goodall.APIError
          with Provider "typesafe": 401 Unauthorized, 422 InvalidRequest, 429 RateLimited,
          529 Overloaded; the transport's retries and Retry-After apply. State is data:
          the package documents that the model does not defend against instructions in it.
          Files: typesafe/client.go, options.go, questions.go, answers.go, wire.go,
          errors.go, models.go and their tests; typesafe/testdata; scripts/record-fixtures/typesafe.go;
          an internal/livemodel entry; a typesafe/live_test.go that skips loudly without
          TYPESAFE_API_KEY.
        criteria:
          - Fixtures under typesafe/testdata are recorded with scripts/record-fixtures -provider typesafe and the offline suite decodes every answer type from them
          - Two consecutive requests with the same questions marshal to byte-identical bodies with options in author order
          - Each HTTP error status maps to the documented goodall error kind, asserted with an httptest server
          - The live test runs one mixed request against jev-latest when the key is present and skips naming the key and the file when it is not
          - Every exported identifier carries a doc comment
      - title: TypeSafe adapters for the agent
        ref: adapters
        blockedBy: [client]
        desc: |
          Two ways a conversational agent reaches Jev, both in goodall/typesafe, no core change.
          Tool(client, name, description, questions Questions, opts...) is a goodall.Tool whose
          input is the state to judge (a string or a JSON object, schema-described) and whose
          result is the answers as JSON through goodall.JSONResult; the questions are fixed
          by the developer. AuthoredTool(client, name, description, opts...) lets the model
          supply both the state and the questions at call time, with the input schema inferred
          from the question structs and the tool description carrying TypeSafe's literalness
          guidance so the model writes exact conditions and named options. Route(client,
          questions) returns a goodall BeforeSend-compatible helper: it asks Jev about the new
          turn's text and hands the typed answers to a consumer callback that may shape the
          request (model, tools, a system line) or refuse the turn. Files: typesafe/tool.go,
          tool_authored.go, route.go and tests against internal/fake and an httptest server.
        criteria:
          - The fixed-questions tool round-trips through a fake agent run and its result decodes back into Answers
          - The authored tool rejects a call with no questions or an unknown question type with an IsError result naming the problem
          - Route runs inside BeforeSend and can change the request's model and tools without touching earlier turns
          - The README's package map names goodall/typesafe and the findings doc's mental-model section is linked from it
      - title: Agent as a tool
        ref: agent-tool
        desc: |
          goodall.AgentTool(child *Agent, name, description string, opts...) (Tool, error) in the
          root package: the parent model calls it with a prompt, the child runs a fresh
          conversation on the parent's context to a terminal event, and the final assistant
          text comes back as the result. A Stopped child becomes an IsError result carrying
          Cause and Message; a child that Defers for approval is reported as an error too,
          since a nested pause has no path to the parent's caller yet (that is the accounting
          leaf's problem to rule on). The child's Budget is its own; cancellation propagates
          through ctx. Files: agent_tool.go, agent_tool_test.go against internal/fake.
        criteria:
          - A parent run through internal/fake calls the tool, the child runs to Done, and the parent's next turn sees the child's answer as a tool result
          - Canceling the parent's context ends the child run and the parent's terminal event says Canceled
          - A child that ends in Stopped yields an IsError tool result carrying the cause
          - The child's events do not appear in the parent's stream (documented as the gap the accounting leaf fills)
      - title: Nested events and per-call accounting
        ref: accounting
        blockedBy: [adapters, agent-tool]
        labels: [decision]
        desc: |
          The one core change delegation asks for. Design against the two adapters that now
          exist: an optional interface a tool may implement to emit events during Execute,
          wrapped by the loop as ToolEvent{ToolUseID, Event} in the parent stream; optional
          Usage and Cost on ToolCallEnd for what the call itself spent; a cost-only roll-up on
          Result (tokens across providers are not commensurable, money is), reported only when
          every contributor reported. Budgets stay on the parent's own tokens. chat.Redact
          must decide what of a nested event travels. Rule on the shape first, as a note on this
          leaf, then build.
        criteria:
          - The design is recorded as a dated block quote in the architecture plan before code
          - AgentTool and the TypeSafe tools report what they spent on ToolCallEnd
          - A front end watching a redacted stream sees a child's progress without its content
```
