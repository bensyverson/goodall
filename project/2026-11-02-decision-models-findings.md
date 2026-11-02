# Decision models beyond Jev

Findings, 2026-11-02, on whether goodall can support decision models other than TypeSafe's Jev the way it supports more than one conversational provider. **It can, and most of it already works:** 24 of the 27 models OpenRouter serves answered every typed question through today's `typesafe.Client` with only the base URL changed. Two wires cover the whole field that was checked. The owner [ruled](#decisions-for-the-owner) for a neutral package with both wires; the work is staged in the [plan](2026-11-02-decision-models-plan.md).

The industry has settled on the name **decision model** (OpenAI's Decisions API, OpenRouter's decision models). goodall's docs say "judgment model" for the same thing, and "System One" is TypeSafe's own term.

## Sources

**Observed**, 2026-11-02, with `go run ./scripts/probe-decisions -env /abs/path/.env -out /abs/dir`. The script asks one fixed choice, noul and score of one easy support ticket, through `typesafe.Client.Ask` with retries off, once over a string state and once over an object state, for every concrete model in the catalog. It also makes raw calls for what that client cannot send. The sweep's 49 readable calls cost $0.000982 by OpenRouter's own `usage.cost`.

**Documented**, read the same day and not otherwise exercised. Each is served as Markdown:

- OpenAI: `https://developers.openai.com/api/docs/guides/decisions` and `https://developers.openai.com/api/reference/resources/decisions/methods/create`.
- OpenRouter: `https://openrouter.ai/skills/openrouter-decisions`, `/docs/guides/community/jev`, `/docs/guides/community/typesafe-sdk`, `/docs/guides/community/multimodal-decisions` and `/docs/api/api-reference/alphadecisions/submit-a-decisions-request`.
- TypeSafe: `https://docs.typesafe.ai/models.md` and `/api.md`.

Third-party write-ups disagree about launch dates, status and whether confidence is returned. They are not used here: OpenAI's own guide says the API is generally available and returns `confidence` on choice and score, and the probe saw it.

## The field

`GET https://openrouter.ai/api/v1/models?output_modalities=decisions` lists 33 rows on 2026-11-02: 27 concrete models once the `~` aliases and the `:free` variant are dropped, under 14 author prefixes. Decision models are left out of the default model list, so the filter is required. The catalog grew from the 22 models OpenRouter's Jev page counted on October 16, so any list written down here is already aging; look it up rather than remembering it.

Prices run from free (`respan/span-01-lite:free`) to $0.24 per million input tokens (`cloudflare/clef`); Jev is $0.042 and GPT-6 Luna $0.10. Context ranges from 8k (`jaredpalmer/kev-4b`) to 1.05M (GPT-6 Luna), and Jev is listed at 262k. On top of the catalog, OpenAI serves `gpt-6-luna` directly.

## Three surfaces, two wires

| | TypeSafe | OpenRouter | OpenAI |
|---|---|---|---|
| Path | `POST /v1/systemone` | `POST /api/v1/systemone`, also `POST /api/alpha/decisions` | `POST /v1/decisions` |
| Evidence | `state`: string, object, array | same | `input`: a string, or messages, tool calls and tool outputs |
| Questions | object keyed by id | same | array, each with an optional `name` |
| Yes or no | `noul`, answer `noul` | same | `predicate`, answer `probability`; no criteria member |
| One of a set | `choice`, `criteria` map | same | `choice`, `choices[]{value, description}`; 2 to 255 |
| Ordered | `score`, `criteria` array, `legend` echoed | same | `score`, `levels[]{label, description}`; 2 to 10 |
| Distribution | map of key to probability | same | array of `{value, probability}` |
| Locate in an image | `box`, Jev 1.14 only | same | none |
| Refusal | none documented | none in the response schema | an answer of `type: refusal`, per question |
| Usage | input, output | adds `usage.cost`, `id`, `provider` | adds cached, cache-write and reasoning tokens; no cost |
| Error body | `{"detail": …}` | `{"error": {"message", "code"}}` | `{"error": {"message", "type", "param", "code"}}` |

> **Corrected 2026-11-02.** An earlier draft of the plan said LM Studio and generic servers have no decision API. That was an assumption, never checked, and it was wrong: see [Local servers](#local-servers).

OpenRouter's two paths and TypeSafe's are one wire. OpenAI's is a second, with the same three ideas spelled differently. It also offers things the first wire does not: a conversation as evidence, images per question, hosted image URLs, prompt-cache controls, and up to 400 questions, while TypeSafe documents no question count at all.

## What the probe saw

**The existing client reads almost everything.** `typesafe.New(key, typesafe.WithBaseURL("https://openrouter.ai/api"))` read 49 of 54 calls with every typed accessor working.

| Result | Models |
|---|---|
| Every question, both states | 24 models, including `openai/gpt-6-luna-decisions`, `microsoft/microsoft-decision-1`, `perplexity/pplx-decider-v1.2`, `cloudflare/clef`, `liquid/d1` and `typesafe/jev-1.14` |
| String state only | `scaledown/classify-1`: 400, "state must be a string or an object with only text, document, and document_mime_type" |
| Refused the question set | `respan/span-01`, `respan/span-01-lite`: 400, "only accepts noul questions" for the string state, and a message-shaped state requirement for the object one |

Each failure is the model's own limit, not a wire mismatch, and each arrived as a 400 with a sentence naming it, which the client surfaced as `KindInvalidRequest`. Nothing in the catalog predicts them: `supported_parameters` is empty on every row. A capability table in goodall would have nothing to read from, so a loud 400 is the right behavior.

**OpenRouter and the native APIs agree.** The same request on both, one call each, no noul criteria:

| Model | Native | Through `/api/alpha/decisions` |
|---|---|---|
| GPT-6 Luna | score 2.13, levels 0, 0.01, 0.85, 0.14, 422 input tokens | identical |
| Jev 1.14 | score 2.21, levels 0, 0, 0.78, 0.22, 427 input tokens | score 2.22, same levels and tokens |

So a caller who wants Luna does not need the native wire to get Luna's answers. OpenRouter's `usage.cost` matched the token count times the catalog price on both ($0.0000422 for Luna, $0.000017934 for Jev). The sweep sent Luna 439 tokens where this table's request, identical but for the noul's criteria, sent 422, which is consistent with OpenRouter folding criteria into the request; that is inference, since a predicate has no criteria member.

**Thresholds will not travel.** On one easy ticket the score ran from 1.99 to 2.91 and choice confidence from 0.67 to 1.00 across models, all picking the same department. That is two samples per model and says nothing about which is better. It does confirm OpenRouter's advice to retune for each model.

**A slug does not always pin the host.** `cloudflare/clef` was served by PrimeIntellect on one call and DeepInfra on the next, and the response reported the undated `cloudflare/clef`, where most models report a dated id such as `baseten/joey-1-20261029`.

## Local servers

Documented, read 2026-11-02, and **none of it run**. The two wires hold locally too: every server below serves System One, and two also serve OpenAI's shape.

| Server | Publisher | System One | OpenAI shape | Default address | Error body |
|---|---|---|---|---|---|
| [LM Studio](https://lmstudio.ai/docs/developer/jev-compat/systemone) | LM Studio | yes | [yes](https://lmstudio.ai/docs/developer/openai-compat/decisions) | `localhost:1234` | not documented |
| [llama.cpp](https://huggingface.co/blog/ggml-org/decision-models-in-llamacpp) | ggml-org | yes | not mentioned | `localhost:8080` in the blog; the server README states another default | not documented |
| [Ollama](https://docs.ollama.com/api/systemone.md), v0.35 and later | Ollama | yes | none found | `localhost:11434` | `{"error": "message"}` |
| [LocalAI](https://localai.io/docs/features/decisions/) | LocalAI project | yes, plus `/permute` and `/separate` | yes | not noted here | `{"error": {"message", "type"}}` |
| [ollajev](https://pypi.org/project/ollajev/) | independent | yes | none found | `127.0.0.1:8000` | `{"detail": [{"loc", "msg", "type"}]}` |

The ollajev row and LocalAI's error body come from a subagent's reading and were not re-checked by hand. LM Studio's page defers to llama.cpp's docs for the System One detail. Open-weight models named include Julia-1 (144 MB), Laya, Kev-4B, Clef, Nimble and OpenJev; OpenJev and native Nimble are noncommercial, per the blog and LocalAI.

**The servers agree on shape and differ at the edges**, and three of those differences matter to a client:

- **Errors.** `openrouter`'s decoder reads `error` as an object, so Ollama's string form would fail to decode and lose the server's sentence. Read from the code, not run.

  > Corrected 2026-11-02: "lose the server's sentence" was wrong in its mechanism. With `go test -short -run TestDecideSurfacesEveryLocalServersSentence ./openrouter` before the fix, a failing status with a string error reached the caller with the whole JSON body as `APIError.Message`, from the transport's fallback, so the sentence was wrapped and not lost. Inside a 200 the same body was a `ProtocolError` naming the Go type, and there the sentence was lost. Both now read as the sentence (leaf MxIOGV), through `wireError` and `systemone.ErrorMessage`.
  >
  > Re-checked by reading the pages on 2026-11-02: LocalAI's page gives `{"error": {"message": "...", "type": "invalid_request"}}` and ollajev's gives only the shape above, no values, so the sentences in the tests for those two are authored. Ollama's `{"error": "text and schema must not exceed 64 KiB"}` is its 413 example. None of the three has been run.

- **What a state means.** An object is JSON text on Ollama and labeled lines on LocalAI. On llama.cpp an array is read as a list of chat messages. goodall should pass the state through and document this, since normalizing it would hide a real difference in what the model reads.
- **What confidence means.** An entropy measure on Ollama and a normalized margin for some models on LocalAI, so even a threshold on the same model need not carry between servers.


### LM Studio, run live

Run 2026-11-02 against LM Studio 0.4.28+2 (`CFBundleShortVersionString`) with `julia-1`, the Q8_0 GGUF of `ggml-org/Julia-1-GGUF` at 168.17 MB, with `go test -count=1 -v ./openrouter -run TestLiveLMStudio`. One machine, one version and one model, and it shows the wire works, not that the model judges well: on the payouts ticket Julia-1 sent the ticket to `technical` at 0.99, where Luna said `billing`.

- **No key and no model named both work.** `Decide` read a choice, a predicate with criteria and a score through the typed accessors in 0.12s for the test, and the response named `julia-1`. No cost is reported, and `Answers.Cost` stays unreported.
- **The score carries a legend and integer-keyed probabilities**, and a confidence. The predicate's body carried no confidence, so that tolerance rests on the authored test and on the leaf's report of it.
- **Errors are this server's own.** An unknown model is a 400 of kind `invalid_request` with code `invalidModelIdentifier` and the sentence "Invalid model identifier \"no-such-model\". Please specify a valid downloaded model (e.g., julia-1)." With nothing loaded it is a 400 with code `noModelsLoaded` and "No models loaded. Please load a model in the developer page or use the 'lms load' command." Both reach `APIError.Message`.
- **The version matters.** 0.4.19+2 had julia-1 loaded and did not route `/v1/systemone` or `/v1/decisions`, answering each with HTTP 200 and `{"error":"Unexpected endpoint or method. (POST /v1/systemone)"}`, while its chat endpoints said "use /v1/systemone". Updating to 0.4.28+2 fixed it. Which version between them added the routes is not established, since the changelog lines read seem to sit under a Bionic release. `Decide` now reports that body as an error.

llama.cpp, Ollama and LocalAI remain documented and not run.

## What falls short today

> **Landed 2026-11-02, goodall v1.5:** every item below is resolved, and this section describes the code before that work. `goodall/decision` holds the questions, the answers and the adapters with no vendor names. `openrouter.Client` implements `decision.Decider`, with `Answers.Cost`, `Generation` and `ServedBy` filled from what OpenRouter reports, errors stamped `openrouter`, a `DecisionModels` catalog, no default model, and OpenAI's wire under the OpenAI dialect. `typesafe.Client` keeps `jev-latest` as a default of its own and is no longer the way to reach another vendor. [The conformance suite](#the-conformance-suite) proves it.

Read from the code, except where marked:

- **`Answers.Cost` is documented as never reported**, and through OpenRouter the money is on the response and dropped. So are `id` and `provider`.
- **Errors carry the wrong provider name.** A failure through OpenRouter is stamped `typesafe`.
- **`Models` would return nothing.** It reads a `models` member and OpenRouter answers with `data`, so the likely result is an empty list and no error. Not run.
- **`DefaultModel` is a TypeSafe alias.** OpenRouter documents mapping `jev-latest` to `~typesafe/jev-latest`, so it works there by accommodation. Not run.
- **OpenAI's wire is not reachable at all.** Sending the System One shape there gets 400, "Missing required parameter: 'input'" (observed). *(Since built: see [The OpenAI wire](#the-openai-wire).)*
- **The name.** `typesafe.New(openRouterKey, …)` asking Microsoft's model a question reads as a mistake, and the question types, `Tool`, `AuthoredTool` and `Route` are not about TypeSafe.

## Options

**A. Document the base URL.** Add a README line, read `usage.cost` and fix the provider name. Half a day, and 24 models work officially. It leaves the misleading name and no OpenAI wire, and every consumer's code says `typesafe` forever. At this stage, with zero users, the rename is at its cheapest now.

**B. A neutral `goodall/decision` package, with wires behind it.** This is the recommendation. It mirrors what the core already does for conversation: neutral types above, wire translation below.

```go
// goodall/decision: no vendor names, no HTTP.
type Decider interface {
    Decide(ctx context.Context, state any, q Questions, opts ...Option) (*Answers, error)
}
// Questions, Predicate or Noul, Choice, Score, Answers, Refusal,
// and the adapters Tool, AuthoredTool and Route, moved here and written over Decider.

// goodall/typesafe: TypeSafe's native API.
d := typesafe.New(os.Getenv("TYPESAFE_API_KEY"))

// goodall/openrouter: the existing client gains Decide, choosing the wire by dialect.
d := openrouter.New(openrouter.WithAPIKey(os.Getenv("OPENROUTER_API_KEY")))
d := openrouter.New(openrouter.WithAPIKey(key), openrouter.WithBaseURL("https://api.openai.com/v1"), openrouter.WithDialect(openrouter.OpenAI))

answers, err := d.Decide(ctx, ticket, questions, decision.WithModel("microsoft/microsoft-decision-1"))
```

The dialect is the seam that already says how an OpenAI-shaped server differs from OpenRouter, so a second client type is not needed. The System One codec would be shared from `internal/`, so neither provider package imports the other. The root package does not change, and neither do the dependencies, which stay at zero.

Building both wires together is what proves the types are neutral. A neutral package built against one wire would just be the TypeSafe package under a new name, and the differences that test it (refusals, arrays against maps, no criteria on a predicate, what JSON evidence may be) are all on OpenAI's side.

**C. Make decision models a `Provider`.** Already rejected in the [Jev findings](2026-09-17-typesafe-jev-findings.md#it-is-not-a-provider), and nothing here changes that: there is still no conversation, no block and no stream.

**How B squares with the backlog.** The parked ["delegate" or "judge" seam](backlog.md) would have put a decision model and a nested agent behind one interface in the provider layer, and it stays parked. `Decider` is narrower: it unifies decision vendors with each other, as `Provider` does conversational ones, and agents keep reaching it through a tool or a hook.

## What B has to settle while building

- **Refusals.** A neutral `RefusalAnswer` whose typed accessor returns an error. OpenRouter's response schema has no refusal variant, so what it does with an OpenAI refusal is unknown; the accessors already fail loudly on a missing or unknown answer, so neither case reads as a zero value.
- **Noul criteria on OpenAI.** *Settled: folded, as measured in [The OpenAI wire](#predicate-criteria-folded).* A predicate has no criteria member, so `True` and `False` must fold into the instructions on that wire, or be refused locally. Folding is an adapter making a prompting decision, so measure it. Choice descriptions and score labels map directly.
- **JSON evidence on OpenAI.** *Settled: see [The OpenAI wire](#the-state-rule).* A string becomes `input`. Anything else becomes an `input_json` part, documented as an object or an array of objects, with limits of 32 levels, 10,000 values and 256 KiB. An array of strings needs a rule.
- **Which OpenRouter path.** `/api/v1/systemone` is not labeled alpha and carried the sweep, whereas `/api/alpha/decisions` is the documented primary but says alpha in its URL and was only exercised twice. Start on `systemone`; it is one constant to change.

## The OpenAI wire

Built 2026-11-02 in `openrouter/decide_openai.go`. Every figure here came from `go run ./scripts/probe-openai-decisions -env /abs/path/.env` with the `-mode` named beside it, against `gpt-6-luna` on `api.openai.com` and `openai/gpt-6-luna-decisions` on OpenRouter. The tool run's code was uncommitted on top of `bd411f8`, since the leaf's agent does not commit; it is pinned by the SHA-256 of `openrouter/decide_openai.go`, `7257d55e…`, and of the probe's `criteria.go`, `ec04ef2f…`. Commit `d53d65c` holds exactly those bytes, so that is the pin; a later one-word change to how an empty error member reads does not touch what these figures measured.

### The state rule

A state is judged by what it marshals to, not its Go type. **A string is `input`. An object or an array of objects, empty ones included, is one user message holding one `input_json` part. Anything else is refused before any call**, with `KindInvalidRequest` and a sentence naming what would be accepted.

Observed with `-mode states`, one call each:

| What `data` held | Answer |
|---|---|
| object, array of objects, `{}`, `[]` | 200 |
| array of strings, array mixing objects and strings, array of arrays | 400, `invalid_type`, "Invalid type for 'input[0].content[0].data[0]': expected an object…" |
| string, number, boolean, null | 400, `invalid_type`, "Invalid type for 'input[0].content[0].data'…" |
| 33 levels of objects | 400, null `code`, "input_json data exceeds the maximum depth of 32" |
| one 300 KiB string in an object | 400, null `code`, "input_json data exceeds the maximum serialized size of 262144 bytes" |

The limit of 10,000 values was not provoked. An object as bare `input` is a 400 too, so the message wrapper is not optional.

**Why refuse rather than convert.** The two ways to make a list of strings fit both decide how the model reads the caller's data: inventing an object key, or writing the list out as JSON text in `input`. Neither has been measured, the server is clear about what it takes, and the way out is one line in the caller's own hands, who knows what the list is. The refusal costs no call, and it reads better than the server's sentence, which names an inner path of a message the caller never wrote. It reaches a model through `Tool` and `AuthoredTool` as an error result it can act on, and ends a `Route` the way any failed judgment does, so neither adapter breaks. The 32 levels and 256 KiB stay the server's to state, as the plan rules for limits, and each arrives as a sentence.

The other wire does not have this rule: System One takes any JSON, so a list of strings that works there is refused here. That is a real gap in "one set of questions over every backend", and it is in the state rather than the questions.

An object state reads alike on both wires. The recorded mixed exchange, the same ticket and questions on each, came back with 433 input tokens on both and identical figures (`openrouter/testdata/live_decision_mixed.json` against `live_openai_decision_mixed.json`, held together by `TestBothWiresReadAsTheSameNeutralShapes`).

### Predicate criteria: folded

**`True` and `False` are written into the instructions: the question, a blank line, then `True: …` and `False: …` on a line each, leaving out one that is empty.** A question written for Jev and a question written for Luna are then the same value, which is the goal, and a developer does not need to know this wire has no member for them. Refusal would have broken that for an option the neutral type advertises and `AuthoredTool` offers to a model. It was the fallback if folding measured badly.

Measured with `-mode criteria -runs 3`, every arm through `Client.Decide`. `openrouter_criteria` sends the descriptions as System One criteria, `openai_criteria` folds them, and the last two arms frame them differently in the instructions:

| Case | OpenRouter, none | OpenAI, none | **OpenRouter, criteria** | **OpenAI, folded** | one newline | `Yes:` and `No:` |
|---|---|---|---|---|---|---|
| urgency, soft ticket | 0.83 | 0.83 | **0.19** | **0.19** | 0.21 | 0.15 |
| sarcasm, literal words | 0 | 0 | **0** | **0** | 0 | 0.01 |
| refund window | 0.50 | 0.50 | **0** | **0** | 0 | 0.68 |
| mild satisfaction | 0.21 | 0.21 | **1** | **1** | 0.72 | 0.99 |
| sales direction | 1 | 1 | **0** | **0** | 0 | 0 |
| bug or feature | 0.97 | 0.97 | **0.82** | **0.82** | 0.80 | 0.99 |

Input tokens matched on both routes in every row, with and without criteria (for example 177 and 194 on the first). All three framed arms matched those counts too, so tokens alone did not choose among them; the probabilities did.

What this says:

- **The measurement can see a difference.** Criteria moved the answer in 5 of the 6 rows, and the two neighboring spellings missed OpenRouter in 3 and 5 of the 6.
- **There is no run-to-run noise to swallow an effect.** All 138 answered calls of this run repeated their arm's figure exactly, as did the 110 of an earlier run at `-runs 5`. So the spellings differ by more than chance.
- **The fold was checked on cases it was not picked from.** An earlier run, with the single newline as the shipped spelling, missed OpenRouter on urgency (0.21 against 0.19) while the blank line hit it. The refund, satisfaction, sales and bug rows were written after that choice.
- **OpenRouter very probably folds the same way.** That is still inference: 6 cases on one model on one day, and what OpenRouter sends upstream is not visible. If it changes its folding, these wires will part without an error.

The cases are chosen to be movable, not a sample of real questions, and nothing here says the fold judges well, only that it judges as the other route does.

**One-sided descriptions have no comparison.** OpenRouter refuses them: `True` alone is a 400, "questions.q.criteria.false: Invalid input", and `False` alone the mirror of it, 8 calls each. So the neutral type's "optional" on each is not true of every backend. OpenAI's wire has only text, so it folds whichever is present, and that does move the answer (0.84 to 0 with `True` alone, and 1 to 0.31 with `False` alone), but nothing says it is what a server taking one-sided criteria would do.

### What was seen and not documented

- **The error shape** is on neither page read: `{"error":{"message","type","param","code"}}`, with `code` a string such as `invalid_type` or null.
- **The body names no id**, as documented, and every response carries `x-request-id`, so `Answers.Generation` is that.
- **`output_tokens` was 0** on every call, as the reference's example shows.
- **A refusal was never drawn**, in any call here. Its handling rests on the documented shape, with only `type` and `name`. A boolean choice value, likewise.
- **A choice with one option** passes `decision.Questions.Validate` and draws a 400 here, "array too short". The limits of 2 to 255 choices, 2 to 10 levels and 400 questions stay the server's to state, as the plan rules.

The answer members arrive in another order than the reference lists them, which matters to nothing.

### What the calls cost

294 calls in all: 16 `-mode states`, 9 `-mode spellings`, 120 and 144 `-mode criteria`, 4 recordings and one live test. 29 of them were 400s. At the catalog's $0.10 per million input tokens, roughly 50,000 tokens is about $0.005; that is arithmetic, not a figure either server stated.

## The conformance suite

Built 2026-11-02 in `internal/conformance`, one table of backends read by both the suite and `go run ./scripts/record-fixtures -provider conformance`. The same three questions, over one ticket as an object and as a string, go through `decision.Tool`, `AuthoredTool` and `Route` on TypeSafe (`jev-1.13.0`), OpenRouter (`openai/gpt-6-luna-decisions`), OpenAI (`gpt-6-luna`) and LM Studio 0.4.28+2 with nothing named. `go test -short ./internal/conformance` runs them from fixtures with no `.env`, and `GOODALL_ENV_FILE=/abs/path/.env go test -count=1 -v ./internal/conformance -run TestLiveConformance` runs them live. It asserts structure and accessors, never one model's judgment. The offline replay answers only a request whose bytes match a recording, which is what proves `Tool` and `AuthoredTool` send what `Decide` sends.

**It found one real bug.** Each backend is also asked of a model that does not exist, so a server's own refusal has to reach the consumer as its sentence. TypeSafe answers that with a 400 of `{"detail":{"error_type":"api_usage_error","message":"Unknown model: no-such-model"}}`, a third shape of `detail` beside the string and the list the reader knew, and `systemone.ErrorMessage` reported the inner object as raw JSON. It now reads the message. The same request is a 400 on OpenRouter, whose sentence names `typesafe/no-such-model`, so a bare id is apparently taken as TypeSafe's, and on LM Studio, and a 404 of kind `not_found` on OpenAI. So the status of one mistake differs by server, and only the sentence is common to all four.

### The sweep, re-run through Decide

Run 2026-11-02 with `go run ./scripts/probe-decisions -env /abs/path/.env -out /abs/dir`, now through `openrouter.Client.Decide`, with `DecisionModels` as the catalog:

| | The first sweep, through `typesafe.Client` | This sweep, through `openrouter.Client.Decide` |
|---|---|---|
| Catalog | 33 rows, 27 concrete models | 33 rows, 27 concrete models |
| Readable | 49 of 54 | **49 of 54** |
| Failing | `respan/span-01` and `respan/span-01-lite` on both states, `scaledown/classify-1` on the object state | the same 5 calls, naming the same limits |
| Reported cost of the readable calls | $0.000982 | $0.000982339 |

**There is no difference to explain**, in the count or in which calls. The figure is not a measure of stability: it is one sweep on the same day as the last, against a catalog of the same size. The questions and the states are the same, and the path that changed is the one under test. `cloudflare/clef` was served by DeepInfra and PrimeIntellect on the two calls this time, again from two hosts.

## Not established

- **Which model is good.** The probe tests whether a model can be read, not how well it judges.
- **Refusals, 401, 429 and 529 through OpenRouter.** Only 400s were provoked.
- **Images and Box on any wire.** goodall has neither today, and there are three image syntaxes between the surfaces. Leave them out of the first pass; a Box answer already arrives as `UnknownAnswer`.
- **OpenAI's conversation input.** It could let a `goodall.Conversation` be judged directly, which is promising for an agent, but nothing here tried it.
- **Vendors' own direct APIs.** Cloudflare, Perplexity, Microsoft and the rest were only exercised through OpenRouter. If one has its own wire, nobody has checked it.
- **The Jev Router.** `typesafe/jev-router` is documented as an ordinary model on Chat Completions, so `goodall/openrouter` may already drive it; not run.

## Decisions for the owner

> **Ruled 2026-11-02 by the owner:** all four as recommended, namely option B with both wires, `Predicate`, `goodall/decision`, and moving the types with no shims. Recorded in the [architecture plan](2026-09-14-architecture-plan.md), staged in the [plan](2026-11-02-decision-models-plan.md).

1. **Scope.** Option B with both wires (recommended), B with the System One wire first, or A.
2. **The name of the yes-or-no question.** `Predicate` (recommended; plain English and owned by no vendor, and OpenAI uses it) or `Noul` (TypeSafe's coinage, which OpenRouter's wire also uses).
3. **Package name.** `goodall/decision` (recommended, matching what the field now calls these) or `goodall/judge`, keeping the docs' "judgment".
4. **Breaking change.** B moves `typesafe.Tool`, `AuthoredTool` and `Route`, and changes `examples/triage`. No users exist, so the recommendation is to move them with no shims.
