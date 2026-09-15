package markdown

import "strings"

// maxNesting caps how deep block quotes may nest. The parser recurses once
// per level, and the input is untrusted: a line of a million ">" characters
// would otherwise exhaust the goroutine stack, which is fatal and cannot be
// recovered. Chat prose nests a couple of levels at most, so past the cap the
// markers become ordinary escaped text like any other unsupported construct.
const maxNesting = 64

// renderBlocks writes the HTML for a sequence of lines. It is recursive: the
// lines inside a block quote go through it again at depth+1, which is what
// lets a quote hold paragraphs, headings, lists and code blocks.
//
// The order of the cases below is load-bearing. A thematic break is tested
// before a list because "- - -" matches both, and a fence before everything
// because its contents are not Markdown at all.
func renderBlocks(lines []string, out *strings.Builder, depth int) {
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			i++
		case isFenceStart(line):
			i = writeFence(lines, i, out)
		case isThematicBreak(line):
			out.WriteString("<hr>\n")
			i++
		case headingLevel(line) > 0:
			writeHeading(line, out)
			i++
		case isQuoteStart(line) && depth < maxNesting:
			i = writeQuote(lines, i, out, depth)
		case isListStart(line):
			l, next := parseList(lines, i)
			writeList(l, out)
			i = next
		default:
			i = writeParagraph(lines, i, out)
		}
	}
}

// startsOtherBlock reports whether line begins a block that interrupts a
// paragraph or a list item. A line that begins nothing is a lazy continuation
// of whatever is open, which is how a model's wrapped prose stays one block.
func startsOtherBlock(line string) bool {
	return isFenceStart(line) || isThematicBreak(line) ||
		headingLevel(line) > 0 || isQuoteStart(line)
}

// fence describes an opening code fence.
type fence struct {
	char byte
	n    int
	info string
}

// openFence parses an opening fence. A backtick fence's info string may not
// contain a backtick, or a paragraph mentioning `a` and `b` would open a code
// block that swallowed the rest of the message.
func openFence(line string) (fence, bool) {
	s := strings.TrimLeft(line, " \t")
	if s == "" || (s[0] != '`' && s[0] != '~') {
		return fence{}, false
	}
	n := runLength(s, 0, s[0])
	if n < 3 {
		return fence{}, false
	}
	info := strings.TrimSpace(s[n:])
	if s[0] == '`' && strings.ContainsRune(info, '`') {
		return fence{}, false
	}
	return fence{char: s[0], n: n, info: info}, true
}

func isFenceStart(line string) bool {
	_, ok := openFence(line)
	return ok
}

// closesFence reports whether line is f's closing fence: at least as many of
// the same character, and nothing else.
func closesFence(line string, f fence) bool {
	s := strings.TrimSpace(line)
	return runLength(s, 0, f.char) == len(s) && len(s) >= f.n
}

// writeFence renders a fenced code block. A fence still open at the end of the
// input closes there: a model's answer cut off mid snippet must not have its
// remaining lines parsed as Markdown.
func writeFence(lines []string, i int, out *strings.Builder) int {
	f, _ := openFence(lines[i])
	out.WriteString("<pre><code")
	if lang, _, _ := strings.Cut(f.info, " "); lang != "" {
		out.WriteString(` class="language-`)
		escapeTo(out, lang)
		out.WriteByte('"')
	}
	out.WriteByte('>')
	j := i + 1
	for ; j < len(lines); j++ {
		if closesFence(lines[j], f) {
			j++
			break
		}
		escapeTo(out, lines[j])
		out.WriteByte('\n')
	}
	out.WriteString("</code></pre>\n")
	return j
}

// isThematicBreak reports whether line is three or more of "-", "*" or "_",
// all the same, with nothing but spaces between them.
func isThematicBreak(line string) bool {
	var c byte
	n := 0
	for i := range len(line) {
		switch b := line[i]; b {
		case ' ', '\t':
		case '-', '*', '_':
			if n > 0 && b != c {
				return false
			}
			c, n = b, n+1
		default:
			return false
		}
	}
	return n >= 3
}

// headingLevel returns the level of an ATX heading, or 0 for anything else.
// The hashes must be followed by a space, so "#hashtag" stays a paragraph.
func headingLevel(line string) int {
	s := strings.TrimLeft(line, " \t")
	n := runLength(s, 0, '#')
	if n < 1 || n > 6 {
		return 0
	}
	if n < len(s) && s[n] != ' ' && s[n] != '\t' {
		return 0
	}
	return n
}

func writeHeading(line string, out *strings.Builder) {
	level := headingLevel(line)
	text := strings.TrimSpace(strings.TrimLeft(line, " \t")[level:])
	// A closing run of hashes is decoration only when a space precedes it,
	// so a heading that ends in "C#" keeps its sharp.
	if trimmed := strings.TrimRight(text, "#"); trimmed != text {
		if bare := strings.TrimRight(trimmed, " \t"); bare != trimmed || trimmed == "" {
			text = bare
		}
	}
	tag := [...]string{"h1", "h2", "h3", "h4", "h5", "h6"}[level-1]
	out.WriteByte('<')
	out.WriteString(tag)
	out.WriteByte('>')
	out.WriteString(renderInline(text))
	out.WriteString("</")
	out.WriteString(tag)
	out.WriteString(">\n")
}

func isQuoteStart(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), ">")
}

// writeQuote renders a block quote. Every line must carry its own ">": there
// is no lazy continuation, so a quote ends where the markers do.
func writeQuote(lines []string, i int, out *strings.Builder, depth int) int {
	var inner []string
	j := i
	for ; j < len(lines); j++ {
		s := strings.TrimLeft(lines[j], " \t")
		if !strings.HasPrefix(s, ">") {
			break
		}
		inner = append(inner, strings.TrimPrefix(s[1:], " "))
	}
	out.WriteString("<blockquote>\n")
	renderBlocks(inner, out, depth+1)
	out.WriteString("</blockquote>\n")
	return j
}

// marker describes a list item marker at the start of a line.
type marker struct {
	indent  int    // visual columns of indentation before the marker
	ordered bool   // the marker is "1." rather than "-", "*" or "+"
	text    string // the item's text, with the marker and its space removed
}

// listMarker parses a bullet ("-", "*", "+") or ordered ("1.") marker.
func listMarker(line string) (marker, bool) {
	indent, i := 0, 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		if line[i] == '\t' {
			indent += 4
		} else {
			indent++
		}
		i++
	}
	rest := line[i:]
	width := 0
	ordered := false
	switch {
	case rest == "":
		return marker{}, false
	case rest[0] == '-' || rest[0] == '*' || rest[0] == '+':
		width = 1
	default:
		d := 0
		for d < len(rest) && isASCIIDigit(rest[d]) {
			d++
		}
		if d == 0 || d > 9 || d >= len(rest) || rest[d] != '.' {
			return marker{}, false
		}
		width, ordered = d+1, true
	}
	if width >= len(rest) || (rest[width] != ' ' && rest[width] != '\t') {
		return marker{}, false
	}
	return marker{indent: indent, ordered: ordered, text: strings.TrimLeft(rest[width:], " \t")}, true
}

func isListStart(line string) bool {
	_, ok := listMarker(line)
	return ok
}

// listItem is one item: its text, and the one nested list it may hold.
type listItem struct {
	text   string
	nested *list
}

// list is a flat list whose items may each carry one nested list.
//
// A marker indented two or more columns past the list's own marker starts
// that nested list, and every deeper marker folds into the same level rather
// than nesting further: one level covers what chat answers actually contain,
// and an unbounded tree would be another thing an untrusted input controls.
type list struct {
	ordered bool
	items   []listItem
}

// parseList collects a list starting at lines[start] and returns the index of
// the first line after it. Lists are tight: a blank line ends the list, so an
// item never becomes a paragraph of its own.
func parseList(lines []string, start int) (*list, int) {
	base, _ := listMarker(lines[start])
	l := &list{ordered: base.ordered}
	i := start
	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			break
		}
		m, ok := listMarker(line)
		switch {
		case ok && len(l.items) > 0 && m.indent >= base.indent+2:
			it := &l.items[len(l.items)-1]
			if it.nested == nil {
				it.nested = &list{ordered: m.ordered}
			}
			it.nested.items = append(it.nested.items, listItem{text: m.text})
		case ok:
			l.items = append(l.items, listItem{text: m.text})
		case startsOtherBlock(line):
			return l, i
		default:
			appendContinuation(l, strings.TrimLeft(line, " \t"))
		}
	}
	return l, i
}

// appendContinuation adds a lazily continued line to the innermost open item.
func appendContinuation(l *list, text string) {
	it := &l.items[len(l.items)-1]
	if it.nested != nil {
		it = &it.nested.items[len(it.nested.items)-1]
	}
	it.text += "\n" + text
}

func writeList(l *list, out *strings.Builder) {
	tag := "ul"
	if l.ordered {
		tag = "ol"
	}
	out.WriteByte('<')
	out.WriteString(tag)
	out.WriteString(">\n")
	for _, it := range l.items {
		out.WriteString("<li>")
		out.WriteString(renderInline(it.text))
		if it.nested != nil {
			out.WriteByte('\n')
			writeList(it.nested, out)
		}
		out.WriteString("</li>\n")
	}
	out.WriteString("</")
	out.WriteString(tag)
	out.WriteString(">\n")
}

// writeParagraph renders consecutive non-blank lines as one paragraph. Their
// leading indentation is dropped, because an indented code block is not part
// of the subset and indented prose is far more likely to be a wrapped line.
func writeParagraph(lines []string, i int, out *strings.Builder) int {
	var parts []string
	j := i
	for ; j < len(lines); j++ {
		line := lines[j]
		if strings.TrimSpace(line) == "" {
			break
		}
		if j > i && (startsOtherBlock(line) || isListStart(line)) {
			break
		}
		parts = append(parts, strings.TrimLeft(line, " \t"))
	}
	out.WriteString("<p>")
	out.WriteString(renderInline(strings.Join(parts, "\n")))
	out.WriteString("</p>\n")
	return j
}
