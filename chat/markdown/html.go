package markdown

import "strings"

// escapeTo writes s to b with every character that carries meaning in HTML
// escaped. One function serves both text nodes and quoted attribute values:
// a renderer that escaped the two positions differently would make the caller
// choose, and the wrong choice is a hole rather than a typo.
func escapeTo(b *strings.Builder, s string) {
	for i := range len(s) {
		switch c := s[i]; c {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteByte(c)
		}
	}
}

// escape returns s with every character that carries meaning in HTML escaped.
func escape(s string) string {
	var b strings.Builder
	escapeTo(&b, s)
	return b.String()
}

// urlContext says where a destination is about to be used. The schemes that
// are safe differ between the two positions, so the check cannot be written
// once without knowing which one it is answering for.
type urlContext int

const (
	// linkHref is an <a href>. Only http and https are allowed: every other
	// scheme either executes (javascript, vbscript), reaches outside the page
	// (file), or carries its own content (data).
	linkHref urlContext = iota
	// imageSrc is an <img src>. http, https and data URLs that declare an
	// image media type are allowed; a browser renders an image without
	// running script in it, even an SVG one.
	imageSrc
)

// safeURL cleans raw and reports whether it may be used in ctx. The returned
// string is ready to be escaped into an attribute value; when ok is false the
// caller drops the destination entirely rather than emitting a dead link.
func safeURL(raw string, ctx urlContext) (string, bool) {
	u := stripControls(raw)
	if u == "" {
		return "", false
	}
	switch urlScheme(u) {
	case "http", "https":
		return percentEncode(u), true
	case "data":
		if ctx == imageSrc && isImageDataURL(u) {
			return percentEncode(u), true
		}
	}
	return "", false
}

// stripControls removes ASCII control characters, including tabs and newlines,
// and trims surrounding whitespace. Browsers ignore those characters when they
// resolve a scheme, so "java\tscript:" navigates and a check that did not strip
// them first would pass it through.
func stripControls(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		if c := s[i]; c == ' ' || (c > 0x20 && c != 0x7f) {
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String())
}

// urlScheme returns the lower-cased scheme of u, or "" when u has none. A
// destination with no scheme — a relative path, a bare host, a protocol
// relative "//host" — returns "" and is therefore never linked.
func urlScheme(u string) string {
	for i := range len(u) {
		c := u[i]
		switch {
		case c == ':':
			if i == 0 {
				return ""
			}
			return strings.ToLower(u[:i])
		case isASCIILetter(c):
		case i > 0 && (isASCIIDigit(c) || c == '+' || c == '-' || c == '.'):
		default:
			return ""
		}
	}
	return ""
}

// isImageDataURL reports whether a data: URL declares an image media type.
// The media type runs from the colon to the first ";" or ",".
func isImageDataURL(u string) bool {
	_, rest, _ := strings.Cut(u, ":")
	if i := strings.IndexAny(rest, ";,"); i >= 0 {
		rest = rest[:i]
	}
	const prefix = "image/"
	return len(rest) > len(prefix) && strings.EqualFold(rest[:len(prefix)], prefix)
}

// percentEncode encodes the characters that would end an attribute value early
// or start markup of their own. The rest of the URL is left alone so that it
// still resolves; "&" in particular survives here and is escaped to "&amp;"
// when the value is written.
func percentEncode(u string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(u))
	for i := range len(u) {
		switch c := u[i]; c {
		case ' ', '"', '<', '>', '`':
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isASCIILetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// isSpaceByte reports whether c is whitespace that can appear inside a block.
func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' }

// isWordByte reports whether c can be part of a word, which decides whether an
// underscore sits inside one. Every non-ASCII byte counts, so emphasis behaves
// the same in the middle of an accented or non-Latin word as in an ASCII one.
func isWordByte(c byte) bool {
	return isASCIILetter(c) || isASCIIDigit(c) || c >= 0x80
}

// runLength counts the consecutive bytes equal to c starting at i.
func runLength(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}
