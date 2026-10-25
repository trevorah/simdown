package simdown

import (
	"bufio"
	"encoding/binary"
	stdhtml "html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// FuzzToHTML checks that ToHTML never panics, that its output does not
// depend on where the input falls relative to 64-byte blocks, and that stage
// 2's fast paths agree with its general path.
func FuzzToHTML(f *testing.F) {
	for _, e := range loadSpec(f) {
		f.Add(e.Markdown, uint8(e.Example))
	}
	f.Fuzz(func(t *testing.T, src string, shift uint8) {
		want := string(ToHTML([]byte(src)))
		k := 1 + int(shift)%64
		if got := string(ToHTML([]byte(strings.Repeat("\n", k) + src))); got != want {
			t.Fatalf("shift by %d changes output\nwant %q\ngot  %q", k, want, got)
		}
		if general := string(toHTMLGeneral([]byte(src))); general != want {
			t.Fatalf("fast paths differ\ngeneral %q\nfast    %q", general, want)
		}
	})
}

// FuzzMatchesCommonmarkJS compares ToHTML with commonmark.js, the spec's
// other reference implementation, which follows the spec in corners where
// cmark does not. SIMDOWN_COMMONMARKJS names a directory with the commonmark
// package (0.31.2) installed, which bench/c/commonmark.js renders with.
//
// The spec defines a document as Unicode characters, so only valid UTF-8 is
// compared. Where commonmark.js departs from the spec, the input is skipped
// or the comparison adjusted:
//
//   - It trims every character JavaScript counts as whitespace (\v, \f,
//     U+00A0, ...) from the ends of paragraphs, headings and info strings,
//     where the spec trims only spaces or tabs (§4.3, §4.5, §4.8), and lets
//     ASCII control characters into link destinations (§6.3). Input with
//     any of these is skipped.
//   - Inside links and link reference definitions it skips spaces where
//     the spec skips spaces or tabs (§4.7, §6.3). Input with a tab and a
//     "](" or "]:" is skipped.
//   - It maps numeric character references to 128-159 through
//     Windows-1252, as HTML5 does, where the spec takes the code point as
//     it is (§2.5). The comparison maps simdown's output the same way.
//   - It keeps an empty last line after a final \r, though not after a
//     final \n. It is given such input with the \r made \r\n.
//   - In a list item, it drops all of a blank line's indentation, where
//     the spec removes only the item's (§5.2), which a code or HTML block
//     keeps. Output is compared with blank lines emptied.
//   - It takes brackets around only spaces, tabs and line endings after a
//     link's text as a link label, which the spec does not (§6.3). Input
//     with such brackets after a "]" is skipped.
//   - It looks at the UTF-16 code unit next to a delimiter run, so takes
//     punctuation outside the Basic Multilingual Plane, such as an emoji,
//     as neither punctuation nor whitespace (§6.2). Input with such a
//     character is skipped where the outputs differ.
//   - It leaves an empty paragraph where a paragraph of link reference
//     definitions meets a setext heading underline; simdown never writes
//     one. Empty paragraphs are removed.
//   - It writes raw HTML into an image's alt text as it is, where simdown,
//     like cmark, escapes it. Alt text is compared decoded.
//   - The spec leaves the escaping of URLs to the renderer, and
//     commonmark.js percent-encodes more than simdown, which escapes as
//     cmark does. URLs are compared decoded.
func FuzzMatchesCommonmarkJS(f *testing.F) {
	dir := os.Getenv("SIMDOWN_COMMONMARKJS")
	if dir == "" {
		f.Skip("SIMDOWN_COMMONMARKJS not set")
	}
	for _, e := range loadSpec(f) {
		f.Add(e.Markdown)
	}
	var js *renderPipe
	f.Fuzz(func(t *testing.T, src string) {
		if !utf8.ValidString(src) || departsInJS(src) || tabInLink(src) || blankLabel.MatchString(src) {
			return
		}
		if js == nil {
			var err error
			if js, err = startRenderPipe(dir); err != nil {
				t.Fatal(err)
			}
		}
		jsSrc := src
		if strings.HasSuffix(src, "\r") {
			jsSrc += "\n"
		}
		want, err := js.render(jsSrc)
		if err != nil {
			t.Fatal(err)
		}
		got := string(ToHTML([]byte(src)))
		if got != want {
			want, got = normalize(want), normalize(got)
		}
		if got != want && strings.Contains(src, "&#") {
			got = strings.Map(win1252, got)
		}
		if got != want && astralPunct(src) {
			return
		}
		if got != want {
			t.Fatalf("differs from commonmark.js on %q\ncommonmark.js %q\nsimdown       %q", src, want, got)
		}
	})
}

// renderPipe is a running bench/c/commonmark.js, which renders the
// documents written to it one after another.
type renderPipe struct {
	w io.Writer
	r *bufio.Reader
}

func startRenderPipe(dir string) (*renderPipe, error) {
	cmd := exec.Command("node", "bench/c/commonmark.js")
	cmd.Env = append(os.Environ(), "NODE_PATH="+filepath.Join(dir, "node_modules"))
	cmd.Stderr = os.Stderr
	w, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	r, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &renderPipe{w: w, r: bufio.NewReader(r)}, nil
}

func (c *renderPipe) render(src string) (string, error) {
	msg := binary.LittleEndian.AppendUint32(nil, uint32(len(src)))
	if _, err := c.w.Write(append(msg, src...)); err != nil {
		return "", err
	}
	var h [4]byte
	if _, err := io.ReadFull(c.r, h[:]); err != nil {
		return "", err
	}
	html := make([]byte, binary.LittleEndian.Uint32(h[:]))
	_, err := io.ReadFull(c.r, html)
	return string(html), err
}

func tabInLink(s string) bool {
	return strings.Contains(s, "\t") && (strings.Contains(s, "](") || strings.Contains(s, "]:"))
}

// normalize undoes, in html, the differences in rendering between
// commonmark.js and simdown that FuzzMatchesCommonmarkJS allows.
func normalize(html string) string {
	html = strings.ReplaceAll(html, "<p></p>\n", "")
	html = altText.ReplaceAllStringFunc(html, stdhtml.UnescapeString)
	return decodeURLs(blankLine.ReplaceAllString(html, "$1"))
}

// altText runs to the end of the img tag, as raw HTML in commonmark.js's
// alt text may have quotes.
var altText = regexp.MustCompile(`(?s)alt=".*?" />`)

// blankLine is a line of only spaces and tabs, which may be the first line
// of a code block.
var blankLine = regexp.MustCompile(`(?m)(^|<pre><code[^>]*>)[ \t]+$`)

// astralPunct reports whether s has punctuation or a symbol outside the
// Basic Multilingual Plane.
func astralPunct(s string) bool {
	for _, r := range s {
		if r > 0xffff && (unicode.IsPunct(r) || unicode.IsSymbol(r)) {
			return true
		}
	}
	return false
}

var blankLabel = regexp.MustCompile(`\]\[[ \t\r\n]+\]`)

var urlAttr = regexp.MustCompile(`(href|src)="[^"]*"`)

// decodeURLs undoes the HTML escaping and then the percent-encoding of
// each URL in html, leaving a % that does not start an escape as it is.
func decodeURLs(html string) string {
	return urlAttr.ReplaceAllStringFunc(html, func(a string) string {
		u := stdhtml.UnescapeString(a)
		var b strings.Builder
		for i := 0; i < len(u); i++ {
			if u[i] == '%' && i+2 < len(u) && isHex(u[i+1]) && isHex(u[i+2]) {
				n, _ := strconv.ParseUint(u[i+1:i+3], 16, 8)
				b.WriteByte(byte(n))
				i += 2
			} else {
				b.WriteByte(u[i])
			}
		}
		return b.String()
	})
}

func isHex(c byte) bool { return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' }

// win1252 maps the C1 controls that HTML5 reads as Windows-1252 to the
// characters they stand for there.
func win1252(r rune) rune {
	if r >= 0x80 && r <= 0x9f {
		return win1252C1[r-0x80]
	}
	return r
}

var win1252C1 = [32]rune{
	0x20ac, 0x81, 0x201a, 0x0192, 0x201e, 0x2026, 0x2020, 0x2021,
	0x02c6, 0x2030, 0x0160, 0x2039, 0x0152, 0x8d, 0x017d, 0x8f,
	0x90, 0x2018, 0x2019, 0x201c, 0x201d, 0x2022, 0x2013, 0x2014,
	0x02dc, 0x2122, 0x0161, 0x203a, 0x0153, 0x9d, 0x017e, 0x0178,
}

// departsInJS reports whether s has a character that commonmark.js handles
// differently from the spec: an ASCII control character other than tab, a
// line ending or NUL (which becomes U+FFFD), or whitespace other than
// spaces, tabs and line endings that JavaScript's trim removes.
func departsInJS(s string) bool {
	for _, r := range s {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == 0:
		case r < ' ' || r == 0x7f || unicode.IsSpace(r) || r == 0xfeff:
			return true
		}
	}
	return false
}
