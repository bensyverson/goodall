package mailbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The new message has no Date header, so it takes the file's mod time, which
// is later than the dated one in cur however the fixtures were checked out.
var maildirNewestFirst = []string{"Package delivered", "Invoice 2024-014"}

func TestMaildirListsCurAndNew(t *testing.T) {
	src, err := OpenMaildir(maildirDir)
	if err != nil {
		t.Fatalf("OpenMaildir: %v", err)
	}
	got := subjects(t, src, 0)
	if !equalStrings(got, maildirNewestFirst) {
		t.Fatalf("subjects = %q, want %q", got, maildirNewestFirst)
	}
}

func TestMaildirIgnoresTmp(t *testing.T) {
	src, err := OpenMaildir(maildirDir)
	if err != nil {
		t.Fatalf("OpenMaildir: %v", err)
	}
	for _, s := range subjects(t, src, 0) {
		if s == "Never delivered" {
			t.Fatal("the reader listed a message from tmp")
		}
	}
}

func TestMaildirHonorsTheLimit(t *testing.T) {
	src, err := OpenMaildir(maildirDir)
	if err != nil {
		t.Fatalf("OpenMaildir: %v", err)
	}
	for _, limit := range []int{1, 2, 5, 0, -1} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			want := maildirNewestFirst
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

func TestMaildirUsesTheDateHeader(t *testing.T) {
	src, err := OpenMaildir(maildirDir)
	if err != nil {
		t.Fatalf("OpenMaildir: %v", err)
	}
	msgs := mustList(t, src, 0)
	dated := msgs[len(msgs)-1]
	want := time.Date(2024, time.March, 4, 10, 0, 0, 0, time.UTC)
	if !dated.Received.Equal(want) {
		t.Fatalf("received %s, want %s", dated.Received, want)
	}
}

func TestMaildirFallsBackToModTime(t *testing.T) {
	src, err := OpenMaildir(maildirDir)
	if err != nil {
		t.Fatalf("OpenMaildir: %v", err)
	}
	msgs := mustList(t, src, 1)
	if len(msgs) != 1 {
		t.Fatalf("List(1) returned %d messages", len(msgs))
	}
	info, err := os.Stat(msgs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !msgs[0].Received.Equal(info.ModTime()) {
		t.Fatalf("received %s, want the file's mod time %s", msgs[0].Received, info.ModTime())
	}
}

func TestMaildirSkipsDotfilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "cur")
	if err := os.MkdirAll(filepath.Join(cur, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	msg := "From: a@example.test\nSubject: Kept\nDate: Mon, 4 Mar 2024 10:00:00 +0000\nContent-Type: text/plain\n\nhello\n"
	if err := os.WriteFile(filepath.Join(cur, "1.mail"), []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cur, ".hidden"), []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := OpenMaildir(dir)
	if err != nil {
		t.Fatalf("OpenMaildir: %v", err)
	}
	got := subjects(t, src, 0)
	if !equalStrings(got, []string{"Kept"}) {
		t.Fatalf("subjects = %q, want [Kept]", got)
	}
}

// TestMaildirSkipsAFileThatWillNotParse covers a delivery that landed as
// something other than a message: it costs that file, not the listing.
func TestMaildirSkipsAFileThatWillNotParse(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "cur")
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	good := "From: a@example.test\nSubject: Kept\nDate: Mon, 4 Mar 2024 10:00:00 +0000\nContent-Type: text/plain\n\nhello\n"
	if err := os.WriteFile(filepath.Join(cur, "1.mail"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(cur, "2.mail")
	if err := os.WriteFile(bad, []byte("this line is not a header at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := OpenMaildir(dir)
	if err != nil {
		t.Fatalf("OpenMaildir: %v", err)
	}
	listing, err := src.List(t.Context(), 0)
	if err != nil {
		t.Fatalf("an unparsable file failed the whole listing: %v", err)
	}
	var got []string
	for _, m := range listing.Messages {
		got = append(got, m.Record(50).Subject)
	}
	if !equalStrings(got, []string{"Kept"}) {
		t.Fatalf("subjects = %q, want [Kept]", got)
	}
	if paths := skipPaths(listing.Skipped); !equalStrings(paths, []string{bad}) {
		t.Fatalf("skipped = %q, want %q", paths, []string{bad})
	}
}

func TestMaildirFailsWhenTheDirectoryCannotBeRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode, so this cannot be tested as root")
	}
	dir := t.TempDir()
	cur := filepath.Join(dir, "cur")
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := OpenMaildir(dir)
	if err != nil {
		t.Fatalf("OpenMaildir: %v", err)
	}
	if err := os.Chmod(cur, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(cur, 0o755) })
	if _, err := src.List(t.Context(), 0); err == nil {
		t.Fatal("List returned no error for an unreadable cur directory")
	}
}

func TestMaildirRejectsADirectoryWithNoCurOrNew(t *testing.T) {
	if _, err := OpenMaildir(t.TempDir()); err == nil {
		t.Fatal("OpenMaildir returned no error for a directory with no cur or new")
	}
}
