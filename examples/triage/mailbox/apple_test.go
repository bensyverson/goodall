package mailbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The order the Apple fixtures must come back in: date-received descending.
var appleNewestFirst = []string{
	"Disk space warning",
	"Quarterly café budget",
	"Five things this week",
}

func TestAppleListsNewestFirst(t *testing.T) {
	src, err := OpenApple(appleMailbox)
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	got := subjects(t, src, 0)
	if !equalStrings(got, appleNewestFirst) {
		t.Fatalf("subjects = %q, want %q", got, appleNewestFirst)
	}
}

func TestAppleHonorsTheLimit(t *testing.T) {
	cases := []struct {
		limit int
		want  []string
	}{
		{1, appleNewestFirst[:1]},
		{2, appleNewestFirst[:2]},
		{3, appleNewestFirst},
		{9, appleNewestFirst},
		{0, appleNewestFirst},
		{-1, appleNewestFirst},
	}
	src, err := OpenApple(appleMailbox)
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.limit), func(t *testing.T) {
			got := subjects(t, src, c.limit)
			if !equalStrings(got, c.want) {
				t.Fatalf("subjects = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAppleAcceptsEveryPathShape(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"store root", appleStore},
		{"V10", appleV10},
		{"account uuid", appleAccount},
		{"folder", appleFolder},
		{"mailbox uuid", appleMailbox},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, err := OpenApple(c.path)
			if err != nil {
				t.Fatalf("OpenApple(%q): %v", c.path, err)
			}
			got := subjects(t, src, 0)
			if !equalStrings(got, appleNewestFirst) {
				t.Fatalf("subjects = %q, want %q", got, appleNewestFirst)
			}
		})
	}
}

// Tab completion hands a shell a trailing separator, so a path that arrives
// with one has to mean the same directory it would without.
func TestAppleAcceptsATrailingSeparator(t *testing.T) {
	src, err := OpenApple(appleFolder + string(filepath.Separator))
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	got := subjects(t, src, 0)
	if !equalStrings(got, appleNewestFirst) {
		t.Fatalf("subjects = %q, want %q", got, appleNewestFirst)
	}
}

func TestAppleReadsOnlyTheInboxUnderAStore(t *testing.T) {
	src, err := OpenApple(appleStore)
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	for _, s := range subjects(t, src, 0) {
		if s == "Re: Quarterly budget" {
			t.Fatal("a store scan picked up Sent Messages.mbox")
		}
	}
}

func TestAppleOpensASentFolderWhenNamedDirectly(t *testing.T) {
	src, err := OpenApple(appleSent)
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	got := subjects(t, src, 0)
	want := []string{"Re: Quarterly budget"}
	if !equalStrings(got, want) {
		t.Fatalf("subjects = %q, want %q", got, want)
	}
}

func TestAppleReadsDateReceivedFromThePlist(t *testing.T) {
	src, err := OpenApple(appleMailbox)
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	msgs := mustList(t, src, 0)
	want := map[string]time.Time{
		"Disk space warning":    time.Date(2024, time.March, 6, 20, 51, 0, 0, time.UTC),
		"Quarterly café budget": time.Date(2024, time.March, 5, 20, 51, 0, 0, time.UTC),
		"Five things this week": time.Date(2024, time.March, 4, 20, 51, 0, 0, time.UTC),
	}
	for _, m := range msgs {
		rec := m.Record(200)
		if !m.Received.Equal(want[rec.Subject]) {
			t.Errorf("%q received %s, want %s", rec.Subject, m.Received, want[rec.Subject])
		}
		if !strings.HasSuffix(m.Path, ".emlx") {
			t.Errorf("%q has path %q, want a .emlx file", rec.Subject, m.Path)
		}
	}
}

func TestAppleReadsAPartialMessage(t *testing.T) {
	src, err := OpenApple(appleMailbox)
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	msgs := mustList(t, src, 1)
	if len(msgs) != 1 {
		t.Fatalf("List(1) returned %d messages", len(msgs))
	}
	if !strings.HasSuffix(msgs[0].Path, ".partial.emlx") {
		t.Fatalf("the newest message is %q, want the .partial.emlx", msgs[0].Path)
	}
	rec := msgs[0].Record(200)
	if rec.Snippet != "The backup volume is at 91 percent." {
		t.Fatalf("snippet = %q", rec.Snippet)
	}
}

// writeEmlx writes one .emlx file into a Messages directory under dir.
func writeEmlx(t *testing.T, dir, name, body string) string {
	t.Helper()
	msgDir := filepath.Join(dir, "Data", "Messages")
	if err := os.MkdirAll(msgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(msgDir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// emlxBytes renders a .emlx file the way Mail does, with the count computed
// from the message rather than typed.
func emlxBytes(message string, received time.Time) string {
	return fmt.Sprintf("%-10d\n%s<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"+
		"<plist version=\"1.0\">\n<dict>\n<key>date-received</key>\n<integer>%d</integer>\n</dict>\n</plist>\n",
		len(message), message, received.Unix())
}

func TestAppleSkipsAMalformedFile(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty", ""},
		{"count line too short", "12\nFrom: a@example.test\n\nbody\n"},
		{"count not a number", "abcdefghij\nFrom: a@example.test\n\nbody\n"},
		{"count wider than the field", "12345678901\nFrom: a@example.test\n\nbody\n"},
		{"interior space in the count", "12 34     \nFrom: a@example.test\n\nbody\n"},
		{"count longer than the file", "999999    \nFrom: a@example.test\n\nbody\n"},
		{"no newline after the count", "4         From: a@example.test\n\nbody\n"},
		{"the message is not a message", emlxBytes("no header here at all\n", time.Now())},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeEmlx(t, dir, "1.emlx", c.body)
			src, err := OpenApple(dir)
			if err != nil {
				t.Fatalf("OpenApple: %v", err)
			}
			listing, err := src.List(t.Context(), 0)
			if err != nil {
				t.Fatalf("a malformed file failed the whole listing: %v", err)
			}
			if len(listing.Messages) != 0 {
				t.Fatalf("List returned %d messages for a malformed .emlx", len(listing.Messages))
			}
			if got := skipPaths(listing.Skipped); !equalStrings(got, []string{path}) {
				t.Fatalf("skipped = %q, want %q", got, []string{path})
			}
			if !strings.Contains(listing.Skipped[0].Err.Error(), filepath.Base(path)) {
				t.Fatalf("the skip's error does not name the file: %v", listing.Skipped[0].Err)
			}
		})
	}
}

// TestAppleSkipsOneBadFileAmongGoodOnes is the case the whole Listing type
// exists for: Mail writes .emlx files while it runs, so a store of thousands
// can always hold one that is half-written, and it must cost that one message
// rather than the listing.
func TestAppleSkipsOneBadFileAmongGoodOnes(t *testing.T) {
	dir := t.TempDir()
	good := func(subject string, day int) string {
		return emlxBytes("From: a@example.test\nSubject: "+subject+"\n\nbody\n",
			time.Date(2024, time.March, day, 12, 0, 0, 0, time.UTC))
	}
	writeEmlx(t, dir, "1.emlx", good("Older", 4))
	writeEmlx(t, dir, "2.emlx", good("Newer", 6))
	bad := writeEmlx(t, dir, "3.emlx", "not a count line at all\n")

	src, err := OpenApple(dir)
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	listing, err := src.List(t.Context(), 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var got []string
	for _, m := range listing.Messages {
		got = append(got, m.Record(50).Subject)
	}
	if !equalStrings(got, []string{"Newer", "Older"}) {
		t.Fatalf("subjects = %q, want [Newer Older]", got)
	}
	if paths := skipPaths(listing.Skipped); !equalStrings(paths, []string{bad}) {
		t.Fatalf("skipped = %q, want %q", paths, []string{bad})
	}
}

// TestAppleLimitCountsOnlyReadableMessages pins the part of the contract that
// is easy to get wrong: a skip must not eat a slot, or asking for fifty
// messages over a store with one bad file quietly returns forty-nine.
func TestAppleLimitCountsOnlyReadableMessages(t *testing.T) {
	dir := t.TempDir()
	// The newest file parses as far as its count line and no further, so the
	// skip happens after the sort, where it can shorten the result.
	bad := writeEmlx(t, dir, "1.emlx", emlxBytes("no header here at all\n",
		time.Date(2024, time.March, 8, 12, 0, 0, 0, time.UTC)))
	writeEmlx(t, dir, "2.emlx", emlxBytes("From: a@example.test\nSubject: Newer\n\nbody\n",
		time.Date(2024, time.March, 6, 12, 0, 0, 0, time.UTC)))
	writeEmlx(t, dir, "3.emlx", emlxBytes("From: a@example.test\nSubject: Older\n\nbody\n",
		time.Date(2024, time.March, 4, 12, 0, 0, 0, time.UTC)))

	src, err := OpenApple(dir)
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	listing, err := src.List(t.Context(), 1)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listing.Messages) != 1 {
		t.Fatalf("List(1) returned %d messages, want 1", len(listing.Messages))
	}
	if got := listing.Messages[0].Record(50).Subject; got != "Newer" {
		t.Fatalf("subject = %q, want %q", got, "Newer")
	}
	if paths := skipPaths(listing.Skipped); !equalStrings(paths, []string{bad}) {
		t.Fatalf("skipped = %q, want %q", paths, []string{bad})
	}
}

func TestAppleFailsWhenTheStoreItselfCannotBeRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode, so this cannot be tested as root")
	}
	dir := t.TempDir()
	writeEmlx(t, dir, "1.emlx", emlxBytes("From: a@example.test\nSubject: One\n\nbody\n", time.Now()))
	src, err := OpenApple(dir)
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	messages := filepath.Join(dir, "Data", "Messages")
	if err := os.Chmod(messages, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(messages, 0o755) })
	if _, err := src.List(t.Context(), 0); err == nil {
		t.Fatal("List returned no error for an unreadable directory")
	}
}

func TestAppleFallsBackToModTimeWithoutDateReceived(t *testing.T) {
	dir := t.TempDir()
	msg := "From: a@example.test\nSubject: Undated\n\nhi\n"
	body := fmt.Sprintf("%-10d\n%s%s", len(msg), msg,
		"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<plist version=\"1.0\">\n<dict>\n<key>flags</key><integer>1</integer>\n</dict>\n</plist>\n")
	path := writeEmlx(t, dir, "1.emlx", body)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	src, err := OpenApple(dir)
	if err != nil {
		t.Fatalf("OpenApple: %v", err)
	}
	msgs := mustList(t, src, 0)
	if len(msgs) != 1 {
		t.Fatalf("List returned %d messages", len(msgs))
	}
	if !msgs[0].Received.Equal(info.ModTime()) {
		t.Fatalf("received %s, want the file's mod time %s", msgs[0].Received, info.ModTime())
	}
}

func TestPlistInt(t *testing.T) {
	const header = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`
	const footer = `</dict>
</plist>
`
	cases := []struct {
		name  string
		body  string
		want  int64
		found bool
		fails bool
	}{
		{
			name:  "present",
			body:  "<key>date-received</key>\n<integer>1700000000</integer>\n",
			want:  1700000000,
			found: true,
		},
		{
			name:  "absent",
			body:  "<key>flags</key>\n<integer>3</integer>\n",
			found: false,
		},
		{
			name:  "a nested dict does not shadow the top level",
			body:  "<key>parts</key>\n<dict><key>date-received</key><integer>111</integer></dict>\n<key>date-received</key>\n<integer>1700000000</integer>\n",
			want:  1700000000,
			found: true,
		},
		{
			name:  "only nested is not found",
			body:  "<key>parts</key>\n<dict><key>date-received</key><integer>111</integer></dict>\n",
			found: false,
		},
		{
			name:  "surrounding whitespace",
			body:  "<key>date-received</key>\n<integer> 1700000000 </integer>\n",
			want:  1700000000,
			found: true,
		},
		{
			name:  "a string value is not an integer",
			body:  "<key>date-received</key>\n<string>1700000000</string>\n",
			found: false,
		},
		{
			name:  "not a number",
			body:  "<key>date-received</key>\n<integer>soon</integer>\n",
			fails: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, found, err := plistInt([]byte(header+c.body+footer), "date-received")
			if c.fails {
				if err == nil {
					t.Fatal("plistInt returned no error")
				}
				return
			}
			if err != nil {
				t.Fatalf("plistInt: %v", err)
			}
			if found != c.found {
				t.Fatalf("found = %v, want %v", found, c.found)
			}
			if found && got != c.want {
				t.Fatalf("value = %d, want %d", got, c.want)
			}
		})
	}
}

func TestPlistIntRejectsBrokenXML(t *testing.T) {
	if _, _, err := plistInt([]byte("<plist><dict><key>date-received</key>"), "date-received"); err == nil {
		t.Fatal("plistInt returned no error for a truncated plist")
	}
}
