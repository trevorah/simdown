package index

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// naiveIndex restates stage 1 a byte at a time, straight from the spec's
// definitions rather than from the bitwise algebra of index.go.
func naiveIndex(src []byte) (ix Index, hasNUL bool) {
	n := len(src)
	escaped := make([]bool, n+1)
	for i := 0; i < n; i++ {
		if src[i] == '\\' && !escaped[i] {
			escaped[i+1] = true
		}
	}
	for start := 0; start < n; {
		term := start
		for term < n && src[term] != '\n' && src[term] != '\r' {
			term++
		}
		lead := start
		for lead < term && (src[lead] == ' ' || src[lead] == '\t') {
			lead++
		}
		ix.Term = append(ix.Term, uint32(term))
		ix.Lead = append(ix.Lead, uint32(lead))
		start = term + 1
		if term+1 < n && src[term] == '\r' && src[term+1] == '\n' {
			start++
		}
	}
	for i := 0; i < n; i++ {
		c := src[i]
		if c == 0 {
			hasNUL = true
		}
		if escaped[i] {
			continue
		}
		live := func(j int) bool { return j < n && !escaped[j] }
		var hit bool
		switch c {
		case '\\':
			hit = i+1 < n && (isASCIIPunct(src[i+1]) || src[i+1] == '\n' || src[i+1] == '\r')
		case '`', '*', '_':
			hit = i == 0 || src[i-1] != c || escaped[i-1]
		case '!':
			hit = i+1 < n && src[i+1] == '[' && live(i+1)
		case '[':
			hit = i == 0 || src[i-1] != '!' || escaped[i-1]
		case ']', '<', '&':
			hit = true
		}
		if hit {
			ix.Inline = append(ix.Inline, uint32(i))
		}
	}
	return ix, hasNUL
}

func checkIndex(t *testing.T, src []byte) {
	t.Helper()
	var got Index
	gotNUL := got.Build(src)
	want, wantNUL := naiveIndex(src)
	if !slices.Equal(got.Term, want.Term) || !slices.Equal(got.Lead, want.Lead) ||
		!slices.Equal(got.Inline, want.Inline) || gotNUL != wantNUL {
		t.Fatalf("index mismatch for %q\nterm   got %v\n       want %v\nlead   got %v\n       want %v\ninline got %v\n       want %v\nnul got %v want %v",
			src, got.Term, want.Term, got.Lead, want.Lead, got.Inline, want.Inline, gotNUL, wantNUL)
	}
}

func TestIndexRandom(t *testing.T) {
	// Alphabets weighted towards the bytes that carry state across blocks:
	// backslash runs, CR/LF pairs, runs of delimiters and leading blanks.
	alphabets := []string{
		"\\\\\\\\\\*_`[]!<&\n\r \ta",
		"\\\\\\\\\\\\\\\\a*",
		"\r\n\r\n  \t\tx",
		"!![[[\\a",
		"``***___  a\n",
		"abc \n\\#\x00é",
	}
	r := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 20000; iter++ {
		alpha := alphabets[iter%len(alphabets)]
		n := r.IntN(300)
		b := make([]byte, n)
		for i := range b {
			b[i] = alpha[r.IntN(len(alpha))]
		}
		checkIndex(t, b)
	}
}

func TestIndexEdges(t *testing.T) {
	for _, s := range []string{"", "a", "\n", "\r", "\r\n", "  ", "\\", "!", "![", "\\\\"} {
		for pad := 0; pad < 130; pad++ {
			checkIndex(t, []byte(strings.Repeat("x", pad)+s))
			checkIndex(t, []byte(strings.Repeat("\\", pad)+s))
			checkIndex(t, []byte(strings.Repeat(" ", pad)+s))
		}
	}
}

func FuzzIndex(f *testing.F) {
	f.Add([]byte("a \\\\*b\\* _c_ ![i](j) \\[x] **d** & `e\\` <f> x\\\\\\_y"))
	f.Fuzz(func(t *testing.T, b []byte) { checkIndex(t, b) })
}
