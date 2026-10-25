package simdown

import (
	"bytes"
	"strings"
	"testing"
)

// TestAppendLinesMatchesPerLine checks that copying runs of contiguous lines
// at once writes what writing the lines one at a time does, over every code
// and HTML block in the spec examples. The variants break the runs in each
// way they can be broken: CRLF and CR line endings, container prefixes
// between the lines, and tabs that a container marker partly consumes
// (vspace).
func TestAppendLinesMatchesPerLine(t *testing.T) {
	variants := []func(string) string{
		func(s string) string { return s },
		func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") },
		func(s string) string { return strings.ReplaceAll(s, "\n", "\r") },
		func(s string) string { return "> " + strings.ReplaceAll(s, "\n", "\n> ") },
		func(s string) string { return "- " + strings.ReplaceAll(s, "\n", "\n  ") },
		func(s string) string { return ">\t" + strings.ReplaceAll(s, "\n", "\n>\t") },
	}
	blocks := 0
	for _, e := range loadSpec(t) {
		for _, v := range variants {
			p := parsed([]byte(v(e.Markdown)))
			for _, b := range p.blocks {
				if b.kind != kCodeBlock && b.kind != kHTMLBlock {
					continue
				}
				blocks++
				escape := b.kind == kCodeBlock
				var want []byte
				for _, s := range p.spans[b.a:b.b] {
					want = append(want, bytes.Repeat([]byte(" "), int(s.vspace))...)
					if escape {
						want = escapeHTML(want, p.src[s.start:s.end])
					} else {
						want = append(want, p.src[s.start:s.end]...)
					}
					want = append(want, '\n')
				}
				if got := p.appendLines(nil, p.spans[b.a:b.b], escape); !bytes.Equal(got, want) {
					t.Fatalf("example %d, source %q:\nwant %q\ngot  %q", e.Example, p.src, want, got)
				}
			}
		}
	}
	if blocks < 500 {
		t.Fatalf("only %d code and HTML blocks checked", blocks)
	}
}
