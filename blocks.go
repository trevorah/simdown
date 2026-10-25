package simdown

import (
	"bytes"
	"slices"
)

// blockKind is the kind of a block tape entry or open block.
type blockKind uint8

const (
	kDocument blockKind = iota
	kBlockQuote
	kList
	kItem
	kParagraph
	kHeading
	kCodeBlock
	kHTMLBlock
	kThematicBreak
	kRefDefs // a list item's paragraph of only link reference definitions
	kClose   // closes the container its link points to
)

// Block flags.
const (
	fLastLineBlank = 1 << iota
	fTight         // on a list, and on each paragraph that is a child of its items
	fOrdered
	fFenced
	fSetext
)

// block is one entry of the block tape.
//
//   - Containers have an open entry and a kClose entry whose links point at
//     each other. For a list, a is the start number.
//   - A paragraph or heading has its spans in [a, b) until stage 3 replaces
//     them with its range of the inline tape.
//   - A code or HTML block has its spans in [a, b). A fenced code block's
//     link indexes parser.infos.
type block struct {
	kind  blockKind
	flags uint8
	extra uint16 // heading level
	a, b  uint32
	link  uint32
}

// span is one line of a leaf's content: src[start:end], preceded by vspace
// columns left over from a tab that a container marker partly consumed.
type span struct {
	start, end uint32
	vspace     uint32
}

type rng struct{ start, end uint32 }

type refDef struct{ dest, title rng }

// openBlock is the parser's state for a block that is still open.
type openBlock struct {
	idx       int32 // tape index
	kind      blockKind
	lastChild int32 // tape index, or -1
	startLine int32
	// lists and items
	bullet       byte // '-', '+', '*', or '.' / ')' for ordered lists
	markerOffset int32
	padding      int32
	// fenced code
	fenceChar   byte
	fenceLen    int32
	fenceOffset int32
	// HTML block start condition
	htmlType int8
}

const tabStop = 4

// blockState is stage 2's working state.
type blockState struct {
	stack []openBlock

	line                 int32
	ls, le               int // current line [ls, le), le at the terminator
	offset, column       int
	firstNonspace        int
	firstNonspaceColumn  int
	partiallyConsumedTab bool

	scratch  []byte
	offs     []int
	refLabel []byte // a definition's label, folded

	// general sends every line through the general path, so that tests
	// can check the fast paths against it.
	general bool
}

// maybeSpecial marks the bytes that can begin a block other than a paragraph
// (block quote, heading, fence, HTML, list, setext underline or thematic
// break). A line whose lead is anything else, and which is not indented as
// code, is text.
var maybeSpecial = func() (t [256]bool) {
	for _, c := range []byte(">#`~<-*+=_0123456789") {
		t[c] = true
	}
	return
}()

func (p *parser) parseBlocks() {
	ix := &p.ix
	p.line = 0
	p.stack = p.stack[:0]
	p.blocks = append(p.blocks, block{kind: kDocument})
	p.stack = append(p.stack, openBlock{idx: 0, kind: kDocument, lastChild: -1})

	start := 0
	for i := 0; i < len(ix.Term); i++ {
		if len(p.stack) == 2 && !p.general {
			// Runs of fenced code, paragraph text and raw HTML at the top
			// level are most of a document's lines.
			if top := &p.stack[1]; top.kind == kCodeBlock && top.fenceChar != 0 {
				i, start = p.fenceRun(i, start)
			} else if top.kind == kParagraph {
				i, start = p.paraRun(i, start)
			} else if top.kind == kHTMLBlock {
				i, start = p.htmlRun(i, start)
			}
			if i == len(ix.Term) {
				break
			}
		}
		p.ls, p.le = start, int(ix.Term[i])
		p.processLine(int(ix.Lead[i]))
		start = nextLine(p.src, p.le)
	}
	for len(p.stack) > 0 {
		p.finalize()
	}
}

// nextLine returns the start of the line after the terminator at le.
func nextLine(src []byte, le int) int {
	start := le + 1
	if start < len(src) && src[le] == '\r' && src[start] == '\n' {
		start++
	}
	return start
}

// fenceRun consumes the lines from line i, which starts at start, that
// continue the fenced code block open at the top level, as shallowLine
// would one at a time. It stops at a line that may close the block or that
// has a tab in its first columns, and returns the next line and its start.
func (p *parser) fenceRun(i, start int) (int, int) {
	src, term, leads := p.src, p.ix.Term, p.ix.Lead
	top := &p.stack[1]
	fc, fo := top.fenceChar, int(top.fenceOffset)
	spans := p.spans
	first := i
	for ; i < len(term); i++ {
		ls, le, lead := start, int(term[i]), int(leads[i])
		n := lead - ls
		if n <= 3 && lead < le && src[lead] == fc || hasTab(src[ls:min(lead, ls+4)]) {
			break
		}
		spans = append(spans, span{start: uint32(ls + min(fo, n)), end: uint32(le)})
		start = nextLine(src, le)
	}
	p.endRun(spans, i-first)
	return i, start
}

// paraRun is fenceRun for the lines that continue a paragraph open at the
// top level.
func (p *parser) paraRun(i, start int) (int, int) {
	src, term, leads := p.src, p.ix.Term, p.ix.Lead
	spans := p.spans
	first := i
	for ; i < len(term); i++ {
		ls, le, lead := start, int(term[i]), int(leads[i])
		if lead >= le || lead-ls < 4 && mayStart(src, lead, le) || hasTab(src[ls:min(lead, ls+4)]) {
			break
		}
		spans = append(spans, span{start: uint32(lead), end: uint32(le)})
		start = nextLine(src, le)
	}
	p.endRun(spans, i-first)
	return i, start
}

// htmlRun is fenceRun for html, duh
func (p *parser) htmlRun(i, start int) (int, int) {
	src, term, leads := p.src, p.ix.Term, p.ix.Lead
	t := int(p.stack[1].htmlType)
	spans := p.spans
	first := i
	for ; i < len(term); i++ {
		ls, le, lead := start, int(term[i]), int(leads[i])
		if lead >= le || t <= 5 && htmlBlockEnds(t, src[lead:le]) {
			break
		}
		spans = append(spans, span{start: uint32(ls), end: uint32(le)})
		start = nextLine(src, le)
	}
	p.endRun(spans, i-first)
	return i, start
}

// endRun records n lines appended by a run to the leaf at the top level.
func (p *parser) endRun(spans []span, n int) {
	if n == 0 {
		return
	}
	b := &p.blocks[p.stack[1].idx]
	if b.b == 0 {
		b.a = uint32(len(p.spans))
	}
	b.b = uint32(len(spans))
	b.flags &^= fLastLineBlank
	p.blocks[0].flags &^= fLastLineBlank
	p.spans = spans
	p.line += int32(n)
}

func hasTab(b []byte) bool { return slices.Contains(b, '\t') }

func (p *parser) peek(i int) byte {
	if i < p.le {
		return p.src[i]
	}
	return '\n'
}

func (p *parser) findNextNonspace() {
	// cmark rescans whenever firstNonspace <= offset. When they are equal
	// and the column has not moved (no tab was partly consumed since), the
	// scan would find the same answer, and that is the case on most lines.
	// The scan is out of line so that this check inlines.
	if p.firstNonspace < p.offset || p.firstNonspace == p.offset && p.firstNonspaceColumn != p.column {
		p.scanNonspace()
	}
}

//go:noinline
func (p *parser) scanNonspace() {
	p.firstNonspace = p.offset
	p.firstNonspaceColumn = p.column
	charsToTab := tabStop - p.column%tabStop
	for p.firstNonspace < p.le {
		c := p.src[p.firstNonspace]
		if c == ' ' {
			p.firstNonspace++
			p.firstNonspaceColumn++
			charsToTab--
			if charsToTab == 0 {
				charsToTab = tabStop
			}
		} else if c == '\t' {
			p.firstNonspace++
			p.firstNonspaceColumn += charsToTab
			charsToTab = tabStop
		} else {
			break
		}
	}
}

func (p *parser) indent() int { return p.firstNonspaceColumn - p.column }
func (p *parser) blank() bool { return p.firstNonspace >= p.le }

func (p *parser) advanceOffset(count int, columns bool) {
	for count > 0 && p.offset < p.le {
		if p.src[p.offset] == '\t' {
			charsToTab := tabStop - p.column%tabStop
			if columns {
				p.partiallyConsumedTab = charsToTab > count
				adv := min(count, charsToTab)
				p.column += adv
				if !p.partiallyConsumedTab {
					p.offset++
				}
				count -= adv
			} else {
				p.partiallyConsumedTab = false
				p.column += charsToTab
				p.offset++
				count--
			}
		} else {
			p.partiallyConsumedTab = false
			p.offset++
			p.column++
			count--
		}
	}
}

func (p *parser) processLine(lead int) {
	p.line++
	if p.general {
	} else if len(p.stack) <= 2 {
		if p.shallowLine(lead) {
			return
		}
	} else if p.itemLine(lead) {
		return
	}
	p.offset, p.column = p.ls, 0
	p.partiallyConsumedTab = false
	// The line index gives the first non-blank byte directly. Its column is
	// its offset unless the leading blanks hold a tab, which is rare.
	p.firstNonspace, p.firstNonspaceColumn = lead, lead-p.ls
	for i := p.ls; i < lead; i++ {
		if p.src[i] == '\t' {
			p.firstNonspace = -1
			break
		}
	}

	container, allMatched, ok := p.checkOpenBlocks()
	if !ok {
		return
	}
	lastMatched := container
	container = p.openNewBlocks(container, allMatched)
	p.addTextToContainer(container, lastMatched)
}

// mayStart is maybeSpecial refined by the byte after the lead, for the fast
// paths: a fence needs a second '`' or '~', an ATX heading a second '#' or a
// blank, a bullet or thematic break a blank or a repeat, and an ordered
// list marker a digit, '.' or ')'. A line whose lead fails this cannot open a
// block, whatever else it holds.
func mayStart(src []byte, lead, le int) bool {
	// The table check inlines; the rest is out of line.
	return maybeSpecial[src[lead]] && mayStartNext(src, lead, le)
}

func mayStartNext(src []byte, lead, le int) bool {
	c := src[lead]
	if lead+1 >= le {
		return true
	}
	next := src[lead+1]
	switch c {
	case '`', '~':
		return next == c
	case '#', '-', '*', '_', '+':
		return next == c || next == ' ' || next == '\t'
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return isDigit(next) || next == '.' || next == ')'
	}
	return true
}

// shallowLine handles the commonest lines of a document with no container
// open: a paragraph's continuation, a line of fenced code or raw HTML, a
// blank line, and a line of text or HTML that starts a paragraph or an HTML
// block. It does exactly what
// checkOpenBlocks, openNewBlocks and addTextToContainer would, and reports
// false, having done nothing, for any other line.
func (p *parser) shallowLine(lead int) bool {
	src, ls, le := p.src, p.ls, p.le
	// Leading blanks are only measured in bytes here, which is their width
	// unless they hold a tab. Only the first four can matter.
	for i := ls; i < lead && i < ls+4; i++ {
		if src[i] == '\t' {
			return false
		}
	}
	doc := &p.stack[0]
	if len(p.stack) == 1 {
		switch {
		case lead >= le:
			if doc.lastChild >= 0 {
				p.blocks[doc.lastChild].flags |= fLastLineBlank
			}
			p.blocks[0].flags |= fLastLineBlank
		case lead-ls < 4 && !mayStart(src, lead, le):
			p.blocks[0].flags &^= fLastLineBlank
			p.addChild(0, kParagraph)
			p.addSpan(lead, le)
		case lead-ls < 4 && src[lead] == '<':
			p.blocks[0].flags &^= fLastLineBlank
			t := htmlBlockStart(src[lead:le], true)
			if t == 0 {
				p.addChild(0, kParagraph)
				p.addSpan(lead, le)
				break
			}
			p.addChild(0, kHTMLBlock)
			p.stack[1].htmlType = int8(t)
			p.addSpan(ls, le)
			if t <= 5 && htmlBlockEnds(t, src[lead:le]) {
				p.finalize()
			}
		case lead-ls < 4 && src[lead] == '#':
			return p.atxHeading(0, lead)
		case lead-ls < 4 && (src[lead] == '`' || src[lead] == '~'):
			return p.openFence(0, lead, ls)
		case lead-ls < 4 && isListChar(src[lead]):
			return p.addItem(0, lead, ls)
		default:
			return false
		}
		return true
	}
	top := &p.stack[1]
	tb := &p.blocks[top.idx]
	switch {
	case lead >= le && (top.kind == kParagraph || top.kind == kHeading || top.kind == kThematicBreak ||
		top.kind == kHTMLBlock && top.htmlType >= 6):
		tb.flags |= fLastLineBlank
		p.blocks[0].flags |= fLastLineBlank
		p.finalize()
	case top.kind == kParagraph && lead < le && (lead-ls >= 4 || !mayStart(src, lead, le)):
		tb.flags &^= fLastLineBlank
		p.blocks[0].flags &^= fLastLineBlank
		p.addSpan(lead, le)
	case top.kind == kParagraph && lead < le && lead-ls < 4 && isListChar(src[lead]):
		return p.addItem(1, lead, ls)
	case top.kind == kHTMLBlock:
		// A blank line that does not end the block, or a line that may.
		tb.flags &^= fLastLineBlank
		p.blocks[0].flags &^= fLastLineBlank
		p.addSpan(ls, le)
		if t := int(top.htmlType); t <= 5 && htmlBlockEnds(t, src[min(lead, le):le]) {
			p.finalize()
		}
	case top.kind == kCodeBlock && top.fenceChar != 0:
		if lead-ls <= 3 && lead < le && src[lead] == top.fenceChar && p.scanCloseFence(lead) >= int(top.fenceLen) {
			p.finalize()
			return true
		}
		tb.flags &^= fLastLineBlank
		p.blocks[0].flags &^= fLastLineBlank
		p.addSpan(ls+min(int(top.fenceOffset), lead-ls), le)
	default:
		return false
	}
	return true
}

// atxHeading opens an ATX heading at the top level for a line whose lead,
// at most three columns in, is '#', or reports false if it is not one.
func (p *parser) atxHeading(parent, lead int) bool {
	src, le := p.src, p.le
	level := 0
	for lead+level < le && src[lead+level] == '#' {
		level++
	}
	if level > 6 || lead+level < le && !isSpaceOrTab(src[lead+level]) {
		return false
	}
	p.clearLastLineBlank(parent)
	b := &p.blocks[p.stack[p.addChild(parent, kHeading)].idx]
	b.extra = uint16(level)
	s := skipSpaces(src[:le], lead+level)
	e := atxEnd(src, s, le)
	b.a = uint32(len(p.spans))
	if e > s {
		p.spans = append(p.spans, span{start: uint32(s), end: uint32(e)})
	}
	b.b = uint32(len(p.spans))
	return true
}

// openFence opens a fenced code block at the top level for a line whose
// lead, at most three columns in, is '`' or '~', or reports false if the
// line does not open one.
func (p *parser) openFence(parent, lead, off int) bool {
	src, le := p.src, p.le
	c := src[lead]
	n := 0
	for lead+n < le && src[lead+n] == c {
		n++
	}
	if n < 3 || c == '`' && bytes.IndexByte(src[lead+n:le], '`') >= 0 {
		return false
	}
	p.clearLastLineBlank(parent)
	ob := &p.stack[p.addChild(parent, kCodeBlock)]
	ob.fenceChar, ob.fenceLen, ob.fenceOffset = c, int32(n), int32(lead-off)
	b := &p.blocks[ob.idx]
	b.flags |= fFenced
	s, e := skipSpaces(src[:le], lead+n), le
	for e > s && isSpaceOrTab(src[e-1]) {
		e--
	}
	b.link = uint32(len(p.infos))
	p.infos = append(p.infos, p.unescapedAt(src[s:e], uint32(s)))
	return true
}

// itemBlank is itemLine for a blank line, when every item matches it: it
// closes a paragraph at the top of the stack, or adds a blank line to
// fenced code, or marks the innermost item as ending in a blank line.
func (p *parser) itemBlank(lead int) bool {
	src, ls := p.src, p.ls
	for i := ls; i < lead; i++ {
		if src[i] == '\t' {
			return false
		}
	}
	off := ls
	n := len(p.stack) - 1
	for i := 1; i <= n; i++ {
		b := &p.stack[i]
		switch b.kind {
		case kList:
		case kItem:
			if b.lastChild < 0 {
				// The blank line ends the empty item: the general path
				// closes it.
				return false
			}
			if need := int(b.markerOffset + b.padding); lead-off >= need {
				off += need
			} else {
				off = lead
			}
		case kParagraph, kCodeBlock:
			if i != n || p.stack[n-1].kind != kItem {
				return false
			}
		default:
			return false
		}
	}
	top := &p.stack[n]
	switch {
	case top.kind == kParagraph:
		it := &p.stack[n-1]
		p.blocks[it.lastChild].flags |= fLastLineBlank
		p.blocks[it.idx].flags |= fLastLineBlank
		p.clearLastLineBlank(n - 2)
		p.finalize()
	case top.kind == kCodeBlock && top.fenceChar != 0:
		p.clearLastLineBlank(n)
		p.addSpan(off+min(int(top.fenceOffset), lead-off), p.le)
	case top.kind == kItem:
		if top.lastChild >= 0 {
			p.blocks[top.lastChild].flags |= fLastLineBlank
		}
		setFlag(&p.blocks[top.idx].flags, fLastLineBlank, !(top.lastChild < 0 && top.startLine == p.line))
		p.clearLastLineBlank(n - 1)
	default:
		return false
	}
	return true
}

// itemLine is shallowLine for a non-blank line under list items. In
// "1. one\n   two\n2. three", the line "   two" reaches the item's content
// column, so continues its paragraph, and "2. three" does not, so is the
// next item of the list (addItem). Once every item matches, the line may
// also continue fenced code, or start a paragraph in the innermost item.
// Anything else, including a lazy continuation line, is left to the general
// path.
func (p *parser) itemLine(lead int) bool {
	src, ls, le := p.src, p.ls, p.le
	if lead >= le {
		return p.itemBlank(lead)
	}
	for i := ls; i < lead; i++ {
		if src[i] == '\t' {
			return false
		}
	}
	off := ls
	n := len(p.stack) - 1
	for i := 1; i <= n; i++ {
		b := &p.stack[i]
		switch b.kind {
		case kList:
		case kItem:
			need := int(b.markerOffset + b.padding)
			if lead-off < need {
				// The item does not continue. Unless the line is lazy, it
				// may be the next item of the list.
				return lead-off < 4 && p.addItem(i-1, lead, off)
			}
			off += need
		default:
			if i != n {
				return false
			}
		}
	}
	top := &p.stack[n]
	switch {
	case top.kind == kParagraph && (lead-off >= 4 || !mayStart(src, lead, le)):
	case top.kind == kCodeBlock && top.fenceChar != 0:
		if lead-off <= 3 && src[lead] == top.fenceChar && p.scanCloseFence(lead) >= int(top.fenceLen) {
			p.finalize()
			return true
		}
		lead = off + min(int(top.fenceOffset), lead-off)
	case top.kind == kItem && lead-off < 4:
		switch c := src[lead]; {
		case !mayStart(src, lead, le):
			p.clearLastLineBlank(n)
			p.addChild(n, kParagraph)
			p.addSpan(lead, le)
			return true
		case c == '#':
			return p.atxHeading(n, lead)
		case c == '`' || c == '~':
			return p.openFence(n, lead, off)
		case isListChar(c):
			return p.addItem(n, lead, off)
		}
		return false
	default:
		return false
	}
	p.clearLastLineBlank(n)
	p.addSpan(lead, le)
	return true
}

func isListChar(c byte) bool { return c == '-' || c == '*' || c == '+' || isDigit(c) }

// listMarker parses the list marker at pos in its simplest form: a bullet,
// or up to nine digits and a delimiter, then one to four spaces and more
// text on the line. It returns the marker's length, the spaces after it,
// the start number and the bullet or delimiter, or n == 0 for anything
// else, which the general path handles.
func listMarker(src []byte, pos, le int) (n, sp, start int, bullet byte) {
	switch c := src[pos]; {
	case c == '-' || c == '*' || c == '+':
		n, bullet = 1, c
	case isDigit(c):
		i := pos
		for i < le && isDigit(src[i]) && i-pos < 9 {
			start = start*10 + int(src[i]-'0')
			i++
		}
		if i >= le || src[i] != '.' && src[i] != ')' {
			return 0, 0, 0, 0
		}
		n, bullet = i+1-pos, src[i]
	default:
		return 0, 0, 0, 0
	}
	for pos+n+sp < le && src[pos+n+sp] == ' ' {
		sp++
	}
	if sp < 1 || sp > 4 || pos+n+sp >= le || src[pos+n+sp] == '\t' {
		return 0, 0, 0, 0
	}
	return n, sp, start, bullet
}

// addItem opens a list item, its text a paragraph, for the marker at lead.
// ci is the stack index of the last block the line matched, and off where
// that block's content starts. The item continues the list at ci, starts a
// list in the document or item at ci, or interrupts the paragraph at ci.
// Unless the marker is in listMarker's simple form and its text starts no
// block of its own, addItem does nothing and reports false.
func (p *parser) addItem(ci, lead, off int) bool {
	src, le := p.src, p.le
	n, sp, start, bullet := listMarker(src, lead, le)
	if n == 0 || mayStart(src, lead+n+sp, le) {
		return false
	}
	ordered := bullet == '.' || bullet == ')'
	switch c := &p.stack[ci]; c.kind {
	case kList:
		if c.bullet != bullet {
			return false
		}
	case kParagraph:
		if ci != len(p.stack)-1 || ordered && start != 1 {
			return false
		}
		p.finalize()
		ci--
	case kDocument, kItem:
	default:
		return false
	}
	if p.stack[ci].kind != kList {
		ci = p.addChild(ci, kList)
		p.stack[ci].bullet = bullet
		lb := &p.blocks[p.stack[ci].idx]
		lb.a = uint32(start)
		if ordered {
			lb.flags |= fOrdered
		}
	}
	it := p.addChild(ci, kItem)
	b := &p.stack[it]
	b.bullet, b.markerOffset, b.padding = bullet, int32(lead-off), int32(n+sp)
	p.clearLastLineBlank(it)
	p.addChild(it, kParagraph)
	p.addSpan(lead+n+sp, le)
	return true
}

// clearLastLineBlank clears fLastLineBlank on the open blocks up to stack
// index n, as a non-blank line does.
func (p *parser) clearLastLineBlank(n int) {
	for i := n; i >= 0; i-- {
		p.blocks[p.stack[i].idx].flags &^= fLastLineBlank
	}
}

// addSpan appends src[s:e] to the open leaf at the top of the stack.
func (p *parser) addSpan(s, e int) {
	b := &p.blocks[p.stack[len(p.stack)-1].idx]
	if b.b == 0 {
		b.a = uint32(len(p.spans))
	}
	p.spans = append(p.spans, span{start: uint32(s), end: uint32(e)})
	b.b = uint32(len(p.spans))
}

// checkOpenBlocks matches the line against the continuation markers of the
// open blocks. It returns the stack index of the last matched block, and
// false if the line was consumed (by a closing code fence).
func (p *parser) checkOpenBlocks() (int, bool, bool) {
	c := 0
	for c+1 < len(p.stack) {
		b := &p.stack[c+1]
		p.findNextNonspace()
		switch b.kind {
		case kBlockQuote:
			if p.indent() > 3 || p.peek(p.firstNonspace) != '>' {
				return c, false, true
			}
			p.advanceOffset(p.indent()+1, true)
			if isSpaceOrTab(p.peek(p.offset)) {
				p.advanceOffset(1, true)
			}
		case kItem:
			// A list item can begin with at most one blank line (§5.2),
			// however far it is indented. cmark matches an indented one,
			// commonmark.js does not.
			if p.blank() && b.lastChild < 0 {
				return c, false, true
			} else if p.indent() >= int(b.markerOffset+b.padding) {
				p.advanceOffset(int(b.markerOffset+b.padding), true)
			} else if p.blank() {
				p.advanceOffset(p.firstNonspace-p.offset, false)
			} else {
				return c, false, true
			}
		case kCodeBlock:
			if b.fenceChar == 0 {
				if p.indent() >= 4 {
					p.advanceOffset(4, true)
				} else if p.blank() {
					p.advanceOffset(p.firstNonspace-p.offset, false)
				} else {
					return c, false, true
				}
			} else {
				if p.indent() <= 3 && p.peek(p.firstNonspace) == b.fenceChar {
					if n := p.scanCloseFence(p.firstNonspace); n >= int(b.fenceLen) {
						p.finalize()
						return 0, false, false
					}
				}
				for i := b.fenceOffset; i > 0 && isSpaceOrTab(p.peek(p.offset)); i-- {
					p.advanceOffset(1, true)
				}
			}
		case kHeading, kThematicBreak:
			return c, false, true
		case kHTMLBlock:
			if b.htmlType >= 6 && p.blank() {
				return c, false, true
			}
		case kParagraph:
			if p.blank() {
				return c, false, true
			}
		}
		c++
	}
	return c, true, true
}

func (p *parser) openNewBlocks(container int, allMatched bool) int {
	top := &p.stack[len(p.stack)-1]
	maybeLazy := top.kind == kParagraph
	for {
		kind := p.stack[container].kind
		if kind == kCodeBlock || kind == kHTMLBlock {
			break
		}
		p.findNextNonspace()
		indent := p.indent()
		indented := indent >= 4
		c := p.peek(p.firstNonspace)
		if !indented && !maybeSpecial[c] {
			break
		}
		fns := p.firstNonspace

		switch {
		case !indented && c == '>':
			p.advanceOffset(fns+1-p.offset, false)
			if isSpaceOrTab(p.peek(p.offset)) {
				p.advanceOffset(1, true)
			}
			container = p.addChild(container, kBlockQuote)

		case !indented && c == '#' && p.scanATXStart(fns) > 0:
			level := 0
			for p.peek(fns+level) == '#' {
				level++
			}
			p.advanceOffset(fns+level-p.offset, false)
			container = p.addChild(container, kHeading)
			p.blocks[p.stack[container].idx].extra = uint16(level)

		case !indented && (c == '`' || c == '~') && p.scanOpenFence(fns) > 0:
			n := p.scanOpenFence(fns)
			container = p.addChild(container, kCodeBlock)
			b := &p.stack[container]
			// The fence's indentation in columns, which a partly consumed
			// tab makes more than its bytes (§4.5); cmark counts bytes.
			b.fenceChar, b.fenceLen, b.fenceOffset = c, int32(n), int32(p.indent())
			p.blocks[b.idx].flags |= fFenced
			p.advanceOffset(fns+n-p.offset, false)
			// The rest of the line is the info string.
			s, e := skipSpaces(p.src[:p.le], p.offset), p.le
			for e > s && isSpaceOrTab(p.src[e-1]) {
				e--
			}
			p.blocks[b.idx].link = uint32(len(p.infos))
			p.infos = append(p.infos, p.unescapedAt(p.src[s:e], uint32(s)))
			p.offset = p.le

		case !indented && c == '<' && p.htmlStart(fns, kind != kParagraph && !(maybeLazy && !allMatched)) > 0:
			t := p.htmlStart(fns, kind != kParagraph && !(maybeLazy && !allMatched))
			container = p.addChild(container, kHTMLBlock)
			p.stack[container].htmlType = int8(t)

		case !indented && kind == kParagraph && (c == '=' || c == '-') && p.scanSetext(fns) > 0:
			if p.resolveRefDefs(p.stack[container].idx) {
				b := &p.blocks[p.stack[container].idx]
				b.kind = kHeading
				b.flags |= fSetext
				b.extra = uint16(p.scanSetext(fns))
				p.stack[container].kind = kHeading
				p.offset = p.le
			} else if c == '-' && p.scanThematicBreak(fns) {
				container = p.addChild(container, kThematicBreak)
				p.offset = p.le
			} else {
				// All reference definitions, and '=' is text.
				goto done
			}

		case !indented && !(kind == kParagraph && !allMatched) && (c == '*' || c == '-' || c == '_') && p.scanThematicBreak(fns):
			container = p.addChild(container, kThematicBreak)
			p.offset = p.le

		case !indented:
			n, bullet, start := p.parseListMarker(fns, kind == kParagraph)
			if n == 0 {
				goto done
			}
			p.advanceOffset(fns+n-p.offset, false)
			saveTab, saveOffset, saveColumn := p.partiallyConsumedTab, p.offset, p.column
			for p.column-saveColumn <= 5 && isSpaceOrTab(p.peek(p.offset)) {
				p.advanceOffset(1, true)
			}
			i := p.column - saveColumn
			var padding int
			if i >= 5 || i < 1 || p.offset >= p.le {
				padding = n + 1
				p.offset, p.column, p.partiallyConsumedTab = saveOffset, saveColumn, saveTab
				if i > 0 {
					p.advanceOffset(1, true)
				}
			} else {
				padding = n + i
			}
			if kind != kList || p.stack[container].bullet != bullet {
				container = p.addChild(container, kList)
				p.stack[container].bullet = bullet
				lb := &p.blocks[p.stack[container].idx]
				lb.a = uint32(start)
				if bullet == '.' || bullet == ')' {
					lb.flags |= fOrdered
				}
			}
			container = p.addChild(container, kItem)
			b := &p.stack[container]
			b.bullet, b.markerOffset, b.padding = bullet, int32(indent), int32(padding)

		case indented && !maybeLazy && !p.blank():
			p.advanceOffset(4, true)
			container = p.addChild(container, kCodeBlock)

		default:
			goto done
		}

		if k := p.stack[container].kind; k == kParagraph || k == kHeading || k == kCodeBlock || k == kThematicBreak {
			break
		}
		maybeLazy = false
	}
done:
	return container
}

func (p *parser) addTextToContainer(container, lastMatched int) {
	p.findNextNonspace()
	cb := &p.stack[container]
	if p.blank() && cb.lastChild >= 0 {
		p.blocks[cb.lastChild].flags |= fLastLineBlank
	}
	// A blank line in fenced code or an HTML block (one that a blank line
	// does not end) is part of the block's content, so it separates
	// nothing (§5.3). cmark counts it for HTML blocks; commonmark.js, which
	// looks at where blocks start and end, does not.
	lastLineBlank := p.blank() && cb.kind != kBlockQuote && cb.kind != kHeading &&
		cb.kind != kThematicBreak && !(cb.kind == kCodeBlock && cb.fenceChar != 0) &&
		cb.kind != kHTMLBlock && !(cb.kind == kItem && cb.lastChild < 0 && cb.startLine == p.line)
	setFlag(&p.blocks[cb.idx].flags, fLastLineBlank, lastLineBlank)
	for i := container - 1; i >= 0; i-- {
		p.blocks[p.stack[i].idx].flags &^= fLastLineBlank
	}

	top := len(p.stack) - 1
	if top != lastMatched && container == lastMatched && !p.blank() && p.stack[top].kind == kParagraph {
		// Lazy continuation line.
		p.advanceOffset(p.firstNonspace-p.offset, false)
		p.addLine()
		return
	}
	for len(p.stack)-1 > container {
		p.finalize()
	}
	cb = &p.stack[container]
	switch cb.kind {
	case kCodeBlock:
		if cb.fenceChar == 0 || p.blocks[cb.idx].b != 0 || p.stack[container].startLine != p.line {
			p.addLine()
		}
	case kHTMLBlock:
		p.addLine()
		if t := int(cb.htmlType); t <= 5 && htmlBlockEnds(t, p.src[p.firstNonspace:p.le]) {
			p.finalize()
		}
	case kParagraph:
		if !p.blank() {
			p.advanceOffset(p.firstNonspace-p.offset, false)
			p.addLine()
		}
	case kHeading:
		if p.blocks[cb.idx].flags&fSetext == 0 {
			s := p.firstNonspace
			e := atxEnd(p.src, s, p.le)
			if s < p.offset {
				s = p.offset
			}
			b := &p.blocks[cb.idx]
			b.a = uint32(len(p.spans))
			if e > s {
				p.spans = append(p.spans, span{start: uint32(s), end: uint32(e)})
			}
			b.b = uint32(len(p.spans))
		}
	case kThematicBreak:
	default:
		if !p.blank() {
			container = p.addChild(container, kParagraph)
			p.advanceOffset(p.firstNonspace-p.offset, false)
			p.addLine()
		}
	}
}

// atxEnd returns the end of an ATX heading's content in src[s:e]: before
// trailing blanks and the optional closing sequence of '#'.
func atxEnd(src []byte, s, e int) int {
	for e > s && isSpaceOrTab(src[e-1]) {
		e--
	}
	h := e
	for h > s && src[h-1] == '#' {
		h--
	}
	if h < e && (h == s || isSpaceOrTab(src[h-1])) {
		e = h
		for e > s && isSpaceOrTab(src[e-1]) {
			e--
		}
	}
	return e
}

func setFlag(f *uint8, bit uint8, on bool) {
	if on {
		*f |= bit
	} else {
		*f &^= bit
	}
}

// addLine appends the rest of the current line to the open leaf at the top
// of the stack.
func (p *parser) addLine() {
	b := &p.blocks[p.stack[len(p.stack)-1].idx]
	var vs uint32
	if p.partiallyConsumedTab {
		p.offset++
		vs = uint32(tabStop - p.column%tabStop)
	}
	if b.b == 0 {
		b.a = uint32(len(p.spans))
	}
	p.spans = append(p.spans, span{start: uint32(p.offset), end: uint32(max(p.offset, p.le)), vspace: vs})
	b.b = uint32(len(p.spans))
}

// addChild closes open blocks down to parent, then until one can contain a
// block of the given kind, then opens it. It returns its stack index.
func (p *parser) addChild(parent int, kind blockKind) int {
	for len(p.stack)-1 > parent {
		p.finalize()
	}
	for {
		pk := p.stack[parent].kind
		if pk == kList && kind == kItem || pk != kList && kind != kItem &&
			(pk == kDocument || pk == kBlockQuote || pk == kItem) {
			break
		}
		p.finalize()
		parent--
	}
	idx := int32(len(p.blocks))
	p.blocks = append(p.blocks, block{kind: kind})
	p.stack[parent].lastChild = idx
	p.stack = append(p.stack, openBlock{idx: idx, kind: kind, lastChild: -1, startLine: p.line})
	return len(p.stack) - 1
}

// finalize closes the block at the top of the stack.
func (p *parser) finalize() {
	ob := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	b := &p.blocks[ob.idx]
	switch ob.kind {
	case kParagraph:
		if !p.resolveRefDefs(ob.idx) {
			if len(p.stack) > 0 && p.stack[len(p.stack)-1].kind == kItem {
				// The definitions are still a block of the item, which
				// a blank line may separate from another and so make
				// the list loose (§5.3). cmark drops them first;
				// commonmark.js, like the spec, does not.
				b.kind = kRefDefs
				return
			}
			// Nothing but reference definitions: drop the paragraph. It is
			// the last entry on the tape.
			p.blocks = p.blocks[:ob.idx]
			if len(p.stack) > 0 {
				parent := &p.stack[len(p.stack)-1]
				parent.lastChild = -1
				if j := ob.idx - 1; j != parent.idx {
					if p.blocks[j].kind == kClose {
						j = int32(p.blocks[j].link)
					}
					parent.lastChild = j
				}
			}
			return
		}
		p.trimLastSpan(b)
		p.leaves = append(p.leaves, uint32(ob.idx))
	case kHeading:
		p.trimLastSpan(b)
		p.leaves = append(p.leaves, uint32(ob.idx))
	case kCodeBlock:
		if ob.fenceChar == 0 {
			for b.b > b.a && p.blankSpan(p.spans[b.b-1]) {
				b.b--
			}
		}
	case kList:
		p.setTight(ob.idx)
		fallthrough
	case kDocument, kBlockQuote, kItem:
		c := uint32(len(p.blocks))
		p.blocks = append(p.blocks, block{kind: kClose, link: uint32(ob.idx)})
		p.blocks[ob.idx].link = c
	}
}

func (p *parser) blankSpan(s span) bool {
	for _, c := range p.src[s.start:s.end] {
		if !isSpaceOrTab(c) {
			return false
		}
	}
	return true
}

// trimLastSpan strips trailing blanks from a paragraph's or heading's last
// line; stage 3 sees them on inner lines, where they make hard breaks.
func (p *parser) trimLastSpan(b *block) {
	if b.b > b.a {
		s := &p.spans[b.b-1]
		for s.end > s.start && isSpaceOrTab(p.src[s.end-1]) {
			s.end--
		}
	}
}

// Tape navigation, valid once a container is closed.

func (p *parser) next(i uint32) uint32 {
	if k := p.blocks[i].kind; k <= kItem {
		return p.blocks[i].link + 1
	}
	return i + 1
}

func (p *parser) lastChild(i uint32) (uint32, bool) {
	j := p.blocks[i].link - 1
	if j == i {
		return 0, false
	}
	if p.blocks[j].kind == kClose {
		j = p.blocks[j].link
	}
	return j, true
}

func (p *parser) endsWithBlankLine(i uint32) bool {
	for {
		b := p.blocks[i]
		if b.flags&fLastLineBlank != 0 {
			return true
		}
		if b.kind != kList && b.kind != kItem {
			return false
		}
		var ok bool
		if i, ok = p.lastChild(i); !ok {
			return false
		}
	}
}

// setTight decides whether the list at tape index li is tight. Its items are
// closed, and so is the list apart from its close entry, which is next.
func (p *parser) setTight(li int32) {
	end := uint32(len(p.blocks))
	p.blocks[li].link = end // provisional, so lastChild works on the list
	tight := true
outer:
	for item := uint32(li) + 1; item < end; {
		nextItem := p.next(item)
		if p.blocks[item].flags&fLastLineBlank != 0 && nextItem < end {
			tight = false
			break
		}
		for sub := item + 1; sub < p.blocks[item].link; {
			nextSub := p.next(sub)
			if (nextItem < end || nextSub < p.blocks[item].link) && p.endsWithBlankLine(sub) {
				tight = false
				break outer
			}
			sub = nextSub
		}
		item = nextItem
	}
	if !tight {
		return
	}
	// The paragraphs of a tight list's items render without <p>. Marking
	// them here, where the list is known to be tight, means rendering needs
	// no stack of open containers to find out.
	p.blocks[li].flags |= fTight
	for item := uint32(li) + 1; item < end; item = p.next(item) {
		for sub := item + 1; sub < p.blocks[item].link; sub = p.next(sub) {
			if p.blocks[sub].kind == kParagraph {
				p.blocks[sub].flags |= fTight
			}
		}
	}
}

// Block-start scanners. Each looks at the current line from pos.

func (p *parser) scanATXStart(pos int) int {
	n := 0
	for p.peek(pos+n) == '#' {
		n++
	}
	if n < 1 || n > 6 {
		return 0
	}
	if c := p.peek(pos + n); c != ' ' && c != '\t' && c != '\n' {
		return 0
	}
	return n
}

func (p *parser) scanOpenFence(pos int) int {
	c := p.peek(pos)
	n := 0
	for p.peek(pos+n) == c {
		n++
	}
	if n < 3 {
		return 0
	}
	if c == '`' {
		for i := pos + n; i < p.le; i++ {
			if p.src[i] == '`' {
				return 0
			}
		}
	}
	return n
}

func (p *parser) scanCloseFence(pos int) int {
	c := p.peek(pos)
	n := 0
	for p.peek(pos+n) == c {
		n++
	}
	if n < 3 {
		return 0
	}
	for i := pos + n; i < p.le; i++ {
		if !isSpaceOrTab(p.src[i]) {
			return 0
		}
	}
	return n
}

func (p *parser) htmlStart(pos int, allow7 bool) int {
	return htmlBlockStart(p.src[pos:p.le], allow7)
}

func (p *parser) scanSetext(pos int) int {
	c := p.peek(pos)
	i := pos
	for p.peek(i) == c {
		i++
	}
	for isSpaceOrTab(p.peek(i)) {
		i++
	}
	if i < p.le {
		return 0
	}
	if c == '=' {
		return 1
	}
	return 2
}

func (p *parser) scanThematicBreak(pos int) bool {
	c := p.peek(pos)
	n := 0
	for i := pos; i < p.le; i++ {
		switch p.src[i] {
		case c:
			n++
		case ' ', '\t':
		default:
			return false
		}
	}
	return n >= 3
}

// parseListMarker returns the length of a list marker at pos, the bullet
// character or ordered delimiter, and the start number.
func (p *parser) parseListMarker(pos int, interruptsParagraph bool) (int, byte, int) {
	c := p.peek(pos)
	i := pos
	var bullet byte
	start := 0
	switch {
	case c == '*' || c == '-' || c == '+':
		i++
		bullet = c
	case isDigit(c):
		for isDigit(p.peek(i)) && i-pos < 9 {
			start = start*10 + int(p.peek(i)-'0')
			i++
		}
		if interruptsParagraph && start != 1 {
			return 0, 0, 0
		}
		c = p.peek(i)
		if c != '.' && c != ')' {
			return 0, 0, 0
		}
		bullet = c
		i++
	default:
		return 0, 0, 0
	}
	if c := p.peek(i); !isSpaceOrTab(c) && c != '\n' {
		return 0, 0, 0
	}
	if interruptsParagraph {
		j := i
		for isSpaceOrTab(p.peek(j)) {
			j++
		}
		if j >= p.le {
			return 0, 0, 0
		}
	}
	return i - pos, bullet, start
}

// resolveRefDefs removes the link reference definitions at the start of the
// paragraph at tape index idx and reports whether any content remains.
func (p *parser) resolveRefDefs(idx int32) bool {
	b := &p.blocks[idx]
	spans := p.spans[b.a:b.b]
	if len(spans) == 0 {
		return false
	}
	if p.src[spans[0].start] != '[' {
		return true
	}
	// Definitions end at line ends, so work on the joined text and count
	// whole lines consumed.
	t := p.scratch[:0]
	offs := p.offs[:0]
	for i, s := range spans {
		if i > 0 {
			t = append(t, '\n')
		}
		offs = append(offs, len(t))
		t = append(t, p.src[s.start:s.end]...)
	}
	p.scratch, p.offs = t, offs
	pos := 0
	for pos < len(t) && t[pos] == '[' {
		n := p.parseRefDef(t, pos)
		if n < 0 {
			break
		}
		pos = n
	}
	if pos == 0 {
		return true
	}
	consumed := len(spans)
	for i, o := range offs {
		if o >= pos {
			consumed = i
			break
		}
	}
	b.a += uint32(consumed)
	return b.a < b.b
}

// parseRefDef parses one link reference definition at t[i] and returns the
// offset just past its line, or -1.
func (p *parser) parseRefDef(t []byte, i int) int {
	end, ls, le := scanLinkLabel(t, i)
	if end < 0 || end >= len(t) || t[end] != ':' {
		return -1
	}
	p.refLabel = appendLabel(p.refLabel[:0], t[ls:le])
	if len(p.refLabel) == 0 {
		return -1
	}
	j := skipSpnl(t, end+1)
	dend, ds, de := scanLinkDest(t, j)
	if dend < 0 || dend == j && (j >= len(t) || t[j] != '<') {
		return -1
	}
	beforeTitle := dend
	j = skipSpnl(t, dend)
	lineEnd := func(k int) int {
		k = skipSpaces(t, k)
		if k == len(t) {
			return k
		}
		if t[k] == '\n' {
			return k + 1
		}
		return -1
	}
	ts, te, next := 0, 0, -1
	if j > beforeTitle {
		if tend := scanLinkTitle(t, j, nil); tend > 0 {
			if next = lineEnd(tend); next >= 0 {
				ts, te = j+1, tend-1
			}
		}
	}
	if next < 0 {
		// No title, or junk after it: the definition may still end after
		// the destination.
		if next = lineEnd(beforeTitle); next < 0 {
			return -1
		}
	}
	if p.refs == nil {
		p.refs = make(map[string]refDef)
	}
	// Only a new label is made a string: the lookup does not allocate.
	if _, dup := p.refs[string(p.refLabel)]; !dup {
		p.refs[string(p.refLabel)] = refDef{dest: p.unescapeRange(t[ds:de]), title: p.unescapeRange(t[ts:te])}
	}
	return next
}
