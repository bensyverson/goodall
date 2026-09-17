# TypeSafe fixtures

Every file here is **recorded**, named `live_*`. They were captured from the
live judgment API by `scripts/record-fixtures` on **2026-09-17** against
**`jev-1.13.0`** (the versioned id, not the `jev-latest` alias — see
`internal/livemodel`), and they are exactly the bytes the API returned: no
headers, no status line, no key. Refresh them with

    go run ./scripts/record-fixtures -provider typesafe -env .env

Each recorded request has a `.request.json` beside it holding the body goodall
sent, so a reader can see what produced the answer. Those request files are
indented for reading; member order is untouched, so they still show the
byte-stable order the marshaler builds. `live_models.json` has no request file:
the catalog is a GET and there is no body to show.

| File | What it is |
| --- | --- |
| `live_mixed.json` | One `noul`, one `choice` and one `score` over an object state, answered in one call. The only recording that carries all three answer shapes. |
| `live_noul_criteria.json` | A `noul` with `true` and `false` criteria over a plain string state, so both state forms are on disk. |
| `live_models.json` | `GET /v1/models`: the two aliases, with descriptions and release dates. |
| `live_refused.json` | A real HTTP 422 envelope, from a body carrying an unknown question type. |

## Why the refusal was posted by hand

`live_refused.json` is the one recording the client did not produce. The
`Question` interface is sealed to the three types the API serves, so a request
carrying a fourth cannot be built through the typed API at all — which is the
point of sealing it. The recorder therefore posts that body itself
(`recordRefusal` in `scripts/record-fixtures/typesafe.go`) and records the
answer. Nothing else in the file is hand-authored: the detail list, the `loc`
path and the `expected_tags` text are TypeSafe's own.

That envelope is also where the fourth question type came from. Its
`expected_tags` names `bounding_box` beside `noul`, `choice` and `score`, and
the published API reference lists only three; the finding is recorded in
`project/2026-09-17-typesafe-jev-findings.md`. This package does not model it,
so an answer of that type decodes to a `typesafe.UnknownAnswer` carrying its
bytes.

## What the offline tests may assert

The answers in these recordings are one model's judgments, so the offline tests
assert the *decoding* and the shape — that `department` is a choice carrying
three probabilities, that a `score` legend names four levels — plus the exact
figures in the files, which are fixed bytes on disk and cannot move. The live
test in `live_test.go` is the one that asserts about the judgment itself, and it
asserts what a caller's threshold would ("this is a billing ticket") rather than
a figure that a new release may shift.

Nothing here is a secret and nothing here needs a key to read.
