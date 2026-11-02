# Drift report, 2026-11-02: a rehearsal against a rewound baseline

The first run of the `/drift-check` Skill's judgment. Today's real run reports no drift, which leaves nothing to judge, so this one asks what the Skill would have said had the baseline predated GPT-6. The rewind and its limits are in [the check-drift findings](2026-11-02-check-drift-findings.md).

```
python3 scripts/check-drift/rewind.py scripts/check-drift/baseline.json > before.json
go run ./scripts/check-drift -env .env -baseline before.json
RESULT: drift, 69 changes
COST: 75 live calls; USD 0.008486023 reported across 54; 21 with no reported price (6950 input and 620 output tokens)
```

Nothing was unchecked. **No issue was filed: every change that matters is already tracked or built, so each is declined below with its reason.**

## Would break goodall, or worth adopting

| Change | Verdict | Why no issue |
|---|---|---|
| `loop/openai/*` at effort low: 400, "use /v1/responses" | Breaks tools with reasoning on direct OpenAI | Tracked as `yEETo1`, goodall v1.7 |
| GPT-6 Astra, Sol, Luna and Terra in the catalogs | Worth adopting | All four are in `loopTargets`, and Astra and Luna are pinned in `internal/livemodel` |
| Oct 22 and Oct 6, the Decisions API: beta, then GA with `input_json` | Worth adopting | Built: `openrouter/decide_openai.go` |
| Oct 22, images in question instructions | Worth adopting | Parked in `project/backlog.md`, with the disagreement between surfaces |
| Sep 3, async tool calling and steering on Responses | Possibly | Named as open in `yEETo1` |
| Sep 2, `429 slow_down` and `503 server_is_overloaded` | Checked | `internal/transport` maps both by status, so the new codes change nothing |

## No action

| Changes | Why |
|---|---|
| Agents API, sandboxes, hosted computer use (Oct 16, Oct 9, Sep 29, Sep 10) | A hosted agent product, which is what goodall is the alternative to |
| GPT-Live, voices (Oct 16, Oct 15, Sep 10) | A realtime voice surface goodall does not speak |
| Ultrafast mode, 12-hour caching, cache diagnostics (Oct 20, Oct 8, Sep 29, Sep 8) | Responses-only, so they wait on v1.7 |
| GPT Image, Rosalind, `chat-latest` (Oct 30, Oct 7, Sep 8) | Not agent-loop models, or gated by a trusted-access program |
| Billing, tiers, HIPAA, key governance, IPv6 (Oct 28, Oct 21, Oct 6, Oct 5, Sep 15, Sep 10, Sep 1) | Account matters with no wire effect |
| Sep 25, image encoding fix | Server side |
| `-pro` and `:batch` variants on OpenRouter | Same wire as the models they vary |

## What the rehearsal says about the check

Most of the 69 lines were noise a reader would skim, which is why the judgment sits in a Skill. OpenRouter's `:batch` and `-pro` variants alone are 16 catalog lines, and they would come back at each new model. That is tolerable, so nothing is filed.
