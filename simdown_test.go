package simdown

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// TestSpecShifted moves every spec example across 64-byte block boundaries
// by prefixing blank lines, which cannot change the output. The spec
// examples are short, so without this they hardly exercise the carries
// between blocks.
func TestSpecShifted(t *testing.T) {
	for _, e := range loadSpec(t) {
		want := string(ToHTML([]byte(e.Markdown)))
		for k := 1; k <= 70; k++ {
			for _, nl := range []string{"\n", "\r\n", " \n"} {
				src := strings.Repeat(nl, k) + e.Markdown
				if got := string(ToHTML([]byte(src))); got != want {
					t.Fatalf("example %d shifted by %d×%q:\nwant %q\ngot  %q", e.Example, k, nl, want, got)
				}
			}
		}
	}
}

func ExampleToHTML() {
	fmt.Print(string(ToHTML([]byte("# Hi\n\n> *a* [b](/c)\n"))))
	// Output:
	// <h1>Hi</h1>
	// <blockquote>
	// <p><em>a</em> <a href="/c">b</a></p>
	// </blockquote>
}

func TestEscapeHTML(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	const alpha = "ab&<>\"x\x80\n"
	for iter := 0; iter < 20000; iter++ {
		// b is cut from a larger buffer, as text ranges are cut from src,
		// so that the NEON version reads past its end. The bytes after it
		// are all escapes or line endings, which must not be taken for
		// b's.
		after := r.IntN(20)
		buf := make([]byte, r.IntN(100)+after)
		for i := range buf {
			buf[i] = alpha[r.IntN(len(alpha))]
		}
		b := buf[:len(buf)-after]
		for i := len(b); i < len(buf); i++ {
			buf[i] = "<\n"[i%2]
		}
		var want, wantCode []byte
		for _, c := range b {
			if e := htmlEscapes[c]; e != "" {
				want = append(want, e...)
				wantCode = append(wantCode, e...)
			} else {
				want = append(want, c)
				wantCode = append(wantCode, c)
			}
			if c == '\n' {
				wantCode[len(wantCode)-1] = ' '
			}
		}
		// out has anything from no spare room to plenty, so that every
		// growth path is taken.
		out := append(make([]byte, 0, 3+r.IntN(40)), "pre"...)
		if got := escapeHTML(out, b); string(got) != "pre"+string(want) {
			t.Fatalf("escapeHTML(%q) = %q, want %q", b, got[3:], want)
		}
		out = append(make([]byte, 0, 3+r.IntN(40)), "pre"...)
		if got := escapeCode(out, b); string(got) != "pre"+string(wantCode) {
			t.Fatalf("escapeCode(%q) = %q, want %q", b, got[3:], wantCode)
		}
		if strings.Trim(string(buf[len(b):]), "<\n") != "" {
			t.Fatalf("escapeHTML(%q) wrote past the end of its input", b)
		}
	}
}

// TestSpecOverCmark covers places where cmark departs from the spec and
// simdown does not. Unless a case says otherwise, the expected HTML is
// commonmark.js's.
func TestSpecOverCmark(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		// Only spaces are removed before a line break (§6.8), not tabs.
		{"0\t\n0", "<p>0\t\n0</p>\n"},
		{"0 \t\n0", "<p>0 \t\n0</p>\n"},
		{"0\t  \n0", "<p>0\t<br />\n0</p>\n"},
		{"0\t\n\t0", "<p>0\t\n0</p>\n"},
		{"- 0\t\n  0", "<ul>\n<li>0\t\n0</li>\n</ul>\n"},
		{"> 0\t\n0", "<blockquote>\n<p>0\t\n0</p>\n</blockquote>\n"},
		{"*0*\t\n0", "<p><em>0</em>\t\n0</p>\n"},
		{"0\t", "<p>0</p>\n"},
		// A list item can begin with at most one blank line (§5.2), even an
		// indented one.
		{"*\n  \n  0", "<ul>\n<li></li>\n</ul>\n<p>0</p>\n"},
		{"1.\n   \n   0", "<ol>\n<li></li>\n</ol>\n<p>0</p>\n"},
		{"- a\n- \n  \n  0", "<ul>\n<li>a</li>\n<li></li>\n</ul>\n<p>0</p>\n"},
		// Link reference definitions are a block (§4.7), so a blank line
		// between them and another block of the item makes the list loose
		// (§5.3).
		{"* 0\n\n  [0]:0", "<ul>\n<li>\n<p>0</p>\n</li>\n</ul>\n"},
		{"* [0]:0\n\n  b", "<ul>\n<li>\n<p>b</p>\n</li>\n</ul>\n"},
		{"* ```\n  x\n  ```\n  [0]:0\n\n  b", "<ul>\n<li>\n<pre><code>x\n</code></pre>\n<p>b</p>\n</li>\n</ul>\n"},
		{"* [0]:0", "<ul>\n<li></li>\n</ul>\n"},
		{"* [0]:0\n\n* b", "<ul>\n<li></li>\n<li>\n<p>b</p>\n</li>\n</ul>\n"},
		// A blank line inside an HTML block, like one inside fenced code,
		// does not separate list items (§5.3).
		{"- <!A0\n\n- 00", "<ul>\n<li>\n<!A0\n\n</li>\n<li>00</li>\n</ul>\n"},
		{"- <pre>\n\n- c", "<ul>\n<li>\n<pre>\n\n</li>\n<li>c</li>\n</ul>\n"},
		{"- <!--\n\n- c", "<ul>\n<li>\n<!--\n\n</li>\n<li>c</li>\n</ul>\n"},
		{"- <div>\n\n- c", "<ul>\n<li>\n<div>\n</li>\n<li>\n<p>c</p>\n</li>\n</ul>\n"},
		// Brackets around only spaces, tabs and line endings are not a link
		// label (§6.3), so do not stop a shortcut reference. cmark takes
		// them as a collapsed reference and commonmark.js as a label that
		// matches nothing; this is the spec's reading, which neither has.
		{"[Foo][ ]\n\n[foo]:0", "<p><a href=\"0\">Foo</a>[ ]</p>\n"},
		{"[foo][\n]\n\n[foo]:0", "<p><a href=\"0\">foo</a>[\n]</p>\n"},
		{"![foo][ ]\n\n[foo]:0", "<p><img src=\"0\" alt=\"foo\" />[ ]</p>\n"},
		{"[foo][]\n\n[foo]:0", "<p><a href=\"0\">foo</a></p>\n"},
		// A link label's limit is 999 characters, not bytes (§6.3).
		{"[" + strings.Repeat("\xc3\xa9", 999) + "]\n\n[" + strings.Repeat("\xc3\xa9", 999) + "]: /u",
			"<p><a href=\"/u\">" + strings.Repeat("\xc3\xa9", 999) + "</a></p>\n"},
		{"[" + strings.Repeat("\xc3\xa9", 1000) + "]\n\n[" + strings.Repeat("\xc3\xa9", 1000) + "]: /u",
			"<p>[" + strings.Repeat("\xc3\xa9", 1000) + "]</p>\n<p>[" + strings.Repeat("\xc3\xa9", 1000) + "]: /u</p>\n"},
		{"[x][" + strings.Repeat("\xc3\xa9", 999) + "]\n\n[" + strings.Repeat("\xc3\xa9", 999) + "]: /u",
			"<p><a href=\"/u\">x</a></p>\n"},
		// A fence's indentation is counted in columns (§4.5), including
		// what is left of a tab that a container partly consumed.
		{"*\n\t```\n    0", "<ul>\n<li>\n<pre><code>0\n</code></pre>\n</li>\n</ul>\n"},
		{">\t ```\n>     0", "<blockquote>\n<pre><code> 0\n</code></pre>\n</blockquote>\n"},
	} {
		if got := string(ToHTML([]byte(c.src))); got != c.want {
			t.Errorf("ToHTML(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}
