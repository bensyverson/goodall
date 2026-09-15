package chat

import (
	"bytes"
	"testing"
)

// TestSSEFrameSplitsMultiLineData asserts the guard the SSE spec requires: a
// data payload carrying a newline becomes one "data:" line per segment, so a
// frame can never be truncated at the first newline. goodall's own event JSON
// is always one line, which is why this is tested here rather than through
// WriteSSE: the guard exists for the day an encoder stops being compact.
func TestSSEFrameSplitsMultiLineData(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSSEFrame(&buf, "note", []byte("{\n  \"a\": 1\n}")); err != nil {
		t.Fatalf("writeSSEFrame: %v", err)
	}
	const want = "event: note\ndata: {\ndata:   \"a\": 1\ndata: }\n\n"
	if buf.String() != want {
		t.Errorf("writeSSEFrame wrote\n%q\nwant\n%q", buf.String(), want)
	}
}
