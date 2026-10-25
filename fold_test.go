package simdown

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// TestLinkLabelFold checks label matching (spec §6.3, "matches") on the
// cases the spec examples leave out: the multi-character folds besides the
// sharp s, the Turkish dotted and dotless i, which full case folding keeps
// apart from i, and whitespace other than spaces, tabs and line endings,
// which normalization leaves alone.
func TestLinkLabelFold(t *testing.T) {
	tests := []struct {
		name, ref, def string
		match          bool
	}{
		{"sharp s", "\xe1\xba\x9e", "SS", true},
		{"small sharp s", "\xc3\x9f", "ss", true},
		{"greek", "\xce\x91\xce\x93\xce\xa9", "\xce\xb1\xce\xb3\xcf\x89", true},
		{"long s", "\xc5\xbf", "S", true},
		{"ligature fi", "\xef\xac\x81", "FI", true},
		{"ligature st", "\xef\xac\x86", "st", true},
		{"iota subscript", "\xe1\xbe\xbc", "\xce\x91\xce\x99", true},
		{"n preceded by apostrophe", "\xc5\x89", "\xca\xbcN", true},
		{"iota dialytika tonos", "\xce\x90", "\xce\xb9\xcc\x88\xcc\x81", true},
		{"dotted capital i", "\xc4\xb0", "i\xcc\x87", true},
		{"dotted capital i vs i", "\xc4\xb0", "i", false},
		{"dotless i vs I", "\xc4\xb1", "I", false},
		{"dotless i vs i", "\xc4\xb1", "i", false},
		{"no-break space", "a\xc2\xa0b", "a b", false},
		{"ideographic space", "a\xe3\x80\x80b", "a b", false},
		{"leading no-break space", "\xc2\xa0a", "a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := "[" + tt.ref + "]\n\n[" + tt.def + "]: /u\n"
			want := "<p>[" + tt.ref + "]</p>\n"
			if tt.match {
				want = "<p><a href=\"/u\">" + tt.ref + "</a></p>\n"
			}
			if got := string(ToHTML([]byte(in))); got != want {
				t.Errorf("ToHTML(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

// TestFoldRuneMatchesCaseFolding checks foldRune against the full case
// folding of testdata/CaseFolding.txt (statuses C and F, as the spec's
// "Unicode case fold" and cmark's utf8proc mean), downloaded for the Unicode
// version of Go's unicode package. foldRune need not produce the same
// strings, only the same equivalence: it must give r what it gives r's full
// folding, and must keep apart the runes that full folding leaves alone.
func TestFoldRuneMatchesCaseFolding(t *testing.T) {
	full, version := loadCaseFolding(t)
	if version != unicode.Version {
		t.Fatalf("testdata/CaseFolding.txt is Unicode %s, Go's unicode package is %s", version, unicode.Version)
	}
	foldString := func(s string) string {
		var b []byte
		for _, r := range s {
			b = foldRune(b, r)
		}
		return string(b)
	}
	owner := make(map[rune]rune)
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		f, ok := full[r]
		if !ok {
			f = string(r)
		}
		got := string(foldRune(nil, r))
		if want := foldString(f); got != want {
			t.Errorf("foldRune(U+%04X) = %q, but its full folding %q gives %q", r, got, f, want)
		}
		if ok {
			continue
		}
		k, n := utf8.DecodeRuneInString(got)
		if n != len(got) {
			t.Errorf("foldRune(U+%04X) = %q, want a single rune for a rune that folds to itself", r, got)
			continue
		}
		if o, dup := owner[k]; dup {
			t.Errorf("foldRune gives U+%04X and U+%04X the same %q, but full folding keeps them apart", o, r, got)
			continue
		}
		owner[k] = r
	}
}

// loadCaseFolding returns the C and F mappings of testdata/CaseFolding.txt
// and the Unicode version named on its first line.
func loadCaseFolding(t *testing.T) (map[rune]string, string) {
	f, err := os.Open("testdata/CaseFolding.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	full := make(map[rune]string)
	version := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "# CaseFolding-"); ok {
			version = strings.TrimSuffix(v, ".txt")
		}
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Split(line, ";")
		if len(fields) < 3 {
			continue
		}
		status := strings.TrimSpace(fields[1])
		if status != "C" && status != "F" {
			continue
		}
		var to []rune
		for _, h := range strings.Fields(fields[2]) {
			to = append(to, parseHex(t, h))
		}
		full[parseHex(t, strings.TrimSpace(fields[0]))] = string(to)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return full, version
}

func parseHex(t *testing.T, s string) rune {
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	return rune(n)
}
