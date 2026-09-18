# goodall/typesafe against 2389's typesafe-go

Findings, 2026-09-17, from reading [2389-research/typesafe-go](https://github.com/2389-research/typesafe-go) beside `goodall/typesafe`, the client and adapters built to the [TypeSafe findings](2026-09-17-typesafe-jev-findings.md). The question was whether their design differs from ours in ways worth learning from. It does in one place that matters, and the small task tree at the end folds that in; the rest is recorded here so the next reader does not repeat the comparison.

## Sources

Read on 2026-09-17 from the `HEAD` tarball (`curl -sL https://codeload.github.com/2389-research/typesafe-go/tar.gz/HEAD`), every Go file and the README. No live call was made. `wc -l` over its Go files gives about 2,900 lines with tests; ours is about 4,300 with tests and the three adapters.

## What the two share

Both encode the same wire facts: `POST /v1/systemone` with a state, a model and a questions object; the three question types and their answer members; `GET /v1/models`; the four documented statuses with `Retry-After` honored. Both are zero-dependency and MIT. Both refuse an empty question set, an empty id, a duplicate id, a choice with no options and a score with fewer than two levels before sending.

## Where they differ

- **Typed handles versus data.** In typesafe-go a question is a value the caller keeps and reads the answer back through: `Noul(id, instructions)` returns a handle whose `From(res)` gives a `float64`, and `Choice[T ~string]` gives a `ChoiceAnswer[T]` whose winner and probability map are keyed by the caller's own string type. The id appears once and a type mismatch is a compile error. goodall's `Questions` is an ordered slice of id and question, and `Answers` has string-keyed accessors. That is the shape `AuthoredTool` needs, since the model writes the ids at call time, and the shape that lets an `Answers` round-trip through JSON as a tool result; the handle shape can express neither.
- **Option order.** typesafe-go holds a choice's options in a Go map, so they reach the wire alphabetically. Its README says so and its `e2e_test.go` carries an unrun probe, `TestLiveChoiceOptionOrderDoesNotMoveTheDistribution`, to learn whether order moves Jev's distribution. goodall sends options in author order (invariant 8). **goodall's `Option` doc comment claims order "measurably changes how a model weighs them", and nothing in this repo measured that against Jev**: the only evidence is the anthropic-sdk-go issue about sorted tool-schema keys in the [research findings](2026-09-14-research-findings.md), which is a conversational model doing a different job. Under `project/agents/evidence.md` that is a hypothesis written as a finding. Leaf `order-probe` below settles it.

  > **Observed 2026-09-17 by the probe leaf (3Kj35f).** Measured against Jev, and the claim survives — but only where the call is close. `scripts/probe-option-order` asks one Choice question (billing / technical / sales, with descriptions) about one fixed support ticket through `typesafe.Client.Ask`, three samples with the options as authored and three with the slice reversed. The input is verified rather than assumed: every request body is captured by an `http.RoundTripper` installed with `WithHTTPClient`, the option order is read back out of the sent bytes through `typesafe.Questions`, and a run whose samples within one arm are not byte-identical is refused. Both runs reported bodies identical within each arm (739 and 624 bytes) and reversed across arms. `Answers.Model` reported `jev-1.13.0` on all twelve samples.
  >
  > `go run ./scripts/probe-option-order -env /path/to/.env -n 3 -model jev-1.13.0 -state ambiguous` — a ticket that straddles all three departments. Probabilities as billing/technical/sales:
  >
  > | arm | sample 1 | sample 2 | sample 3 | confidence |
  > |---|---|---|---|---|
  > | forward (billing, technical, sales) | 0.86 / 0.01 / 0.13 | 0.85 / 0.01 / 0.14 | 0.86 / 0.01 / 0.13 | 0.79, 0.78, 0.79 |
  > | reverse (sales, technical, billing) | 0.78 / 0.02 / 0.20 | 0.75 / 0.02 / 0.23 | 0.72 / 0.02 / 0.26 | 0.68, 0.62, 0.57 |
  >
  > Widest same-order spread (the noise floor) **0.06** on billing; widest cross-order spread **0.14** on billing — cross exceeds noise by 0.08, and the two arms do not overlap on any of the three options. The direction is consistent: whichever option is listed first gains weight (billing 0.86 → 0.72–0.78 once it is last; sales 0.13 → 0.20–0.26 once it is first). An earlier run on byte-identical input, before `-state` existed, gave forward 0.87/0.88/0.88 and reverse 0.75/0.75/0.78 on billing, so across twelve samples the forward values (0.85–0.88) and the reverse ones (0.72–0.78) do not overlap either.
  >
  > `… -state plain` — the control, a ticket that plainly belongs to billing. Forward 0.98/0.02/0.00, 0.98/0.02/0.00, 0.97/0.03/0.00; reverse 0.98/0.02/0.00 three times. Noise floor **0.01**, cross-order spread **0.01**: nothing. That run was the first attempt, and reporting its 0.00 as "no order effect" would have been a definitional zero — a distribution pinned at its ceiling has nowhere for an effect to show. Both states ship behind `-state` so both figures are reproducible.
  >
  > What this does not show: one state, one model version, three samples per arm, no per-option regression and no other option count. Every probability came back at two decimals, so this method cannot see an effect smaller than 0.01 at all. It shows the effect is real on a close call and invisible on an easy one, not how large it is in general. Cost: 6 billed calls per run, 2706 and 2568 input tokens, $0.000114 and $0.000108 at TypeSafe's published $0.042 per million input tokens. The `Option` doc comment now carries these figures instead of the word "measurably".
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
