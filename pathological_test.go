package simdown

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// cmark's test/pathological_tests.py: inputs that make a naive parser
// quadratic or worse. Each must parse in well under a second, and where the
// expected output has a simple shape, produce it.
func TestPathological(t *testing.T) {
	r := strings.Repeat
	var nestedLists, backticks, manyRefs strings.Builder
	for i := 0; i < 1000; i++ {
		nestedLists.WriteString(r("  ", i) + "* a\n")
	}
	for i := 1; i < 5000; i++ {
		backticks.WriteString("e" + r("`", i))
	}
	for i := 0; i < 50000; i++ {
		fmt.Fprintf(&manyRefs, "[%d]: u\n", i)
	}
	manyRefs.WriteString(r("[0] ", 50000))

	cases := []struct {
		name, in string
		want     string // substring of the output, if not empty
	}{
		{"nested strong emph", r("*a **a ", 65000) + "b" + r(" a** a*", 65000), "<em>a <strong>a"},
		{"many emph closers with no openers", r("a_ ", 65000), ""},
		{"many emph openers with no closers", r("_a ", 65000), ""},
		{"many link closers with no openers", r("a]", 65000), ""},
		{"many link openers with no closers", r("[a", 65000), ""},
		{"mismatched openers and closers", r("*a_ ", 50000), ""},
		{"openers and closers multiple of 3", "a**b" + r("c* ", 50000), ""},
		{"link openers and emph closers", r("[ a_", 50000), ""},
		{"pattern [ (]( repeated", r("[ (](", 80000), ""},
		{"nested brackets", r("[", 50000) + "a" + r("]", 50000), ""},
		{"nested block quotes", r("> ", 50000) + "a", "<blockquote>\n<blockquote>"},
		{"deeply nested lists", nestedLists.String(), "<ul>\n<li>a\n<ul>"},
		{"U+0000 in input", "abc\x00de\x00", "abc\xef\xbf\xbdde\xef\xbf\xbd"},
		{"backticks", backticks.String(), ""},
		{"unclosed links A", r("[a](<b", 30000), ""},
		{"unclosed links B", r("[a](b", 30000), ""},
		{"unclosed <!--", "</" + r("<!--", 300000), ""},
		{"unclosed <?", r("<?", 300000), ""},
		{"unclosed <![CDATA[", r("<![CDATA[", 100000), ""},
		{"unclosed <!A", r("<!A ", 300000), ""},
		{"unclosed titles", r(`[a](b "`, 50000), ""},
		{"unclosed single titles", r(`[a](b '`, 50000), ""},
		{"many references", manyRefs.String(), `<a href="u">0</a>`},
		{"long code span runs", r("`", 100000) + "a" + r("`", 99999), ""},
		{"nested parens in dest", "[a](" + r("(", 100000) + r(")", 100000) + ")", ""},
		{"many list items", r("- a\n", 100000), "<li>a</li>"},
		{"backslash run", r("\\", 1000001) + "*a*", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start := time.Now()
			out := string(ToHTML([]byte(c.in)))
			if d := time.Since(start); d > time.Second {
				t.Errorf("took %v", d)
			}
			if c.want != "" && !strings.Contains(out, c.want) {
				t.Errorf("output does not contain %q; starts %q", c.want, out[:min(len(out), 200)])
			}
		})
	}
}
