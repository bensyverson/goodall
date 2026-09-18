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

## Design

`examples/triage` is a command, and `examples/triage/mailbox` is the one piece of infrastructure it needs, kept as a package so its readers are testable and reusable by the separate mail-triage repo the owner may build later.

**`mailbox`.** A `Source` opens a location and lists messages newest first up to a limit; `Open(kind, path)` picks the reader by a typed `Kind` (`apple`, `maildir`, `mbox`). A `Message` is the parsed `net/mail` message with its received time and the path it came from. `Record` is the filtered view the judge and the models see: `From` (display name and address), `Subject`, `Date`, `Snippet`. `Message.Record(maxSnippet)` builds it: the first `text/plain` part, or `text/html` with tags stripped when there is no plain part, decoded per its transfer encoding and charset where the standard library can, quoted reply lines and signature separators dropped, whitespace collapsed, cut at the limit on a word boundary. Attachments are never decoded. The mbox reader splits on `From ` lines and reverses `>From ` quoting; the Maildir reader lists `cur` and `new`; the Apple reader walks `Data/**/Messages/*.emlx`, reads the count line, slices the message and reads `date-received` from the plist with `encoding/xml`.

**The agent.** One run per invocation, over the instruction given on the command line (default: triage the inbox, say where each message goes, draft replies for the ones that need one). Tools:

- `list_inbox`, deterministic: the filtered records with stable ids, no model involved.
- `triage_inbox`, a `goodall.NewReportingTool` over the mailbox and a `typesafe.Client`: the model names message ids, or all, and the tool asks the judge about each record and returns the typed answers. It is a hand-built tool rather than a `typesafe.Tool` because the state is already in the process and the model should name records, not echo fifty of them through its own output. It asks **one `Ask` per message, in parallel with bounded concurrency**, three questions each: `department` (a `Choice`), `needs_reply` and `is_urgent` (`Noul`s). One array state with questions addressed by id is the indirection TypeSafe's jaggedness page says degrades the model (the finding is recorded in the [TypeSafe findings](2026-09-17-typesafe-jev-findings.md)). Every question and every threshold lives in one file, `questions.go`. Usage is summed across the calls and declared on the call's `ToolCallEnd`; cost stays unreported.
- `draft_reply`, a `goodall.AgentTool` on a cheaper model with its own system prompt and budget; its events render under the call as the nested `ToolEvent`s arrive.
- `check_draft`, a `typesafe.Tool` with fixed questions: a `Score` on how well the draft answers the message and a `Noul` on whether it promises anything the sender was not asked for. The parent escalates a draft below threshold to the person instead of showing it as ready.
- A `typesafe.Route` on the user's turn: a `Choice` between a question that a summary answers and a request for drafts, which picks the model and narrows the tools.

**Output.** A plain-text report: each message's proposed department with its probability, urgency, whether it needs a reply, then each draft with its score and whether it is ready or escalated. Tool calls and the delegate's nested turns print through `examples/internal/render`, which is `examples/cli/print.go` moved so both commands share one renderer.

**Tests.** Readers against synthetic fixtures under `examples/triage/mailbox/testdata` that copy the observed structures byte for byte, including a `.partial.emlx`, `>From ` quoting and a Maildir with both `cur` and `new`. The agent against `internal/fake` for both the parent and the delegate and an `httptest` judge, over the bundled `examples/triage/testdata/sample.mbox`, a synthetic inbox of about a dozen messages including a newsletter whose body carries instruction-looking text and a phishing message, so the offline suite proves the questions decide and the state is data. The sample inbox is also `-source sample`, the default.

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
          draft.go (the AgentTool delegate), route.go (the BeforeSend route), report.go,
          and tests against internal/fake and an httptest judge over
          examples/triage/testdata/sample.mbox, a synthetic dozen-message inbox with a
          newsletter carrying instruction-looking text and a phishing message.
        criteria:
          - "With no flags the example runs on the sample inbox and prints a report; with -source apple -path it runs on a store"
          - "The offline test drives a whole run through internal/fake for both agents and an httptest judge and asserts the report's routes, drafts and escalations"
          - "The judge receives Records only: a test asserts no request body carries a full message body or an attachment"
          - "triage_inbox declares the summed judge usage on its ToolCallEnd and the delegate's events render nested under its call"
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
