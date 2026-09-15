package markdown

import "strings"

// renderInline renders the text of one block. Trailing spaces at the very end
// of a block are dropped, so only a break between two lines can be hard.
func renderInline(src string) string {
	in := inliner{src: strings.TrimRight(src, " \t")}
	in.b.Grow(len(in.src) + len(in.src)/4)
	in.run()
	return in.b.String()
}

// inliner scans the text of one block once, left to right.
//
// It carries the failure memos that keep that scan linear. Every closing
// delimiter this package looks for is recognised from its own surroundings,
// never from where the search began, so a search that reaches the end of the
// text without finding one proves that no later opener of the same shape can
// find one either. Without the memos an input like "*a *a *a ..." makes every
// opener rescan the whole remainder.
type inliner struct {
	src string
	b   strings.Builder

	backtickFail map[int]bool // code-span run length with no closing run
	emphFail     [2][2]bool   // [* or _][single or double] has no closer
	bracketFail  bool         // no "]" remains
	parenFail    bool         // no ")" remains
}

func (in *inliner) run() {
	for i := 0; i < len(in.src); {
		n := 0
		switch c := in.src[i]; c {
		case '`':
			n = in.codeSpan(i)
		case '!':
			n = in.image(i)
		case '[':
			n = in.link(i)
		case '<':
			n = in.autolink(i)
		case '*', '_':
			n = in.emphasis(i)
		case '\\':
			n = in.backslashBreak(i)
		case ' ':
			n = in.spaceBreak(i)
		case '\n':
			in.b.WriteByte('\n')
			n = 1
		}
		if n == 0 {
			// Nothing claimed this byte, so it is literal text.
			escapeTo(&in.b, in.src[i:i+1])
			n = 1
		}
		i += n
	}
}

// spaceBreak handles the run of spaces at the end of a line: two or more make
// the break hard. It reports 0 when the spaces are ordinary text.
func (in *inliner) spaceBreak(i int) int {
	j := i
	for j < len(in.src) && in.src[j] == ' ' {
		j++
	}
	if j >= len(in.src) || in.src[j] != '\n' {
		return 0
	}
	if j-i >= 2 {
		in.b.WriteString("<br>\n")
	} else {
		in.b.WriteByte('\n')
	}
	return j - i + 1
}

// backslashBreak handles a backslash at the end of a line, the other spelling
// of a hard break. A backslash anywhere else is literal: this subset has no
// backslash escapes, so what a model writes is what a reader sees.
func (in *inliner) backslashBreak(i int) int {
	if i+1 < len(in.src) && in.src[i+1] == '\n' {
		in.b.WriteString("<br>\n")
		return 2
	}
	return 0
}

// codeSpan renders a backtick span. The closing run must be the same length as
// the opening one, which is how a span containing a backtick is written.
func (in *inliner) codeSpan(i int) int {
	n := runLength(in.src, i, '`')
	if in.backtickFail[n] {
		return 0
	}
	for j := i + n; j < len(in.src); {
		if in.src[j] != '`' {
			j++
			continue
		}
		m := runLength(in.src, j, '`')
		if m == n {
			in.b.WriteString("<code>")
			escapeTo(&in.b, codeSpanContent(in.src[i+n:j]))
			in.b.WriteString("</code>")
			return j + m - i
		}
		j += m
	}
	if in.backtickFail == nil {
		in.backtickFail = make(map[int]bool)
	}
	in.backtickFail[n] = true
	return 0
}

// codeSpanContent folds the line breaks inside a span into spaces and strips
// the one pair of padding spaces that lets a span start or end with a backtick.
func codeSpanContent(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) >= 2 && s[0] == ' ' && s[len(s)-1] == ' ' && strings.TrimSpace(s) != "" {
		s = s[1 : len(s)-1]
	}
	return s
}

// emphasis renders a run of "*" or "_". A run of one is emphasis and a run of
// two or more is strong; three or more do not nest, they are simply strong.
func (in *inliner) emphasis(i int) int {
	c := in.src[i]
	n := runLength(in.src, i, c)
	strong := n >= 2
	char, weight := 0, 0
	if c == '_' {
		char = 1
	}
	if strong {
		weight = 1
	}
	if in.emphFail[char][weight] {
		return 0
	}
	// An opener has text after it, and an underscore one does not sit inside
	// a word, so identifiers like snake_case_names survive intact.
	after := i + n
	if after >= len(in.src) || isSpaceByte(in.src[after]) {
		return 0
	}
	if c == '_' && i > 0 && isWordByte(in.src[i-1]) {
		return 0
	}
	for j := after; j < len(in.src); {
		if in.src[j] != c {
			j++
			continue
		}
		m := runLength(in.src, j, c)
		if in.closesEmphasis(j, m, c, strong) {
			tag := "em"
			if strong {
				tag = "strong"
			}
			in.b.WriteByte('<')
			in.b.WriteString(tag)
			in.b.WriteByte('>')
			in.b.WriteString(renderInline(in.src[after:j]))
			in.b.WriteString("</")
			in.b.WriteString(tag)
			in.b.WriteByte('>')
			return j + m - i
		}
		j += m
	}
	in.emphFail[char][weight] = true
	return 0
}

// closesEmphasis reports whether the run of m delimiters at j closes an open
// run. It reads only the text around j, which is what makes the failure memo
// in [inliner] sound.
func (in *inliner) closesEmphasis(j, m int, c byte, strong bool) bool {
	if strong != (m >= 2) {
		return false
	}
	if isSpaceByte(in.src[j-1]) {
		return false
	}
	return !(c == '_' && j+m < len(in.src) && isWordByte(in.src[j+m]))
}

// autolink renders "<https://example.com>". The scheme is checked before the
// closing angle bracket is looked for, so ordinary text that happens to start
// with "<" costs a few comparisons and is then escaped like any other text.
func (in *inliner) autolink(i int) int {
	rest := in.src[i+1:]
	const maxScheme = 32
	j := 0
	for j < len(rest) && rest[j] != ':' {
		c := rest[j]
		if !isASCIILetter(c) && !(j > 0 && (isASCIIDigit(c) || c == '+' || c == '-' || c == '.')) {
			return 0
		}
		if j == maxScheme {
			return 0
		}
		j++
	}
	if j == 0 || j >= len(rest) {
		return 0
	}
	if s := strings.ToLower(rest[:j]); s != "http" && s != "https" {
		return 0
	}
	// An autolink holds no whitespace and no further "<", which bounds the
	// search for its end.
	end := 0
	for end < len(rest) && rest[end] != '>' {
		if isSpaceByte(rest[end]) || rest[end] == '<' {
			return 0
		}
		end++
	}
	if end >= len(rest) {
		return 0
	}
	href, ok := safeURL(rest[:end], linkHref)
	if !ok {
		return 0
	}
	in.b.WriteString(`<a href="`)
	escapeTo(&in.b, href)
	in.b.WriteString(`">`)
	escapeTo(&in.b, rest[:end])
	in.b.WriteString("</a>")
	return end + 2
}

// link renders "[text](destination)". A destination the scheme check rejects
// is dropped and the link text is rendered on its own, so the reader still
// sees what the model wrote without a way to follow it.
func (in *inliner) link(i int) int {
	text, dest, title, n, ok := in.parseLinkLike(i)
	if !ok {
		return 0
	}
	href, safe := safeURL(dest, linkHref)
	if !safe {
		in.b.WriteString(renderInline(text))
		return n
	}
	in.b.WriteString(`<a href="`)
	escapeTo(&in.b, href)
	in.b.WriteByte('"')
	in.writeTitle(title)
	in.b.WriteByte('>')
	in.b.WriteString(renderInline(text))
	in.b.WriteString("</a>")
	return n
}

// image renders "![alt](source)". A rejected source leaves the alt text, which
// is the description the model wrote and the best thing a reader can be given.
func (in *inliner) image(i int) int {
	if i+1 >= len(in.src) || in.src[i+1] != '[' {
		return 0
	}
	alt, dest, title, n, ok := in.parseLinkLike(i + 1)
	if !ok {
		return 0
	}
	src, safe := safeURL(dest, imageSrc)
	if !safe {
		escapeTo(&in.b, alt)
		return n + 1
	}
	in.b.WriteString(`<img src="`)
	escapeTo(&in.b, src)
	in.b.WriteString(`" alt="`)
	escapeTo(&in.b, alt)
	in.b.WriteByte('"')
	in.writeTitle(title)
	in.b.WriteByte('>')
	return n + 1
}

func (in *inliner) writeTitle(title string) {
	if title == "" {
		return
	}
	in.b.WriteString(` title="`)
	escapeTo(&in.b, title)
	in.b.WriteByte('"')
}

// parseLinkLike parses "[text](destination)" or "[text](destination "title")"
// starting at the "[" at start, and reports how many bytes it spans. The text
// may not contain "]", so a link inside link text is not parsed as one.
func (in *inliner) parseLinkLike(start int) (text, dest, title string, n int, ok bool) {
	if in.bracketFail || in.parenFail {
		return
	}
	rel := strings.IndexByte(in.src[start:], ']')
	if rel < 0 {
		in.bracketFail = true
		return
	}
	closeBracket := start + rel
	if closeBracket+1 >= len(in.src) || in.src[closeBracket+1] != '(' {
		return
	}
	end, found := matchParen(in.src, closeBracket+1)
	if !found {
		if strings.IndexByte(in.src[closeBracket+1:], ')') < 0 {
			in.parenFail = true
		}
		return
	}
	dest, title, ok = splitDestination(in.src[closeBracket+2 : end])
	if !ok {
		return "", "", "", 0, false
	}
	return in.src[start+1 : closeBracket], dest, title, end + 1 - start, true
}

// matchParen finds the ")" that closes the "(" at open, counting nesting so
// that a destination like "https://e/a_(b)" stays whole. A destination lives
// on one line, so a newline ends the search.
func matchParen(s string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, true
			}
		case '\n':
			return 0, false
		}
	}
	return 0, false
}

// splitDestination separates a destination from its optional quoted title.
// Whitespace separates the two, so a destination containing a space and
// anything but a well-formed title makes the whole construct invalid — which
// is what keeps a stray quote from ever arriving at an attribute unparsed.
func splitDestination(inner string) (dest, title string, ok bool) {
	s := strings.TrimSpace(inner)
	i := strings.IndexAny(s, " \t\n")
	if i < 0 {
		// An empty destination is well-formed; the scheme check rejects it.
		return s, "", true
	}
	rest := strings.TrimSpace(s[i:])
	if len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"' &&
		!strings.Contains(rest[1:len(rest)-1], `"`) {
		return s[:i], rest[1 : len(rest)-1], true
	}
	return "", "", false
}
