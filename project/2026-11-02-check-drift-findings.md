# Findings, 2026-11-02: what scripts/check-drift sees

`go test` replays against pinned models and recordings, so it cannot see a vendor move ([why this exists](2026-11-02-model-currency-findings.md#why-this-went-unnoticed)). `scripts/check-drift` looks outward, and one run costs under a cent of reported money plus 21 unpriced calls. This records what was built, the runs that prove it and what it cannot see.

## What it compares

| Check | Against | Reported |
|---|---|---|
| Catalogs: Anthropic, OpenAI, OpenRouter (filtered to `anthropic/`, `google/`, `openai/`, `typesafe/`), OpenRouter's decision models, TypeSafe | the baseline and `livemodel.Pins` | a retired pin, a new family, a new version, a departure |
| Changelogs: five vendor pages, plus TypeSafe's models page since it publishes no changelog | section digests in the baseline | a new, changed or removed section, by heading, with the vendor's words clipped |
| Live: the agent loop on 15 model and effort rows, every concrete decision model, and the native decision wires | pass or fail in the baseline | any row whose outcome changed, new or gone, including a recovery |
| Shapes: the member paths of those same live responses | every recorded fixture, plus paths accepted into the baseline | a new path, saying whether any wire struct has the name |

Exit bits: 1 drift, 2 not checked, 4 could not start. `go run` collapses them to 1, so the `RESULT` line says the same thing in words.

## The decisions the leaf left open

- **Snapshots live in one file, `scripts/check-drift/baseline.json`, as digests rather than page text.** It keeps vendor documentation out of an MIT repository and keeps 880 sections to about 1,350 lines. Each section is one line, `digest heading`, so accepting a baseline shows in a commit as the headings that moved. The price is that the old words are not in the repo, so a changed section shows only its new words.
- **A baseline is accepted with `-accept`, by a person, in a commit.** What a run could not read stays as it was, so an outage cannot forget a source. A retired pin keeps reporting after an accept, because only repinning repairs it.
- **Changelogs compare by section rather than by whole page or by date.** Vendors do not share a date format, and OpenRouter's page has no dated headings at all, so headings are the only thing common to all of them. Whitespace is normalized, so a reflow is not an edit.
- **Modeled means the name appears in a `json` tag in the wire's packages,** read from source because the wire structs are unexported. It is a name check, so a known name at a new path reads as "not in any recording" rather than as unmodeled.
- **Local servers are left out,** and the report says so. See [the backlog](backlog.md).

## The runs

All on 2026-11-02, built with `go build -o <bin> ./scripts/check-drift`.

**No drift when nothing changed.** `<bin> -env .env` against the accepted baseline exited 0:

```
Compared: 5 catalogs (361 models), 6 changelog pages (880 sections), 75 live rows, 4 wires (174 member paths).
COST: 75 live calls; USD 0.008490843 reported across 54; 21 with no reported price (6950 input and 620 output tokens)
RESULT: no drift
```

The 21 unpriced calls are 16 to Anthropic, OpenAI and TypeSafe, which report no money, and 5 decision calls OpenRouter refused. Their tokens are stated rather than priced, since a price typed in here would go stale. The whole run took about 20 seconds.

**It would have flagged the GPT-6 Responses change.** `python3 scripts/check-drift/rewind.py scripts/check-drift/baseline.json > before.json`, then `<bin> -env .env -baseline before.json`, exited 1 with 69 changes, among them:

```
gpt-6-astra: new family in the openai catalog
openai: Changelog > September, 2026 > Sep 3: new entry
  | Feature · Model: gpt-6-astra · API: v1/responses · API: v1/chat/completions Released [GPT-6 Astra]…
loop/openai/gpt-6-astra/low: new row, fail
  | FAIL turns=0 tool_calls=0 cause=error kind=invalid_request: openrouter: invalid_request (400): Function tools with reasoning_effort are not supported for gpt-6-astra in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'.
```

The rewind is an approximation. The state before GPT-6 was never captured, so it removes what GPT-6 added: the `gpt-6` ids, the OpenAI changelog sections from September and October, and the `gpt-6` live rows. It also drops `loop/openai/gpt-5.6-terra/low`, which the same refusal covers and whose start date is not established. It shows the check reports this change, not that it would have fired on the first day.

**The shape check finds real members.** Every fresh path was already in a recording, so a clean run alone would not show it can fire. In a copy of the tree with all but `live_error` removed from `anthropic/testdata`, a live run reported 46 Anthropic lines, among them `delta.container`, `delta.stop_details` and `content_block.caller` as members no wire struct models. Authored cases in `compare_test.go` cover the same path.

**Guards proved by mutation:** dropping the tool-call condition from `liveprobe.Result.Passed`, comparing a live row's detail, ignoring skipped rows, accepting an empty catalog and reading HTML as a page each fail named tests. Removing a `pin.Catalog == ""` guard failed nothing, because an unread catalog already covers it, so the guard was deleted.

## What it cannot see

- **A live pass does not show reasoning happened.** Every row reports `reasoning=0`, as [the earlier probe found](2026-11-02-model-currency-findings.md). A pass shows the loop completes with thinking requested.
- **A member that stops arriving is not reported,** since most members are optional. A rename shows up as its new name.
- **Recorded members no wire struct models are not listed,** because they are not new. Anthropic's `stop_details` and `container` are in the recordings and unread today. Whether goodall should read them is a separate question.
- **The money limit can overshoot** by the calls in flight, at most six, since a cost is known only when a call ends. Calls with no reported price count against `-max-calls` alone.
- **A pass or fail can flap** on a vendor's bad minute. The check reports it, and the judgment belongs to the next leaf, the `/drift-check` Skill.
- **`loopTargets` is a hand-kept list.** A new family appears in the catalogs, and a person decides whether it earns a row.

## Also changed

`scripts/probe-agent-loop` and this tool now share `internal/liveprobe`. A run that finishes without calling the tool used to print `ok`, and now prints `FAIL`, because it says nothing about the second turn the probe exists to test. `livemodel.Pins` lists every model constant, and a test holds the list to them.

## How it runs

**Ruled by the owner on 2026-11-02: locally, weekly.** A launchd job runs the `/drift-check` Skill on Mondays at 09:00 from this checkout, using the `.env` already here, so the keys gain no second home. A cloud routine would have needed all four keys and a way to push its results back. launchd runs a job missed during sleep on wake, but a Mac that stays off misses it.

```
ln -s /Users/ben/git/goodall/scripts/drift-check/com.bensyverson.goodall.drift-check.plist ~/Library/LaunchAgents/
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.bensyverson.goodall.drift-check.plist
launchctl kickstart gui/$(id -u)/com.bensyverson.goodall.drift-check    # one run now, to test it
```

The log is `local/drift-check.log`. The Skill reports and files, and leaves its report uncommitted for the owner, who also runs `-accept`. Until then, the same findings come back each week.

**Installed and tested on 2026-11-02.** The owner ran the commands above, and the `kickstart` run under launchd finished with `last exit code = 0` (`launchctl print gui/$(id -u)/com.bensyverson.goodall.drift-check`): no drift, nothing unchecked, nothing filed, USD 0.008466183 reported across 54 of 75 calls. That run had nothing to judge, so the Skill's judgment has only been exercised by [the rehearsal](2026-11-02-drift-report-rehearsal.md). It has not yet fired on the calendar.

> **Added 2026-11-02.** A live row refused with `rate_limited`, `overloaded` or `server` is now reported as `NOT CHECKED` with the vendor's words, and the baseline keeps what it had. `decide/openrouter/nace-ai/drex-max` raised false drift on 2 of 5 runs that day with a 429, "Model busy, retry later". The cost: a row that stays refused never reads as drift, only as a hole in every report.
