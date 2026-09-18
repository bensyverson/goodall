package mailbox

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
	"unicode"
)

// Record is the filtered view of a message: everything a triage agent or a
// judge is shown, and nothing else. Building one is the privacy line, since
// the record is what leaves the process for a model.
type Record struct {
	// ID is the message's stable id, as [Message.ID].
	ID string
	// From is the parsed From header. When the header cannot be parsed as an
	// address, Name is empty and Address holds the raw header.
	From mail.Address
	// Subject is the Subject header with any RFC 2047 encoded words decoded;
	// a word in a charset the standard library cannot read is left as it was.
	Subject string
	// Date is the message's received time, the same value the source sorted on.
	Date time.Time
	// Snippet is the start of the message's text, cleaned and bounded.
	Snippet string
}

// Record builds the filtered view of the message, with the snippet cut to at
// most maxSnippet runes — zero or less leaves it uncut.
//
// The snippet is the first text/plain part of the message, walking nested
// multiparts; a text/html part is used only when there is no plain one, with
// its tags removed and its entities decoded. The part is decoded per its
// Content-Transfer-Encoding and, where the standard library can, its charset.
// Quoted reply lines, the "On … wrote:" attribution above them and everything
// from a "-- " signature separator on are dropped, then whitespace is
// collapsed and the text is cut on a word boundary with an ellipsis.
//
// No attachment is decoded: a part marked Content-Disposition: attachment, and
// any part whose media type is not text, is skipped without its body being
// read.
func (m Message) Record(maxSnippet int) Record {
	return Record{
		ID:      m.ID,
		From:    fromAddress(m.Header),
		Subject: decodeHeader(m.Header.Get("Subject")),
		Date:    m.Received,
		Snippet: snippet(m.Header, m.Body, maxSnippet),
	}
}

// fromAddress parses the From header, keeping the raw header when it is not an
// address the standard library recognizes — a triage agent still wants to see
// whatever the sender put there.
func fromAddress(h mail.Header) mail.Address {
	raw := strings.TrimSpace(h.Get("From"))
	if raw == "" {
		return mail.Address{}
	}
	if addr, err := mail.ParseAddress(raw); err == nil && addr != nil {
		return *addr
	}
	return mail.Address{Address: raw}
}

// decodeHeader decodes RFC 2047 encoded words, leaving the header alone when
// it uses a charset the standard library has no reader for.
func decodeHeader(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(s)
	if err != nil {
		return s
	}
	return decoded
}

// header is the part of mail.Header and textproto.MIMEHeader the MIME walk
// needs, so one walk covers the whole message and each of its parts.
type header interface{ Get(string) string }

const (
	// maxMIMEDepth bounds multipart nesting, so a message that nests itself
	// cannot spin the walk.
	maxMIMEDepth = 8
	// maxTextPartBytes bounds how much of one text part is read. Beyond this a
	// snippet cannot get any better, and a mail store is not a trusted input.
	maxTextPartBytes = 1 << 20
)

// snippet extracts, cleans and cuts the message's text.
func snippet(h mail.Header, body []byte, maxSnippet int) string {
	var f textFinder
	f.walk(h, body, 0)
	text, isHTML := f.plain, false
	if !f.havePlain {
		text, isHTML = f.html, true
	}
	if isHTML {
		text = stripHTML(text)
	}
	return cut(collapse(dropQuotesAndSignature(text)), maxSnippet)
}

// textFinder holds the best text found so far while walking a MIME tree: the
// first text/plain part wins outright, and the first text/html part is kept
// only in case no plain one turns up.
type textFinder struct {
	plain     string
	html      string
	havePlain bool
	haveHTML  bool
}

func (f *textFinder) walk(h header, body []byte, depth int) {
	if f.havePlain || depth > maxMIMEDepth {
		return
	}
	if isAttachment(h) {
		return
	}
	mediaType, params := contentType(h)
	if strings.HasPrefix(mediaType, "multipart/") {
		if boundary := params["boundary"]; boundary != "" {
			f.walkParts(body, boundary, depth)
			return
		}
		// A multipart header with no boundary cannot be walked, so fall
		// through and treat the bytes as text rather than losing them.
	} else if mediaType != "" && !strings.HasPrefix(mediaType, "text/") {
		return
	}
	text := decodeBody(h, body)
	if mediaType == "text/html" {
		if !f.haveHTML {
			f.html, f.haveHTML = text, true
		}
		return
	}
	f.plain, f.havePlain = text, true
}

// walkParts walks one multipart body. It uses NextRawPart so that nothing is
// decoded before the walk has decided whether the part may be read at all.
func (f *textFinder) walkParts(body []byte, boundary string, depth int) {
	r := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := r.NextRawPart()
		if err != nil {
			// io.EOF, or the truncated body of a .partial.emlx: whatever was
			// found before the break still stands.
			return
		}
		ph := textproto.MIMEHeader(part.Header)
		mediaType, _ := contentType(ph)
		readable := mediaType == "" || strings.HasPrefix(mediaType, "text/") || strings.HasPrefix(mediaType, "multipart/")
		if !readable || isAttachment(ph) {
			part.Close()
			continue
		}
		// A truncated part — the body of a .partial.emlx, say — still reads as
		// far as it goes, and what it holds is worth keeping.
		data, err := io.ReadAll(io.LimitReader(part, maxTextPartBytes))
		part.Close()
		if err != nil && len(data) == 0 {
			continue
		}
		f.walk(ph, data, depth+1)
		if f.havePlain {
			return
		}
	}
}

// contentType returns the lowercased media type and its parameters, treating
// an unparsable or missing header as no type at all.
func contentType(h header) (string, map[string]string) {
	raw := h.Get("Content-Type")
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	mediaType, params, err := mime.ParseMediaType(raw)
	if err != nil {
		return "", nil
	}
	return strings.ToLower(mediaType), params
}

// isAttachment reports whether the part is attached rather than displayed.
func isAttachment(h header) bool {
	raw := h.Get("Content-Disposition")
	if strings.TrimSpace(raw) == "" {
		return false
	}
	disposition, _, err := mime.ParseMediaType(raw)
	if err != nil {
		// An unparsable disposition that names an attachment is still one.
		return strings.Contains(strings.ToLower(raw), "attachment")
	}
	return strings.EqualFold(disposition, "attachment")
}

// decodeBody undoes the part's transfer encoding and charset. A body that
// fails to decode is kept as it was: a mangled snippet tells a reader more
// than an empty one.
func decodeBody(h header, body []byte) string {
	switch strings.ToLower(strings.TrimSpace(h.Get("Content-Transfer-Encoding"))) {
	case "quoted-printable":
		if decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body))); err == nil {
			body = decoded
		}
	case "base64":
		if decoded, ok := decodeBase64(body); ok {
			body = decoded
		}
	}
	_, params := contentType(h)
	return decodeCharset(body, params["charset"])
}

// decodeBase64 decodes a body whose base64 is wrapped across lines.
func decodeBase64(body []byte) ([]byte, bool) {
	packed := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, string(body))
	if decoded, err := base64.StdEncoding.DecodeString(packed); err == nil {
		return decoded, true
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(packed); err == nil {
		return decoded, true
	}
	return nil, false
}

// decodeCharset converts the charsets the standard library covers. Anything
// else is returned as its bytes: the snippet may show mojibake, which is
// visibly wrong, rather than nothing at all, which is silently wrong.
func decodeCharset(body []byte, charset string) string {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(charset), `"`)) {
	// Windows-1252 is read as ISO-8859-1, which is exact above 0x9F and turns
	// the handful of bytes below it — smart quotes, mostly — into C1 controls
	// that the whitespace collapse then drops.
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "cp819", "windows-1252":
		var b strings.Builder
		b.Grow(len(body))
		for _, c := range body {
			b.WriteRune(rune(c))
		}
		return b.String()
	default:
		return string(body)
	}
}
