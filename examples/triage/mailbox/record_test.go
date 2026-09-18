package mailbox

import (
	"encoding/base64"
	"net/mail"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fixedTime is the received time every hand-written message in these tests
// carries, so Record's Date can be asserted without reading a header.
var fixedTime = time.Date(2024, time.March, 4, 12, 0, 0, 0, time.UTC)

func build(t *testing.T, raw string) Message {
	t.Helper()
	m, err := parseMessage([]byte(raw), "/store/one.eml", fixedTime)
	if err != nil {
		t.Fatalf("parseMessage: %v", err)
	}
	return m
}

func TestRecordFields(t *testing.T) {
	src, err := OpenApple(appleMailbox)
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	msgs := mustList(t, src, 0)
	var found bool
	for _, m := range msgs {
		if m.ID != "apple-one@northwind.example" {
			continue
		}
		found = true
		rec := m.Record(200)
		want := Record{
			ID:      "apple-one@northwind.example",
			From:    mail.Address{Name: "Ann Example", Address: "ann@northwind.example"},
			Subject: "Quarterly café budget",
			Date:    m.Received,
			Snippet: "Can you approve the budget before Friday?",
		}
		if rec != want {
			t.Fatalf("record = %#v, want %#v", rec, want)
		}
	}
	if !found {
		t.Fatal("the fixture message was not listed")
	}
}

func TestRecordKeepsAnUnparsableFromHeader(t *testing.T) {
	m := build(t, "From: not an address\nSubject: Odd\n\nbody\n")
	rec := m.Record(100)
	if rec.From.Address != "not an address" {
		t.Fatalf("From = %#v, want the raw header in Address", rec.From)
	}
}

func TestRecordDecodesTheSubject(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Subject: Plain subject\n", "Plain subject"},
		{"q-encoded utf-8", "Subject: =?utf-8?Q?Quarterly_caf=C3=A9_budget?=\n", "Quarterly café budget"},
		{"b-encoded utf-8", "Subject: =?utf-8?B?Q2Fmw6kgbWVldGluZw==?=\n", "Café meeting"},
		{"q-encoded latin-1", "Subject: =?ISO-8859-1?Q?Caf=E9_meeting?=\n", "Café meeting"},
		{"missing", "", ""},
		{"undecodable charset is left alone", "Subject: =?shift_jis?Q?abc?=\n", "=?shift_jis?Q?abc?="},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := build(t, "From: a@example.test\n"+c.in+"\nbody\n")
			if got := m.Record(100).Subject; got != c.want {
				t.Fatalf("subject = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRecordDateIsTheReceivedTime(t *testing.T) {
	m := build(t, "From: a@example.test\nDate: Mon, 1 Jan 2001 00:00:00 +0000\n\nbody\n")
	if !m.Record(100).Date.Equal(fixedTime) {
		t.Fatalf("date = %s, want the received time %s", m.Record(100).Date, fixedTime)
	}
}

func TestRecordSnippet(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "plain text",
			raw:  "From: a@example.test\nContent-Type: text/plain\n\nHello there.\nSecond line.\n",
			want: "Hello there. Second line.",
		},
		{
			name: "no content type is plain text",
			raw:  "From: a@example.test\n\nHello there.\n",
			want: "Hello there.",
		},
		{
			name: "quoted reply and its attribution are dropped",
			raw: "From: a@example.test\nContent-Type: text/plain\n\n" +
				"Thanks, that works for me.\n\n" +
				"On Mon, 4 Mar 2024 at 09:15, Ann <ann@northwind.example> wrote:\n" +
				"> Can we move it to Tuesday?\n> I have a conflict.\n",
			want: "Thanks, that works for me.",
		},
		{
			name: "a wrapped attribution is dropped",
			raw: "From: a@example.test\nContent-Type: text/plain\n\n" +
				"Thanks, that works for me.\n\n" +
				"On Mon, 4 Mar 2024 at 09:15, Ann Example <ann@northwind.example>\n" +
				"wrote:\n" +
				"> Can we move it to Tuesday?\n",
			want: "Thanks, that works for me.",
		},
		{
			name: "a line that merely mentions a quote survives",
			raw: "From: a@example.test\nContent-Type: text/plain\n\n" +
				"On Tuesday we ship.\nNothing is quoted here.\n",
			want: "On Tuesday we ship. Nothing is quoted here.",
		},
		{
			name: "nested quoting is dropped",
			raw: "From: a@example.test\nContent-Type: text/plain\n\n" +
				"Agreed.\n>> original\n> reply\n",
			want: "Agreed.",
		},
		{
			name: "the signature separator ends the snippet",
			raw: "From: a@example.test\nContent-Type: text/plain\n\n" +
				"The report is ready.\n\n-- \nAnn Example\nNorthwind, Ltd.\n",
			want: "The report is ready.",
		},
		{
			name: "quoted-printable is decoded",
			raw: "From: a@example.test\nContent-Type: text/plain; charset=\"utf-8\"\n" +
				"Content-Transfer-Encoding: quoted-printable\n\n" +
				"Meet at the caf=C3=A9 and bring the=\n numbers.\n",
			want: "Meet at the café and bring the numbers.",
		},
		{
			name: "base64 text is decoded",
			raw: "From: a@example.test\nContent-Type: text/plain; charset=\"utf-8\"\n" +
				"Content-Transfer-Encoding: base64\n\n" +
				base64.StdEncoding.EncodeToString([]byte("Hello from base64 land.")) + "\n",
			want: "Hello from base64 land.",
		},
		{
			name: "iso-8859-1 is decoded",
			raw: "From: a@example.test\nContent-Type: text/plain; charset=\"iso-8859-1\"\n\n" +
				"caf\xe9 society\n",
			want: "café society",
		},
		{
			name: "an unsupported charset passes its bytes through",
			raw: "From: a@example.test\nContent-Type: text/plain; charset=\"shift_jis\"\n\n" +
				"caf\xe9 society\n",
			want: "caf\xe9 society",
		},
		{
			name: "html is used when there is no plain part",
			raw: "From: a@example.test\nContent-Type: text/html; charset=\"utf-8\"\n\n" +
				"<html><head><style>p{color:red}</style></head><body><h1>Sale&nbsp;ends</h1>" +
				"<p>Up to 50&#37; off &amp; free shipping.</p><script>alert(1)</script></body></html>\n",
			want: "Sale ends Up to 50% off & free shipping.",
		},
		{
			name: "alternative prefers the plain part",
			raw: "From: a@example.test\nMIME-Version: 1.0\n" +
				"Content-Type: multipart/alternative; boundary=\"b1\"\n\n" +
				"--b1\nContent-Type: text/html\n\n<p>the html one</p>\n" +
				"--b1\nContent-Type: text/plain\n\nthe plain one\n" +
				"--b1--\n",
			want: "the plain one",
		},
		{
			name: "a nested alternative is walked",
			raw: "From: a@example.test\nMIME-Version: 1.0\n" +
				"Content-Type: multipart/mixed; boundary=\"outer\"\n\n" +
				"--outer\nContent-Type: multipart/alternative; boundary=\"inner\"\n\n" +
				"--inner\nContent-Type: text/html\n\n<p>the html one</p>\n" +
				"--inner\nContent-Type: text/plain\n\nthe nested plain one\n" +
				"--inner--\n" +
				"--outer--\n",
			want: "the nested plain one",
		},
		{
			name: "an attached text part is skipped",
			raw: "From: a@example.test\nMIME-Version: 1.0\n" +
				"Content-Type: multipart/mixed; boundary=\"b1\"\n\n" +
				"--b1\nContent-Type: text/plain; name=\"notes.txt\"\n" +
				"Content-Disposition: attachment; filename=\"notes.txt\"\n\nATTACHED-SECRET\n" +
				"--b1\nContent-Type: text/plain\n\nthe body\n" +
				"--b1--\n",
			want: "the body",
		},
		{
			name: "a non-text part is skipped",
			raw: "From: a@example.test\nMIME-Version: 1.0\n" +
				"Content-Type: multipart/mixed; boundary=\"b1\"\n\n" +
				"--b1\nContent-Type: application/pdf\nContent-Transfer-Encoding: base64\n\n" +
				base64.StdEncoding.EncodeToString([]byte("PDF-SECRET")) + "\n" +
				"--b1\nContent-Type: text/plain\n\nthe body\n" +
				"--b1--\n",
			want: "the body",
		},
		{
			name: "an attached html part is not the fallback",
			raw: "From: a@example.test\nMIME-Version: 1.0\n" +
				"Content-Type: multipart/mixed; boundary=\"b1\"\n\n" +
				"--b1\nContent-Type: text/html\nContent-Disposition: attachment; filename=\"page.html\"\n\n" +
				"<p>ATTACHED-SECRET</p>\n" +
				"--b1--\n",
			want: "",
		},
		{
			name: "a message with no text part has no snippet",
			raw: "From: a@example.test\nMIME-Version: 1.0\n" +
				"Content-Type: multipart/mixed; boundary=\"b1\"\n\n" +
				"--b1\nContent-Type: image/png\nContent-Transfer-Encoding: base64\n\n" +
				base64.StdEncoding.EncodeToString([]byte("PNG-SECRET")) + "\n" +
				"--b1--\n",
			want: "",
		},
		{
			name: "an unopenable multipart falls back to the raw body",
			raw:  "From: a@example.test\nContent-Type: multipart/mixed\n\nno boundary was declared\n",
			want: "no boundary was declared",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := build(t, c.raw).Record(0).Snippet
			if got != c.want {
				t.Fatalf("snippet = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRecordNeverDecodesAnAttachment(t *testing.T) {
	secret := strings.Repeat("SECRETPAYLOAD", 20000)
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	raw := "From: a@example.test\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=\"b1\"\n\n" +
		"--b1\nContent-Type: application/octet-stream; name=\"blob.bin\"\n" +
		"Content-Disposition: attachment; filename=\"blob.bin\"\n" +
		"Content-Transfer-Encoding: base64\n\n" + encoded + "\n" +
		"--b1\nContent-Type: text/plain\n\nthe body\n" +
		"--b1--\n"

	rec := build(t, raw).Record(0)
	if rec.Snippet != "the body" {
		t.Fatalf("snippet = %.120q, want %q", rec.Snippet, "the body")
	}
	if strings.Contains(rec.Snippet, "SECRETPAYLOAD") {
		t.Fatal("the attachment was decoded into the snippet")
	}
	if strings.Contains(rec.Snippet, encoded[:64]) {
		t.Fatal("the attachment's base64 leaked into the snippet")
	}
	if len(rec.Snippet) > 256 {
		t.Fatalf("the attachment inflated the record to %d bytes", len(rec.Snippet))
	}
}

// TestRecordDoesNotReadAnAttachmentIntoMemory watches what Record allocates.
// Asserting on the snippet alone is not enough: the walk would produce the
// same snippet while still copying every attachment body it passed, so the
// guard that skips a part before reading it can only be proved by the bytes
// that are never allocated. Run with: go test -run
// TestRecordDoesNotReadAnAttachmentIntoMemory ./examples/triage/mailbox
func TestRecordDoesNotReadAnAttachmentIntoMemory(t *testing.T) {
	const attachmentBytes = 8 << 20
	raw := "From: a@example.test\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=\"b1\"\n\n" +
		// The attachment comes first, so the walk has to decide about it
		// rather than stopping at a plain part it found earlier.
		"--b1\nContent-Type: text/plain; name=\"log.txt\"\n" +
		"Content-Disposition: attachment; filename=\"log.txt\"\n\n" +
		strings.Repeat("a", attachmentBytes) + "\n" +
		"--b1\nContent-Type: text/plain\n\nthe body\n" +
		"--b1--\n"
	m := build(t, raw)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	rec := m.Record(400)
	runtime.ReadMemStats(&after)

	if rec.Snippet != "the body" {
		t.Fatalf("snippet = %.80q, want %q", rec.Snippet, "the body")
	}
	// Reading the attachment would cost at least maxTextPartBytes; skipping it
	// costs the multipart reader's own buffers and nothing else.
	const budget = 512 << 10
	if grew := after.TotalAlloc - before.TotalAlloc; grew > budget {
		t.Fatalf("Record allocated %d bytes for a message whose only large part is an attachment, over the %d-byte budget", grew, budget)
	}
}

func TestRecordCutsOnAWordBoundary(t *testing.T) {
	const text = "one two three four five"
	cases := []struct {
		name       string
		body       string
		maxSnippet int
		want       string
	}{
		{"no limit", text, 0, text},
		{"negative is no limit", text, -1, text},
		{"exactly the limit", text, len(text), text},
		{"one over the limit", text, len(text) + 1, text},
		{"cut back to a word boundary", text, 12, "one two…"},
		{"cut near the end", text, len(text) - 1, "one two three four…"},
		{"a single long word is cut hard", "supercalifragilistic", 10, "supercali…"},
		{"the limit counts runes", "café café café", 7, "café…"},
		{"a tiny limit still ends in the ellipsis", text, 1, "…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := build(t, "From: a@example.test\nContent-Type: text/plain; charset=\"utf-8\"\n\n"+c.body+"\n")
			got := m.Record(c.maxSnippet).Snippet
			if got != c.want {
				t.Fatalf("snippet = %q, want %q", got, c.want)
			}
			if c.maxSnippet > 0 && len([]rune(got)) > c.maxSnippet {
				t.Fatalf("snippet is %d runes, over the limit of %d", len([]rune(got)), c.maxSnippet)
			}
		})
	}
}
