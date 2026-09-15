package sse

import "testing"

// TestSplitSSELines exercises splitSSELines directly against the WHATWG
// line-ending rule (CR, LF or CRLF) and the "ask for more data" contract a
// bufio.SplitFunc uses when a lone CR at the end of the buffer might still
// turn out to be half of a CRLF pair from the next read.
func TestSplitSSELines(t *testing.T) {
	const wantMore = -1 // sentinel: this case expects (0, nil, nil)

	cases := []struct {
		name        string
		data        string
		atEOF       bool
		wantAdvance int
		wantToken   string
	}{
		{"LF terminates a line", "abc\ndef", false, 4, "abc"},
		{"CRLF terminates a line", "abc\r\ndef", false, 5, "abc"},
		{"a lone CR mid-buffer terminates a line", "abc\rdef", false, 4, "abc"},
		{"CR at the end, not at EOF, asks for more data", "abc\r", false, wantMore, ""},
		{"CR at the end, at EOF, is a lone-CR line ending", "abc\r", true, 4, "abc"},
		{"no terminator, at EOF, yields the final token", "abc", true, 3, "abc"},
		{"no terminator, not at EOF, asks for more data", "abc", false, wantMore, ""},
		{"empty input at EOF signals done", "", true, wantMore, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			advance, token, err := splitSSELines([]byte(tc.data), tc.atEOF)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantAdvance == wantMore {
				if advance != 0 || token != nil {
					t.Fatalf("advance=%d token=%q, want a request for more data (0, nil)", advance, token)
				}
				return
			}
			if advance != tc.wantAdvance {
				t.Fatalf("advance = %d, want %d", advance, tc.wantAdvance)
			}
			if string(token) != tc.wantToken {
				t.Fatalf("token = %q, want %q", token, tc.wantToken)
			}
		})
	}
}

// TestSplitSSELines_CRLFStraddlesReads proves the case the table above
// only checks in isolation: a lone trailing CR is not split from a CRLF
// pair when the LF arrives in a later read. bufio.Scanner re-invokes the
// split function with more data appended to the same buffer, so this
// simulates that by feeding the CR first (not at EOF) and then the LF.
func TestSplitSSELines_CRLFStraddlesReads(t *testing.T) {
	advance, token, err := splitSSELines([]byte("line\r"), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if advance != 0 || token != nil {
		t.Fatalf("first call: advance=%d token=%q, want a request for more data", advance, token)
	}

	// The scanner would now read more, growing data to "line\r\nnext".
	advance, token, err = splitSSELines([]byte("line\r\nnext"), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if advance != 6 || string(token) != "line" {
		t.Fatalf("second call: advance=%d token=%q, want advance=6 token=%q", advance, token, "line")
	}
}
