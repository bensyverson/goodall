# Decision models beyond Jev: the plan

Plan, 2026-11-02, for goodall v1.5: support decision models from any vendor the way goodall supports more than one conversational provider. The [findings](2026-11-02-decision-models-findings.md) carry the evidence, the wire comparison and the options weighed; this doc carries what was ruled and how the work is staged.

## Goal

A developer writes one set of typed questions, and asks them of Jev on TypeSafe, of any of OpenRouter's decision models, of GPT-6 Luna on OpenAI, or of a model on their own machine, by choosing a client and a model. The `Tool`, `AuthoredTool` and `Route` adapters work over all of them, and the core loop never hears about it.

## Rulings

Made by the owner on 2026-11-02, on the findings' recommendations, and recorded in the [architecture plan](2026-09-14-architecture-plan.md).

1. **A neutral package, with both wires built now.** Building the second wire is what proves the types are neutral; the differences that test them are all on OpenAI's side.
2. **The package is `goodall/decision`**, the name the field has settled on.
3. **The yes-or-no question is `Predicate`**, in plain English and owned by no vendor. `noul` survives only as a spelling inside the System One codec.
4. **A clean break.** `typesafe.Tool`, `AuthoredTool`, `Route` and the question and answer types move, with no shims or aliases. goodall has no users yet.

## Architecture

```
goodall (root)         unchanged
goodall/decision       questions, answers, the Decider seam, the three adapters; no HTTP, no vendor names
internal/systemone     the System One codec, shared so neither provider package imports the other
goodall/typesafe       TypeSafe's native API, implementing Decider over the codec
goodall/openrouter     *Client implements Decider, choosing the wire by Dialect:
                         OpenAI                       -> /decisions, its own translation
                         OpenRouter, LMStudio, Generic -> /systemone, over the codec
```

The arrows mirror the conversational side: neutral types above, translation below, one client per server. `Dialect` is already how goodall says that an OpenAI-shaped server differs from OpenRouter, so no second client type appears. System One is the default for every dialect but OpenAI because it is the one wire every local server checked serves ([findings](2026-11-02-decision-models-findings.md#local-servers)), and this client already sends no `Authorization` header when it has no key.

Three decisions bind several leaves:

- **The seam takes a request struct, as `Provider` does.** A model, a state and the questions. This also spares `decision.Option`, the name a choice's option already holds, from colliding with a functional option type.
- **Only `typesafe` has a default model.** `jev-latest` is TypeSafe's own default. OpenRouter's guidance is never to pick from memory and never to pin an alias, and thresholds do not travel between models, so a decision call without a model is refused locally on the OpenRouter and OpenAI dialects. On a local server it is the server's call: llama.cpp ignores the member when one model is loaded, so an empty model is omitted and sent.
- **Limits are the server's to state.** The catalog says nothing about question types, so goodall keeps no capability table: a model's own limit arrives as a 400 with a sentence naming it, and the error carries that sentence ([findings](2026-11-02-decision-models-findings.md#what-the-probe-saw)).

## Order, and why

The neutral package comes first and is a large mechanical move guarded by behavior that must not change: the recorded TypeSafe fixtures keep passing byte for byte. The OpenRouter wire comes next because it is the small one, reusing the codec and establishing how `Client` dispatches by dialect. Local servers and the OpenAI wire follow, and each tests the neutral types differently: local servers on tolerance of small deviations, OpenAI on a different spelling of everything. The last leaf is the stage's definition of done, proving every backend through the same code.

All five touch overlapping files in a chain, so this is **one agent working in order, or the main thread, rather than a fan-out** (`project/agents/delegation.md`, "A chain of dependent leaves").

## After this: the drift checker

The owner staged this after the decision work on 2026-11-02, as `goodall v1.6` in the tree below. Nothing in the repo looks outward between sessions, so new models, stale pins and moved features stay invisible until someone asks ([why](2026-11-02-model-currency-findings.md#why-this-went-unnoticed)). A script does the mechanical comparison and a thin Skill does the judgment, which keeps the decision logic in one reusable place. It comes second so it can sweep the finished decision wires, not the old client.

## Out of scope

Each is parked in the [backlog](backlog.md) with what would revive it: images and Box questions, OpenAI's conversation input and prompt-cache controls, vendors' own direct APIs, direct Gemini, and the Jev Router. OpenAI's Responses wire gets its own plan after the decision work (`Uz2EPX` in `job`). The parked "delegate" or "judge" seam also stays parked: `Decider` unifies decision vendors with each other, and agents keep reaching them through a tool or a hook.

## Task tree

Imported 2026-11-02 as `hAG3tq` (goodall v1.5) and `S1lGYS` (goodall v1.6). The tracker is now the plan: change a leaf with `job edit`, not the YAML below.

```yaml
tasks:
  - title: 'goodall v1.5: decision models from any vendor'
    desc: |
      Support decision models from any vendor the way goodall supports more than one
      conversational provider: one set of typed questions, asked of Jev on TypeSafe, any of
      OpenRouter's decision models, GPT-6 Luna on OpenAI, or a model on a local server. Plan and rulings in
      project/2026-11-02-decision-models-plan.md; evidence and the wire comparison in
      project/2026-11-02-decision-models-findings.md.
    children:
      - title: Move the question, answer and adapter types into a neutral goodall/decision
        ref: neutral
        desc: |
          Why: goodall/typesafe holds types and adapters that are not about TypeSafe, and a probe
          on 2026-11-02 showed 24 of OpenRouter's 27 concrete decision models answer through
          today's client. Everything else in this tree builds on a neutral home for them, so
          this leaf comes first and unblocks the OpenRouter and OpenAI wires.

          What: a new goodall/decision package holds the questions, the answers, the Decider
          seam and the Tool, AuthoredTool and Route adapters, written over the seam, with no HTTP
          and no vendor names. goodall/typesafe shrinks to TypeSafe's native client implementing
          Decider. The System One encoding and decoding moves to internal/systemone, so the
          OpenRouter leaf can share it without either provider package importing the other.
          Every consumer is migrated: examples/triage, scripts/record-fixtures,
          scripts/probe-option-order, scripts/probe-decisions and the typesafe tests and examples.

          Already decided (project/2026-11-02-decision-models-plan.md): the package is
          goodall/decision. The yes-or-no question is Predicate, and noul survives only inside
          the codec. This is a clean break with no shims or aliases. The seam takes a request
          struct of model, state and questions, as Provider does, not functional options, since
          decision.Option is already the name of a choice's option. TypeSafe keeps jev-latest as
          its default model. Decision models are still not a goodall.Provider and the root
          package does not change.

          Add a neutral refusal answer here even though only OpenAI's wire produces one, because
          the adapters must handle it: a typed accessor reading a refused question returns an
          error naming the id, never a zero value (invariant 6), and a tool result tells the model
          that question was declined while the others still stand.

          Seams and traps. Question types currently marshal themselves to the wire, and that has
          to move into the codec: neutral questions must not know a wire. Answers has unexported
          entries, so the codecs in other packages need a way to build one. Answers also
          round-trips through JSON as a tool result, so its JSON form is what a conversational
          model reads; it needs a neutral form of its own rather than one wire's. AuthoredTool's
          schema enum and desc tags and DefaultAuthoredDescription are model-facing prompts, and
          the noul to predicate rename changes them. The route callback is currently named
          typesafe.Decide, which will read badly beside a Decide method. Invariants 7 and 8
          stand: ordered slices, deterministic bytes. Project rules: TDD red first, every
          exported identifier has a doc comment, comments say why.

          Out of scope: any new server, and images or Box, which stay UnknownAnswer. Also the
          judge in examples/triage stays pinned to jev-1.13.0, whose thresholds it was tuned on.

          The builder's call: exact names, whether WithToolModel and WithRouteModel collapse into
          one option now that they share a package (backlog, "One model option"; delete that
          entry if it lands), and the route callback's new name.

          Risk: this is the largest diff in the tree, and most of it is mechanical, which is
          where a behavior change hides. The recorded fixtures are the guard.
        criteria:
          - goodall/decision imports no provider package and no third-party module, shown by go list -deps
          - The TypeSafe request bytes are unchanged, shown by the existing fixture tests in typesafe passing against the unmodified request recordings in typesafe/testdata
          - A typed accessor on a refused question returns an error naming the id, and a Tool result reports the refusal beside the other answers, shown by unit tests with authored values
          - An unknown answer type still decodes to UnknownAnswer carrying its bytes, shown by a test
          - No package outside internal/systemone and typesafe mentions noul, shown by grep over the Go sources
          - The live authored-tool test (today TestLiveAuthoredToolWritesItsOwnQuestions in typesafe, wherever it moves) passes with the renamed schema, and any change in how the model writes questions is recorded as a note
          - examples/triage runs on its bundled inbox with its judge pinned to jev-1.13.0 and sorts as it did, compared against the calibration in project/2026-09-17-triage-example-plan.md
          - go test -short -race ./... passes with no .env
      - title: Add the OpenRouter decision wire, with reported cost and a model catalog
        ref: openrouter-wire
        blockedBy: [neutral]
        desc: |
          Why: this is the wire that reaches the field. OpenRouter's System One route serves 27
          concrete decision models from 14 author prefixes, and the catalog is growing, so a
          consumer with one key gets all of them.

          What: openrouter.Client implements decision.Decider under the OpenRouter dialect,
          posting to /systemone relative to its base URL over the shared codec in
          internal/systemone. Answers carry what OpenRouter reports and the native API does not:
          the cost, as a reported goodall.Cost in USD decoded as a Decimal the way
          openrouter/translate_response.go already does, plus the generation id and the serving
          host. A way to list the decision models reads
          GET /models?output_modalities=decisions, since decision models are left out of the
          default list; it should not disturb the existing ModelLister, whose answers are about
          conversational models. Errors go through this package's own decoder, so they are stamped
          openrouter.

          Already decided (plan doc): a call with no model is refused locally with
          KindInvalidRequest, as OpenRouter's guidance is never to pick from memory or pin an
          alias. No capability table: a model's own limit arrives as a 400 whose sentence reaches
          the caller. This client dispatches by Dialect. The LMStudio and Generic dialects take
          the same System One wire, and the local-servers leaf proves them. The OpenAI dialect
          has its own wire in a later leaf; until then it refuses locally.

          Known facts, observed 2026-11-02 with scripts/probe-decisions: the OpenRouter error body
          is {"error":{"message","code"}}, and for a schema failure the message is a JSON array
          inside a string, so check that the caller gets something readable. respan/span-01 and
          scaledown/classify-1 reject parts of a normal request with clear 400s. cloudflare/clef
          answered from two hosts in two calls and reports an undated id, which is why the serving
          host is worth surfacing. The response has no refusal variant in its published schema.

          Which path: use /api/v1/systemone rather than /api/alpha/decisions. It carried the sweep
          and is not labeled alpha, and the two have the same shape; keep it one constant.

          Seams: openrouter/client.go, openrouter/dialect.go, new files in openrouter;
          scripts/record-fixtures gains the exchanges (live_* under openrouter/testdata), and
          internal/livemodel gains a pinned decision model. Every live test skips loudly with no
          key, as the others do.

          Out of scope: OpenRouter's provider routing preferences on a decision request (nothing
          has tested that the System One route accepts them), images and Box.
        criteria:
          - A recorded live exchange against a pinned non-Jev model decodes through Decide with every typed accessor working, shown by a fixture test under openrouter/testdata
          - Answers report the cost as Reported true in USD, matching the fixture's usage.cost exactly as a Decimal, shown by a test
          - A call with no model under the OpenRouter dialect is refused locally with KindInvalidRequest before any request is sent, shown by a test
          - A 400 from a model's own limit surfaces its sentence on a *goodall.APIError stamped openrouter with KindInvalidRequest, shown by a fixture test using a recorded respan or scaledown refusal
          - Listing the decision models returns the live catalog's rows with their input modalities, shown by a fixture test, and a live run names the count it saw
      - title: Prove the System One wire against local servers
        ref: local-servers
        blockedBy: [openrouter-wire]
        desc: |
          Why: people run decision models on their own machines, and every local server checked
          serves /v1/systemone, advertised as drop-in. Consumers building a prototype want this
          with no key and no bill. It should need little new code, but the servers differ in small
          ways that a client written against one will trip over.

          What: openrouter.New(WithBaseURL(local), WithDialect(LMStudio or Generic)) with no key
          asks a local decision model through the same Decide, and every documented deviation below
          is either tolerated or surfaced clearly.

          Documented on 2026-11-02, and none of it run yet. LM Studio at localhost:1234 serves
          /v1/systemone and /v1/decisions and needs a loaded decision model. llama.cpp's llama serve
          serves /v1/systemone at localhost:8080 per its blog, while the server README gives another
          default port; models include ggml-org/Julia-1-GGUF at 144 MB. Ollama at localhost:11434
          serves /v1/systemone from v0.35. LocalAI serves both paths. All sit under /v1, so the
          relative path holds.

          Traps. Error bodies differ: Ollama sends {"error":"message"} as a string, which
          openrouter's decodeError cannot read today (its error member is a struct), so the
          server's sentence would be lost; LocalAI nests it; ollajev uses {"detail":[…]}, which
          typesafe's decoder already reads, so the tolerant decoder probably belongs in
          internal/systemone. A score may arrive with no legend (ollajev's documented example), and
          a noul may carry an extra confidence. Confidence means different things: an entropy
          measure on Ollama, a normalized margin for some models on LocalAI.

          State means different things, which no client can paper over, and the docs should say so.
          An object is JSON text on Ollama and labeled lines on LocalAI. On llama.cpp an array is read
          as a list of chat messages, so an array of records may not mean what the author intended.
          Do not normalize this; pass the state through and document it in the decision package.

          Open, the builder's call: whether the error tolerance is enough or a local dialect is needed.
          Default: no new dialect, since none of these differences affects what is sent.

          Needs the owner: the real server is LM Studio, installed on the owner's machine with no
          model loaded as of 2026-11-02. Which decision model to download and load is the owner's
          call; the small end of the range is Julia-1 at 144 MB, if LM Studio's catalog carries it.
          Agents' sandboxes refuse local ports, so the live run is the integrator's, with the
          sandbox off for that one call. If it cannot happen, say so plainly in the leaf rather
          than closing it on fakes.

          Out of scope: model management, images, /permute and /separate.
        criteria:
          - Each documented error body surfaces the server's sentence on a *goodall.APIError, shown by tests with authored bodies quoted from the servers' docs
          - An answer with no legend and a noul with an extra confidence both decode through the typed accessors, shown by tests
          - An empty model is omitted from the request on the LMStudio and Generic dialects, and no Authorization header is sent with no key, shown by tests
          - A live run against LM Studio reads a choice, a predicate and a score through Decide, recording the LM Studio version and model in the findings doc
          - The decision package documents that object and array state differ between servers
      - title: Add the OpenAI decision wire on /v1/decisions
        ref: openai-wire
        blockedBy: [openrouter-wire]
        desc: |
          Why: this wire is what proves goodall/decision is neutral, since it spells the same three
          ideas differently, and it gives direct OpenAI customers a way in without an OpenRouter
          account. OpenRouter serves the same model, with identical answers observed on one call
          each, so this wire earns its place by testing the types and by offering direct access.

          What: openrouter.Client implements Decider under the OpenAI dialect, so
          New(WithBaseURL("https://api.openai.com/v1"), WithDialect(OpenAI)) reaches POST /decisions.
          The translation lives in its own file in the openrouter package, beside the dialect that
          already describes this server.

          Facts, from https://developers.openai.com/api/docs/guides/decisions and the create
          reference, read 2026-11-02, with the shapes marked observed seen by scripts/probe-decisions.
          The request takes model, input and questions, an array with a name per question (observed).
          Types are predicate, choice and score. A choice lists choices of value and description,
          2 to 255; a score lists levels of label and description, 2 to 10; a request holds 1 to
          400 questions. The response is answers in question order, each with type and name
          (observed). A predicate reports probability. Choice and score report probabilities as
          arrays of value and probability, with a label too on a score, plus confidence (observed).
          Any question may instead come back as type refusal, with only its name. No cost is
          reported. Errors are {"error":{"message","type","param","code"}} (observed).

          Traps. goodall.Usage.Input is the uncached prompt tokens, while OpenAI's input_tokens
          includes the cached and cache-write counts, so those must be subtracted rather than
          double counted. A score's probabilities are keyed by level index in the neutral form and
          arrive here as an array. A choice value may be a boolean on this wire, but goodall only
          sends strings, so one coming back is a protocol surprise that should stay loud. An
          unnamed answer has a null name; goodall always sends names.

          Open, and the builder's call with evidence. State mapping: a string becomes input, and
          anything else becomes a user message with an input_json part, documented as an object or
          an array of objects with limits of 32 levels, 10,000 values and 256 KiB. An array of
          strings or a bare number needs a rule; refusing locally and saying why is the default
          unless the builder finds better. Predicate criteria: a predicate has no criteria member, so
          True and False either fold into the instructions or are refused locally. Folding is an
          adapter making a prompting decision, so if it folds, measure it over repeated runs at one
          pinned commit against the same question through OpenRouter, and record the figures and the
          tool that made them in the findings doc (project/agents/evidence.md). The sweep sent 439
          tokens with criteria and 422 without, consistent with OpenRouter folding them, which is
          inference and not an observation.

          Out of scope: conversation input, images, prompt-cache controls and the usage admin
          endpoint, all backlog.
        criteria:
          - A recorded live exchange on gpt-6-luna decodes through Decide with every typed accessor working and the same neutral answer shapes as the System One wire, shown by a fixture test under openrouter/testdata
          - A refusal answer decodes to the neutral refusal while its sibling answers stay readable, shown by a test with an authored body, since a live refusal cannot be provoked on demand
          - Usage reports cached and cache-write tokens without double counting input, shown by a test with authored non-zero details
          - Cost stays Reported false, meaning not known, shown by a test
          - The state rule and the predicate criteria rule are each tested, and written down in the findings doc with the evidence that chose them
      - title: Prove every backend through the same code, and update the docs
        ref: prove
        blockedBy: [openai-wire, local-servers]
        desc: |
          Why: this is the stage's definition of done. The goal is one set of questions and adapters
          over every backend, and until that runs for real it is only a type signature.

          What: one conformance suite runs the same questions through decision.Tool, AuthoredTool
          and Route over TypeSafe, OpenRouter, OpenAI and a local server, offline against recorded fixtures and live
          when keys are present, so a fourth wire would have a test to pass. scripts/probe-decisions
          moves onto Decide and re-sweeps the catalog, updating its doc comment. examples/triage
          gains a way to name the judge's backend and model, and stays on its pinned Jev by default.
          The triage thresholds are tuned to Jev and do not carry to another model, so a non-Jev run
          is a demonstration of the seam and must not be read as a quality result.

          Docs: the README's package map and a short quick start for a non-TypeSafe model, keeping
          its examples compiling as tests. The typesafe and decision package comments. The findings
          doc's "What falls short today" gets a marked note saying what landed. Anything built
          differently from the plan goes in the architecture plan as a dated block quote.

          Out of scope: comparing the models' quality, and tuning any threshold.
        criteria:
          - One table-driven suite runs the same questions through every backend and all three adapters from fixtures with no .env, shown by go test -short
          - The same suite passes live against the hosted backends when keys are present, its output naming the model that answered on each, recorded as a note
          - go run ./scripts/probe-decisions sweeps the live catalog through openrouter Decide, and its readable count is reconciled with the 49 of 54 recorded 2026-11-02, with any difference explained in the findings doc
          - go run ./examples/triage with a named non-Jev judge completes on the bundled inbox, recorded as a note saying it shows the seam and not quality
          - The README quick start for a non-TypeSafe model compiles as an example test
          - The pre-commit steps pass, including agents check
  - title: 'goodall v1.6: a periodic check for API and model drift'
    desc: |
      Notice when vendors move before a consumer does: new models, retired pins, changed response
      shapes, features that move between endpoints. Staged after the decision work by the owner on
      2026-11-02; the why is in project/2026-11-02-model-currency-findings.md, under "Why this went
      unnoticed", and the staging is in project/2026-11-02-decision-models-plan.md.
    children:
      - title: Build scripts/check-drift, a mechanical comparison against the live APIs
        ref: drift-script
        blockedBy: [prove]
        desc: |
          Why: go test replays against pinned models and recordings, so it cannot see a vendor move.
          On 2026-11-02 one manual look found GPT-6.1, Gemini 4, Jev 1.14 with a fourth question type,
          decision models on local servers, and tools with reasoning moving to OpenAI's Responses
          API. All of it had been invisible. A repeatable check replaces asking by hand.

          What: one command, costing cents, that reports what changed since the last accepted
          baseline, as text a person or a model can read. It compares four things. Catalogs: each
          vendor's model list against internal/livemodel, naming a pin that is gone or a new family.
          Changelogs: each vendor's changelog Markdown against a committed snapshot. The live loop:
          the agent-loop probe and the decision sweep, against the current models of each family.
          Response shapes: fresh responses compared with the recorded fixtures by their member names,
          flagging members our wire structs do not model, the most direct sign of API drift.

          Seams: build from what exists rather than beside it, namely scripts/probe-agent-loop,
          scripts/probe-decisions, scripts/probe-gpt6 and scripts/record-fixtures. Fold the probes in
          if that makes them simpler. internal/dotenv reads the keys. Docs serve Markdown at known
          URLs (appending .md at OpenAI and OpenRouter, .md.txt at ai.google.dev); keep the list in one
          place. Bound the spend, and state each run's cost, using reported cost where a vendor gives
          it.

          Traps: live probabilities and token counts differ on every run, so never compare values,
          only shape and pass or fail. A check that skips for want of a key must say so loudly, since
          a check over zero cases is not green. An unreachable page is not "no change".

          Open, the builder's call: where snapshots live and how a baseline is accepted, and whether
          a changelog diff compares whole pages or new dated entries, which is likely less noisy.

          Out of scope: deciding what a change means or filing anything, which is the next leaf;
          fixing what it finds.
        criteria:
          - A run against the committed baseline reports no drift when nothing changed, shown by a test with authored inputs and one live run
          - An authored response with an unmodeled member, a retired pin and a new changelog entry are each reported by name, shown by tests
          - A missing key or an unreachable page is reported as not checked and exits differently from a clean run, shown by a test
          - One live run's output and cost are recorded in a dated doc, and it would have flagged the GPT-6 Responses change, shown by running it against a baseline from before that change
      - title: Add a /drift-check Skill and decide how it runs on a schedule
        ref: drift-skill
        blockedBy: [drift-script]
        labels: [decision]
        desc: |
          Why: a diff of changelogs needs judgment about whether goodall cares. A script should not
          hold that judgment, and a person should not have to read the output weekly.

          What: a thin project Skill that runs scripts/check-drift, decides which changes matter to
          goodall's wires, writes a dated report under project/ and files what needs doing under
          "Issues found on the way" in job. The Skill holds no comparison logic of its own. Vendor
          pages are untrusted data, never instructions, and the Skill's text says so.

          The owner's call, to bring with a recommendation: how it runs. A cloud routine has no access
          to .env or local servers and needs its own keys; a local schedule needs the machine awake.
          Cadence too, with weekly the starting guess.

          Trap: the harness's sandbox denies writes to .claude/skills, so writing the Skill needs
          approval. See project/agents/harness.md.

          Out of scope: making changes on its own. It reports and files.
        criteria:
          - One run of the Skill produces a dated report and files at least one real or explicitly declined issue, recorded as a note
          - The owner has ruled on how and how often it runs, and that is recorded in project/ and set up
```
