package mailbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The order the mbox fixture must come back in: Date header descending, with
// the undated third message placed by its From_ line.
var mboxNewestFirst = []string{"Café meeting", "Trail notes", "Bring the projector"}

func TestMboxListsNewestFirst(t *testing.T) {
	src, err := OpenMbox(mboxFile)
	if err != nil {
		t.Fatalf("OpenMbox: %v", err)
	}
	got := subjects(t, src, 0)
	if !equalStrings(got, mboxNewestFirst) {
		t.Fatalf("subjects = %q, want %q", got, mboxNewestFirst)
	}
}

func TestMboxHonorsTheLimit(t *testing.T) {
	src, err := OpenMbox(mboxFile)
	if err != nil {
		t.Fatalf("OpenMbox: %v", err)
	}
	for _, limit := range []int{1, 2, 3, 7, 0, -1} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			want := mboxNewestFirst
			if limit > 0 && limit < len(want) {
				want = want[:limit]
			}
			got := subjects(t, src, limit)
			if !equalStrings(got, want) {
				t.Fatalf("subjects = %q, want %q", got, want)
			}
		})
	}
}

func TestMboxReversesFromQuoting(t *testing.T) {
	src, err := OpenMbox(mboxFile)
	if err != nil {
		t.Fatalf("OpenMbox: %v", err)
	}
	msgs := mustList(t, src, 0)
	snippet := msgs[1].Record(200).Snippet
	want := "From the top of the hill you can see the whole valley. We should go back in spring."
	if snippet != want {
		t.Fatalf("snippet = %q, want %q", snippet, want)
	}
}

func TestMboxUnquotesOnlyOneLevel(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{">From the top\n", "From the top\n"},
		{">>From the top\n", ">From the top\n"},
		{">>>From the top\n", ">>From the top\n"},
		{"> From the top\n", "> From the top\n"},
		{">Frommage\n", ">Frommage\n"},
		{"From the top\n", "From the top\n"},
		{">From the top\r\n", "From the top\r\n"},
	}
	for _, c := range cases {
		t.Run(strings.TrimSpace(c.in), func(t *testing.T) {
			got := string(unquoteFrom([]byte(c.in)))
			if got != c.want {
				t.Fatalf("unquoteFrom(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMboxDecodesQuotedPrintable(t *testing.T) {
	src, err := OpenMbox(mboxFile)
	if err != nil {
		t.Fatalf("OpenMbox: %v", err)
	}
	msgs := mustList(t, src, 1)
	rec := msgs[0].Record(200)
	want := "Let's meet at the café on Thursday to go over the numbers before the board call."
	if rec.Snippet != want {
		t.Fatalf("snippet = %q, want %q", rec.Snippet, want)
	}
	if rec.Subject != "Café meeting" {
		t.Fatalf("subject = %q, want %q", rec.Subject, "Café meeting")
	}
}

func TestMboxFallsBackToTheFromLineDate(t *testing.T) {
	src, err := OpenMbox(mboxFile)
	if err != nil {
		t.Fatalf("OpenMbox: %v", err)
	}
	msgs := mustList(t, src, 0)
	last := msgs[len(msgs)-1]
	want := time.Date(2024, time.March, 5, 7, 15, 0, 0, time.UTC)
	if !last.Received.Equal(want) {
		t.Fatalf("received %s, want %s", last.Received, want)
	}
	// The same message is the CRLF one, so its snippet proves CRLF handling.
	if got := last.Record(200).Snippet; got != "The room has no HDMI cable either." {
		t.Fatalf("snippet = %q", got)
	}
}

func TestMboxPathCarriesTheOffset(t *testing.T) {
	src, err := OpenMbox(mboxFile)
	if err != nil {
		t.Fatalf("OpenMbox: %v", err)
	}
	msgs := mustList(t, src, 0)
	raw, err := os.ReadFile(mboxFile)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, m := range msgs {
		file, offset, ok := strings.Cut(m.Path, ":")
		if !ok {
			t.Fatalf("path %q carries no offset", m.Path)
		}
		if file != mboxFile {
			t.Fatalf("path names %q, want %q", file, mboxFile)
		}
		n, err := strconv.Atoi(offset)
		if err != nil {
			t.Fatalf("offset %q: %v", offset, err)
		}
		if seen[n] {
			t.Fatalf("offset %d appeared twice", n)
		}
		seen[n] = true
		if !strings.HasPrefix(string(raw[n:]), "From ") {
			t.Fatalf("offset %d does not point at a From_ line", n)
		}
	}
	if !seen[0] {
		t.Fatal("no message starts at offset 0")
	}
}

func TestMboxSplitsOnlyAfterABlankLine(t *testing.T) {
	// A body line that begins "From " but is not preceded by a blank line is
	// part of the message, not a new one.
	body := "From a@example.test Wed Mar  6 08:00:00 2024\n" +
		"From: A <a@example.test>\n" +
		"Subject: One message\n" +
		"Date: Wed, 6 Mar 2024 08:00:00 +0000\n" +
		"Content-Type: text/plain\n" +
		"\n" +
		"first line\n" +
		"From here on it is still the same message.\n"
	path := filepath.Join(t.TempDir(), "one.mbox")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := OpenMbox(path)
	if err != nil {
		t.Fatalf("OpenMbox: %v", err)
	}
	msgs := mustList(t, src, 0)
	if len(msgs) != 1 {
		t.Fatalf("List returned %d messages, want 1", len(msgs))
	}
	want := "first line From here on it is still the same message."
	if got := msgs[0].Record(200).Snippet; got != want {
		t.Fatalf("snippet = %q, want %q", got, want)
	}
}

// TestMboxSkipsAMessageThatWillNotParse covers a truncated or hand-edited
// mbox: one message with no header block must cost that message only.
func TestMboxSkipsAMessageThatWillNotParse(t *testing.T) {
	good := func(subject, addr, from string) string {
		return "From " + addr + " " + from + "\n" +
			"From: A <" + addr + ">\nSubject: " + subject + "\nContent-Type: text/plain\n\nbody\n\n"
	}
	body := good("Older", "a@example.test", "Wed Mar  6 08:00:00 2024") +
		"From bad@example.test Thu Mar  7 09:00:00 2024\n" +
		"this line is not a header at all\n\n" +
		good("Newer", "c@example.test", "Fri Mar  8 10:00:00 2024")
	path := filepath.Join(t.TempDir(), "mixed.mbox")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := OpenMbox(path)
	if err != nil {
		t.Fatalf("OpenMbox: %v", err)
	}
	listing, err := src.List(t.Context(), 0)
	if err != nil {
		t.Fatalf("a malformed message failed the whole listing: %v", err)
	}
	var got []string
	for _, m := range listing.Messages {
		got = append(got, m.Record(50).Subject)
	}
	if !equalStrings(got, []string{"Newer", "Older"}) {
		t.Fatalf("subjects = %q, want [Newer Older]", got)
	}
	if len(listing.Skipped) != 1 {
		t.Fatalf("skipped %d messages, want 1: %q", len(listing.Skipped), skipPaths(listing.Skipped))
	}
	// The path carries the offset, which is how a reader finds the message
	// again in a file that holds thousands.
	file, offset, ok := strings.Cut(listing.Skipped[0].Path, ":")
	if !ok || file != path {
		t.Fatalf("the skip names %q, want %q and an offset", listing.Skipped[0].Path, path)
	}
	if n, err := strconv.Atoi(offset); err != nil || n == 0 {
		t.Fatalf("the skip's offset is %q, want the second message's", offset)
	}
}

func TestMboxFailsWhenTheFileDisappears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone.mbox")
	if err := os.WriteFile(path, []byte("From a@example.test Wed Mar  6 08:00:00 2024\nFrom: a@example.test\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := OpenMbox(path)
	if err != nil {
		t.Fatalf("OpenMbox: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := src.List(t.Context(), 0); err == nil {
		t.Fatal("List returned no error for a file that is gone")
	}
}

func TestMboxReadsAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.mbox")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := OpenMbox(path)
	if err != nil {
		t.Fatalf("OpenMbox: %v", err)
	}
	msgs := mustList(t, src, 0)
	if len(msgs) != 0 {
		t.Fatalf("List returned %d messages, want 0", len(msgs))
	}
}
