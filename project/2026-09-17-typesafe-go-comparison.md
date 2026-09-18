# goodall/typesafe against 2389's typesafe-go

Findings, 2026-09-17, from reading [2389-research/typesafe-go](https://github.com/2389-research/typesafe-go) beside `goodall/typesafe`, the client and adapters built to the [TypeSafe findings](2026-09-17-typesafe-jev-findings.md). The question was whether their design differs from ours in ways worth learning from. It does in one place that matters, and the small task tree at the end folds that in; the rest is recorded here so the next reader does not repeat the comparison.

## Sources

Read on 2026-09-17 from the `HEAD` tarball (`curl -sL https://codeload.github.com/2389-research/typesafe-go/tar.gz/HEAD`), every Go file and the README. No live call was made. `wc -l` over its Go files gives about 2,900 lines with tests; ours is about 4,300 with tests and the three adapters.

## What the two share

Both encode the same wire facts: `POST /v1/systemone` with a state, a model and a questions object; the three question types and their answer members; `GET /v1/models`; the four documented statuses with `Retry-After` honored. Both are zero-dependency and MIT. Both refuse an empty question set, an empty id, a duplicate id, a choice with no options and a score with fewer than two levels before sending.

## Where they differ

- **Typed handles versus data.** In typesafe-go a question is a value the caller keeps and reads the answer back through: `Noul(id, instructions)` returns a handle whose `From(res)` gives a `float64`, and `Choice[T ~string]` gives a `ChoiceAnswer[T]` whose winner and probability map are keyed by the caller's own string type. The id appears once and a type mismatch is a compile error. goodall's `Questions` is an ordered slice of id and question, and `Answers` has string-keyed accessors. That is the shape `AuthoredTool` needs, since the model writes the ids at call time, and the shape that lets an `Answers` round-trip through JSON as a tool result; the handle shape can express neither.
- **Option order.** typesafe-go holds a choice's options in a Go map, so they reach the wire alphabetically. Its README says so and its `e2e_test.go` carries an unrun probe, `TestLiveChoiceOptionOrderDoesNotMoveTheDistribution`, to learn whether order moves Jev's distribution. goodall sends options in author order (invariant 8). **goodall's `Option` doc comment claims order "measurably changes how a model weighs them", and nothing in this repo measured that against Jev**: the only evidence is the anthropic-sdk-go issue about sorted tool-schema keys in the [research findings](2026-09-14-research-findings.md), which is a conversational model doing a different job. Under `project/agents/evidence.md` that is a hypothesis written as a finding. Leaf `order-probe` below settles it.
- **Structured instructions.** typesafe-go's `Entry = any` lets instructions, option descriptions and score levels be a JSON object or array, which the API accepts. goodall sends strings only, a documented deferral. Parked in the [backlog](backlog.md).
- **Construction.** typesafe-go's `New()` reads `TYPESAFE_API_KEY`, `TYPESAFE_BASE_URL` and `TYPESAFE_DEFAULT_MODEL`, validates the key and the base URL, and returns an error. goodall's `New(apiKey)` follows `anthropic.New` and validates nothing, so an empty key surfaces as a 401 on the first call. Not adopted: goodall's convention is that the consumer owns configuration.
- **Errors.** typesafe-go uses sentinel errors and keeps the raw body; its message probe reads `detail` only as a string, so a real 422, whose `detail` is a list (findings doc, observed), yields no message. goodall renders the list as `body.questions.vibe: <msg>` and maps onto the shared `goodall.APIError` kinds. Not adopted.
- **Retry.** typesafe-go carries its own policy with an injected clock, a 30-second wall-clock budget, a 10-second per-attempt timeout and a `retry-after-ms` header TypeSafe does not document. goodall shares `internal/transport` with the providers: full-jitter backoff, `Retry-After` in both RFC 9110 forms, no default per-attempt timeout because the default client serves long streams; `WithHTTPClient` sets one. Not adopted. One sentence of theirs is worth copying: `Ask`'s doc says a retry can bill twice, because the client cannot tell a request that never arrived from a response that was lost. goodall's transport has the same property and `typesafe` does not say so. Leaf `retry-billing` below.
- **Only goodall has** `Tool`, `AuthoredTool`, `Route`, per-call usage on `ToolCallEnd`, `UnknownAnswer` for the undocumented fourth question type, a parsed release date and recorded fixtures.

## Task tree

Imported into `job` as its own root, since the v1 and v1.1 roots are closed. `internal/transport` is not modified.

```yaml
tasks:
  - title: "goodall v1.2: typesafe-go follow-ups"
    desc: |
      Two follow-ups from comparing goodall/typesafe with 2389's typesafe-go,
      recorded in project/2026-09-17-typesafe-go-comparison.md. Read
      project/agents/evidence.md before the probe.
    children:
      - title: Measure whether option order moves Jev's choice distribution
        ref: order-probe
        desc: |
          typesafe/questions.go says on Option that the order options are
          presented in "measurably changes how a model weighs them". Nothing in
          this repo measured that against Jev; the claim was carried over from an
          issue about a conversational model's tool schemas. Settle it with a
          live probe and make the comment true either way.

          Write scripts/probe-option-order: a main package that takes -env (as
          scripts/record-fixtures does), asks one fixed Choice question about one
          fixed state through typesafe.Client.Ask (the production path), n
          samples with the options in author order and n with them reversed, and
          prints every distribution, the widest same-order spread (the noise
          floor) and the widest cross-order spread. Pin the model with WithModel
          so the figure names a version. Default n to 3; six billed calls at
          TypeSafe's published price is well under a cent. Do not turn it into a
          test: it spends money on every run and the offline suite must stay
          free.

          Then record the figure in the comparison doc as a dated block quote
          naming the exact command, and rewrite the Option comment: if the
          cross-order spread sits inside the noise floor, say order is sent as
          authored because deterministic bytes are the invariant and no effect
          was seen at that sample size; if it exceeds it, keep the claim and
          cite the figure. Either way the comment must not assert more than the
          run showed.
        criteria:
          - "scripts/probe-option-order runs through typesafe.Client.Ask with -env and prints per-sample distributions, the same-order spread and the cross-order spread"
          - "The comparison doc carries the observed figures in a dated block quote that names the command, the model version and the sample size"
          - "The Option doc comment in typesafe/questions.go states only what the probe showed"
      - title: Say on the typesafe client that a retry can bill twice
        ref: retry-billing
        desc: |
          typesafe-go's Ask doc says a retry can pay for one evaluation twice,
          because the client cannot tell a request that never reached the server
          from a response lost after the server processed it. goodall's transport
          retries the same way and typesafe says nothing about it. Add one or two
          sentences to Client.Ask and WithMaxRetries in typesafe/client.go and
          typesafe/options.go, pointing at WithMaxRetries(-1) for at-most-once,
          and confirm that -1 does disable retrying (options.go says a negative
          value does). No code change unless that confirmation fails.
        criteria:
          - "Client.Ask and WithMaxRetries say that a retried call may be billed twice and name the option that prevents it"
          - "The claim that a negative WithMaxRetries disables retrying is confirmed by an existing test or a new one"
```
