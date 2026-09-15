# Architecture plan

Decided 2026-09-14 after the [brief](2026-09-14-initial-vision.md) and the [research](2026-09-14-research-findings.md). This is the design goodall v1 is built to; the task tree at the end is the plan of record in `job`. Code is the authority on signatures, and this document records the shape, the reasons and the invariants. Correct it in place, as a marked block quote, when a decision changes.

## Decisions

| Decision | Ruling | Why |
|---|---|---|
| One library or two | **One module**, `github.com/bensyverson/goodall`, layered by package with import direction enforced by a test | The Swift split cost re-exports, twenty type aliases and a reversal; Go modules would add version skew and `replace` directives |
| Licence | **MIT** (`LICENSE` at the root) | The norm for a library of this kind |
| Package layout | `goodall` (types, provider seam, tools, loop), `goodall/anthropic`, `goodall/openrouter`, `goodall/chat`, `goodall/internal/sse`, `examples/` | The loop is the product, so it lives in the root; optional layers stay optional |
| OpenRouter client | **Chat Completions**, built as an OpenAI-compatible client with a typed `Dialect` so LM Studio, OpenAI and other compatible servers work with configuration | The wire format is shared, the quirks are not; a typed constant per known target carries them |
| Thinking control | A typed `Effort` ladder plus a display setting; no token budgets in the public API | Adaptive thinking made effort the knob; legacy budget models get a budget derived from effort |
| Model tiering | **None**; a model is a string the consumer aliases | The ladder keeps changing shape and a tier table goes stale like a price list |
| Cost | **Reported only when the provider reports it** (OpenRouter); Anthropic threads report usage and a cost of "not reported" | No price list to keep current |
| Markdown | A **zero-dependency chat-safe subset** behind a `Renderer` interface: paragraphs, headings, emphasis, inline and fenced code, links, images, lists, block quotes; never raw HTML | Most chat UIs render client-side; the subset is a few hundred lines and testable |
| JSON Schema inference | **Written in-house**, stdlib reflection, ordered properties | About four hundred lines; property order is semantically load-bearing so maps are out |
| Thread ownership | **The back end owns threads.** The front end receives a redacted view and sends a thread id plus its new input | The front end must not be forced to hold the system prompt or tool results, and cannot be trusted with the history |
| Runs and requests | **Runs are owned by the chat service, not the HTTP request** that started them; clients subscribe, detach and reattach | Mobile apps background mid-answer; a run must finish and persist with nobody listening |
| Turn budget | **20 model calls per run** by default (per user message in the chat layer); token and cost ceilings live on the thread | Long enough for a tool-using answer, finite by construction |
| MCP | **Out of scope** | Decided by the owner; a protocol stack does not belong in the core |
| Dependencies | **Zero** in the module | On Go 1.27, json/v2, `uuid` and `synctest` remove every reason the incumbents had |

## Invariants

These are the rules every package obeys; a test guards each one where a test can.

1. **History is append-only.** Preserved thinking binds each thinking block to the prefix that produced it. Nothing in the library edits, reorders or deletes an earlier turn; hooks may shape the *new* user turn before it is committed and may append. Compaction, when it comes, is "summary as a fresh first message". Redaction is a view.
2. **Thinking blocks round-trip byte-exact and in order.** The neutral block carries display text and the provider's raw block; providers re-emit the raw block untouched.
3. **One neutral block model in both directions.** Text, image, document, tool use, tool result, thinking and redacted thinking, on input and output, across both providers.
4. **Every event is JSON-serialisable** and errors travel as message plus typed code.
5. **Streaming is the provider path; blocking is `Collect`.** A provider may add a native `Complete` for the cases streaming is refused, such as a zero-token cache pre-warm.
6. **HTTP 200 is not success; stop reasons and error kinds are typed; unknown enum values and event types are surfaced, never dropped.**
7. **Schemas are ordered typed structs, never maps.**
8. **Request bytes are deterministic**: declaration-order fields, stable tool order, no clocks or ids in the prefix. A test asserts two consecutive requests share a byte-identical prefix.
9. **Every `tool_use` gets a `tool_result`**, including parse failures and unknown tools, assembled with the assistant message as one unit.
10. **A terminal event on every exit path**, including cancellation, carrying the conversation so far and the usage.
11. **Capabilities are runtime, tri-state facts**; unknown means try.
12. **Values are safe for concurrent use after construction**: agents, providers and services hold no per-run state; streams are single-consumer; a thread has one active run.

## The core package

**Blocks and messages.** `Block` is a sealed interface (unexported marker method) with concrete structs `Text`, `Image`, `Document`, `ToolUse`, `ToolResult`, `Thinking`, `RedactedThinking`. `Image` and `Document` carry a `Source` (bytes with media type, URL, or provider file id). `ToolUse` holds `ID`, `Name` and `Input` as `jsontext.Value`. `ToolResult` holds `ToolUseID`, `IsError` and `[]Block`. `Thinking` holds `Text`, `Signature` and `Raw jsontext.Value`, the provider's block as received. Tag dispatch at the JSON seam uses json/v2 `MarshalToFunc`/`UnmarshalFromFunc` options, so each concept has one struct. `Message` is `Role` plus `[]Block` and, on assistant messages, `Partial bool` for an interrupted turn. `Conversation` is a value with `Append` and read accessors; there is no method that edits an earlier message.

> **Corrected 2026-09-14 while implementing the block model.** Two details of this paragraph did not survive contact with `encoding/json/v2` on go1.27.0.
>
> *A block list is a named type, `Blocks []Block`, not a bare `[]Block`.* json/v2 cannot choose a concrete type for a non-empty interface without an `UnmarshalFromFunc` option, and a plain `[]Block` gives it nowhere to attach one: `json.Unmarshal(data, &[]Block{})` fails with "cannot derive concrete type for nil interface". `Blocks` carries `UnmarshalJSONFrom`, which installs the dispatch internally, so plain `json.Unmarshal` works on a `Message`, a `Conversation` or any consumer struct with a `Blocks` field. `Blocks` and `[]Block` are mutually assignable, so this costs a caller nothing. `Message.Content` and `ToolResult.Content` are `Blocks`.
>
> *Marshalling uses methods, not the `MarshalToFunc` option.* Each block type has a `MarshalJSONTo` writing its own `"type"` tag, so a block is self-describing wherever it appears — alone, in a map, in a struct a consumer wrote — and encoding never needs options. Only decoding does. Each concept is still exactly one struct: the tag is spliced on by a generic `tagged[T]` wrapper over a method-less defined type, not by a hand-written wire twin.

**Request.** A struct of per-call parameters: `Model`, `System`, `Messages`, `Tools`, `ToolChoice`, `MaxTokens`, `Thinking`, `Cache`, `StopSequences`, `Metadata`. Unset fields are omitted with `omitzero`. Per-request options are a struct; client construction uses functional options. Provider-specific extras go through a typed `Extensions` field each provider defines and documents rather than a bag of `any`.

**Provider seam.**

```go
type Provider interface {
    Stream(ctx context.Context, req *Request) Stream
}
type Completer interface {          // optional
    Complete(ctx context.Context, req *Request) (*Message, error)
}
type ModelLister interface {        // optional
    Model(ctx context.Context, id string) (*ModelInfo, error)
}
```

**Streams and events.** `type Stream iter.Seq2[Event, error]` with `Collect() (*Message, error)`, which runs the accumulator. Events are a sealed family. Provider events: `MessageStart`, `BlockStart`, `TextDelta`, `ThinkingDelta`, `ToolInputDelta`, `BlockStop`, `MessageDelta` (stop reason, cumulative usage), `MessageStop`, `Unknown{Type, Raw}`. Loop events: `TurnStart`, `ToolCallStart`, `ToolCallEnd`, `TurnEnd`, `Done{Result}`, `Stopped{Reason, Result}`. Each event has a stable JSON shape with a `type` discriminator. The accumulator exposes "just finished" edges (a completed tool-use block) and a resumable prefix (all complete text blocks).

> **Corrected 2026-09-14 while building the provider seam and the event stream.** Five details of the two paragraphs above changed; the code is the authority.
>
> *Collect returns a `*Response`, not a `*Message`, and so does `Completer.Complete`.* A turn that returned only the message loses the stop reason and the usage — the Swift predecessor's bug — and the loop's whole job is to act on the stop reason. `Response{ID, Model, Message, StopReason, StopSequence, Usage, Cost}`. A run stream is collected by a second method, `CollectResult() (*Result, error)`, which reads the `Result` off the terminal `Done` or `Stopped` rather than folding events; a run stream with no terminal event is a `*ProtocolError`.
>
> *The provider event list was missing `SignatureDelta{Index, Signature}`*, which Anthropic sends as its own delta type and which `display: "omitted"` sends even when the thinking text is empty. `BlockStop` carries an optional `Raw jsontext.Value` — the provider's finished block, for the cases the neutral fields cannot rebuild it (OpenRouter's `reasoning_details`).
>
> *The unknown provider event is `UnknownEvent`, not `Unknown`*: `Unknown` is already the unknown *block*, and both live in the root package. It writes itself under goodall's own `"unknown"` tag with the provider's tag in `event_type` and its bytes in `raw`, rather than re-emitting the raw verbatim as the unknown block does: an event travels to a front end and never back to a provider, so a front end's switch on `type` never meets a surprise value.
>
> *`Stopped` is `{Cause StopCause, Message string, Kind ErrorKind, Result Result}`.* "Reason" collided with `StopReason`, which is why the *model* stopped generating; a run can end for reasons the model knows nothing about. Errors travel as message plus kind (invariant 4), so `Stopped` stays JSON-serialisable and is not itself an error.
>
> *`Request.Extensions` is an `Extension` interface* with one method, `Provider() string`. Each provider defines its own options struct, names itself, and rejects another provider's before sending, so a request built for one provider fails loudly on another instead of losing its options.

> **Added 2026-09-15 before the provider fan-out.** OpenRouter normalises the upstream model's stop string into its own `finish_reason` and reports the original as `native_finish_reason`; the neutral model had no slot for it. `MessageDelta` and `Response` now carry `NativeStopReason string`, empty on a provider whose wire string *is* the `StopReason` (Anthropic). It is diagnostic only: the loop acts on `StopReason`.

**Tools.**

```go
type Tool interface {
    Name() string
    Description() string
    Schema() *Schema
    Execute(ctx context.Context, input jsontext.Value) (ToolResult, error)
}
func NewTool[In any](name, description string, run func(context.Context, In) (ToolResult, error)) (Tool, error)
```

`NewTool` infers the schema from `In` by reflection: struct fields become properties in declaration order, `json` tags name them, `omitzero`/`omitempty` make them optional, a `desc` tag supplies the description, `CaseIterable`-style enums come from a `enum` tag, nested structs and slices recurse, and the schema has `additionalProperties: false`. Registration validates the schema and returns an error rather than panicking. A decode failure at call time produces a result with `IsError` and a message naming the bad parameter and listing the expected ones, so the model can self-correct. `Schema` is an ordered typed struct. Helpers build results from text, JSON and blocks.

> **Added 2026-09-14 while building the tools leaf.** Two things the paragraph above leaves open, decided in code and verified on `go1.27.0 darwin/arm64`:
>
> - **`encoding/json/v2` does not enforce required members.** A required parameter the model omits decodes silently to the Go zero value, so the schema's promise and the handler's input would drift apart — the model would be told `city` is required, omit it, and the tool would run on `""`. `Execute` therefore checks the decoded object against the schema's `Required` list (recursively, through nested objects and array elements) and returns the same `IsError` result shape as a decode failure: `missing required parameter "window.start"`. An author who is happy with the zero value marks the field `omitzero`, which is what makes it optional in the schema too.
> - **The error text is one problem line, a blank line, then the whole parameter listing** in schema order, with dotted names for nested objects and a `[]` suffix for array elements, each line carrying the JSON type, required-or-optional, any enum values and the description. A model that got one member wrong should not have to guess at the rest. json/v2 keeps the offending literal only when it rejected a string's or number's *content* (`3.5` into an `int`); a plain kind mismatch reports the kind alone.

**Vocabulary types.** `StopReason` (`EndTurn`, `MaxTokens`, `StopSequence`, `ToolUse`, `PauseTurn`, `Refusal`, `Unknown`), `Effort` (`Default`, `Off`, `Low`, `Medium`, `High`, `XHigh`, `Max`), `ThinkingDisplay`, `CachePolicy` (`Auto`, `Manual`, `Off`), `Support` (`Unknown`, `Supported`, `Unsupported`), `Usage` (input, output, cache read, cache write, reasoning), `Cost` (amount, currency, `Reported bool`), `ModelInfo` with `Capabilities` (image input, PDF input, audio input, tools, thinking with supported efforts, cache control, structured output, context window, max output).

> **Corrected 2026-09-14 (build session).** The thinking *configuration* is `ThinkingConfig{Effort, Display}`, not `Thinking`: that name belongs to the thinking *block*, and the two collided the moment both leaves merged. `Agent.Thinking` and `Request.Thinking` are of type `ThinkingConfig`.

**Errors.** `*APIError{Provider, Status, Kind, Type, Message, RequestID, RetryAfter, Raw}` where `Kind` is a typed constant (`RateLimited`, `Overloaded`, `Unauthorized`, `InvalidRequest`, `ContextLength`, `UnsupportedInput`, `NotFound`, `Server`, `Unknown`) mapped from Anthropic's `error.type` and OpenRouter's `error.metadata.error_type`. `*CapabilityError` names the modality or feature the model is known not to accept. Retries with jittered backoff on rate-limit, overload and server errors honour `Retry-After` and stop after the first streamed byte; they are tested with `testing/synctest`.

**The loop.**

```go
type Agent struct {
    Provider Provider
    Model    string
    System   string
    Tools    []Tool
    Thinking Thinking
    Cache    CachePolicy
    Budget   Budget      // MaxTurns (default 20), MaxTokens, Timeout
    Hooks    Hooks
    Logger   *slog.Logger
}
func (a *Agent) Run(ctx context.Context, conv Conversation, input ...Block) Stream
```

`Run` appends the input as a user message, then loops: apply hooks, send, accumulate, classify the stop reason (tool use runs tools; pause resends; refusal, max tokens and unknown stop), run tools concurrently with `sync.WaitGroup.Go`, append the assistant message and all results together, repeat until the budget or a terminal stop. `Collect` on the run stream yields a `Result{Message, Conversation, Usage, Cost, StopReason, Pending []ToolUse}`. *(Corrected 2026-09-14: `CollectResult` on the run stream yields `Result{Response *Response, Conversation, Usage, Cost, StopReason, Pending []ToolUse}` — the whole last response rather than its message alone, and read off the terminal event rather than accumulated. See the block quote under "Streams and events".)* Cancellation propagates to the request and to running tools; the loop waits for them, marks the assistant message partial, and emits `Stopped`. Resuming is another `Run` over the returned conversation.

> **Added 2026-09-15 while building the loop.** Six things the paragraph above leaves open, decided in code; the code is the authority.
>
> *`Agent` carries `MaxTokens` and no `Hooks` yet.* `Thinking` is a `ThinkingConfig`, `MaxTokens` is the per-turn output cap on the `Request` (zero leaves the provider's default), and `Budget` is `{MaxTurns, MaxTokens, Timeout}` in `budget.go`, where `MaxTurns` under one means `DefaultMaxTurns` (20) and there is no unlimited mode. The token budget is measured by `TokensSpent(Usage) = TotalInput() + Output`, cumulative over the run; reasoning tokens are already inside `Output`, so counting them again would charge twice. `Hooks` and `Resume` arrive with the hooks leaf, which has insertion points marked in `run.go` and `run_tools.go`.
>
> *The run stream never yields a non-nil error.* Every ending is a terminal event, including a misconfigured agent — no provider, no model, two tools sharing a name — which ends in `Stopped{Cause: StopCauseError, Kind: KindInvalidRequest}`. One contract covers every exit, `CollectResult` always has something to read, and a library embedded in a server never panics out of a caller's `range`.
>
> *`StopNone` — a `message_stop` with no stop reason — is `Done`.* The provider closed the message and told the loop nothing to act on; the run's `StopReason` carries the silence through to the caller. Treating it as an error would turn a provider's omission into a failed run, and resending would loop for ever.
>
> *Every tool call the loop declines to run still gets an `IsError` tool_result*, not just the cancelled ones: `max_tokens`, `refusal`, an unrecognised stop reason and a turn that carried tool calls without stopping for them all append the assistant message and one error result per call as a single unit. Invariant 9 is about the history being sendable, not only about parse failures, and a conversation ending in a dangling `tool_use` is a 400 the moment anyone resumes it. A partial assistant message with no content at all is not appended, for the same reason.
>
> *The budget is checked at the top of the loop, before a turn is sent.* A turn the model asked for is always finished — its tools run, its results appended — so the conversation on a `TurnLimit` or `TokenLimit` stop is intact and resumable. The alternative, stopping the moment the count is passed, would leave the run holding an answer it never committed.
>
> *A deadline on the caller's own context reads as `StopCauseTimeout`, not `StopCauseCancelled`.* The run's `Timeout` cancels with a sentinel cause through `context.WithTimeoutCause`, so the two clocks are told apart; but when the caller's context carries the deadline, the cause really was a clock and `context.Cause` says so. Only an explicit cancel is `Cancelled`.

**Stop and pause are different mechanisms.** *Stop* means "stop generating tokens now": cancel the run's context, the provider aborts the request (both providers stop billing on an aborted stream), and the partial text is kept. *Pause* means the loop yields at a boundary and can be continued later: a hook defers a tool call for approval, or the turn budget runs out, and the result carries the conversation and any pending calls. Both end in a terminal event; neither loses the conversation.

**Hooks.** A struct of typed funcs: `BeforeSend(ctx, *Request) error` (may shape the new turn and the request, never earlier turns), `AfterReceive(ctx, *Message) error`, `BeforeToolCall(ctx, ToolUse) (Decision, error)` with `Allow`, `Deny{Reason}`, `Modify{Input}` and `Defer`, and `AfterToolCall(ctx, ToolUse, ToolResult) error`. Every hook error short-circuits the run. `Defer` ends the run with the pending calls in the result; `Agent.Resume(ctx, conv, results)` appends the caller's results and continues. Transport concerns use an injected HTTP client or round tripper.

**Cache policy.** `Auto` sends the provider's top-level automatic marker plus one explicit breakpoint on the last static system block. `Manual` honours markers the caller sets on blocks and rejects more than four. `Off` sends none. Tools serialise in declaration order.

**Capabilities.** Providers that implement `ModelLister` populate `ModelInfo`; the agent caches it per model and fails a request carrying an input the model is known not to accept with `*CapabilityError` before the network call. Provider messages such as "no endpoints found that support image input" translate into the same error.

## The providers

**`goodall/anthropic`.** Wire structs for the Messages API, translation both ways, the SSE event decoder, `Complete`, the models endpoint, error mapping, and beta headers as typed options. Thinking config maps effort to `thinking: {type: adaptive}` plus `output_config.effort`, with `display` from the request; on budget-only models the budget derives from effort by the same ratios OpenRouter uses. `Off` is omitted on models that reject `disabled`. The system prompt is sent as blocks so a cache marker can sit on it.

> **Added 2026-09-15 while building the Anthropic wire structs and translation.** What the paragraph above leaves open, decided in code (`anthropic/wire.go`, `translate.go`, `thinking.go`, `options.go`, `events.go`):
>
> - **`max_tokens` has a named default.** Anthropic requires the member and publishes no default, so `anthropic.DefaultMaxTokens` (8192) is substituted when `Request.MaxTokens` is zero — within every current model's output limit, and to be overridden by `ModelInfo` once the catalogue is wired in.
> - **Effort becomes a budget by whole percent of `max_tokens`** on budget-only models: low 20, medium 50, high 80 — OpenRouter's published ratios, so a thread thinks about as hard on either provider — with xhigh 90 and max 95 extending the ladder up to the most OpenRouter ever asks for. The result is clamped to at least Anthropic's 1024-token minimum and strictly below `max_tokens`; a `max_tokens` of 1024 or less with an effort set is refused with `KindInvalidRequest` rather than sent as a body the API will reject. An effort rung with no ratio (one goodall does not define) is refused the same way on a budget model and passes straight through to `output_config.effort` on an adaptive one.
> - **Model class comes from one name heuristic**, `thinkingStyleFor`: a name containing "fable" is always-on (it refuses `disabled`), otherwise the first *major.minor* in the hyphenated name decides — 4.6 and above adaptive, below it budget — and a name with no version reads as adaptive, since an unrecognised name is likelier to be newer than older. A trailing release date is not mistaken for a minor version, and a vendor prefix does not disturb it. `ModelInfo` overrides it later.
> - **`metadata` carries `user_id` alone.** `Request.Metadata["user_id"]` becomes `metadata.user_id`; any other key is refused with `KindInvalidRequest` naming it. Metadata a caller believed was sent for abuse monitoring and was not is worse than a refusal they can see.
> - **`messages` is the last member of the body**, so the model, the parameters, the system prompt and the tools form the byte-stable prefix invariant 8 asserts on; `stream` is set by the client after translation, since one body serves both paths.
> - **The wire structs are unexported.** The package's API is the provider, `Extensions` (betas plus `service_tier`) and the `Beta` constants; the body is free to follow Anthropic's changes. The SSE payload shapes live in `anthropic/events.go` beside the rest of the wire, and their decoder is the streaming leaf's.

**`goodall/openrouter`.** An OpenAI-compatible Chat Completions client with `Dialect` typed constants (`OpenRouter`, `OpenAI`, `LMStudio`, `Generic`) that decide: the reasoning field name and whether `reasoning_details` is carried, whether `cache_control` markers are sent, whether provider routing, plugins and `session_id` are sent, whether usage carries cost, and whether `strict` tools need the Anthropic beta header. Translation covers tool calls with string arguments, `image_url` and `file` parts, `reasoning_details` round-trip in order, top-level and per-block cache markers, and the attribution headers. The decoder skips comment keep-alives, treats the usage frame as accounting rather than a second terminal, accumulates tool-call fragments by index, and turns a 200 with an error body into `*APIError` on both paths. The models endpoint feeds `ModelInfo` and per-token prices parsed from decimal strings, and `usage.cost` feeds `Cost`.

## The chat layer

**Threads.** `Thread{ID, Conversation, Usage, Cost, Version, CreatedAt, UpdatedAt}` is JSON-serialisable. `ThreadStore` has `Get`, `Put` with a version check, `List` and `Delete`; `MemoryStore` is the built-in implementation and the reference for the contract test any store must pass.

**Service.** `chat.Service` wraps an `Agent` and a store. `Send(ctx, threadID, input)` starts a run owned by the service, refuses a second run on a busy thread with `ErrThreadBusy`, persists the thread when the run ends, and returns a run id. `Subscribe(ctx, threadID)` returns a stream that first replays the accumulated partial message of the active run, then the live tail; it works whether or not the caller started the run. `Stop(threadID)` cancels the active run. `Resolve(threadID, results)` continues a deferred run. `View(threadID)` returns the redacted view. A CLI consumer calls `Send` then `Subscribe` on one goroutine; an HTTP consumer wires them to two routes.

**Redaction view.** `ThreadView` contains user text and media, assistant text, thinking summaries when displayed, tool names and status, and placeholders keyed by opaque ids for the system prompt, tool inputs and tool results. It is computed from the canonical thread and never written back.

**Wire events.** `chat.WriteSSE` and `chat.WriteNDJSON` encode the event stream for a front end using the events' JSON shapes.

**Markdown.** `Renderer` interface with `RenderHTML(text) string`; the built-in subset renderer emits no raw HTML, escapes everything else, and allows only `http`, `https` and `data` image and link schemes. Consumers wanting full CommonMark plug in goldmark in a few lines; the README shows how.

> **Corrected 2026-09-14 (build session).** The scheme rule above was too loose: `data:` is a link vector (`data:text/html`) and is only needed for inline images. The rule as built is: link `href` must be `http` or `https`; image `src` must be `http`, `https` or `data:image/…`; anything else renders as plain text. Relative and scheme-less URLs are rejected too, because model output never legitimately needs them and a chat front end cannot resolve them safely.

## Testing and tooling

- `go test -race ./...` is the suite. A test in the root package walks the import graph and fails if the root or `internal/sse` imports anything outside the standard library, or if `goodall` imports a subpackage.
- Table tests over captured JSON under `testdata/` for every block, event and error shape on both providers; an `httptest.Server` for status handling, retries, cancellation mid-stream and body close on early break; `synctest` for timing.
- `scripts/record-fixtures/` refreshes the fixtures from live APIs when `.env` provides keys; live tests skip loudly without it and say what they skipped.
- OpenRouter's `debug.echo_upstream_body` verifies the translation to Anthropic without a second live provider.
- `examples/cli` and `examples/web` are the previews: every event and every state renders somewhere a human can look.

## Build order

Foundation first because everything types against it; then the two providers in parallel, since they touch disjoint packages; then the loop against a fake provider and both real ones; then the chat layer; then examples and the README. Each leaf below states its files so parallel agents do not collide.

> **Carve note, 2026-09-14 (build session).** Three files moved between leaves so the foundation could fan out without cross-leaf compile dependencies: `request.go` (and a new `provider.go` for the `Provider`, `Completer` and `ModelLister` interfaces) went from the vocabulary leaf to the stream leaf, because `Request` references `Message` and `Tool`; `cache.go` was written on main before the fan-out because the block model and the vocabulary both need `CacheControl`; `Budget` lives in the loop leaf's `budget.go`. `Request.System` is `[]Text` rather than a string so a cache marker can sit on the static system block. The `job` notes on those leaves carry the same rulings.

```yaml
tasks:
  - title: goodall v1
    desc: |
      Build goodall to the 2026-09-14 architecture plan (project/2026-09-14-architecture-plan.md).
      The plan's Decisions and Invariants sections are binding; the research findings
      (project/2026-09-14-research-findings.md) carry the verified API facts every leaf relies on.
    children:
      - title: Foundation
        desc: The root package vocabulary that every other package types against. Standard library only.
        children:
          - title: Scaffold, licence and dependency-direction test
            ref: scaffold
            desc: |
              Add LICENSE (MIT), the root package doc comment, `go.mod` sanity, and a test that walks the
              import graph and fails if `goodall` or `goodall/internal/sse` imports anything outside std,
              or if the root imports a subpackage. Files: LICENSE, doc.go, deps_test.go.
            criteria:
              - LICENSE is MIT with the owner's copyright line
              - deps_test.go fails when a non-std import is added to the root package (proved by mutation)
              - go vet and gofmt are clean
          - title: Block model, messages and conversation
            ref: blocks
            blockedBy: [scaffold]
            desc: |
              Sealed `Block` interface with Text, Image, Document, ToolUse, ToolResult, Thinking,
              RedactedThinking; `Source` for media; `Message` with Partial; append-only `Conversation`.
              json/v2 tag dispatch via MarshalToFunc/UnmarshalFromFunc (handle both T and *T).
              Files: block.go, source.go, message.go, conversation.go, block_json.go and tests.
            criteria:
              - Every block type round-trips through JSON byte-for-byte, including Thinking.Raw
              - A tool_result containing image and document blocks round-trips
              - Conversation exposes no method that mutates an earlier message
              - An unknown block type decodes as Unknown with its raw bytes preserved
          - title: Schema type and struct-tag inference
            ref: schema
            blockedBy: [scaffold]
            desc: |
              Ordered `Schema` struct (never a map) and reflection-based inference for NewTool[In]:
              declaration-order properties, json tags, omitzero/omitempty optional, desc and enum tags,
              nested structs, slices, additionalProperties false, no recursion in v1.
              Files: schema.go, schema_infer.go and tests.
            criteria:
              - Property order in the emitted JSON equals struct declaration order (asserted on bytes)
              - Unsupported field kinds return an error at inference, not a panic
              - Table test covers string, integer, number, boolean, enum, slice, nested struct, optional
          - title: Tools and tool results
            ref: tools
            blockedBy: [schema, blocks]
            desc: |
              `Tool` interface, NewTool[In] closing over a typed handler, ToolResult helpers (text, JSON,
              blocks), and the schema-aware decode-error message that names the bad parameter and lists
              the expected ones. Files: tool.go, tool_result.go and tests.
            criteria:
              - NewTool returns an error for an invalid schema instead of panicking
              - A decode failure yields an IsError result whose text names the parameter and lists all parameters with type and required
              - Execute with a valid input calls the handler with the typed value
          - title: Vocabulary types, errors and usage
            ref: vocab
            blockedBy: [scaffold]
            desc: |
              StopReason, Effort, ThinkingDisplay, Thinking config, CachePolicy, Support, Usage, Cost,
              ModelInfo and Capabilities, Request with Extensions, *APIError with Kind, *CapabilityError,
              Budget. Files: request.go, thinking.go, cache.go, usage.go, capabilities.go, errors.go and tests.
            criteria:
              - Every enum has a String method and an Unknown value that survives decoding
              - Cost distinguishes reported from not reported
              - APIError supports errors.Is on Kind and errors.AsType extraction
          - title: Stream, events and accumulator
            ref: stream
            blockedBy: [blocks, vocab]
            desc: |
              `Stream` as a named iter.Seq2 with Collect; the sealed Event family for provider and loop
              events with stable JSON shapes and a type discriminator; the accumulator that builds a
              Message from events, exposes just-finished tool-use edges and a resumable prefix, and
              validates block indexes. Files: stream.go, event.go, event_json.go, accumulate.go and tests.
            criteria:
              - Collect over a scripted event sequence equals the hand-built Message
              - Every event type round-trips through JSON
              - Breaking out of a Stream early runs the producer's cleanup (asserted with a defer counter)
              - Accumulator rejects a delta for an unopened block index with a typed error
          - title: SSE framer
            ref: sse
            blockedBy: [scaffold]
            desc: |
              `internal/sse`: WHATWG-correct line splitting (LF, CRLF and lone CR), comment lines skipped,
              multi-line data joined, event/data/id/retry fields, a scanner buffer large enough for
              base64 media, and an iterator-shaped API. Files: internal/sse/sse.go and tests.
            criteria:
              - Table test covers LF, CRLF, lone CR, comment lines, multi-line data, missing blank-line separator before a new event field
              - A 5 MB data line is framed without error
          - title: HTTP transport helper with retries
            ref: transport
            blockedBy: [vocab]
            desc: |
              Shared request execution for providers: injected http.Client, jittered backoff on
              RateLimited/Overloaded/Server kinds honouring Retry-After, no retry after the first streamed
              byte, context cancellation closing the body. Files: internal/transport/transport.go and tests
              using httptest and testing/synctest.
            criteria:
              - A 429 with Retry-After 2 is retried after 2 seconds of fake time, proven with synctest
              - No retry occurs once a body byte has been read
              - Cancelling the context mid-body returns ctx.Err and closes the body
      - title: Anthropic provider
        desc: goodall/anthropic, the Messages API client. Disjoint files from goodall/openrouter.
        children:
          - title: Anthropic wire structs and translation
            ref: ant-wire
            blockedBy: [blocks, tools, vocab]
            desc: |
              Wire structs for the request and response, translation of blocks, tools, tool choice,
              system blocks, thinking config (adaptive plus effort; budget derived from effort on
              budget-only models; Off omitted where disabled is rejected), cache markers for Auto and
              Manual, and typed beta-header options. Files: anthropic/wire.go, anthropic/translate.go,
              anthropic/thinking.go and tests.
            criteria:
              - Every neutral block translates to the documented Anthropic shape and back, including thinking with signature and redacted thinking
              - Two consecutive requests that append one message share a byte-identical prefix
              - Manual cache policy with five markers returns an error before sending
          - title: Anthropic streaming, blocking, models and errors
            ref: ant-stream
            blockedBy: [ant-wire, stream, sse, transport]
            desc: |
              Provider implementation: Stream over SSE mapping every documented event and delta type
              including signature_delta, ping and mid-stream error, Unknown for the rest; Complete;
              Model() from GET /v1/models into ModelInfo; error.type mapped to APIError.Kind.
              Files: anthropic/client.go, anthropic/stream.go, anthropic/models.go, anthropic/errors.go and tests.
            criteria:
              - A recorded thinking-plus-tool-use stream collects to the same Message as the recorded blocking response
              - A mid-stream overloaded_error surfaces as an APIError with Kind Overloaded
              - An unknown event type is yielded as Unknown, not dropped
              - ModelInfo reports image and PDF input as separate facts
          - title: Anthropic fixtures and live tests
            ref: ant-live
            blockedBy: [ant-stream]
            desc: |
              scripts/record-fixtures records text, tool-use, thinking, image and PDF exchanges from the
              live API into anthropic/testdata when .env has ANTHROPIC_API_KEY; live tests skip loudly
              without it. Files: scripts/record-fixtures/*, anthropic/testdata/*, anthropic/live_test.go.
            criteria:
              - The unit suite passes offline with the recorded fixtures
              - Live tests report what they skipped when no key is present
              - A live round trip with thinking and a tool call succeeds on the current default model
      - title: OpenRouter provider
        desc: goodall/openrouter, an OpenAI-compatible Chat Completions client with typed dialects. Disjoint files from goodall/anthropic.
        children:
          - title: OpenRouter wire structs, dialects and translation
            ref: or-wire
            blockedBy: [blocks, tools, vocab]
            desc: |
              Wire structs, the Dialect type (OpenRouter, OpenAI, LMStudio, Generic) and its quirk table,
              translation of blocks to text, image_url and file parts, tool calls with string arguments,
              reasoning config to the reasoning object, reasoning_details carried opaquely and re-emitted
              in order, cache markers, provider routing and session options as Extensions, attribution
              headers. Files: openrouter/wire.go, openrouter/dialect.go, openrouter/translate.go and tests.
            criteria:
              - reasoning_details from a recorded response re-serialises byte-identical and in order on the next request
              - The Generic dialect sends no OpenRouter-only fields (asserted on bytes)
              - Tool-call arguments round-trip as a JSON string on the wire and a jsontext.Value in the model
          - title: OpenRouter streaming, blocking, models and errors
            ref: or-stream
            blockedBy: [or-wire, stream, sse, transport]
            desc: |
              Stream over SSE: comment keep-alives skipped, tool-call fragments accumulated by index,
              reasoning deltas, the trailing usage frame treated as accounting, error events and
              200-with-error bodies mapped to APIError via error_type; Complete; models endpoint into
              ModelInfo with prices parsed from decimal strings; usage.cost into Cost.
              Files: openrouter/client.go, openrouter/stream.go, openrouter/models.go, openrouter/errors.go and tests.
            criteria:
              - A recorded stream with a repeated finish_reason collects to one terminal Message with usage attached
              - A 200 response whose body is only an error object returns an APIError on both paths
              - Prices parse to exact decimal values, not floats
              - A text-only model refusing an image maps to CapabilityError or UnsupportedInput
          - title: OpenRouter fixtures and live tests
            ref: or-live
            blockedBy: [or-stream]
            desc: |
              Extend scripts/record-fixtures for OpenRouter (a Claude model and an OpenAI model, with
              reasoning and tools, an image and a PDF) into openrouter/testdata; live tests skip loudly
              without OPENROUTER_API_KEY. Files: scripts/record-fixtures/*, openrouter/testdata/*, openrouter/live_test.go.
            criteria:
              - The unit suite passes offline with the recorded fixtures
              - A live tool-call round trip preserves reasoning across the tool result on a Claude model
      - title: Agent loop
        desc: The loop in the root package, tested against a scripted fake provider and both real providers.
        children:
          - title: Loop core, budget and termination
            ref: loop
            blockedBy: [stream, tools, transport]
            desc: |
              Agent.Run: append input, send, accumulate, classify stop reason per the plan's table, run
              tools concurrently with WaitGroup.Go, append the assistant message and all results as one
              unit including parse failures and unknown tools, enforce Budget, emit Done or Stopped on
              every path including cancellation with Partial set and running tools awaited.
              Files: agent.go, run.go, budget.go, stop.go, fake provider in testing helpers, tests.
            criteria:
              - Every stop reason maps to a documented next step and the test table covers all of them
              - A tool-argument parse failure produces a tool_result with IsError and the run continues
              - Cancelling mid-stream yields Stopped with the partial assistant message in the conversation
              - Exceeding MaxTurns yields Stopped with reason TurnLimit and the conversation intact
              - go test -race passes with tools that run concurrently
          - title: Hooks, approval and resume
            ref: hooks
            blockedBy: [loop]
            desc: |
              Hooks struct with BeforeSend, AfterReceive, BeforeToolCall (Allow, Deny, Modify, Defer),
              AfterToolCall; every error short-circuits; Defer ends the run with Pending; Agent.Resume
              appends caller results and continues. Files: hooks.go, resume.go and tests.
            criteria:
              - A BeforeToolCall error ends the run with Stopped and no tool executed
              - Defer returns Pending tool uses and Resume continues to a Done result
              - BeforeSend cannot alter an earlier message (the request exposes only the new turn and parameters)
          - title: Capability pre-flight, cache policy and cost in the loop
            ref: loop-caps
            blockedBy: [loop, ant-stream, or-stream]
            desc: |
              Cache ModelInfo per model via ModelLister; fail a request carrying an unsupported input
              with CapabilityError before sending; apply CachePolicy; accumulate Usage and Cost into
              Result; end-to-end tests of the loop against both real providers' recorded fixtures.
              Files: preflight.go, loop_cost.go and tests.
            criteria:
              - An image sent to a model whose ModelInfo says Unsupported fails before any HTTP request
              - Unknown support lets the request through
              - Result.Cost is Reported only for OpenRouter fixtures
      - title: Chat layer
        desc: goodall/chat, the optional components for chat-shaped consumers.
        children:
          - title: Threads, store and service
            ref: chat-service
            blockedBy: [hooks]
            desc: |
              Thread, ThreadStore with version check, MemoryStore plus a store contract test, and
              Service with Send (detached run, ErrThreadBusy), Subscribe (catch-up then live),
              Stop, Resolve and per-thread usage and cost totals. Files: chat/thread.go, chat/store.go,
              chat/memory_store.go, chat/service.go and tests under -race.
            criteria:
              - A subscriber attaching mid-run receives the accumulated partial message then live deltas
              - A run finishes and persists with no subscriber attached
              - A second Send on a busy thread returns ErrThreadBusy
              - Two concurrent Puts with the same version leave exactly one winner
          - title: Redaction view and wire events
            ref: chat-view
            blockedBy: [chat-service]
            desc: |
              ThreadView computed from a Thread with opaque ids for system prompt, tool inputs and
              results; WriteSSE and WriteNDJSON for the event stream. Files: chat/view.go, chat/wire.go and tests.
            criteria:
              - The view contains no system prompt text and no tool result text
              - Ids are stable across two computations of the same thread
              - WriteSSE output parses back through internal/sse into the same events
          - title: Markdown subset renderer
            ref: markdown
            blockedBy: [scaffold]
            desc: |
              Renderer interface and the chat-safe subset: paragraphs, headings, emphasis, inline and
              fenced code, links, images, lists, block quotes; no raw HTML ever; http, https and data
              schemes only. Files: chat/markdown/*.go and a fixture corpus of input and expected HTML.
            criteria:
              - Raw HTML in the input is escaped in the output
              - "A javascript: link renders as text, not a link"
              - The fixture corpus covers every supported construct and passes
      - title: Examples and README
        desc: The previews and the front door.
        children:
          - title: CLI chat example
            ref: ex-cli
            blockedBy: [chat-service, markdown]
            desc: |
              examples/cli: pick a provider from .env, stream text and thinking, show tool calls,
              multi-turn over the chat Service, Ctrl-C stops the run and keeps the thread.
            criteria:
              - Runs against both providers with the keys in .env
              - Stopping mid-answer leaves a partial message the next turn can continue from
          - title: Web chat example
            ref: ex-web
            blockedBy: [chat-view]
            desc: |
              examples/web: a small net/http server exposing Send, Subscribe over SSE, Stop and View,
              with a minimal page that reconnects and catches up.
            criteria:
              - Reloading the page mid-answer shows the partial message and continues streaming
              - The page never receives the system prompt
          - title: README and doc audit
            ref: readme
            blockedBy: [ex-cli, ex-web, loop-caps]
            desc: |
              README with the one-line description, what it is, install, a quick start for the chat
              consumer and one for the customiser, links to the project docs, authorship and licence;
              audit that every exported identifier has a doc comment; prune gotchas.
            criteria:
              - Both quick starts compile as example tests
              - go vet reports no missing doc comments under the project's lint
```
