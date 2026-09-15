package markdown

import (
	"strings"
	"testing"
	"time"
)

// TestNoRawHTML checks the first security property: nothing that looks like
// markup in the input may look like markup in the output.
func TestNoRawHTML(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"script tag", "<script>alert(1)</script>", "<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>\n"},
		{"inline tag", "a <b>bold</b> word", "<p>a &lt;b&gt;bold&lt;/b&gt; word</p>\n"},
		{"event handler", `<img src=x onerror=alert(1)>`, "<p>&lt;img src=x onerror=alert(1)&gt;</p>\n"},
		{"entity is escaped", "&copy; &amp; &lt; &#x27;", "<p>&amp;copy; &amp;amp; &amp;lt; &amp;#x27;</p>\n"},
		{"already escaped stays escaped", "&lt;", "<p>&amp;lt;</p>\n"},
		{"quote and apostrophe", `he said "it's fine"`, "<p>he said &quot;it&#39;s fine&quot;</p>\n"},
		{"tag inside code span", "`<script>`", "<p><code>&lt;script&gt;</code></p>\n"},
		{"tag inside fence", "```\n<script>alert(1)</script>\n```", "<pre><code>&lt;script&gt;alert(1)&lt;/script&gt;\n</code></pre>\n"},
		{"tag inside heading", "# <script>x</script>", "<h1>&lt;script&gt;x&lt;/script&gt;</h1>\n"},
		{"comment", "<!-- hi -->", "<p>&lt;!-- hi --&gt;</p>\n"},
		{"cdata", "<![CDATA[x]]>", "<p>&lt;![CDATA[x]]&gt;</p>\n"},
	}
	r := New()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.RenderHTML(c.src)
			if got != c.want {
				t.Errorf("RenderHTML(%q) = %q, want %q", c.src, got, c.want)
			}
			assertNoRawMarkup(t, got)
		})
	}
}

// TestDangerousLinkSchemes checks that only http and https produce an anchor.
// A rejected destination is dropped and the link text renders as plain text.
func TestDangerousLinkSchemes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"javascript", "[x](javascript:alert(1))", "<p>x</p>\n"},
		{"mixed case", "[x](JaVaScRiPt:alert(1))", "<p>x</p>\n"},
		{"leading space", "[x]( javascript:alert(1))", "<p>x</p>\n"},
		// Whitespace separates a destination from its title, so a tab inside
		// one makes the construct invalid Markdown before the scheme is ever
		// examined; the NUL case below is what proves the control-character
		// strip, since a NUL is not whitespace and does reach the check.
		{"tab inside scheme", "[x](java\tscript:alert(1))", "<p>[x](java\tscript:alert(1))</p>\n"},
		{"NUL inside scheme", "[x](java\x00script:alert(1))", "<p>x</p>\n"},
		{"vbscript", "[x](vbscript:msgbox(1))", "<p>x</p>\n"},
		{"data", "[x](data:text/html,<script>alert(1)</script>)", "<p>x</p>\n"},
		{"data image is still not a link", "[x](data:image/png;base64,iVBORw0KGgo=)", "<p>x</p>\n"},
		{"file", "[x](file:///etc/passwd)", "<p>x</p>\n"},
		{"mailto", "[x](mailto:a@example.com)", "<p>x</p>\n"},
		{"relative path", "[x](/about)", "<p>x</p>\n"},
		{"scheme-less host", "[x](example.com/a)", "<p>x</p>\n"},
		{"protocol relative", "[x](//example.com)", "<p>x</p>\n"},
		{"empty destination", "[x]()", "<p>x</p>\n"},
		{"http accepted", "[x](http://example.com)", `<p><a href="http://example.com">x</a></p>` + "\n"},
		{"https accepted", "[x](https://example.com)", `<p><a href="https://example.com">x</a></p>` + "\n"},
		{"uppercase https accepted", "[x](HTTPS://EXAMPLE.COM)", `<p><a href="HTTPS://EXAMPLE.COM">x</a></p>` + "\n"},
		{"rejected link keeps inline markup in its text", "[**x**](javascript:alert(1))", "<p><strong>x</strong></p>\n"},
	}
	r := New()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.RenderHTML(c.src)
			if got != c.want {
				t.Errorf("RenderHTML(%q) = %q, want %q", c.src, got, c.want)
			}
			assertNoRawMarkup(t, got)
		})
	}
}

// TestImageSchemes checks the image rule: http, https and data:image/* only.
// A rejected image degrades to its escaped alt text.
func TestImageSchemes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"png data url", "![x](data:image/png;base64,iVBORw0KGgo=)", `<p><img src="data:image/png;base64,iVBORw0KGgo=" alt="x"></p>` + "\n"},
		{"svg data url", "![x](data:image/svg+xml,%3Csvg%3E)", `<p><img src="data:image/svg+xml,%3Csvg%3E" alt="x"></p>` + "\n"},
		{"uppercase data image", "![x](DATA:IMAGE/PNG;base64,iVBORw0KGgo=)", `<p><img src="DATA:IMAGE/PNG;base64,iVBORw0KGgo=" alt="x"></p>` + "\n"},
		{"html data url rejected", "![x](data:text/html;base64,PHNjcmlwdD4=)", "<p>x</p>\n"},
		{"bare data url rejected", "![x](data:,hello)", "<p>x</p>\n"},
		{"empty media type rejected", "![x](data:;base64,aGk=)", "<p>x</p>\n"},
		{"media type prefix is not enough", "![x](data:imagex/png;base64,aGk=)", "<p>x</p>\n"},
		{"javascript rejected", "![x](javascript:alert(1))", "<p>x</p>\n"},
		{"https accepted", "![a dot](https://example.com/d.png)", `<p><img src="https://example.com/d.png" alt="a dot"></p>` + "\n"},
		{"alt text is escaped", `![<b>"x"</b>](javascript:alert(1))`, "<p>&lt;b&gt;&quot;x&quot;&lt;/b&gt;</p>\n"},
		{"alt attribute is escaped", `![say "hi"](https://example.com/d.png)`, `<p><img src="https://example.com/d.png" alt="say &quot;hi&quot;"></p>` + "\n"},
	}
	r := New()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.RenderHTML(c.src)
			if got != c.want {
				t.Errorf("RenderHTML(%q) = %q, want %q", c.src, got, c.want)
			}
			assertNoRawMarkup(t, got)
		})
	}
}

// TestAttributeCannotBeClosed checks that no destination, title or info string
// can end its attribute early and start one of its own.
func TestAttributeCannotBeClosed(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			"quote in destination",
			`[x](https://example.com/a"onmouseover="alert(1))`,
			`<p><a href="https://example.com/a%22onmouseover=%22alert(1)">x</a></p>` + "\n",
		},
		{
			"angle brackets in destination",
			`[x](https://example.com/<script>)`,
			`<p><a href="https://example.com/%3Cscript%3E">x</a></p>` + "\n",
		},
		{
			"ampersand in destination",
			`[x](https://example.com/?a=1&b=2)`,
			`<p><a href="https://example.com/?a=1&amp;b=2">x</a></p>` + "\n",
		},
		{
			// A title is delimited by double quotes, so it cannot contain
			// one; the other markup characters must still be escaped.
			"markup in title",
			`[x](https://example.com "a <b> & c")`,
			`<p><a href="https://example.com" title="a &lt;b&gt; &amp; c">x</a></p>` + "\n",
		},
		{
			"a title containing a quote does not parse as a link",
			`[x](https://example.com "a" onmouseover="b")`,
			`<p>[x](https://example.com &quot;a&quot; onmouseover=&quot;b&quot;)</p>` + "\n",
		},
		{
			"quote in image source",
			`![x](https://example.com/a".png)`,
			`<p><img src="https://example.com/a%22.png" alt="x"></p>` + "\n",
		},
		{
			"markup in fence info string",
			"```\"><script>\ncode\n```",
			`<pre><code class="language-&quot;&gt;&lt;script&gt;">code` + "\n</code></pre>\n",
		},
	}
	r := New()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.RenderHTML(c.src)
			if got != c.want {
				t.Errorf("RenderHTML(%q) = %q, want %q", c.src, got, c.want)
			}
			assertNoRawMarkup(t, got)
		})
	}
}

// TestUnterminatedFence pins the decision that a fence left open at end of
// input still renders as a code block: a model whose answer was cut off mid
// snippet must not have the rest of its text parsed as Markdown.
func TestUnterminatedFence(t *testing.T) {
	r := New()
	src := "```go\nfmt.Println(\"hi\")\n"
	want := "<pre><code class=\"language-go\">fmt.Println(&quot;hi&quot;)\n</code></pre>\n"
	if got := r.RenderHTML(src); got != want {
		t.Errorf("RenderHTML(%q) = %q, want %q", src, got, want)
	}
}

// TestDeepNestingDoesNotOverflow is the regression test for a stack overflow.
// The block parser recurses once per ">" on a line, and 2 MiB of them
// exhausted the 1 GB goroutine stack — a fatal error Go cannot recover from,
// so a chat back end rendering a model's answer would have died rather than
// returned something. Past maxNesting the quote markers become escaped text,
// which is what every other unsupported construct does.
func TestDeepNestingDoesNotOverflow(t *testing.T) {
	src := strings.Repeat(">", 2<<20) + " x"
	got := New().RenderHTML(src)
	if n := strings.Count(got, "<blockquote>"); n != maxNesting {
		t.Errorf("nested %d block quotes, want %d", n, maxNesting)
	}
	if !strings.Contains(got, "&gt;") {
		t.Error("the quote markers past the limit were not escaped into the text")
	}
	assertNoRawMarkup(t, got)
}

// TestLargeInputFinishes guards against quadratic behavior. The bound is
// deliberately loose so that it fails on an algorithm that degrades rather
// than on a slow or loaded machine, and the input is large enough that the
// loose bound still catches a degradation: with the failure memos in
// [inliner] removed, this input took 28.8s against 44ms with them
// (go test ./chat/markdown -run TestLargeInputFinishes -v, 2026-09-14).
func TestLargeInputFinishes(t *testing.T) {
	// One unterminated emphasis delimiter per repetition, all in a single
	// paragraph: the shape that makes a naive scanner rescan to end of input
	// for every opener it meets.
	chunk := "*unclosed emphasis and `unclosed code and [unclosed link and <unclosed autolink "
	var b strings.Builder
	for b.Len() < 4<<20 {
		b.WriteString(chunk)
	}
	src := b.String()

	r := New()
	start := time.Now()
	got := r.RenderHTML(src)
	elapsed := time.Since(start)
	t.Logf("rendered %d bytes into %d bytes in %s", len(src), len(got), elapsed)
	if elapsed > 10*time.Second {
		t.Errorf("rendering %d bytes took %s, want under 10s", len(src), elapsed)
	}
	assertNoRawMarkup(t, got)
}
