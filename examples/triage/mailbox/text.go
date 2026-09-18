package mailbox

import (
	"html"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// dropQuotesAndSignature removes the parts of a reply that say nothing new:
// quoted lines, the attribution line that introduces them, and everything from
// a signature separator on. What is left is what this sender actually wrote,
// which is what a triage judgment is about.
func dropQuotesAndSignature(text string) string {
	var kept []string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if isSignatureSeparator(line) {
			break
		}
		if isQuoted(line) {
			kept = dropAttribution(kept)
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// isQuoted reports whether the line is quoted from an earlier message.
func isQuoted(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), ">")
}

// isSignatureSeparator reports whether the line is the "-- " that convention
// puts above a signature. The trailing space is optional in the wild.
func isSignatureSeparator(line string) bool {
	return strings.TrimRight(line, " \t\r") == "--"
}

// maxAttributionLines is how far back an "On … wrote:" attribution may be
// looked for: mail clients wrap it, but never far.
const maxAttributionLines = 3

// dropAttribution removes the "On … wrote:" line, wrapped or not, that sits
// above a quoted block.
func dropAttribution(kept []string) []string {
	end := len(kept)
	for end > 0 && strings.TrimSpace(kept[end-1]) == "" {
		end--
	}
	for n := 1; n <= maxAttributionLines && n <= end; n++ {
		joined := strings.ToLower(strings.Join(strings.Fields(strings.Join(kept[end-n:end], " ")), " "))
		if strings.HasPrefix(joined, "on ") && strings.HasSuffix(joined, "wrote:") {
			return kept[:end-n]
		}
	}
	return kept
}

// collapse folds every run of whitespace into one space and drops control
// characters, so a snippet is one line whatever the message's wrapping was.
// A byte that is not valid UTF-8 is passed through rather than replaced: it
// came from a charset the package could not read, and showing it is how a
// reader finds that out.
func collapse(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	pendingSpace := false
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size <= 1 {
			b.WriteByte(text[i])
			pendingSpace = false
			i++
			continue
		}
		i += size
		switch {
		case unicode.IsSpace(r):
			pendingSpace = b.Len() > 0
		case unicode.IsControl(r):
			// Dropped: a control character is not something a reader sees.
		default:
			if pendingSpace {
				b.WriteByte(' ')
				pendingSpace = false
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cut shortens text to at most max runes, ending on a word boundary and an
// ellipsis. A max of zero or less leaves the text alone. A single word longer
// than the limit is cut mid-word, because the alternative is an empty snippet.
func cut(text string, max int) string {
	if max <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	head := runes[:max-1]
	if i := lastIndexRune(head, ' '); i > 0 {
		head = head[:i]
	}
	return strings.TrimRight(string(head), " ") + "…"
}

func lastIndexRune(runes []rune, want rune) int {
	for i, rune := range slices.Backward(runes) {
		if rune == want {
			return i
		}
	}
	return -1
}

// blockTags are the tags whose boundaries are line breaks in the text a reader
// would see; every other tag becomes a space so words either side stay apart.
var blockTags = map[string]bool{
	"address": true, "article": true, "blockquote": true, "br": true,
	"div": true, "dt": true, "dd": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "hr": true, "li": true, "ol": true,
	"p": true, "pre": true, "section": true, "table": true, "td": true,
	"th": true, "tr": true, "ul": true,
}

// skippedTags are the elements whose contents are not text a reader sees.
var skippedTags = map[string]bool{"script": true, "style": true, "head": true}

// stripHTML turns an HTML body into the text a reader would see: markup
// removed, script and style contents dropped, entities decoded. It is a
// deliberately small scanner rather than a parser — a snippet needs the words,
// not the document.
func stripHTML(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '<' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			break
		}
		tag := strings.TrimSpace(s[i+1 : i+end])
		name := tagName(tag)
		i += end + 1
		// Skip to the element's own closing tag, which the next turn of the
		// loop then consumes like any other.
		if !strings.HasPrefix(tag, "/") && skippedTags[name] {
			at := strings.Index(strings.ToLower(s[i:]), "</"+name)
			if at < 0 {
				break
			}
			i += at
			continue
		}
		if blockTags[name] {
			b.WriteByte('\n')
		} else {
			b.WriteByte(' ')
		}
	}
	return html.UnescapeString(b.String())
}

// tagName returns a tag's lowercased name, with any closing slash and
// attributes removed.
func tagName(tag string) string {
	tag = strings.TrimPrefix(strings.TrimSpace(tag), "/")
	end := strings.IndexFunc(tag, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if end >= 0 {
		tag = tag[:end]
	}
	return strings.ToLower(tag)
}
