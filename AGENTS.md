IMPORTANT: As you implement features, keep [README.md](README.md) and the dated documents in [project/](project/) current. Every exported Go identifier carries a doc comment. Update README.md only when a doc file is added or new users must know.

## Overview

You are working on `goodall`, a reusable, extensible core Go library for agentic prototyping: an agentic loop (an LLM conversation with user-definable tools) that works for any "give an LLM tools" task, chat-shaped or not, plus a set of *optional* components that make chat-oriented agents fast to build. It serves two consumers equally — someone spinning up a chat prototype (a web chat, the Go back end of a mobile app) and someone building a highly customised agent who wants the boilerplate handled — so simple things must be easy and complex things possible. The core is provider-flexible, not provider-agnostic: Anthropic and OpenRouter at minimum, each with full support for streaming, vision and other media, thinking blocks and tool schemas. Streaming is the default with a blocking path available. Its Swift predecessors are the sibling repos `../LLM/` (multi-provider API wrapper) and `../Operator/` (the agentic portion); whether goodall is one library or two like them is an open architecture question, and architecture is the first thing to resolve.

Minimal — ideally zero — dependencies, brought in only with a strong case. Modern Go 1.27 idioms, adopting newer language features where they make code clearer, without going wild on abstraction: the codebase should stay small and tractable. The JSON wrangling and cross-provider differences are covered by a decent number of high-quality unit tests. Module path `github.com/bensyverson/goodall` (decided 2026-09-14): the root package `goodall` is the core loop, and providers and optional layers are subpackages (`goodall/anthropic`, `goodall/openrouter`, `goodall/chat`) so the zero-dependency core stays importable on its own. The licence (MIT) and the package layout were decided 2026-09-14 in the architecture plan.

## Documentation

- [README.md](README.md) — not yet written; it will cover what goodall is, install, and the quick start for each of the two consumers
- [project/](project/) — dated design documents, findings and plans, the written history of the project; start with [project/2026-09-14-initial-vision.md](project/2026-09-14-initial-vision.md), **the brief**: use cases, the required product features (providers, streaming, media, thinking, tools, stopping, cache control, Markdown, cost tracking, redaction, hooks), prior art and constraints. Then [project/2026-09-14-research-findings.md](project/2026-09-14-research-findings.md), **the research**: verified facts about the Anthropic and OpenRouter APIs, Go 1.27, Go prior art and the Swift predecessors, the design invariants they imply, and the proposed approach with its open decisions. Then [project/2026-09-14-architecture-plan.md](project/2026-09-14-architecture-plan.md), **the plan**: the decisions and their reasons, the binding invariants, the design of each package, testing, and the task tree that was imported into `job`.
- [project/backlog.md](project/backlog.md) — work decided against and what would un-park it; read it before proposing something that sounds novel
- [project/gotchas.md](project/gotchas.md) — project traps and rule feedback; read it at session start
- [project/agents/harness.md](project/agents/harness.md) — facts about the agent harness (sandbox, `$TMPDIR`, worktrees)
- [project/agents/delegation.md](project/agents/delegation.md) — how to carve, brief and integrate subagent work
- [project/agents/evidence.md](project/agents/evidence.md) — how to treat numbers, claims and causes; read it before measuring or reporting a figure
- [project/agents/jobs.md](project/agents/jobs.md) — the `job` tracker: tree shape, criteria, agent identities

## Build and test

- `go build ./...` builds everything. **`go test -short ./...` is the offline suite** and must pass with no `.env`. A plain `go test ./...` also runs the live-provider tests when `.env` (gitignored, shell-form `export KEY=value`) holds a key, and every pass spends real tokens; they skip loudly, naming the key and the file, when it does not. `GOODALL_ENV_FILE=/abs/path/.env` points them at another file (a worktree has no `.env`). `go.mod` pins `go 1.27.0`.
- `go run ./scripts/record-fixtures -env .env -out anthropic/testdata` refreshes a provider's recorded fixtures (`live_*`) from the live API; `-list` names the exchanges, `-only <name>` records one.
- Before every commit: `go fix ./...`, `gofmt -l -w $(git ls-files '*.go')` (never `gofmt -w .`: it recurses into `.claude/worktrees/`), `go vet ./...`, `go mod tidy`, then `go test -short -race ./...`. **No pre-commit hook yet**; run them yourself.
- `git` refuses inside the Bash sandbox (`~/.gitconfig` is unreadable there); re-run git commands with the sandbox disabled, one call at a time — see `project/agents/harness.md`.

<!-- agents:begin core@3a7a5e -->
## Working rules

**Understand the why.** If the goal behind a request isn't clear, ask before solving — beware the XY problem.

**Diverge, then converge.** First brainstorm options (create choices), weigh them against the user's goals, recommend one (make choices), confirm, then execute.

**Ambiguity.** If the *code* could go several ways, choose the idiomatic one for the language. If the *requirement* is ambiguous or the question is architectural, stop and ask — don't decide.

**Dependencies.** Avoid them unless re-implementing would be unreasonable; ask before adding one; each is security and maintenance surface.

**TDD, strictly red/green.** Write tests for every case and every new method first, watch them *all* fail, then implement. A test that is green during red tests nothing — remove or rewrite it. If an existing test must change to pass because the behavior or expectation has changed, explain why clearly. Every bug fix starts with a regression test.

**Plans and tasks live in `job`.** Open every session with `job orient` (no arguments), then read `project/gotchas.md` — while reading, prune it: delete any entry that's now fixed, obvious, or a general rule, marking it `rule:` first if it's general. Don't use Plan Mode or ad-hoc todo lists.

**Don't tour the codebase.** Start from the README and the docs (an Explore agent is fine); dig only where the task leads — once you have a specific need, read as much as that need requires.

**Scripts.** Analysis tooling goes in `scripts/` so it can be re-run — check there before writing one.

**Critique before declaring done.** Re-read the original request: is the need actually met? Do lint and tests pass? Are docs updated? What would an expert flag? Fix serious flaws before reporting.

**Tidiness.** No stray files in the repo root; delete transients, and file valuable artifacts (reports, scripts) where they belong.

**Documentation.** Keep the project docs current as you build. Touch the README only when a doc file is added or new users must know.

**Gotchas.** When a project quirk costs you time and no rule predicts it, append it to `project/gotchas.md`. If a rule in this file was wrong or misled you, record that there too, prefixed `rule:`.

**Where these rules come from.** The marked regions are generated and shared across repos via a CLI tool named `agents`; don't edit inside them. If a rule here is wrong or cost you time, say so in `project/gotchas.md` prefixed `rule:`; that is how shared rules get reviewed.

**Local rulings.** A repo-local ruling, or an override of a shared rule, lives in the project-owned head of `AGENTS.md`, above the generated regions — say plainly that it overrides, and link a dated project doc for the why.

## Git

- Offer to commit when a unit of work is complete and accepted. Rebase onto upstream; ask on real conflicts, explaining the conflict in plain terms first.
- Commit all uncommitted files together — later changes usually depend on earlier ones, and a half-working state helps nobody. Never amend.
- The subject completes "This commit…": present-tense verb first — "Adds…", "Fixes…", "Retires…". Detail goes in the body.
- Pass the message with `-F <file>`, not inline `-m`; the shell interprets `-m` first. Same for `job`: `note`, `done`, `add` and `edit` all take `-F <file>` (`-F -` reads stdin).
- Pre-commit hooks run the formatter and tests. Run them yourself first (see the stack rules).
- Never pipe a gating command (`git commit … | tail`) — the pipe swallows its exit status, so a following `&&` runs even after a failure.
<!-- agents:end core -->

<!-- agents:begin principles@7a5b19 -->
## Principles

Defaults, not laws. When we break one, we do it consciously and say so in the report and the docs.

- **Pragmatism.** Builders, not purists. Practical choices that serve the near-term goal and protect the long-term one.
- **Eat the frog.** No band-aids. Given an easy-but-compromised path and a correct one, take the correct one; fix problems at the source. Keep YAGNI in mind, but when a need is obvious, don't underdeliver.
- **Composability.** Simple, strong components composed into systems — never a monolith.
- **Library + thin executable.** Core logic in a library; the app or CLI is a light consumer, so the core can be reused elsewhere. An adapter that holds a decision rather than wiring one is a bug.
- **Decoupling.** Tight coupling makes testing, debugging and refactoring hard — separate concerns. Separating a model, its storage and its UI is the everyday case: databases and UI frameworks change; today's web app may grow a CLI or mobile app.
- **Just enough abstraction.** One layer around an LLM provider is prudent; a `TextGenerationProvider` above it is not.
- **Readable file sizes.** Aim for files a reader can hold in their head (a few hundred lines; ~400 is the comfortable ceiling). Past ~2k lines, navigation degrades and errors accumulate; splitting also makes functionality discoverable by filename.
- **Comments say why, not what.** Doc comments state *what* concisely; other comments only explain the non-obvious. No change history in comments. Most code needs none.
- **Strongly typed.** Prefer enums, named constants and config over magic strings and numbers; prefer typed structs over dictionaries, even for wire types. Two packages exchanging data across a serialization seam share **one** struct that both import, never a hand-written twin on each side — the type checker cannot see across encode/decode, and two definitions drift. Given a bool and a typed constant, take the typed constant: a bool named for one consequence gets reused to gate the others until it means several things, so name the underlying *fact* as a type and let the behaviors follow.
- **Previews.** Give each UI component a way to render in its various states — a SwiftUI `#Preview`, a demo page, a story — the foundation for tests and for human review.
- **Async by default.** Keep the app interactive during heavy work; surface loading and error states. On the web, prefer progressive enhancement over full reloads.
- **Event streams where they fit.** Append-only logs are auditable, undoable, and time-travelable.
<!-- agents:end principles -->

<!-- agents:begin stage-build@3d5d83 -->
## Stage: BUILD

Pre-launch, zero users, no existing data. Never spend effort on backward compatibility — assume every use is green-field — but flag breaking changes and update the affected tests. Be ambitious: if a feature is important, build it fully now rather than an MVP; balance that against over-engineering and future-proofing.
<!-- agents:end stage-build -->

<!-- agents:begin go@91ab6a -->
## Go

- Before committing: `go fix ./...`, `gofmt -w .`, `go vet ./...` and `go mod tidy`, then the tests you touched. `go fix` converges over several passes — "re-run to apply more" is progress, not failure; re-run until clean before editing code.
- **Run `go fix ./...` before staging, not just before committing.** A pre-commit hook that re-stages `gofmt` rewrites will not re-stage `go fix` rewrites: a file `go fix` changed that is already gofmt-clean commits unfixed, and your working tree quietly diverges from what you committed.
- **Tests that share a database need `-p 1` and a database per agent.** `go test ./...` runs packages in parallel, so packages that seed the same fixtures and truncate the same tables produce a wall of unrelated-looking failures that survives a re-run and reads as a real regression.
- **Schema changes are numbered migrations** in the project's migrations directory (the head names it). Never run one by hand — the binary migrates when it starts or opens the database and records the version; the next run applies it. Read the full note history on the task (`job show <id>`) before writing schema; it is the most expensive thing to change.
- **On SQLite:** **`CHECK` passes on NULL.** `CHECK (a = b)` admits any row where either side is NULL — guard every comparison with `IS NOT NULL`, or it enforces nothing.
- **On SQLite:** **NULLs are distinct in a `UNIQUE` index.** A nullable column in a dedup key admits duplicates forever; wrap it in `COALESCE(col, '')` in the index expression.
- **On SQLite:** **Never hold a transaction open across a model or network call.** `BeginTx` is deferred — it pins a read snapshot at the first read, so the write at the end fails with `SQLITE_BUSY_SNAPSHOT` if any other connection committed meanwhile, and `busy_timeout` cannot rescue it because waiting cannot refresh a stale snapshot. Split into a step that reads and calls but writes nothing, and a short transaction that persists the result.
- Wire types are structs, not `map[string]any`, unless the shape is genuinely dynamic.
- **`r.ParseForm()` reads a body only when it is urlencoded**; for multipart it leaves it empty without erroring. Keep one wire format per route — a handler that accepts two body shapes needs two sets of checks where the design wanted one.
<!-- agents:end go -->

<!-- agents:begin docs@7ba2fd -->
## Documentation practice

- **Plans, findings, designs and decisions go in `project/` as dated documents** (`YYYY-MM-DD-title.md`) — the written history of the project. They are point-in-time records: correct an earlier one *in place*, as a marked block quote, rather than silently editing a number or leaving a stale claim standing.
- **Every figure names the tool and flags that reproduce it.**
- **Work decided against goes in `project/backlog.md`**, not into silence: one dated H2 per item — what it is, why it's parked, and *what would un-park it*. Nothing there is scheduled or blocking; active work lives in `job`. Check it before proposing something that sounds novel.
- **When a finding overturns a premise, edit the premise.** Readers act on the title and opening; a correction appended underneath doesn't reach them.
- **A wrong documented cause is worse than none** — it stops the next reader looking. Correcting one means saying it was wrong, not quietly rewording.
- **Open the note before you cite it**, and check whether a recorded ruling has been superseded before passing it on.
- **Every repo has a README; if none exists, write one (delegate it if you can).** It is tight: the project's name and a one-line description a non-technical reader understands (6th-grade reading level); one short paragraph of what it is; how to install or consume it; a crisp Quick Start with an example or two; links out to the specific docs for anything more; authorship and license at the end.
- **The head of `AGENTS.md` lists where the docs live; keep that list current.**
<!-- agents:end docs -->

<!-- agents:begin prompting@5b4283 -->
## Prompting

For the prompts a model reads at runtime — system prompts, generation templates, judge prompts. They are product surface, engineered like the rest of it.

- **Positive direction, not prohibitions.** Tell the model what to do; a "don't say X" list spends attention on X and plants the shape you were avoiding. Where the ban is the point, state the behavior you want and let the ban be its consequence.
- **Give the reason, not just the instruction.** A rule carrying its *why* survives the cases it doesn't name, while a bare directive is followed literally and no further. Ask the same of the output — not "prefers hybrid" but "tired of working alone, wants a team".
- **Fewer rules; each competes for attention with all the others.** Current models need very little: the goal, the why, and what fills the field. The mechanism is priority conflict, so an added rule can degrade an output it never mentions — and adding one while shortening the prompt makes the result unattributable.
- **Put a load-bearing instruction where the model is looking when it acts.** An obligation about a field belongs in that field's schema description, not several thousand tokens away in the system prompt; a rule the model cannot see at the moment of the decision enforces nothing.
- **Illustrate the shape, not the value.** Adjectives ("professional", "concise") are unfalsifiable and an example of the form is worth more — but abstract it. Concrete sample names and figures get planted and come back verbatim in real output.
- **Separate what the model must know from what it must do, and keep the knowing stable.** Standing instructions and reference material are one constant block; per-call data goes after it. Interpolating a date, an id or a name into the standing block breaks the cacheable prefix and makes two runs incomparable.
- **Untrusted text is data, never instructions.** A transcript, resume, page or user message placed in the system block is an instruction nobody wrote. Keep it in the data position, labelled as material to work from.
- **The model's output is untrusted input.** Parse and validate before anything downstream touches it, and keep a malformed result distinguishable from a truncated one and from a refusal. Output rendered straight into HTML, SQL or a filed record is the same bug as trusting a form field.
- **Prompts are versioned data and every output names its version.** Mint versions rather than typing them, keep published ones immutable, and stamp each generated artifact with the version that produced it — otherwise a bad output cannot be joined back to the text that caused it.
- **Type the template's inputs and fail loudly when they are missing.** A prompt is a function over data with a shape; discovering placeholders by regex misses a second valid syntax, so coverage reads perfect while the model is handed nothing, and a swallowed render error ships the fallback and records success.
- **Test prompts through the path production uses, on real data.** An eval that builds its request differently from the shipping code measures a parallel system, and a fixture asserting example text passes with no data at all. Assert on behavior, never on prompt wording — the wording is the author's to change.
- **A judge prompt is a spec, so write the criteria first.** Decide what counts as right, what tolerance each class of miss earns, and how results will be cross-tabulated, then write the prompt to that. A judge reporting one number cannot tell a fix from a relabelling.
- **Measure a prompt change over repeated runs at one pinned commit.** Run-to-run spread on identical input is wide enough to swallow a real effect, arms run at different commits are not comparable, and prompts compiled into the binary serve the build the process started with.
<!-- agents:end prompting -->

<!-- agents:begin index@b19dd5 -->
## Situational instructions

These files carry instructions for specific situations. When one applies, read the file before acting and follow it.

| Situation | File |
|---|---|
| Before dispatching any subagent | `project/agents/delegation.md` |
| Before measuring anything, verifying a claim, or writing down a cause | `project/agents/evidence.md` |
| Before filing or claiming work in job, and when running as a subagent | `project/agents/jobs.md` |
| The first time a tool call is refused or denied, and before briefing a subagent | `project/agents/harness.md` |
<!-- agents:end index -->
