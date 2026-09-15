package sse

import (
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// eventsEqual compares two Event slices field by field; Event has no
// slices or maps, so == would do, but a helper gives better failure output.
func eventsEqual(a, b []Event) bool {
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

func collect(t *testing.T, r io.Reader) []Event {
	t.Helper()
	var got []Event
	for ev, err := range Events(r) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = append(got, ev)
	}
	return got
}

func TestEvents_Table(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []Event
	}{
		{
			name:  "LF line endings",
			input: "event: greeting\ndata: hello\n\n",
			want:  []Event{{Type: "greeting", Data: "hello"}},
		},
		{
			name:  "CRLF line endings",
			input: "event: greeting\r\ndata: hello\r\n\r\n",
			want:  []Event{{Type: "greeting", Data: "hello"}},
		},
		{
			name:  "lone CR line endings",
			input: "event: greeting\rdata: hello\r\r",
			want:  []Event{{Type: "greeting", Data: "hello"}},
		},
		{
			name:  "comment lines are ignored",
			input: ": OPENROUTER PROCESSING\ndata: hi\n\n",
			want:  []Event{{Type: "message", Data: "hi"}},
		},
		{
			name:  "multi-line data is joined with newline",
			input: "data: line one\ndata: line two\n\n",
			want:  []Event{{Type: "message", Data: "line one\nline two"}},
		},
		{
			name:  "data field with no space after the colon",
			input: "data:no-space\n\n",
			want:  []Event{{Type: "message", Data: "no-space"}},
		},
		{
			name:  "bare data field with no colon at all",
			input: "data\ndata: after\n\n",
			want:  []Event{{Type: "message", Data: "\nafter"}},
		},
		{
			name:  "missing blank line dispatches the pending event before the next",
			input: "event: a\ndata: 1\nevent: b\ndata: 2\n\n",
			want: []Event{
				{Type: "a", Data: "1"},
				{Type: "b", Data: "2"},
			},
		},
		{
			name:  "a blank line with no pending data yields nothing",
			input: "\n\ndata: hi\n\n",
			want:  []Event{{Type: "message", Data: "hi"}},
		},
		{
			name:  "an id containing NUL is ignored",
			input: "id: ab\x00cd\ndata: hi\n\n",
			want:  []Event{{Type: "message", Data: "hi", ID: ""}},
		},
		{
			name:  "an id is set",
			input: "id: 42\ndata: hi\n\n",
			want:  []Event{{Type: "message", Data: "hi", ID: "42"}},
		},
		{
			name:  "id does not carry forward to the next event",
			input: "id: 42\ndata: 1\n\ndata: 2\n\n",
			want: []Event{
				{Type: "message", Data: "1", ID: "42"},
				{Type: "message", Data: "2", ID: ""},
			},
		},
		{
			name:  "a valid all-digit retry sets the reconnection time",
			input: "retry: 1500\ndata: hi\n\n",
			want:  []Event{{Type: "message", Data: "hi", Retry: 1500 * time.Millisecond}},
		},
		{
			name:  "a non-numeric retry is ignored",
			input: "retry: soon\ndata: hi\n\n",
			want:  []Event{{Type: "message", Data: "hi"}},
		},
		{
			name:  "an unknown field is ignored",
			input: "banana: yellow\ndata: hi\n\n",
			want:  []Event{{Type: "message", Data: "hi"}},
		},
		{
			name:  "a leading BOM is stripped",
			input: "\xEF\xBB\xBF" + "data: hi\n\n",
			want:  []Event{{Type: "message", Data: "hi"}},
		},
		{
			name:  "EOF with pending data discards it instead of yielding a partial event",
			input: "event: a\ndata: unterminated",
			want:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collect(t, strings.NewReader(tc.input))
			if !eventsEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestEvents_CRLFSplitAcrossReads(t *testing.T) {
	// iotest.OneByteReader forces every Read to return a single byte, so
	// the CRLF pair after "hi" is guaranteed to straddle two reads.
	input := "data: hi\r\n\r\n"
	r := iotest.OneByteReader(strings.NewReader(input))

	got := collect(t, r)
	want := []Event{{Type: "message", Data: "hi"}}
	if !eventsEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestEvents_LargeDataLine(t *testing.T) {
	const size = 5 * 1024 * 1024
	payload := strings.Repeat("a", size)
	wantSum := crc32.ChecksumIEEE([]byte(payload))

	input := "data: " + payload + "\n\n"
	got := collect(t, strings.NewReader(input))

	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if len(got[0].Data) != size {
		t.Fatalf("data length = %d, want %d", len(got[0].Data), size)
	}
	if sum := crc32.ChecksumIEEE([]byte(got[0].Data)); sum != wantSum {
		t.Fatalf("checksum mismatch: got %d, want %d", sum, wantSum)
	}
}

// errAfterReader delivers r's bytes normally, then substitutes err for the
// io.EOF that r would otherwise return.
type errAfterReader struct {
	r   io.Reader
	err error
}

func (e *errAfterReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err == io.EOF {
		return n, e.err
	}
	return n, err
}

func TestEvents_ReadErrorYieldedOnce(t *testing.T) {
	wantErr := errors.New("boom")
	r := &errAfterReader{r: strings.NewReader("data: hi\n\n"), err: wantErr}

	var got []Event
	var gotErr error
	yields := 0
	for ev, err := range Events(r) {
		yields++
		if err != nil {
			if gotErr != nil {
				t.Fatalf("error yielded more than once")
			}
			gotErr = err
			continue
		}
		got = append(got, ev)
	}

	if yields != 2 {
		t.Fatalf("iterator yielded %d times, want 2 (one event, one error)", yields)
	}
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("got error %v, want %v", gotErr, wantErr)
	}
	if len(got) != 1 || got[0].Data != "hi" {
		t.Fatalf("got events %#v, want one event with data %q", got, "hi")
	}
}

// countingReader tracks how many bytes have actually been pulled from the
// underlying reader.
type countingReader struct {
	r     io.Reader
	total int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.total += n
	return n, err
}

func TestEvents_EarlyBreakStopsReading(t *testing.T) {
	var b strings.Builder
	const eventCount = 40
	for range eventCount {
		fmt.Fprintf(&b, "data: %s\n\n", strings.Repeat("x", 200))
	}
	input := b.String()

	cr := &countingReader{r: strings.NewReader(input)}
	count := 0
	for ev, err := range Events(cr) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_ = ev
		count++
		break
	}

	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if cr.total >= len(input) {
		t.Fatalf("read %d of %d bytes; breaking early did not stop reading", cr.total, len(input))
	}
}

func TestEvents_LineTooLongYieldsError(t *testing.T) {
	long := strings.Repeat("a", 200)
	input := "data: " + long + "\n\n"

	var got []Event
	var gotErr error
	for ev, err := range eventsWithLimit(strings.NewReader(input), 64) {
		if err != nil {
			gotErr = err
			continue
		}
		got = append(got, ev)
	}

	if gotErr == nil {
		t.Fatal("want an error for a line longer than the cap, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("got %d events before the error, want 0", len(got))
	}
}
