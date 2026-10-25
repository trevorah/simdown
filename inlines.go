package simdown

import (
	"bytes"
	"unicode/utf8"
)

// inlineKind is the kind of an inline tape entry or scratch node.
type inlineKind uint8

const (
	iText inlineKind = iota
	iSoftBreak
	iHardBreak
	iCode
	iHTML
	iEmphOpen
	iEmphClose
	iStrongOpen
	iStrongClose
	iLinkOpen
	iLinkClose
	iImageOpen
	iImageClose
)

// inline is one entry of the inline tape. Text, code and HTML hold a range
// [a, b).
type inline struct {
	kind inlineKind
	a, b uint32
	link uint32
}

type linkInfo struct {
	dest, title rng
	mailto      bool
}

// inode is a node of the scratch list a leaf is built in.
type inode struct {
	kind       inlineKind
	a, b       uint32
	prev, next int32
}

type delim struct {
	node       int32
	char       byte
	canOpen    bool
	canClose   bool
	origLen    int32
	prev, next int32
}

type bracket struct {
	node         int32
	labelStart   int // relative offset just past the '[' or '!['
	delimBottom  int32
	image        bool
	active       bool
	bracketAfter bool
}

type inlineState struct {
	t    []byte   // the leaf's content, lines joined by '\n'
	base uint32   // address of t[0]
	ents []uint32 // inline index entries inside t, as addresses
	nls  []int32  // offsets in t of the '\n' between lines
	cur  int      // global cursor into the inline index

	// A leaf's nodes go straight to the inline tape from index start on,
	// until emphasis has to insert nodes between them. Then they move to
	// nodes, a linked list, for emit to write back (linked is set).
	start      int
	linked     bool
	pinned     int32 // the last node a delimiter or bracket refers to
	nodes      []inode
	head, tail int32
	delims     []delim
	lastDelim  int32
	brackets   []bracket

	// The last backtick run seen of each length (cmark's backtick cache,
	// without its limit on the length).
	tickLast    []int32
	tickScanned bool
	closers     closerCache

	pos, textStart int
	entScratch     []uint32
	openers        []uint32 // emit's open entries
	linkLabel      []byte
}

func (p *parser) parseInlines() {
	p.cur = 0
	for _, i := range p.leaves {
		p.leaf(&p.blocks[i], p.ix.Inline)
	}
}

// seek moves the global cursor to the first entry at or after pos. Entries
// in code blocks, HTML blocks and container markers are passed over here, a
// run of them at a time.
func (p *parser) seek(idx []uint32, pos uint32) {
	// Usually the cursor is already there; the check inlines.
	if c := p.cur; c < len(idx) && idx[c] < pos {
		p.seekFrom(idx, pos, c)
	}
}

func (p *parser) seekFrom(idx []uint32, pos uint32, c int) {
	// A few entries (a list marker, say) are skipped one by one, and a
	// code block's by galloping.
	for end := min(c+4, len(idx)); c < end; c++ {
		if idx[c] >= pos {
			p.cur = c
			return
		}
	}
	c--
	step := 1
	for c+step < len(idx) && idx[c+step] < pos {
		c += step
		step *= 2
	}
	lo, hi := c+1, min(c+step, len(idx))
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if idx[m] < pos {
			lo = m + 1
		} else {
			hi = m
		}
	}
	p.cur = lo
}

func (p *parser) leaf(b *block, idx []uint32) {
	spans := p.spans[b.a:b.b]
	start := uint32(len(p.inl))
	b.a, b.b = start, start
	if len(spans) == 0 {
		return
	}
	if len(spans) == 1 {
		// Over half of all leaves are one line.
		s := spans[0]
		p.seek(idx, s.start)
		e0 := p.cur
		p.seek(idx, s.end)
		if e0 == p.cur {
			if a := uint32(skipSpaces(p.src[:s.end], int(s.start))); s.end > a {
				p.inl = append(p.inl, inline{kind: iText, a: a, b: s.end, link: a})
			}
			b.b = uint32(len(p.inl))
			return
		}
		p.t, p.base, p.ents = p.src[s.start:s.end], s.start, idx[e0:p.cur]
		p.nls = p.nls[:0]
		p.parseText()
		if p.linked {
			p.emit()
		}
		b.b = uint32(len(p.inl))
		return
	}
	p.nls = p.nls[:0]
	contiguous := true
	for k := 0; k+1 < len(spans); k++ {
		if spans[k+1].start != spans[k].end+1 || p.src[spans[k].end] != '\n' {
			contiguous = false
			break
		}
	}
	if contiguous {
		// The common case: the leaf is one slice of the source, and its
		// entries one slice of the index.
		lo, hi := spans[0].start, spans[len(spans)-1].end
		p.seek(idx, lo)
		e0 := p.cur
		p.seek(idx, hi)
		if e0 == p.cur {
			p.plain(spans)
			b.b = uint32(len(p.inl))
			return
		}
		p.t, p.base = p.src[lo:hi], lo
		for _, s := range spans[:len(spans)-1] {
			p.nls = append(p.nls, int32(s.end-lo))
		}
		p.ents = idx[e0:p.cur]
	} else if !p.hasEntries(spans, idx) {
		p.plain(spans)
		b.b = uint32(len(p.inl))
		return
	} else {
		// Container prefixes or indentation sit between the lines: copy
		// the content out, and move its entries with it.
		auxStart := len(p.aux)
		base := uint32(len(p.src) + auxStart)
		ents := p.entScratch[:0]
		for k, s := range spans {
			if k > 0 {
				p.nls = append(p.nls, int32(len(p.aux)-auxStart))
				p.aux = append(p.aux, '\n')
			}
			at := uint32(len(p.src)+len(p.aux)) - s.start
			p.seek(idx, s.start)
			for p.cur < len(idx) && idx[p.cur] < s.end {
				ents = append(ents, idx[p.cur]+at)
				p.cur++
			}
			p.aux = append(p.aux, p.src[s.start:s.end]...)
		}
		p.entScratch = ents
		p.t, p.base, p.ents = p.aux[auxStart:], base, ents
	}
	p.parseText()
	if p.linked {
		p.emit()
	}
	b.b = uint32(len(p.inl))
}

// hasEntries reports whether any index entry falls inside spans. It
// leaves the cursor at the first entry after spans[0].start either way.
func (p *parser) hasEntries(spans []span, idx []uint32) bool {
	p.seek(idx, spans[0].start)
	c := p.cur
	for _, s := range spans {
		p.seek(idx, s.start)
		if p.cur < len(idx) && idx[p.cur] < s.end {
			p.cur = c
			return true
		}
	}
	return false
}

// plain writes a leaf with no index entry inside its spans, which is almost
// half of them, straight to the inline tape: its lines, as text, joined by
// breaks, as newline and emit would write them.
func (p *parser) plain(spans []span) {
	src := p.src
	last := len(spans) - 1
	join := false
	for k, s := range spans {
		a := uint32(skipSpaces(src[:s.end], int(s.start)))
		e := s.end
		if k < last {
			// Only spaces before a line break are removed (§6.8), not tabs.
			for e > a && src[e-1] == ' ' {
				e--
			}
		}
		if join {
			// The break before this line is kept in the text.
			p.inl[len(p.inl)-1].b = e
			join = false
		} else if e > a {
			p.inl = append(p.inl, inline{kind: iText, a: a, b: e, link: a})
		}
		if k < last {
			if n := spans[k+1]; e == s.end && e > a && n.start == e+1 && src[e] == '\n' && !isSpaceOrTab(src[n.start]) {
				join = true
				continue
			}
			kind := iSoftBreak
			if s.end-s.start >= 2 && src[s.end-1] == ' ' && src[s.end-2] == ' ' {
				kind = iHardBreak
			}
			p.inl = append(p.inl, inline{kind: kind})
		}
	}
}

func (p *parser) parseText() {
	p.start, p.linked, p.pinned = len(p.inl), false, -1
	p.nodes = p.nodes[:0]
	p.head, p.tail = -1, -1
	p.delims = p.delims[:0]
	p.lastDelim = -1
	p.brackets = p.brackets[:0]
	p.tickScanned = false
	p.closers.reset()
	p.tickLast = p.tickLast[:0]
	p.pos, p.textStart = 0, 0

	// One pass over the entries, with the line breaks merged in. A handler
	// may consume the text past later entries and breaks (a code span, a
	// link destination); p.pos says how far, and those are skipped.
	t, ents, nls, base := p.t, p.ents, p.nls, p.base
	ni := 0
	for ei := 0; ei < len(ents); ei++ {
		q := int(ents[ei] - base)
		for ni < len(nls) && int(nls[ni]) < q {
			if nl := int(nls[ni]); nl >= p.pos {
				p.newline(nl)
			}
			ni++
		}
		if q < p.pos {
			continue
		}
		switch t[q] {
		case '\\':
			p.backslash(q)
		case '`':
			p.codeSpan(q, ei)
		case '*', '_':
			p.delimRun(q)
		case '[':
			p.openBracket(q, 1, false)
		case '!':
			p.openBracket(q, 2, true)
		case ']':
			p.closeBracket(q)
		case '<':
			p.angle(q)
		case '&':
			p.entity(q)
		default:
			p.pos = q + 1
		}
	}
	for ; ni < len(nls); ni++ {
		if nl := int(nls[ni]); nl >= p.pos {
			p.newline(nl)
		}
	}
	p.flush(len(t))
	p.processEmphasis(0)
}

// Node list

func (p *parser) newNode(kind inlineKind, a, b uint32) int32 {
	p.nodes = append(p.nodes, inode{kind: kind, a: a, b: b, prev: -1, next: -1})
	return int32(len(p.nodes) - 1)
}

// returns its index
func (p *parser) push(kind inlineKind, a, b uint32) int32 {
	if !p.linked {
		p.inl = append(p.inl, inline{kind: kind, a: a, b: b, link: a})
		return int32(len(p.inl) - 1 - p.start)
	}
	n := p.newNode(kind, a, b)
	p.nodes[n].prev = p.tail
	if p.tail >= 0 {
		p.nodes[p.tail].next = n
	} else {
		p.head = n
	}
	p.tail = n
	return n
}

// pushClose appends the closer of the link or image whose opener is node
// opener.
func (p *parser) pushClose(kind inlineKind, opener int32) {
	c := p.push(kind, 0, 0)
	if !p.linked {
		inl := p.inl[p.start:]
		inl[opener].link = uint32(p.start) + uint32(c)
		inl[c].a, inl[c].link = uint32(p.start)+uint32(opener), uint32(p.start)+uint32(opener)
	}
}

// link moves the leaf's nodes from the tape to the linked list.
func (p *parser) link() {
	inl := p.inl[p.start:]
	for i, e := range inl {
		p.nodes = append(p.nodes, inode{kind: e.kind, a: e.a, b: e.b, prev: int32(i) - 1, next: int32(i) + 1})
	}
	p.head, p.tail = -1, int32(len(inl))-1
	if len(inl) > 0 {
		p.head = 0
		p.nodes[len(inl)-1].next = -1
	}
	p.inl = p.inl[:p.start]
	p.linked = true
}

func (p *parser) insertAfter(at int32, kind inlineKind) {
	n := p.newNode(kind, 0, 0)
	nx := p.nodes[at].next
	p.nodes[n].prev, p.nodes[n].next = at, nx
	p.nodes[at].next = n
	if nx >= 0 {
		p.nodes[nx].prev = n
	} else {
		p.tail = n
	}
}

func (p *parser) insertBefore(at int32, kind inlineKind) {
	n := p.newNode(kind, 0, 0)
	pv := p.nodes[at].prev
	p.nodes[n].prev, p.nodes[n].next = pv, at
	p.nodes[at].prev = n
	if pv >= 0 {
		p.nodes[pv].next = n
	} else {
		p.head = n
	}
}

func (p *parser) unlink(n int32) {
	pv, nx := p.nodes[n].prev, p.nodes[n].next
	if pv >= 0 {
		p.nodes[pv].next = nx
	} else {
		p.head = nx
	}
	if nx >= 0 {
		p.nodes[nx].prev = pv
	} else {
		p.tail = pv
	}
}

// flush emits the pending text up to end.
func (p *parser) flush(end int) {
	if end > p.textStart {
		a, b := p.base+uint32(p.textStart), p.base+uint32(end)
		if !p.linked {
			// Merge with the text before, as emit would, unless a
			// delimiter or bracket may still change that node.
			if l := len(p.inl) - 1; l >= p.start && int32(l-p.start) > p.pinned &&
				p.inl[l].kind == iText && p.inl[l].b == a {
				p.inl[l].b = b
				return
			}
		}
		p.push(iText, a, b)
	}
}

// emitAt flushes the text before q, emits a node, and resumes after end.
func (p *parser) emitAt(q, end int, kind inlineKind, a, b uint32) int32 {
	p.flush(q)
	n := p.push(kind, a, b)
	p.pos, p.textStart = end, end
	return n
}

// Handlers. Each is given the offset q of an index entry. One that rejects
// the entry leaves textStart alone, so the byte stays in the text around it.

func (p *parser) newline(q int) {
	t := p.t
	if q > 0 && t[q-1] != ' ' && !isSpaceOrTab(t[q+1]) {
		// A soft break with nothing to strip on either side renders as
		// the '\n' that is already in t, so it stays in the text around
		// it: one text entry for the run of lines instead of two per
		// line, and one escapeHTML call instead of one per line.
		p.pos = q + 1
		return
	}
	// Spaces before the break are removed (§6.8), but not tabs, and spaces
	// or tabs after it are.
	e := q
	for e > p.textStart && t[e-1] == ' ' {
		e--
	}
	p.flush(e)
	kind := iSoftBreak
	if q >= 2 && t[q-1] == ' ' && t[q-2] == ' ' {
		kind = iHardBreak
	}
	p.push(kind, 0, 0)
	p.pos = skipSpaces(t, q+1)
	p.textStart = p.pos
}

func (p *parser) backslash(q int) {
	t := p.t
	if q+1 < len(t) {
		if c := t[q+1]; c == '\n' {
			p.flush(q)
			p.push(iHardBreak, 0, 0)
			p.pos = skipSpaces(t, q+2)
			p.textStart = p.pos
			return
		} else if isASCIIPunct(c) {
			// Drop the backslash; the escaped byte starts the next text.
			p.flush(q)
			p.textStart, p.pos = q+1, q+2
			return
		}
	}
	p.pos = q + 1
}

func runLength(t []byte, i int, c byte) int {
	n := 0
	for i+n < len(t) && t[i+n] == c {
		n++
	}
	return n
}

// codeSpan tries a code span opening at q, whose entry is ents[ei].
func (p *parser) codeSpan(q, ei int) {
	t := p.t
	n := runLength(t, q, '`')
	// Once a search has run to the end of the leaf without a match,
	// tickLast holds the last run of each length, so an opener with no
	// closer after it fails at once (spec's pathological case).
	if p.tickScanned && (n >= len(p.tickLast) || int(p.tickLast[n]) <= q) {
		p.pos = q + n
		return
	}
	end := q + n
	for j := ei + 1; j < len(p.ents); j++ {
		r := int(p.ents[j] - p.base)
		if r < end {
			continue
		}
		// A raw backtick run starts at a backtick entry, or just after
		// a backslash entry, since backslashes are literal in code spans.
		switch {
		case t[r] == '`':
		case t[r] == '\\' && r+1 < len(t) && t[r+1] == '`':
			r++
		default:
			continue
		}
		m := runLength(t, r, '`')
		if !p.tickScanned {
			for len(p.tickLast) <= m {
				p.tickLast = append(p.tickLast, -1)
			}
			p.tickLast[m] = int32(r)
		}
		if m == n {
			s, e := q+n, r
			if e-s >= 2 && (t[s] == ' ' || t[s] == '\n') && (t[e-1] == ' ' || t[e-1] == '\n') &&
				len(bytes.Trim(t[s:e], " \n")) > 0 {
				s++
				e--
			}
			p.emitAt(q, r+m, iCode, p.base+uint32(s), p.base+uint32(e))
			return
		}
		end = r + m
	}
	p.tickScanned = true
	p.pos = q + n
}

func (p *parser) delimRun(q int) {
	t := p.t
	c := t[q]
	n := runLength(t, q, c)
	bws, bp := true, false
	if q > 0 {
		bws, bp = flankClass(t[:q], true)
	}
	aws, ap := true, false
	if q+n < len(t) {
		aws, ap = flankClass(t[q+n:], false)
	}
	left := !aws && (!ap || bws || bp)
	right := !bws && (!bp || aws || ap)
	canOpen, canClose := left, right
	if c == '_' {
		canOpen = left && (!right || bp)
		canClose = right && (!left || ap)
	}
	if !canOpen && !canClose {
		// Not a delimiter, such as the _ inside a word: it stays in the
		// text around it.
		p.pos = q + n
		return
	}
	node := p.emitAt(q, q+n, iText, p.base+uint32(q), p.base+uint32(q+n))
	p.pinned = node
	p.delims = append(p.delims, delim{node: node, char: c, canOpen: canOpen, canClose: canClose,
		origLen: int32(n), prev: p.lastDelim, next: -1})
	i := int32(len(p.delims) - 1)
	if p.lastDelim >= 0 {
		p.delims[p.lastDelim].next = i
	}
	p.lastDelim = i
}

// flankClass reports whether the character at the end of t (before is
// true) or at its start is Unicode whitespace or punctuation, for the
// flanking rules of spec §6.2. ASCII, which is nearly always what it finds,
// goes by table.
func flankClass(t []byte, before bool) (ws, punct bool) {
	var c byte
	if before {
		c = t[len(t)-1]
	} else {
		c = t[0]
	}
	if c < utf8.RuneSelf {
		k := asciiFlank[c]
		return k&1 != 0, k&2 != 0
	}
	var r rune
	if before {
		r, _ = utf8.DecodeLastRune(t)
	} else {
		r, _ = utf8.DecodeRune(t)
	}
	return isUnicodeWhitespace(r), isUnicodePunct(r)
}

// asciiFlank holds, for each ASCII byte, 1 if it is whitespace and 2 if it
// is punctuation.
var asciiFlank = func() (t [128]uint8) {
	for c := range t {
		if isUnicodeWhitespace(rune(c)) {
			t[c] |= 1
		}
		if isASCIIPunct(byte(c)) {
			t[c] |= 2
		}
	}
	return
}()

func (p *parser) openBracket(q, n int, image bool) {
	node := p.emitAt(q, q+n, iText, p.base+uint32(q), p.base+uint32(q+n))
	p.pinned = node
	if len(p.brackets) > 0 {
		p.brackets[len(p.brackets)-1].bracketAfter = true
	}
	p.brackets = append(p.brackets, bracket{node: node, labelStart: q + n,
		delimBottom: int32(len(p.delims)), image: image, active: true})
}

func (p *parser) closeBracket(q int) {
	t := p.t
	p.pos = q + 1
	if len(p.brackets) == 0 {
		return
	}
	op := p.brackets[len(p.brackets)-1]
	if !op.active {
		p.brackets = p.brackets[:len(p.brackets)-1]
		return
	}
	after := q + 1
	var li linkInfo
	end := -1

	// Inline link: [text](dest "title")
	if after < len(t) && t[after] == '(' {
		j := skipSpnl(t, after+1)
		if dend, ds, de := scanLinkDest(t, j); dend >= 0 {
			k := skipSpnl(t, dend)
			ts, te := -1, -1
			if k > dend {
				if tend := scanLinkTitle(t, k, &p.closers); tend > 0 {
					ts, te = k+1, tend-1
					k = skipSpnl(t, tend)
				}
			}
			if k < len(t) && t[k] == ')' {
				li.dest = p.unescapedAt(t[ds:de], p.base+uint32(ds))
				if ts >= 0 {
					li.title = p.unescapedAt(t[ts:te], p.base+uint32(ts))
				}
				end = k + 1
			}
		}
	}

	// Reference link: full [label], collapsed [], or shortcut.
	if end < 0 {
		var label []byte
		next := after
		lend, ls, le := scanLinkLabel(t, after)
		if lend > 0 {
			label = bytes.TrimFunc(t[ls:le], func(r rune) bool { return r < 0x80 && isSpace(byte(r)) })
		}
		if len(label) > 0 {
			next = lend
		} else if !op.bracketAfter {
			// Collapsed if [] follows, else shortcut. Brackets around only
			// spaces, tabs and line endings are not a link label (§6.3),
			// so they leave a shortcut link and stay as text.
			label = t[op.labelStart:q]
			if lend == after+2 {
				next = lend
			}
		}
		if len(label) > 0 && (len(label) <= 999 || utf8.RuneCount(label) <= 999) && len(p.refs) > 0 {
			p.linkLabel = appendLabel(p.linkLabel[:0], label)
			if ref, ok := p.refs[string(p.linkLabel)]; ok {
				li.dest, li.title = ref.dest, ref.title
				end = next
			}
		}
	}

	p.brackets = p.brackets[:len(p.brackets)-1]
	if end < 0 {
		return
	}

	p.flush(q)
	open, close := iLinkOpen, iLinkClose
	if op.image {
		open, close = iImageOpen, iImageClose
	}
	if p.linked {
		on := &p.nodes[op.node]
		on.kind, on.a = open, uint32(len(p.links))
	} else {
		on := &p.inl[p.start+int(op.node)]
		on.kind, on.a = open, uint32(len(p.links))
	}
	p.links = append(p.links, li)
	p.pushClose(close, op.node)
	p.pos, p.textStart = end, end

	p.processEmphasis(op.delimBottom)
	if !op.image {
		// Links may not contain links.
		for i := range p.brackets {
			if !p.brackets[i].image {
				p.brackets[i].active = false
			}
		}
	}
}

func (p *parser) angle(q int) {
	t := p.t
	if end, email := scanAutolink(t, q); end > 0 {
		li := linkInfo{dest: rng{p.base + uint32(q+1), p.base + uint32(end-1)}, mailto: email}
		p.flush(q)
		o := p.push(iLinkOpen, uint32(len(p.links)), 0)
		p.links = append(p.links, li)
		p.push(iText, li.dest.start, li.dest.end)
		p.pushClose(iLinkClose, o)
		p.pos, p.textStart = end, end
		return
	}
	if end := scanRawHTML(t, q, &p.closers); end > 0 {
		p.emitAt(q, end, iHTML, p.base+uint32(q), p.base+uint32(end))
		return
	}
	p.pos = q + 1
}

func (p *parser) entity(q int) {
	start := len(p.src) + len(p.aux)
	end, aux := scanEntity(p.t, q, p.aux)
	if end < 0 {
		p.pos = q + 1
		return
	}
	p.aux = aux
	p.emitAt(q, end, iText, uint32(start), uint32(len(p.src)+len(p.aux)))
}

// Emphasis (spec §6.2, "process emphasis").

func (p *parser) removeDelim(i int32) {
	dl := &p.delims[i]
	if dl.prev >= 0 {
		p.delims[dl.prev].next = dl.next
	}
	if dl.next >= 0 {
		p.delims[dl.next].prev = dl.prev
	} else {
		p.lastDelim = dl.prev
	}
}

func (p *parser) processEmphasis(bottom int32) {
	if p.lastDelim < bottom {
		// No delimiters: the common case, for a leaf and for link text.
		return
	}
	// openersBottom is indexed by delimiter character, whether the closer
	// can also open, and the closer's length mod 3.
	var openersBottom [2][2][3]int32
	for a := range openersBottom {
		for b := range openersBottom[a] {
			for c := range openersBottom[a][b] {
				openersBottom[a][b][c] = bottom
			}
		}
	}
	closer := int32(-1)
	for x := p.lastDelim; x >= bottom; x = p.delims[x].prev {
		closer = x
	}
	for closer >= 0 {
		cl := p.delims[closer]
		if !cl.canClose {
			closer = cl.next
			continue
		}
		ci, oi := 0, 0
		if cl.char == '_' {
			ci = 1
		}
		if cl.canOpen {
			oi = 1
		}
		ob := &openersBottom[ci][oi][cl.origLen%3]
		opener := cl.prev
		found := false
		for ; opener >= *ob; opener = p.delims[opener].prev {
			op := p.delims[opener]
			if op.canOpen && op.char == cl.char &&
				(!(cl.canOpen || op.canClose) || cl.origLen%3 == 0 || (op.origLen+cl.origLen)%3 != 0) {
				found = true
				break
			}
		}
		if found {
			if !p.linked {
				p.link()
			}
			closer = p.insertEmph(opener, closer)
		} else {
			*ob = closer
			if !cl.canOpen {
				p.removeDelim(closer)
			}
			closer = cl.next
		}
	}
	for p.lastDelim >= bottom {
		p.removeDelim(p.lastDelim)
	}
}

// insertEmph matches opener with closer and returns the closer to continue
// from.
func (p *parser) insertEmph(opener, closer int32) int32 {
	on, cn := p.delims[opener].node, p.delims[closer].node
	olen := p.nodes[on].b - p.nodes[on].a
	clen := p.nodes[cn].b - p.nodes[cn].a
	use := uint32(1)
	if olen >= 2 && clen >= 2 {
		use = 2
	}
	p.nodes[on].b -= use
	p.nodes[cn].a += use

	for x := p.delims[closer].prev; x != opener; {
		prev := p.delims[x].prev
		p.removeDelim(x)
		x = prev
	}

	open, close := iEmphOpen, iEmphClose
	if use == 2 {
		open, close = iStrongOpen, iStrongClose
	}
	p.insertAfter(on, open)
	p.insertBefore(cn, close)

	if olen == use {
		p.unlink(on)
		p.removeDelim(opener)
	}
	if clen == use {
		p.unlink(cn)
		next := p.delims[closer].next
		p.removeDelim(closer)
		return next
	}
	return closer
}

// emit writes the leaf's nodes to the inline tape, merging adjacent text and
// linking each opener to its closer.
func (p *parser) emit() {
	p.openers = p.openers[:0]
	start := len(p.inl)
	for n := p.head; n >= 0; n = p.nodes[n].next {
		nd := p.nodes[n]
		switch nd.kind {
		case iText:
			if nd.a == nd.b {
				continue
			}
			if l := len(p.inl) - 1; l >= start && p.inl[l].kind == iText && p.inl[l].b == nd.a {
				p.inl[l].b = nd.b
				continue
			}
		case iEmphOpen, iStrongOpen, iLinkOpen, iImageOpen:
			p.openers = append(p.openers, uint32(len(p.inl)))
		case iEmphClose, iStrongClose, iLinkClose, iImageClose:
			o := p.openers[len(p.openers)-1]
			p.openers = p.openers[:len(p.openers)-1]
			p.inl[o].link = uint32(len(p.inl))
			nd.a = o
		}
		p.inl = append(p.inl, inline{kind: nd.kind, a: nd.a, b: nd.b, link: nd.a})
	}
}
