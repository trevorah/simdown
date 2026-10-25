package simdown

import (
	"bytes"
	"html"
	"strings"
	"unicode"
	"unicode/utf8"
)

func isASCIIPunct(c byte) bool {
	return c >= '!' && c <= '/' || c >= ':' && c <= '@' || c >= '[' && c <= '`' || c >= '{' && c <= '~'
}

func isSpaceOrTab(c byte) bool { return c == ' ' || c == '\t' }

func isLineEnd(c byte) bool { return c == '\n' || c == '\r' }

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func isAlpha(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlnum(c byte) bool { return isAlpha(c) || isDigit(c) }

// isUnicodeWhitespace and isUnicodePunct classify the characters next to a
// delimiter run (spec §6.2).
func isUnicodeWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v' ||
		r >= 0x80 && unicode.Is(unicode.Zs, r)
}

func isUnicodePunct(r rune) bool {
	if r < 0x80 {
		return isASCIIPunct(byte(r))
	}
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// skipSpaces returns the first offset at or after i that is not a space or
// tab.
func skipSpaces(t []byte, i int) int {
	for i < len(t) && isSpaceOrTab(t[i]) {
		i++
	}
	return i
}

// skipSpnl skips spaces and tabs, at most one line ending, and more spaces
// and tabs.
func skipSpnl(t []byte, i int) int {
	i = skipSpaces(t, i)
	if i < len(t) && t[i] == '\n' {
		i = skipSpaces(t, i+1)
	}
	return i
}

// destPlain marks the bytes a link destination without angle brackets can
// contain with no further thought: not a control character, space, backslash
// or parenthesis.
var destPlain = func() (t [256]bool) {
	for c := 0x21; c < 256; c++ {
		t[c] = c != 0x7f && c != '\\' && c != '(' && c != ')'
	}
	return
}()

// scanLinkDest scans a link destination at i. It returns the end of the
// destination and the bounds of its content (without angle brackets).
func scanLinkDest(t []byte, i int) (end, cs, ce int) {
	if i < len(t) && t[i] == '<' {
		for j := i + 1; j < len(t); j++ {
			switch t[j] {
			case '>':
				return j + 1, i + 1, j
			case '\\':
				if j+1 < len(t) && isASCIIPunct(t[j+1]) {
					j++
				}
			case '\n', '<':
				return -1, 0, 0
			}
		}
		return -1, 0, 0
	}
	depth := 0
	j := i
loop:
	for j < len(t) {
		if destPlain[t[j]] {
			j = skipDestPlain(t, j+1)
			continue
		}
		c := t[j]
		switch {
		case c == '\\' && j+1 < len(t) && isASCIIPunct(t[j+1]):
			j += 2
		case c == '(':
			depth++
			if depth > 32 {
				return -1, 0, 0
			}
			j++
		case c == ')':
			if depth == 0 {
				break loop
			}
			depth--
			j++
		case c <= ' ' || c == 0x7f:
			break loop
		default:
			j++
		}
	}
	if depth != 0 {
		return -1, 0, 0
	}
	return j, i, j
}

// scanLinkTitle scans a link title at i and returns its end, or -1. cc may
// be nil.
func scanLinkTitle(t []byte, i int, cc *closerCache) int {
	if i >= len(t) {
		return -1
	}
	closer := t[i]
	which := closeDQuote
	switch closer {
	case '"':
	case '\'':
		which = closeSQuote
	case '(':
		closer = ')'
	default:
		return -1
	}
	// A search from i+1 that failed also fails from any later start: the
	// later start is just past an opening quote, which the earlier search
	// saw escaped, so both pair up backslashes the same way from there on.
	if cc != nil && closer != ')' && i+1 >= cc[which] {
		return -1
	}
	for j := i + 1; j < len(t); j++ {
		c := t[j]
		switch {
		case c == '\\' && j+1 < len(t) && isASCIIPunct(t[j+1]):
			j++
		case c == closer:
			return j + 1
		case closer == ')' && c == '(':
			return -1
		}
	}
	if cc != nil && closer != ')' {
		cc[which] = i + 1
	}
	return -1
}

// scanLinkLabel scans a link label [..] at i. It returns the end of the label
// and the bounds of its content.
func scanLinkLabel(t []byte, i int) (end, cs, ce int) {
	if i >= len(t) || t[i] != '[' {
		return -1, 0, 0
	}
	// The limit is 999 characters (§6.3), which may be up to 4 bytes each.
	for j := i + 1; j < len(t) && j-i <= 4*1000; j++ {
		switch t[j] {
		case '\\':
			if j+1 < len(t) && isASCIIPunct(t[j+1]) {
				j++
			}
		case '[':
			return -1, 0, 0
		case ']':
			if j-i-1 > 999 && utf8.RuneCount(t[i+1:j]) > 999 {
				return -1, 0, 0
			}
			return j + 1, i + 1, j
		}
	}
	return -1, 0, 0
}

// appendLabel appends label folded for matching (spec §4.7) to dst: Unicode
// case fold, whitespace runs collapsed to one space, and trimmed. It appends
// nothing for a label that is empty after that. Callers fold into a scratch
// buffer, so that a lookup does not allocate a string.
func appendLabel(dst, label []byte) []byte {
	n0 := len(dst)
	space := false
	for i := 0; i < len(label); {
		c := label[i]
		if c < utf8.RuneSelf {
			i++
			if isSpace(c) {
				space = len(dst) > n0
				continue
			}
			if space {
				dst = append(dst, ' ')
				space = false
			}
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			dst = append(dst, c)
			continue
		}
		// Only spaces, tabs and line endings are collapsed (spec §6.3), so
		// a no-break space is part of the label like any other rune.
		r, n := utf8.DecodeRune(label[i:])
		i += n
		if space {
			dst = append(dst, ' ')
			space = false
		}
		dst = foldRune(dst, r)
	}
	return dst
}

// unescapeInto appends t with backslash escapes and entity references
// resolved, which is how link destinations, titles and info strings are read.
func unescapeInto(dst, t []byte) []byte {
	for i := 0; i < len(t); {
		c := t[i]
		switch {
		case c == '\\' && i+1 < len(t) && isASCIIPunct(t[i+1]):
			dst = append(dst, t[i+1])
			i += 2
		case c == '&':
			if end, dec := scanEntity(t, i, dst); end > 0 {
				dst = dec
				i = end
				continue
			}
			dst = append(dst, c)
			i++
		default:
			j := i + 1
			for j < len(t) && t[j] != '\\' && t[j] != '&' {
				j++
			}
			dst = append(dst, t[i:j]...)
			i = j
		}
	}
	return dst
}

// scanEntity recognises an entity or numeric character reference at t[i] ==
// '&'. On success it returns the offset past the ';' and dst with the decoded
// text appended.
func scanEntity(t []byte, i int, dst []byte) (int, []byte) {
	j := i + 1
	if j < len(t) && t[j] == '#' {
		j++
		cp, digits := 0, 0
		if j < len(t) && t[j]|0x20 == 'x' {
			j++
			for ; j < len(t) && digits < 7; j++ {
				c := t[j]
				var v int
				switch {
				case isDigit(c):
					v = int(c - '0')
				case c|0x20 >= 'a' && c|0x20 <= 'f':
					v = int(c|0x20-'a') + 10
				default:
					goto hexDone
				}
				cp = cp*16 + v
				digits++
			}
		hexDone:
			if digits == 0 || digits > 6 {
				return -1, dst
			}
		} else {
			for ; j < len(t) && isDigit(t[j]) && digits < 8; j++ {
				cp = cp*10 + int(t[j]-'0')
				digits++
			}
			if digits == 0 || digits > 7 {
				return -1, dst
			}
		}
		if j >= len(t) || t[j] != ';' {
			return -1, dst
		}
		if cp == 0 || cp > unicode.MaxRune || cp >= 0xd800 && cp <= 0xdfff {
			cp = unicode.ReplacementChar
		}
		return j + 1, utf8.AppendRune(dst, rune(cp))
	}
	for j < len(t) && isAlnum(t[j]) && j-i <= 32 {
		j++
	}
	if j == i+1 || j >= len(t) || t[j] != ';' {
		return -1, dst
	}
	name := t[i : j+1]
	dec := html.UnescapeString(string(name))
	// html.UnescapeString falls back to the longest legacy entity that is a
	// prefix of the name, leaving the rest (and so the ';') in place. Only an
	// exact match consumes the ';', and only &semi; decodes to one.
	if dec == string(name) || strings.HasSuffix(dec, ";") && string(name) != "&semi;" {
		return -1, dst
	}
	return j + 1, append(dst, dec...)
}

// HTML (spec §4.6 and §6.6).

// scanTagName returns the end of a tag name at i, or -1.
func scanTagName(t []byte, i int) int {
	if i >= len(t) || !isAlpha(t[i]) {
		return -1
	}
	i++
	for i < len(t) && (isAlnum(t[i]) || t[i] == '-') {
		i++
	}
	return i
}

// skipTagSpace skips spaces, tabs and line endings; it reports whether it
// skipped any.
func skipTagSpace(t []byte, i int) (int, bool) {
	j := i
	for j < len(t) && (isSpaceOrTab(t[j]) || isLineEnd(t[j])) {
		j++
	}
	return j, j > i
}

// scanOpenTag scans the rest of an open tag after its name at i and returns
// the offset past the closing '>'.
func scanOpenTag(t []byte, i int) int {
	for {
		j, sp := skipTagSpace(t, i)
		if j >= len(t) {
			return -1
		}
		if t[j] == '>' {
			return j + 1
		}
		if t[j] == '/' {
			if j+1 < len(t) && t[j+1] == '>' {
				return j + 2
			}
			return -1
		}
		if !sp {
			return -1
		}
		// attribute name
		c := t[j]
		if !(isAlpha(c) || c == '_' || c == ':') {
			return -1
		}
		j++
		for j < len(t) && (isAlnum(t[j]) || t[j] == '_' || t[j] == '.' || t[j] == ':' || t[j] == '-') {
			j++
		}
		i = j
		// optional value
		k, _ := skipTagSpace(t, j)
		if k < len(t) && t[k] == '=' {
			k, _ = skipTagSpace(t, k+1)
			if k >= len(t) {
				return -1
			}
			switch q := t[k]; q {
			case '"', '\'':
				e := bytes.IndexByte(t[k+1:], q)
				if e < 0 {
					return -1
				}
				i = k + 1 + e + 1
			default:
				e := k
				for e < len(t) && !isSpaceOrTab(t[e]) && !isLineEnd(t[e]) && !strings.ContainsRune("\"'=<>`", rune(t[e])) {
					e++
				}
				if e == k {
					return -1
				}
				i = e
			}
		}
	}
}

// closerCache remembers, for each closing sequence a raw HTML construct or
// link title searches ahead for, the leftmost offset from which the search
// is known to fail. Without it, a leaf full of unclosed "<!--" or '"' is
// quadratic.
type closerCache [6]int

const (
	closeComment = iota // -->
	closePI             // ?>
	closeCDATA          // ]]>
	closeDecl           // >
	closeDQuote         // " (link title)
	closeSQuote         // ' (link title)
)

func (c *closerCache) reset() {
	for i := range c {
		c[i] = 1 << 62
	}
}

// find returns the offset of sep in t at or after from, or -1.
func (c *closerCache) find(t []byte, from, which int, sep string) int {
	if from >= c[which] {
		return -1
	}
	e := bytes.Index(t[from:], []byte(sep))
	if e < 0 {
		c[which] = from
		return -1
	}
	return from + e
}

// scanRawHTML scans an inline raw HTML construct at t[i] == '<'.
func scanRawHTML(t []byte, i int, cc *closerCache) int {
	j := i + 1
	if j >= len(t) {
		return -1
	}
	switch c := t[j]; {
	case isAlpha(c):
		if e := scanTagName(t, j); e > 0 {
			return scanOpenTag(t, e)
		}
	case c == '/':
		e := scanTagName(t, j+1)
		if e < 0 {
			return -1
		}
		e, _ = skipTagSpace(t, e)
		if e < len(t) && t[e] == '>' {
			return e + 1
		}
	case c == '?':
		if e := cc.find(t, j+1, closePI, "?>"); e >= 0 {
			return e + 2
		}
	case c == '!':
		rest := t[j+1:]
		switch {
		case bytes.HasPrefix(rest, []byte("-->")):
			return j + 4
		case bytes.HasPrefix(rest, []byte("--->")):
			return j + 5
		case bytes.HasPrefix(rest, []byte("--")):
			if e := cc.find(t, j+3, closeComment, "-->"); e >= 0 {
				return e + 3
			}
		case bytes.HasPrefix(rest, []byte("[CDATA[")):
			if e := cc.find(t, j+8, closeCDATA, "]]>"); e >= 0 {
				return e + 3
			}
		case len(rest) > 0 && isAlpha(rest[0]):
			if e := cc.find(t, j+1, closeDecl, ">"); e >= 0 {
				return e + 1
			}
		}
	}
	return -1
}

// scanAutolink scans an autolink at t[i] == '<'. It returns the end and
// whether it is an e-mail address.
func scanAutolink(t []byte, i int) (int, bool) {
	j := i + 1
	// URI: scheme ':' then no spaces, controls, '<' or '>'.
	if j < len(t) && isAlpha(t[j]) {
		k := j + 1
		for k < len(t) && (isAlnum(t[k]) || t[k] == '+' || t[k] == '.' || t[k] == '-') {
			k++
		}
		if n := k - j; n >= 2 && n <= 32 && k < len(t) && t[k] == ':' {
			for k++; k < len(t); k++ {
				c := t[k]
				if c == '>' {
					return k + 1, false
				}
				if c <= ' ' || c == '<' || c == 0x7f {
					break
				}
			}
		}
	}
	// E-mail.
	k := j
	for k < len(t) && (isAlnum(t[k]) || strings.IndexByte(".!#$%&'*+/=?^_`{|}~-", t[k]) >= 0) {
		k++
	}
	if k == j || k >= len(t) || t[k] != '@' {
		return -1, false
	}
	k++
	for {
		// one label
		s := k
		for k < len(t) && (isAlnum(t[k]) || t[k] == '-') && k-s < 63 {
			k++
		}
		if k == s || t[s] == '-' || t[k-1] == '-' {
			return -1, false
		}
		if k >= len(t) {
			return -1, false
		}
		switch t[k] {
		case '>':
			return k + 1, true
		case '.':
			k++
		default:
			return -1, false
		}
	}
}

// HTML block starts (spec §4.6).

var htmlBlockTags = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range strings.Fields(`address article aside base basefont blockquote body
		caption center col colgroup dd details dialog dir div dl dt fieldset figcaption
		figure footer form frame frameset h1 h2 h3 h4 h5 h6 head header hr html iframe
		legend li link main menu menuitem nav noframes ol optgroup option p param search
		section summary table tbody td tfoot th thead title tr track ul`) {
		m[s] = true
	}
	return m
}()

var htmlBlock1Tags = []string{"pre", "script", "style", "textarea"}

// lowerTagName returns the tag name at t[i:e] in lower case, or "" if it is
// longer than any name we look up.
func lowerTagName(t []byte) string {
	if len(t) > 10 {
		return ""
	}
	var b [10]byte
	for i, c := range t {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b[:len(t)])
}

// htmlBlockStart returns the start condition (1-7) of an HTML block at line[0]
// == '<', or 0. Condition 7 is tested only if allow7.
func htmlBlockStart(line []byte, allow7 bool) int {
	if len(line) < 2 {
		return 0
	}
	rest := line[1:]
	switch c := rest[0]; {
	case c == '!':
		switch {
		case bytes.HasPrefix(rest, []byte("!--")):
			return 2
		case bytes.HasPrefix(rest, []byte("![CDATA[")):
			return 5
		case len(rest) > 1 && isAlpha(rest[1]):
			return 4
		}
		return 0
	case c == '?':
		return 3
	}
	closing := rest[0] == '/'
	ns := 0
	if closing {
		ns = 1
	}
	e := scanTagName(rest, ns)
	if e < 0 {
		return 0
	}
	name := lowerTagName(rest[ns:e])
	var after byte = '\n'
	if e < len(rest) {
		after = rest[e]
	}
	if !closing {
		for _, n := range htmlBlock1Tags {
			if name == n && (isSpaceOrTab(after) || after == '>' || after == '\n') {
				return 1
			}
		}
	}
	if htmlBlockTags[name] && (isSpaceOrTab(after) || after == '>' || after == '\n' ||
		after == '/' && e+1 < len(rest) && rest[e+1] == '>') {
		return 6
	}
	if !allow7 {
		return 0
	}
	var end int
	if closing {
		end, _ = skipTagSpace(rest, e)
		if end >= len(rest) || rest[end] != '>' {
			return 0
		}
		end++
	} else {
		if name == "pre" || name == "script" || name == "style" || name == "textarea" {
			return 0
		}
		end = scanOpenTag(rest, e)
		if end < 0 {
			return 0
		}
	}
	for _, c := range rest[end:] {
		if !isSpaceOrTab(c) {
			return 0
		}
	}
	return 7
}

// htmlBlockEnds reports whether line satisfies the end condition of an HTML
// block of the given kind (1-5).
func htmlBlockEnds(kind int, line []byte) bool {
	switch kind {
	case 1:
		for i := 0; ; {
			k := bytes.Index(line[i:], []byte("</"))
			if k < 0 {
				return false
			}
			i += k + 2
			for _, n := range htmlBlock1Tags {
				if len(line)-i > len(n) && strings.EqualFold(string(line[i:i+len(n)]), n) && line[i+len(n)] == '>' {
					return true
				}
			}
		}
	case 2:
		return bytes.Contains(line, []byte("-->"))
	case 3:
		return bytes.Contains(line, []byte("?>"))
	case 4:
		return bytes.IndexByte(line, '>') >= 0
	case 5:
		return bytes.Contains(line, []byte("]]>"))
	}
	return false
}
