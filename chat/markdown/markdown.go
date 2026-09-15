// Package markdown renders the Markdown a chat model writes into HTML that is
// safe to put on a page.
//
// A model's output is untrusted text, so the security properties are the
// product: nothing from the input is ever emitted unescaped, and a link or
// image destination is dropped unless its scheme is one that cannot execute.
// Everything the subset does not understand degrades to escaped text rather
// than failing, because a chat answer must always render.
//
// # The subset
//
// [Subset] supports paragraphs, ATX headings, emphasis and strong emphasis,
// inline code spans, fenced code blocks with an info string, links, images,
// unordered and ordered lists with one level of nesting, block quotes,
// thematic breaks, hard line breaks and http(s) autolinks. Raw HTML, tables,
// footnotes, reference-style links, setext headings and indented code blocks
// are not supported and render as escaped text. The exact behavior of every
// construct is pinned by the fixture corpus under testdata.
//
// Two limits keep an adversarial input from costing more than it should:
// lists nest one level deep, and block quotes nest to a fixed depth. Markers
// past either limit render as text.
//
// # Beyond the subset
//
// [Renderer] is the seam. A consumer who needs full CommonMark implements it
// over a parser of their choice, for example yuin/goldmark:
//
//	type goldmarkRenderer struct{ md goldmark.Markdown }
//
//	func (g goldmarkRenderer) RenderHTML(src string) string {
//		var b bytes.Buffer
//		if err := g.md.Convert([]byte(src), &b); err != nil {
//			return ""
//		}
//		return b.String()
//	}
//
// A renderer that emits raw HTML from the input takes on the sanitizing job
// this package does for you.
package markdown

import "strings"

// Renderer turns Markdown source into HTML.
//
// Rendering never fails: a renderer that cannot represent part of its input
// degrades it to escaped text. Empty input renders the empty string.
// Implementations must be safe for concurrent use.
type Renderer interface {
	// RenderHTML renders src as an HTML fragment, not a whole document.
	RenderHTML(src string) string
}

// Subset is the built-in chat-safe renderer. It is stateless; the zero value
// is ready to use and [New] returns it.
type Subset struct{}

// New returns the chat-safe subset renderer. It takes no configuration:
// every construct outside the subset degrades to escaped text.
func New() Subset { return Subset{} }

// RenderHTML renders src as an HTML fragment.
//
// Output is deterministic: each block element is followed by a single
// newline, so empty input produces the empty string and nothing else ever
// ends without one. Line endings in the input are normalized first, so the
// same source renders identically whatever wrote it.
func (Subset) RenderHTML(src string) string {
	src = normalizeNewlines(src)
	// A document's final newline terminates the last line rather than
	// starting an empty one; without this the split produces a phantom
	// blank line, which matters inside a fenced code block.
	src = strings.TrimSuffix(src, "\n")
	if src == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(src) + len(src)/4)
	renderBlocks(strings.Split(src, "\n"), &b, 0)
	return b.String()
}

// normalizeNewlines converts CRLF and lone CR line endings to LF.
func normalizeNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}
