# Handoff, 2026-11-02: decision wires, two leaves left before the proof

The main thread reached its context line after integrating the first two leaves of `goodall v1.5` (`hAG3tq`). This replaces [the earlier handoff](2026-11-02-handoff.md) as the starting point. No agent is running and no worktree is left.

## Done

| Commit | Leaf | What |
|---|---|---|
| `7de0327` | `4UKauU` | Neutral `goodall/decision`, `internal/systemone`, `typesafe` reduced to a client |
| `f93e8fc` | `FWyF1w`, `1JOSCO` | `openrouter.Client` implements `Decider`, with reported cost and `DecisionModels`; a null error code is classified from its status |

Neither commit is pushed unless the log says otherwise (`git log @{u}..`).

## Next

The roster stays one agent in order, since the leaves share `openrouter/`. A new agent needs a written summary; this doc and the leaf notes are it.

1. **`Tfb9SH`, the OpenAI wire.** Needs nothing from the owner. Set `DecisionModelRequired` true on OpenAI and replace `DecisionWireRefused` with its own wire. `DecisionWire` is the seam `FWyF1w` left for it. Its state and predicate-criteria rules are the builder's call with evidence.
2. **`MxIOGV`, local servers.** Ask the owner to choose and load an LM Studio decision model when this leaf is reached. The live run is the integrator's, because agents' sandboxes refuse local ports. `systemone.EncodeRequest` still sends `"model":""`, and the leaf's criterion wants it omitted on LMStudio and Generic.
3. **`VSwcFi`, the proof and docs.** Blocked on both.

`goodall v1.6` (`S1lGYS`) and `goodall v1.7` (`Uz2EPX`) stay blocked on `VSwcFi`, and their relative order is unruled.

## Waiting on the owner

- **An account id is in public git history.** `openrouter/testdata/live_error.json` carried OpenRouter's `user_id` from `968b9b6`, on `origin/main` in a public repo. The current file is redacted and the recorder now redacts, but only rewriting history removes the old value. It is an account identifier and not a key. Rewriting published history is the owner's call and has not been done.

> **Resolved 2026-11-02.** The owner ruled to leave history as it is. See [the backlog](backlog.md).

## Worth knowing

- **Triage has one run after the move.** It sorted as calibrated but differed in close calls and escalations. Run-to-run spread was not measured, so no cause is established (the calibration is in [the triage plan](2026-09-17-triage-example-plan.md), and the run is in the notes on `4UKauU`).
- **Not run live since the move:** `scripts/probe-decisions`, `scripts/probe-option-order` and `TestLiveMixedJudgmentRoundTrip`. They compile. `VSwcFi` moves and re-sweeps the first.
- **Subagents ran on Opus, the same tier as this main thread,** against the harness note's advice, because the lineup left only Sonnet below it and these leaves carry judgment calls.
