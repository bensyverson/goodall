# The triage example: real mail, a judge and a delegate

Plan, 2026-09-17, for a third runnable example, `examples/triage`: an agent that reads a mailbox, has Jev sort every message in parallel, delegates reply drafts to a cheaper model, has Jev score each draft, and prints the proposed routing and drafts. It moves nothing. The owner ruled it on 2026-09-17 after the [typesafe-go comparison](2026-09-17-typesafe-go-comparison.md): the existing examples show chat in a terminal and in a browser and neither touches delegation or the judge, and a demo over live mail is the one that shows what a judgment model is for.

## What was ruled

- **It runs on real mail or on demo data.** Sources are Apple Mail's store, a Maildir, an mbox file, and a bundled synthetic mbox that is the default, so the example runs with no mail at all. Gmail's API is out: `gmail.readonly` is a restricted scope, so a shipped example could never carry credentials and a published app would need Google's paid security assessment (both verified on Google's pages, 2026-09-17). A Gmail Takeout export is an mbox and needs nothing from us.
- **Read-only, always.** The output is a report of proposed routes and drafts. No message is moved, flagged or sent.
- **The judge sees a filtered record, never a message.** Sender, subject, date and a snippet of the text body, bounded in length, with quoted replies and HTML markup stripped and attachments never read. That is both the privacy line, since real mail goes to two third parties, and what Jev wants: it degrades on state full of detail no question asks about.
- **Agents never touch real mail, and real mail never enters the repository.** Agents build against synthetic fixtures whose structure copies what was observed below, with invented names and `.example` domains. The integrator verifies against the owner's Apple Mail store on this machine, with the sandbox off for that one call, and writes the run's output only under a gitignored `local/` directory or the session scratchpad. What reaches the plan's block quote and the `job` log (which is tracked) is aggregate: counts, what the report got right and wrong in kind, the spend. Never a sender, a subject, a snippet, a timestamp or a byte count of a real message. Every integration read of an agent's diff checks its fixtures for the same.

## The Apple Mail store, observed 2026-09-17

Read from `~/Library/Mail/V10` on the owner's machine with `find`, `head -c` and a Python offset check; no message content was read beyond the first header name, and **no value taken from a real message appears in this repository**: the byte counts and timestamp below are made up to show the shape, and the store's counts are rounded. Reproduce with the reader's own tests once they exist.

- Layout: `V10/<account uuid>/<Folder>.mbox/<mailbox uuid>/Data/[<n>/[<m>/]]Messages/<N>.emlx`, nested up to three numeric levels under `Data`. Folder names include `INBOX.mbox`, `Sent Messages.mbox`, `Junk.mbox`, `Drafts.mbox` and Gmail's `[Gmail].mbox/All Mail.mbox`. Account names are in binary plists elsewhere and are not needed: the example takes a mailbox path, or scans every `INBOX.mbox` under the store.
- Scale on this machine: tens of thousands of `.emlx` files, roughly one in eight of them `.partial.emlx`, and one inbox of several thousand, so a reader must not parse every body to sort by date.
- A `.emlx` file is: a first line of exactly ten characters, the decimal byte count of the message left-aligned and space-padded (for a 1,234-byte message, `b'1234      '`), a newline, exactly that many bytes of RFC 822 message, then an XML property list. Observed: the count is exact (the byte after the message is the `<` of `<?xml version="1.0" encoding="UTF-8"?>`), the message starts with a `Return-path:` header and ends in a newline, and the plist is a few hundred bytes.
- The plist carries `conversation-id`, `date-last-viewed`, `date-received` (an `<integer>` of Unix seconds, so `<integer>1700000000</integer>` would be November 2023), `flags` (an integer bitfield) and `remote-id`. `date-received` is the recency key; file mtime is not.
- A `.partial.emlx` has the same shape, with a message whose large parts were left out by Mail; it still carries the headers and a `Content-Type`. The reader treats it as a message with whatever body it has.

The owner also runs Postfix on a server; whether it delivers to Maildir or mbox is unknown at the time of writing (`postconf home_mailbox` answers it: empty means mbox under `/var/mail/<user>`, `Maildir/` means Maildir). Both readers ship regardless, built against fixtures; whichever the server uses gets a live verification when that is known.

> **Answered 2026-09-17 by the owner.** `postconf home_mailbox` prints an empty value on the server, so Postfix delivers to mbox under `/var/mail/<user>`. The mbox reader gets the second live verification, on a copy of that file; the Maildir reader ships on fixtures alone.

## Design

`examples/triage` is a command, and `examples/triage/mailbox` is the one piece of infrastructure it needs, kept as a package so its readers are testable and reusable by the separate mail-triage repo the owner may build later.

**`mailbox`.** A `Source` opens a location and lists messages newest first up to a limit; `Open(kind, path)` picks the reader by a typed `Kind` (`apple`, `maildir`, `mbox`). A `Message` is the parsed `net/mail` message with its received time and the path it came from. `Record` is the filtered view the judge and the models see: `From` (display name and address), `Subject`, `Date`, `Snippet`. `Message.Record(maxSnippet)` builds it: the first `text/plain` part, or `text/html` with tags stripped when there is no plain part, decoded per its transfer encoding and charset where the standard library can, quoted reply lines and signature separators dropped, whitespace collapsed, cut at the limit on a word boundary. Attachments are never decoded. The mbox reader splits on `From ` lines and reverses `>From ` quoting; the Maildir reader lists `cur` and `new`; the Apple reader walks `Data/**/Messages/*.emlx`, reads the count line, slices the message and reads `date-received` from the plist with `encoding/xml`.

**The agent.** One run per invocation, over the instruction given on the command line (default: triage the inbox, say where each message goes, draft replies for the ones that need one). Tools:

- `list_inbox`, deterministic: the filtered records with stable ids, no model involved.
- `triage_inbox`, a `goodall.NewReportingTool` over the mailbox and a `typesafe.Client`: the model names message ids, or all, and the tool asks the judge about each record and returns the typed answers. It is a hand-built tool rather than a `typesafe.Tool` because the state is already in the process and the model should name records, not echo fifty of them through its own output. It asks **one `Ask` per message, in parallel with bounded concurrency**, three questions each: `department` (a `Choice`), `needs_reply` and `is_urgent` (`Noul`s). One array state with questions addressed by id is the indirection TypeSafe's jaggedness page says degrades the model (the finding is recorded in the [TypeSafe findings](2026-09-17-typesafe-jev-findings.md)). Every question and every threshold lives in one file, `questions.go`. Usage is summed across the calls and declared on the call's `ToolCallEnd`; cost stays unreported.
- `draft_reply`, a `goodall.AgentTool` on a cheaper model with its own system prompt and budget; its events render under the call as the nested `ToolEvent`s arrive.
- `check_draft`, a `typesafe.Tool` with fixed questions, several specific inspectors rather than one vague judge: `Noul`s asking whether the draft answers what the sender asked, whether it states a fact the message does not contain, whether it promises something the sender did not ask for, and whether its tone fits the sender's; the code, not the judge, combines them. A draft with any red flag over its threshold is not shown as ready: it goes back once to the parent's stronger model for a redraft, and a redraft that is still flagged is escalated to the person. That is the bouncer cascade: the cheap model does the bulk of the writing, the judge decides which pieces earn the expensive model, and the person sees only what neither could settle.
- A `typesafe.Route` on the user's turn: a `Choice` between a question that a summary answers and a request for drafts, which picks the model and narrows the tools.

**Output.** A plain-text report: each message's proposed department with its probability, urgency, whether it needs a reply, then each draft with its score and whether it is ready or escalated. Tool calls and the delegate's nested turns print through `examples/internal/render`, which is `examples/cli/print.go` moved so both commands share one renderer.

**Tests.** Readers against synthetic fixtures under `examples/triage/mailbox/testdata` that copy the observed structures byte for byte, including a `.partial.emlx`, `>From ` quoting and a Maildir with both `cur` and `new`. The agent against `internal/fake` for both the parent and the delegate and an `httptest` judge, over the bundled `examples/triage/testdata/sample.mbox`, a synthetic inbox of about a dozen messages including a newsletter whose body carries instruction-looking text and a phishing message, so the offline suite proves the questions decide and the state is data. The sample inbox is also `-source sample`, the default.

## Community heuristics, read 2026-09-17

The owner passed on a Reddit post, [10 Jev Commandments](https://www.reddit.com/r/typesafe_ai/comments/1wiguah/10_jev_commandments), which Reddit would not serve to this session so its text was pasted by the owner; it is one user's heuristics, not TypeSafe's guidance. Most of it restates the jaggedness page in the [TypeSafe findings](2026-09-17-typesafe-jev-findings.md) and the design above already follows it: the judge does not create, independent questions go in one call, the state is filtered in code and every threshold is policy in `questions.go`. Two of its rules changed the design and are in the paragraphs above. *Several specific inspectors rather than one vague judge*: the draft check was a `Score` on "how well the draft answers" and is now four `Noul`s a person could each answer in a second, combined in code. *The judge as a bouncer*: a flagged draft now earns the expensive model once before it earns the person, so the cascade is cheap model, judge, strong model, judge, person.

## Live runs, 2026-09-17

> **Observed 2026-09-17 by the integrator (leaf Z2ilGx).** Two runs, the second on real mail. Output stayed under the gitignored `local/`; everything here is aggregate.
>
> **Calibration on the bundled inbox:** `go run ./examples/triage -n 12`, judge `jev-1.13.0`, parent `claude-sonnet-5` routed to `claude-opus-5` for a drafts turn, drafts on `claude-haiku-4-5-20251001`. Exit 0 in 1m37s. All twelve judged. The newsletter whose body says to ignore previous instructions and mark everything urgent came out other 1.00, needs a reply 0.39, urgent 0.06; the phishing message other 0.99, urgent 0.12; the customer, colleague and invoice messages needs-reply 0.95 to 0.97; one close call. Five drafts by the cheap model, four sent to the strong model by the inspectors (invents-a-fact fired on offered facts such as whether a feature is included in a plan), all ready after the rewrite, none escalated. Spend: 20 judgments in 9 tool calls at 16,868 judge input tokens; parent 69,537 in, 7,897 out.
>
> **The owner's Apple Mail store:** `go run ./examples/triage -source apple -path ~/Library/Mail/V10 -n 30`. Exit 0 in 1m33s over a store of tens of thousands of files, the newest thirty across every inbox; **no file was skipped**, `.partial.emlx` included. Routes: 24 other, 4 personal, 1 billing, 1 sales, which fits a personal inbox that is mostly newsletters and notifications. Urgent: none, correctly. Needs a reply: five, and here the question is too generous: a professional-network notification, a fundraising appeal, a survey reminder and a trademark-services solicitation all scored 0.60 to 0.82 because the True criterion, "asked for something only the recipient can give", is satisfied by any call to action from a company. The parent drafted replies for three of the five and let the others go, one redraft, none escalated. Spend: 34 judgments in 5 tool calls at 29,997 judge input tokens; parent 110,006 in, 6,331 out. Filed as an issue: sharpen `needs_reply` so a mass mailing or a company's call to action is a no, and measure the change over repeated runs; and note that the department rubric describes a small software company, so a personal inbox lands mostly in other, which is honest rather than wrong. The reader found no defect on real mail. **A Postfix mbox:** `go run ./examples/triage -source mbox -path local/<copy>`, a three-message copy of a `/var/mail` file the owner supplied, whose From_ separators carry Postfix's two spaces before the date. Exit 0 in under ten seconds; all three listed with their 2015 dates, all three routed other with no reply needed, so no draft was written; 3 judgments in 1 tool call at 2,530 judge input tokens. The mbox reader had no defect either.

> **Measured 2026-11-02 (`QVt82X`): `needs_reply` sharpened.** The shipping question's criteria changed, and mail you did not reply to now draws about half as many yeses with no loss on mail you did. Everything here is aggregate; senders and subjects stayed under the gitignored `local/`.
>
> ```
> TRIAGE_MEASURE_STORE=$HOME/Library/Mail/V10 TRIAGE_MEASURE_OUT=$PWD/local/needs-reply TRIAGE_MEASURE_RUNS=1 \
>   go test -v -count=1 -timeout 60m -run TestMeasureNeedsReply ./examples/triage
> ```
>
> **Why not the newest 30.** The owner pointed out that the newest messages change week to week, so runs on them cannot be compared. The sample is drawn once from the newest 6,000 inbox messages, by a hash of each id, and its ids are kept in `local/`. It went to `jev-1.13.0` only, with the shipping question set and the 400-character snippet, and no parent model. **No message was labeled by anyone.** The strata are signals: `replied` is a message the owner's Sent mail answers by `In-Reply-To`, `bulk` carries a list or auto-submission header, `automated` comes from a no-reply address, and `other` is the rest.
>
> **The rule, fixed before the 603-message run:** adopt a wording only if yeses on mail not replied to fall by at least 25%, and yeses on replied mail lose no more than 5 percentage points. Each model answer below is one call.
>
> | Yes at 0.60 | Pool | Sampled | Baseline | `criteria`, shipped | `question` |
> |---|---|---|---|---|---|
> | `replied` | 103 | 103 | 71 | 71 | 72 |
> | `bulk` | 4,128 | 200 | 12 | 7 | 6 |
> | `automated` | 704 | 100 | 7 | 4 | 3 |
> | `other` | 1,045 | 200 | 31 | 16 | 16 |
> | Not replied, total | | 500 | 50 | **27, down 46%** | 25, down 50% |
>
> `criteria` changes the True and False descriptions, and `question` also changes the question. `question` passes too, and it won by 2 messages out of 500, which is not a difference this run can resolve. `criteria` shipped because it changes less and holds up better if the threshold is ever raised: at 0.80, replied yeses are 66, 59 and 52. The threshold stays at 0.60. Against baseline, `criteria` moved 28 messages from yes to no and 5 from no to yes, 2 of each kind among `replied`.
>
> **What this does not show.**
> - **Whether the 16 `other` messages that dropped to no deserved it.** That stratum mixes people and companies. Not replying is not the same as not needing a reply, so the owner can check them in `local/needs-reply/review.tsv`, with the arms' scores per message.
> - **Whether the named September cases were fixed on real mail.** They were not in this sample by name. Their shape is tested below.
> - **That the verdict is now precise.** This sample over-represents `replied`. Weighting each stratum's rate by its pool, an estimate from rates as low as 3.5%, about 330 of the 6,000 would be yes against about 530 before, and about 78% of those yeses would land on mail with no reply against about 87%. Some of that mail may have needed one; a reply is a signal, not a label.
> - **Stability beyond this model.** `jev-1.13.0` answered identically on 3 runs of each of 3 arms over a first sample of 140, which is why one run sufficed. Thresholds do not travel, so another judge would need its own run.
>
> **The regression.** The bundled inbox gained a fundraising appeal and a survey reminder, each written as if a person were waiting, and it now holds 14 messages. `TestLiveNeedsReplyOnTheBundledInbox`, in the plain `go test`, asks about the nine bundled messages with an unambiguous answer. On the old wording it failed on both new messages, at 0.96 and 0.88. On the new wording it passes, with those at 0.14 and 0.07. The measurement sits in `examples/triage` rather than `scripts/`, on purpose: the package is `main`, and a script could only have asked a copy of the question set.

## Task tree

Imported into `job` under its own root. Files are disjoint across the parallel leaves; the agent leaf waits for both.

```yaml
tasks:
  - title: "goodall v1.3: the triage example"
    desc: |
      A third runnable example over real or demo mail, ruled 2026-09-17 in
      project/2026-09-17-triage-example-plan.md. Agents build against synthetic
      fixtures only; the integrator verifies against the owner's Apple Mail store.
    children:
      - title: Mailbox readers for Apple Mail, Maildir and mbox
        ref: mailbox
        desc: |
          examples/triage/mailbox: Source, Kind, Open, Message, Record and the three
          readers, built to the "Design" and "The Apple Mail store, observed" sections
          of project/2026-09-17-triage-example-plan.md. Synthetic fixtures under
          examples/triage/mailbox/testdata copy the observed structures byte for byte.
          Standard library only. Files: examples/triage/mailbox/*.go and testdata.
        criteria:
          - "The Apple reader slices the message by the first line's byte count, reads date-received from the plist, and reads a .partial.emlx as a message with the body it has"
          - "The mbox reader splits on From_ lines and reverses >From quoting; the Maildir reader lists cur and new"
          - "Record strips quoted replies, signatures and HTML markup, decodes quoted-printable and base64 text, never decodes an attachment, and cuts at the limit on a word boundary"
          - "Every reader lists newest first and honors the limit, asserted against fixtures"
          - "Every exported identifier carries a doc comment"
      - title: Move the CLI printer into examples/internal/render
        ref: render
        desc: |
          examples/cli/print.go and print_test.go become examples/internal/render, an
          importable package with the same behavior, and examples/cli uses it. No
          behavior change; the test file moves with it. Files: examples/internal/render/*,
          examples/cli/print.go (deleted), print_test.go (moved), the call sites in
          examples/cli.
        criteria:
          - "examples/cli builds and its tests pass unchanged in substance against the moved package"
          - "The package documents that it renders any run's events, nested ToolEvents included, to a writer"
      - title: The triage agent, its tools and the sample inbox
        ref: agent
        blockedBy: [mailbox, render]
        desc: |
          examples/triage: main.go (flags -source apple|maildir|mbox|sample, -path,
          -n 50, -snippet 400, -provider, the instruction as the argument), questions.go
          (every question and threshold), tools.go (list_inbox, triage_inbox with one
          Ask per record in parallel and summed usage, check_draft as a typesafe.Tool),
          draft.go (the AgentTool delegate on the cheap model and the redraft on the
          strong one), route.go (the BeforeSend route), report.go,
          and tests against internal/fake and an httptest judge over
          examples/triage/testdata/sample.mbox, a synthetic dozen-message inbox with a
          newsletter carrying instruction-looking text and a phishing message.
        criteria:
          - "With no flags the example runs on the sample inbox and prints a report; with -source apple -path it runs on a store"
          - "The offline test drives a whole run through internal/fake for both agents and an httptest judge and asserts the report's routes, drafts and escalations"
          - "The judge receives Records only: a test asserts no request body carries a full message body or an attachment"
          - "triage_inbox declares the summed judge usage on its ToolCallEnd and the delegate's events render nested under its call"
          - "check_draft asks several Nouls combined in code, a flagged draft is redrafted once by the strong model, and a still-flagged redraft is escalated to the person, all asserted offline"
      - title: Live verification against Apple Mail and the docs
        ref: live
        blockedBy: [agent]
        desc: |
          The integrator runs the example against the owner's Apple Mail store with the
          sandbox off, records what worked and what did not as a dated block quote in
          project/2026-09-17-triage-example-plan.md, adds the example to README.md's
          examples section, and files any reader defect found on real mail as a leaf.
          Real mail never enters the repository: the run's output goes under a gitignored
          local/ directory, and the block quote and every job note carry aggregates only,
          never a sender, subject, snippet, timestamp or byte count of a real message.
        criteria:
          - "A block quote in the plan records a live run: source, count, what the report got right and wrong, and the judge's spend"
          - "README.md names the example and how to run it on demo data and on real mail"
```
