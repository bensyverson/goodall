package mailbox

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Mbox reads a single mbox file: messages separated by a From_ line, which is
// a line beginning "From " at the start of the file or after a blank line.
// Because that separator can also be a body line, mbox writers quote such
// lines with a ">", and this reader puts them back.
type Mbox struct {
	path string
}

// maxMboxHeaderBytes bounds the header block read while scanning for dates.
const maxMboxHeaderBytes = 1 << 16

// OpenMbox opens the mbox file at path.
func OpenMbox(path string) (*Mbox, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("mailbox: mbox %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("mailbox: mbox %s is a directory", path)
	}
	return &Mbox{path: path}, nil
}

// mboxEntry is one message located in the file: where it starts and ends, and
// when it arrived, all learned without reading a body.
type mboxEntry struct {
	start    int64 // the From_ line
	body     int64 // the first byte of the message itself
	end      int64
	received time.Time
}

// List lists the file's messages newest first by the Date header, falling back
// to the date on the From_ line and then to the file's modification time. Only
// the messages that survive the limit are read and parsed.
//
// A message whose header will not parse — a truncated or hand-edited file —
// is reported in the listing's Skipped, named by its offset, and the listing
// goes on. A file that cannot be opened or read is returned as an error.
func (b *Mbox) List(ctx context.Context, limit int) (Listing, error) {
	if err := ctx.Err(); err != nil {
		return Listing{}, err
	}
	entries, err := b.scan(ctx)
	if err != nil {
		return Listing{}, err
	}
	file, err := os.Open(b.path)
	if err != nil {
		return Listing{}, fmt.Errorf("mailbox: %s: %w", b.path, err)
	}
	defer file.Close()

	items := make([]listed, 0, len(entries))
	for _, e := range entries {
		at := fmt.Sprintf("%s:%d", b.path, e.start)
		items = append(items, listed{
			path:     at,
			received: e.received,
			key:      fmt.Sprintf("%020d", e.start),
			read: func() ([]byte, error) {
				raw := make([]byte, e.end-e.body)
				if _, err := file.ReadAt(raw, e.body); err != nil {
					return nil, fmt.Errorf("mailbox: %s: %w", at, err)
				}
				return unquoteFrom(raw), nil
			},
		})
	}
	return collect(ctx, items, limit, nil)
}

// scan walks the file once, recording where each message starts and ends and
// parsing only its header block for a date.
func (b *Mbox) scan(ctx context.Context) ([]mboxEntry, error) {
	file, err := os.Open(b.path)
	if err != nil {
		return nil, fmt.Errorf("mailbox: %s: %w", b.path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("mailbox: %s: %w", b.path, err)
	}

	reader := bufio.NewReader(file)
	var (
		entries  []mboxEntry
		current  *mboxEntry
		fromLine string
		headers  bytes.Buffer
		inHeader bool
		offset   int64
		afterGap = true // the start of the file separates like a blank line does
	)
	finish := func(end int64) {
		if current == nil {
			return
		}
		current.end = end
		current.received = mboxReceived(headers.Bytes(), fromLine, info.ModTime())
		entries = append(entries, *current)
		current, headers = nil, bytes.Buffer{}
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, readErr := reader.ReadBytes('\n')
		lineStart := offset
		offset += int64(len(line))
		if len(line) > 0 {
			trimmed := trimEOL(line)
			switch {
			case afterGap && bytes.HasPrefix(line, []byte("From ")):
				finish(lineStart)
				current = &mboxEntry{start: lineStart, body: offset}
				fromLine = string(trimmed)
				headers = bytes.Buffer{}
				inHeader = true
			case current != nil && inHeader:
				if len(trimmed) == 0 {
					inHeader = false
				} else if headers.Len() < maxMboxHeaderBytes {
					headers.Write(line)
				}
			}
			afterGap = len(trimmed) == 0
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return nil, fmt.Errorf("mailbox: %s: %w", b.path, readErr)
			}
			break
		}
	}
	finish(offset)
	return entries, nil
}

// mboxReceived takes the message's date from its Date header, or from the
// From_ line, or from the file itself, in that order.
func mboxReceived(headers []byte, fromLine string, modTime time.Time) time.Time {
	if h, err := headerOnly(headers); err == nil {
		if when, err := h.Date(); err == nil {
			return when
		}
	}
	if when, ok := fromLineDate(fromLine); ok {
		return when
	}
	return modTime
}

// fromLineDates are the shapes a From_ line's date takes. It has no time zone
// in its usual form, so it is read as UTC.
var fromLineDates = []string{
	"Mon Jan 2 15:04:05 2006",
	"Mon Jan 2 15:04:05 MST 2006",
	"Mon Jan 2 15:04:05 -0700 2006",
	"Mon Jan 2 15:04:05 2006 MST",
}

// fromLineDate reads the date out of "From <address> <date>".
func fromLineDate(line string) (time.Time, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 || fields[0] != "From" {
		return time.Time{}, false
	}
	date := strings.Join(fields[2:], " ")
	for _, layout := range fromLineDates {
		if when, err := time.Parse(layout, date); err == nil {
			return when.UTC(), true
		}
	}
	return time.Time{}, false
}

// unquoteFrom reverses the quoting an mbox writer applies to body lines that
// would otherwise look like a separator: one leading ">" comes off any line
// matching ">+From ".
func unquoteFrom(raw []byte) []byte {
	if !bytes.Contains(raw, []byte(">From ")) {
		return raw
	}
	var out bytes.Buffer
	out.Grow(len(raw))
	rest := raw
	for len(rest) > 0 {
		line := rest
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i+1], rest[i+1:]
		} else {
			rest = nil
		}
		if quoted := bytes.TrimLeft(line, ">"); len(quoted) < len(line) && bytes.HasPrefix(quoted, []byte("From ")) {
			line = line[1:]
		}
		out.Write(line)
	}
	return out.Bytes()
}
