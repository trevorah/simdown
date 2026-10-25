package simdown

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// toHTMLGeneral is ToHTML with every line on stage 2's general path.
func toHTMLGeneral(src []byte) []byte {
	var p parser
	p.general = true
	p.parse(src)
	return p.render(nil)
}

// fastPathLines are the kinds of line stage 2's fast paths handle, and
// their near misses, at a few indentations.
var fastPathLines = []string{
	"", " ", "   ", "text", "more text", "`code` at start", "*emph* at start",
	"_x_", "-flag", "+x", "#tag", "2024 was", "1.5 times",
	"# Heading", "## Heading ##", "#", "####### seven", "#\tx",
	"```", "```go", "~~~", "````", "``` a`b", "``", "~~~~ info",
	"- item", "* item", "+ item", "-  item", "-     item", "-", "- ", "-\titem",
	"1. item", "2. item", "1) item", "10. item", "1234567890. item",
	"- - -", "* * *", "---", "***", "___", "===", "- # h", "- ```", "- - x",
	"> quote", "<div>", "<!-- c -->", "[a]: /url", "[a]", "\ttab", "  \tx",
	"</div>", "<td>x</td>", "<!-- open", "end -->", "<script>", "</script>",
	"<?php", "?>", "<![CDATA[", "]]>", "<!X", "<a href=\"x\">", "<x-y/>", "<",
	"<notatag", "<pre>a</pre>",
	"a  ", "b\\", "c\t", "d \t", "e\t  ",
}

func randomDoc(r *rand.Rand) string {
	var b strings.Builder
	for n := r.IntN(12) + 1; n > 0; n-- {
		b.WriteString(strings.Repeat(" ", []int{0, 0, 0, 1, 2, 3, 4, 5, 6, 8}[r.IntN(10)]))
		b.WriteString(fastPathLines[r.IntN(len(fastPathLines))])
		b.WriteByte('\n')
	}
	return b.String()
}

func TestFastPathsMatchGeneral(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	n := 200000
	if testing.Short() {
		n = 20000
	}
	for i := 0; i < n; i++ {
		src := randomDoc(r)
		want := string(toHTMLGeneral([]byte(src)))
		if got := string(ToHTML([]byte(src))); got != want {
			t.Fatalf("fast paths differ on %q\ngeneral %q\nfast    %q", src, want, got)
		}
	}
	for _, e := range loadSpec(t) {
		want := string(toHTMLGeneral([]byte(e.Markdown)))
		if got := string(ToHTML([]byte(e.Markdown))); got != want {
			t.Fatalf("example %d: fast paths differ\ngeneral %q\nfast    %q", e.Example, want, got)
		}
	}
}

func TestSkipDestPlain(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	const alpha = "ab/:%\x21\x20\x1f\x00\x7f\x80\xff\\()"
	for iter := 0; iter < 20000; iter++ {
		b := make([]byte, r.IntN(40))
		for i := range b {
			if r.IntN(4) == 0 {
				b[i] = alpha[r.IntN(len(alpha))]
			} else {
				b[i] = 'a'
			}
		}
		j := r.IntN(len(b) + 1)
		want := j
		for want < len(b) && destPlain[b[want]] {
			want++
		}
		if got := skipDestPlain(b, j); got != want {
			t.Fatalf("skipDestPlain(%q, %d) = %d, want %d", b, j, got, want)
		}
	}
}
