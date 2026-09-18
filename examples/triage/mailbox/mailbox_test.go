package mailbox

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// Fixture locations, written by testdata/gen. The Apple tree is a whole store
// root so every path shape the reader accepts can be opened from one fixture.
var (
	appleStore   = filepath.FromSlash("testdata/apple")
	appleV10     = filepath.Join(appleStore, "V10")
	appleAccount = filepath.Join(appleV10, "A1B2C3D4-0000-4000-8000-000000000001")
	appleFolder  = filepath.Join(appleAccount, "INBOX.mbox")
	appleMailbox = filepath.Join(appleFolder, "E5F60718-0000-4000-8000-000000000002")
	appleSent    = filepath.Join(appleAccount, "Sent Messages.mbox")

	mboxFile   = filepath.FromSlash("testdata/sample.mbox")
	maildirDir = filepath.FromSlash("testdata/maildir")
)

// mustList lists a source, failing on an error and on any skipped file. Every
// test that is not about skipping goes through it, so a fixture that quietly
// stops parsing shows up as a failure rather than as a shorter list.
func mustList(t *testing.T, s Source, limit int) []Message {
	t.Helper()
	listing, err := s.List(t.Context(), limit)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, skip := range listing.Skipped {
		t.Errorf("unexpected skip: %s: %v", skip.Path, skip.Err)
	}
	return listing.Messages
}

// listIDs lists a source and returns the message ids in the order given.
func listIDs(t *testing.T, s Source, limit int) []string {
	t.Helper()
	msgs := mustList(t, s, limit)
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	return ids
}

// subjects lists a source and returns each message's decoded subject.
func subjects(t *testing.T, s Source, limit int) []string {
	t.Helper()
	msgs := mustList(t, s, limit)
	subs := make([]string, len(msgs))
	for i, m := range msgs {
		subs[i] = m.Record(200).Subject
	}
	return subs
}

// skipPaths returns the paths a listing skipped.
func skipPaths(skipped []Skip) []string {
	paths := make([]string, len(skipped))
	for i, s := range skipped {
		paths[i] = s.Path
	}
	return paths
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOpenPicksTheReader(t *testing.T) {
	cases := []struct {
		name string
		kind Kind
		path string
		want int
	}{
		{"apple", KindApple, appleMailbox, 3},
		{"maildir", KindMaildir, maildirDir, 2},
		{"mbox", KindMbox, mboxFile, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, err := Open(c.kind, c.path)
			if err != nil {
				t.Fatalf("Open(%q, %q): %v", c.kind, c.path, err)
			}
			msgs := mustList(t, src, 0)
			if len(msgs) != c.want {
				t.Fatalf("List returned %d messages, want %d", len(msgs), c.want)
			}
		})
	}
}

func TestOpenRejectsAnUnknownKind(t *testing.T) {
	if _, err := Open(Kind("imap"), mboxFile); err == nil {
		t.Fatal("Open with an unknown kind returned no error")
	}
}

func TestOpenReportsAMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere")
	for _, kind := range []Kind{KindApple, KindMaildir, KindMbox} {
		if _, err := Open(kind, missing); err == nil {
			t.Errorf("Open(%q, missing) returned no error", kind)
		}
	}
}

func TestListHonorsACanceledContext(t *testing.T) {
	sources := map[string]func() (Source, error){
		"apple":   func() (Source, error) { return OpenApple(appleMailbox) },
		"maildir": func() (Source, error) { return OpenMaildir(maildirDir) },
		"mbox":    func() (Source, error) { return OpenMbox(mboxFile) },
	}
	for name, open := range sources {
		t.Run(name, func(t *testing.T) {
			src, err := open()
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := src.List(ctx, 0); err == nil {
				t.Fatal("List on a canceled context returned no error")
			}
		})
	}
}

func TestMessageIDUsesTheMessageIDHeader(t *testing.T) {
	src, err := OpenMbox(mboxFile)
	if err != nil {
		t.Fatalf("OpenMbox: %v", err)
	}
	got := listIDs(t, src, 0)
	want := []string{"mbox-two@example.test", "mbox-one@example.test", "mbox-three@example.test"}
	if !equalStrings(got, want) {
		t.Fatalf("ids = %q, want %q", got, want)
	}
}

func TestMessageIDFallsBackToAStableHash(t *testing.T) {
	raw := []byte("From: a@example.test\r\nSubject: No id\r\n\r\nbody\r\n")
	when := time.Date(2024, time.March, 4, 12, 0, 0, 0, time.UTC)

	first, err := parseMessage(raw, "/store/one.emlx", when)
	if err != nil {
		t.Fatalf("parseMessage: %v", err)
	}
	again, err := parseMessage(raw, "/store/one.emlx", when)
	if err != nil {
		t.Fatalf("parseMessage: %v", err)
	}
	if first.ID == "" {
		t.Fatal("a message with no Message-ID got an empty id")
	}
	if first.ID != again.ID {
		t.Fatalf("the id is not stable across runs: %q then %q", first.ID, again.ID)
	}

	otherPath, err := parseMessage(raw, "/store/two.emlx", when)
	if err != nil {
		t.Fatalf("parseMessage: %v", err)
	}
	if otherPath.ID == first.ID {
		t.Fatal("two paths produced the same id")
	}
	otherTime, err := parseMessage(raw, "/store/one.emlx", when.Add(time.Second))
	if err != nil {
		t.Fatalf("parseMessage: %v", err)
	}
	if otherTime.ID == first.ID {
		t.Fatal("two received times produced the same id")
	}
}
