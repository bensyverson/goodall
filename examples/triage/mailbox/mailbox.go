// Package mailbox reads mail out of the stores the triage example runs on:
// Apple Mail's on-disk store, a Maildir, and an mbox file. It is read-only —
// nothing here writes, moves or flags a message.
//
// A [Source] lists messages newest first. A [Message] carries the parsed
// header, the raw body bytes and the time the store says the message arrived.
// [Message.Record] turns one into a [Record]: the sender, the subject, the
// date and a bounded snippet of the text body, which is all the triage agent
// and its judge are ever shown. Quoted replies, signatures and HTML markup are
// dropped on the way, and attachments are never decoded.
//
// Listing is cheap by design: each reader works out every message's received
// time without parsing a body, sorts, applies the limit, and only then reads
// the messages that survived. An Apple Mail inbox of several thousand messages
// therefore costs a few small reads per file rather than a full parse.
//
// Listing is also tolerant, and says so. A mail store is a live directory —
// Mail writes .emlx files while it runs, an mbox can be truncated, a delivery
// can land as something that is not a message — so one file that will not read
// must not cost a reader the other thousands. [Source.List] therefore returns a
// [Listing]: the messages that read, and a [Skip] for every file that did not,
// each naming the path and the reason. The error return is kept for the store
// itself failing: a directory that cannot be read, a file that cannot be
// opened, a canceled context. A caller that ignores Skipped has silently
// accepted a short list, so print it or count it.
package mailbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/mail"
	"slices"
	"strings"
	"time"
)

// Kind names a mail store format, so a caller can take one from a flag or a
// config file and hand it to [Open] without a switch of its own.
type Kind string

// The store formats the package reads.
const (
	// KindApple is Apple Mail's store: a tree of .emlx files under
	// ~/Library/Mail/V10.
	KindApple Kind = "apple"
	// KindMaildir is a Maildir: one file per message under cur and new.
	KindMaildir Kind = "maildir"
	// KindMbox is a single mbox file with From_ separator lines.
	KindMbox Kind = "mbox"
)

// Source lists the messages in one mail store.
type Source interface {
	// List returns the store's messages, newest received first, at most limit
	// of them. A limit of zero or less returns every message. Messages with
	// the same received time come back in a stable order.
	//
	// A file the reader cannot read or parse is reported in the listing's
	// Skipped rather than returned as an error, so one damaged message costs
	// that message and not the listing. The error return is for the store
	// failing: a directory that cannot be read, a file that cannot be opened,
	// a canceled context.
	List(ctx context.Context, limit int) (Listing, error)
}

// Listing is what a store returned: the messages that read, and the files that
// did not. Skipped is empty on a clean listing, and a caller that ignores it is
// throwing away the only notice that a message is missing.
type Listing struct {
	// Messages are the messages that read, newest received first.
	Messages []Message
	// Skipped names each file that could not be read or parsed, in the order
	// the reader met them.
	Skipped []Skip
}

// Skip is one file a listing passed over, and why.
type Skip struct {
	// Path is the file, or "file:offset" for a message inside an mbox.
	Path string
	// Err is what went wrong, and names the path.
	Err error
}

// Open opens the store at path with the reader for kind. The path means what
// that reader means by it: a store root, folder or mailbox directory for
// [KindApple], a Maildir directory for [KindMaildir], a file for [KindMbox].
func Open(kind Kind, path string) (Source, error) {
	switch kind {
	case KindApple:
		src, err := OpenApple(path)
		if err != nil {
			return nil, err
		}
		return src, nil
	case KindMaildir:
		src, err := OpenMaildir(path)
		if err != nil {
			return nil, err
		}
		return src, nil
	case KindMbox:
		src, err := OpenMbox(path)
		if err != nil {
			return nil, err
		}
		return src, nil
	default:
		return nil, fmt.Errorf("mailbox: unknown kind %q, want one of %q, %q or %q", kind, KindApple, KindMaildir, KindMbox)
	}
}

// Message is one message read out of a store: its header, its undecoded body,
// and where and when it came from. The body is held as the store wrote it —
// still transfer-encoded, still carrying any attachments — because deciding
// what may be decoded is [Message.Record]'s job.
type Message struct {
	// ID is stable across runs: the Message-ID header without its angle
	// brackets, or, when the message carries none, a hash of Path and
	// Received.
	ID string
	// Received is when the store says the message arrived. Each reader
	// documents where it gets this.
	Received time.Time
	// Path is the file the message came from, or "file:offset" for a message
	// inside an mbox.
	Path string
	// Header is the parsed header, with the raw, possibly RFC 2047 encoded
	// values as they appeared on the wire.
	Header mail.Header
	// Body is everything after the header's blank line, undecoded.
	Body []byte
}

// parseMessage parses raw into a Message received at the given time.
func parseMessage(raw []byte, path string, received time.Time) (Message, error) {
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return Message{}, fmt.Errorf("mailbox: %s: %w", path, err)
	}
	body, err := io.ReadAll(parsed.Body)
	if err != nil {
		return Message{}, fmt.Errorf("mailbox: %s: %w", path, err)
	}
	return Message{
		ID:       messageID(parsed.Header, path, received),
		Received: received,
		Path:     path,
		Header:   parsed.Header,
		Body:     body,
	}, nil
}

// messageID prefers the Message-ID header and falls back to a hash, so that a
// message with no id still keeps the same id from one run to the next — the
// triage agent addresses messages by it.
func messageID(h mail.Header, path string, received time.Time) string {
	if id := strings.TrimSpace(h.Get("Message-ID")); id != "" {
		id = strings.TrimSuffix(strings.TrimPrefix(id, "<"), ">")
		if id = strings.TrimSpace(id); id != "" {
			return id
		}
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d", path, received.UnixNano()))
	return hex.EncodeToString(sum[:16])
}

// headerOnly parses a header block that may not be followed by a blank line,
// which is how both the mbox and the Maildir readers learn a message's date
// without reading its body.
func headerOnly(block []byte) (mail.Header, error) {
	parsed, err := mail.ReadMessage(io.MultiReader(bytes.NewReader(block), strings.NewReader("\r\n\r\n")))
	if err != nil {
		return nil, err
	}
	return parsed.Header, nil
}

// sortNewestFirst sorts items by the time at returns, newest first, breaking
// ties on the string it returns so two runs over one store agree.
func sortNewestFirst[T any](items []T, at func(T) (time.Time, string)) {
	slices.SortStableFunc(items, func(a, b T) int {
		ta, ka := at(a)
		tb, kb := at(b)
		if d := tb.Compare(ta); d != 0 {
			return d
		}
		return strings.Compare(ka, kb)
	})
}

// listed is one message a reader has located but not yet read: where it is,
// when it arrived, and how to get its bytes.
type listed struct {
	path     string
	received time.Time
	// key breaks ties between messages received at the same moment. It is the
	// path for a store of files, and a zero-padded offset inside an mbox,
	// where comparing the paths would order message 100 before message 99.
	key  string
	read func() ([]byte, error)
}

// collect sorts located messages newest first and reads them in order until
// limit of them are in hand, so a message that will not read costs a Skip and
// the next candidate takes its place rather than the listing coming up short.
func collect(ctx context.Context, items []listed, limit int, skipped []Skip) (Listing, error) {
	sortNewestFirst(items, func(l listed) (time.Time, string) { return l.received, l.key })
	listing := Listing{Skipped: skipped}
	for _, item := range items {
		if limit > 0 && len(listing.Messages) == limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return Listing{}, err
		}
		raw, err := item.read()
		if err == nil {
			var msg Message
			if msg, err = parseMessage(raw, item.path, item.received); err == nil {
				listing.Messages = append(listing.Messages, msg)
				continue
			}
		}
		listing.Skipped = append(listing.Skipped, Skip{Path: item.path, Err: err})
	}
	return listing, nil
}

// trimEOL strips one line ending, so the readers can compare lines without
// caring whether the store uses CRLF or LF.
func trimEOL(line []byte) []byte {
	line = bytes.TrimSuffix(line, []byte("\n"))
	return bytes.TrimSuffix(line, []byte("\r"))
}
