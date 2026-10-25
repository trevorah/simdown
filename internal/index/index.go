package index

import "math/bits"

// charMasks holds the masks classify64 computes for one block. Bit i is byte i.
//
// This is the 1_1____1 stuff that shows up in the README.
type charMasks struct {
	backslash, backtick, star, underscore uint64
	lbracket, bang, other                 uint64 // other: ']', '<', '&'
	lf, cr                                uint64
	punctNL                               uint64 // ASCII punctuation, LF or CR
	blank                                 uint64 // space or tab
	nul                                   uint64
}

type Index struct {
	// Lead[i] is the offset of the first byte of a line that is not whitespace
	// Term[i] is the offset of the first byte of line i's terminator, or len(src) for a src that doesnt end in a newline
	Lead, Term []uint32
	// Inline holds the offsets where an inline construct may begin.
	Inline []uint32
}

const evenBits = 0b01010101_01010101_01010101_01010101_01010101_01010101_01010101_01010101

// scanner carries the state that crosses block boundaries e.g `\*`. Each field holds a
// single bit, already in position 0 for the next block.
type scanner struct {
	prevEscaped uint64 // the next block's first byte is escaped
	prevCR      uint64
	prevLF      uint64
	leadCarry   uint64 // a line's leading blanks run into the next block
	prevTick    uint64 // run-start detection for ` * _
	prevStar    uint64
	prevUnder   uint64

	// The inline mask is one block behind: X and G look one byte ahead, so
	// bit 63 of a block's mask needs bit 0 of the next block.
	pend     uint64
	pendBase uint32
	pendBang uint64 // an unescaped '!' at bit 63 of the pending block
	havePend bool
	lastEsc  uint64 // the previous block's last byte was escaped
}

// findEscaped returns the bytes that follow an active backslash.
func (s *scanner) findEscaped(backslash uint64) uint64 {
	if backslash == 0 {
		esc := s.prevEscaped
		s.prevEscaped = 0
		return esc
	}
	backslash &^= s.prevEscaped
	follows := backslash<<1 | s.prevEscaped
	oddStarts := backslash &^ evenBits &^ follows
	sum, carry := bits.Add64(oddStarts, backslash, 0)
	s.prevEscaped = carry
	return (evenBits ^ sum<<1) & follows
}

// block consumes the masks of the block at base. It returns the block's
// line terminators and leads, and the finished inline mask of the previous
// block (ok is false for the first block, which has no previous one).
func (s *scanner) block(m *charMasks, base uint32, first bool) (term, lead, inl uint64, inlBase uint32, ok bool) {
	esc := s.findEscaped(m.backslash)
	s.lastEsc = esc >> 63

	// Line index.
	crIn := m.cr<<1 | s.prevCR
	term = m.cr | m.lf&^crIn
	start := (m.lf<<1 | s.prevLF) | crIn&^m.lf
	if first {
		start |= 1
	}
	s.prevCR, s.prevLF = m.cr>>63, m.lf>>63
	sum, carry := bits.Add64(start&m.blank, m.blank, s.leadCarry)
	s.leadCarry = carry
	lead = (start | sum) &^ m.blank

	// Inline index.
	unesc := ^esc
	xs := esc & m.punctNL // escaped bytes whose backslash is live
	lb := m.lbracket & unesc
	bang := m.bang & unesc
	gPrev := s.pendBang & lb // "![" straddling the boundary
	inl, inlBase, ok = s.pend|(xs&1|gPrev)<<63, s.pendBase, s.havePend

	tick := m.backtick & unesc
	star := m.star & unesc
	under := m.underscore & unesc
	k := tick &^ (tick<<1 | s.prevTick)
	a := star &^ (star<<1 | s.prevStar)
	u := under &^ (under<<1 | s.prevUnder)
	s.prevTick, s.prevStar, s.prevUnder = tick>>63, star>>63, under>>63

	g := bang & (lb >> 1)
	q := (lb&^(g<<1|gPrev) | m.other) & unesc
	s.pend = xs>>1 | k | a | u | g | q
	s.pendBase = base
	s.pendBang = bang >> 63
	s.havePend = true
	return
}

// canFast reports whether the next block may take the fast path (fastRun):
// no escape, '!' or escaped byte reaches into it from the block before.
// This means that the cpu doesnt have to do too much on boring blocks of prose
func (s *scanner) canFast() bool {
	return s.prevEscaped|s.pendBang|s.lastEsc == 0
}

type fastState struct {
	prevLF, prevCR, leadCarry, pend uint64
}

func (s *scanner) toFast() fastState {
	return fastState{s.prevLF, s.prevCR, s.leadCarry, s.pend}
}

func (s *scanner) fromFast(f fastState, base uint32, last byte) {
	s.prevLF, s.prevCR, s.leadCarry, s.pend = f.prevLF, f.prevCR, f.leadCarry, f.pend
	s.pendBase, s.havePend = base-64, true
	s.prevTick, s.prevStar, s.prevUnder = b2u(last == '`'), b2u(last == '*'), b2u(last == '_')
}

// fastLines is scanner.block's line index for a block on the fast
// path, which holds no CR: the block's terminators and leads, and the carry
// out of its last line's leading blanks.
func fastLines(lf, blank, prevLF, prevCR, carry uint64) (term, lead, carryOut uint64) {
	term = lf &^ prevCR
	start := lf<<1 | prevLF | prevCR&^lf
	sum, c := bits.Add64(start&blank, blank, carry)
	return term, (start | sum) &^ blank, c
}

// indexBufs is stage 1's output while it is written: arrays at full length
// (see extract) and the count of entries in each.
type indexBufs struct {
	term, lead, inl []uint32
	nt, nl, ni      int
}

func b2u(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// turns a bunch of boolean flags for bytes into actual offsets for an index. Is ripped from simdjson's paper.
func extract(buf []uint32, n int, base uint32, m uint64) ([]uint32, int) {
	buf = room(buf, n)
	return buf, put(buf, n, base, m)
}

// room returns buf with room for a block's worth of positions after n.
//
// buf is kept at full length, rather than appended to, so that put can
// write a block's positions without a capacity check or a call. With an
// append per position instead, stage 1 slows by up to two thirds on dense
// input, and ToHTML by 4-9% on the corpus.
func room(buf []uint32, n int) []uint32 {
	if len(buf)-n < 64 {
		buf = append(buf, make([]uint32, len(buf)+64)...)
	}
	return buf
}

// put is extract for a buf that has room. It makes no call, which matters
// in a loop that holds vectors: Go keeps no register across a call, so a
// call on any path spills them on every iteration.
func put(buf []uint32, n int, base uint32, m uint64) int {
	b := buf[n : n+64]
	if rest := put4(b, base, m); rest != 0 {
		putRest(b, base, rest)
	}
	return n + bits.OnesCount64(m)
}

// put4 writes the positions of the first four bits of m, or base+64 for
// missing ones, and returns the bits it did not write.
func put4(b []uint32, base uint32, m uint64) uint64 {
	b = b[:4]
	b[0] = base + uint32(bits.TrailingZeros64(m))
	m &= m - 1
	b[1] = base + uint32(bits.TrailingZeros64(m))
	m &= m - 1
	b[2] = base + uint32(bits.TrailingZeros64(m))
	m &= m - 1
	b[3] = base + uint32(bits.TrailingZeros64(m))
	return m & (m - 1)
}

// putRest writes the positions after the fourth. It is out of line so that
// put inlines, and so that its call, and the spills around it, are on the
// rare path only.
//
//go:noinline
func putRest(b []uint32, base uint32, m uint64) {
	for i := 4; m != 0; i++ {
		b[i&63] = base + uint32(bits.TrailingZeros64(m))
		m &= m - 1
	}
}

// Build runs stage 1 over src, reusing ix's arrays. It reports whether src
// contains a NUL byte (rare).
func (ix *Index) Build(src []byte) (hasNUL bool) {
	n := len(src)
	term := grow(ix.Term[:0], n/32+64)
	lead := grow(ix.Lead[:0], n/32+64)
	inl := grow(ix.Inline[:0], n/16+64)
	b := indexBufs{term: term[:cap(term)], lead: lead[:cap(lead)], inl: inl[:cap(inl)]}
	var s scanner
	var nul uint64
	pos := 0
	fast := false // the block at pos may take the fast path
	var f fastState
	for pos+64 <= n {
		if fast {
			var full bool
			if pos, full = fastRun(src, pos, &f, &b); full {
				b.term, b.lead, b.inl = room(b.term, b.nt), room(b.lead, b.nl), room(b.inl, b.ni)
				continue
			}
			if pos+64 > n {
				break
			}
			s.fromFast(f, uint32(pos), src[pos-1])
		}
		m := classify64(src[pos : pos+64])
		nul |= m.nul
		tm, lm, im, ib, ok := s.block(&m, uint32(pos), pos == 0)
		b.term, b.nt = extract(b.term, b.nt, uint32(pos), tm)
		b.lead, b.nl = extract(b.lead, b.nl, uint32(pos), lm)
		if ok {
			b.inl, b.ni = extract(b.inl, b.ni, ib, im)
		}
		pos += 64
		if fast = s.canFast(); fast {
			f = s.toFast()
		}
	}
	if fast {
		s.fromFast(f, uint32(pos), src[pos-1])
	}
	term, lead, inl = b.term, b.lead, b.inl
	nt, nl, ni := b.nt, b.nl, b.ni
	if pos < n || n == 0 {
		var buf [64]byte
		copy(buf[:], src[pos:])
		m := classify64(buf[:])
		valid := uint64(1)<<(n-pos) - 1
		m.backslash &= valid
		m.backtick &= valid
		m.star &= valid
		m.underscore &= valid
		m.lbracket &= valid
		m.bang &= valid
		m.other &= valid
		m.lf &= valid
		m.cr &= valid
		m.punctNL &= valid
		m.blank &= valid
		m.nul &= valid
		nul |= m.nul
		tm, lm, im, ib, ok := s.block(&m, uint32(pos), pos == 0)
		// Padding is not blank, so a carry out of the last line's leading
		// blanks lands on the first padding byte; drop it and anything else
		// the padding produced.
		term, nt = extract(term, nt, uint32(pos), tm&valid)
		lead, nl = extract(lead, nl, uint32(pos), lm&valid)
		if ok {
			inl, ni = extract(inl, ni, ib, im)
		}
	}
	if s.havePend {
		inl, ni = extract(inl, ni, s.pendBase, s.pend)
	}
	for ni > 0 && inl[ni-1] >= uint32(n) {
		ni--
	}
	ix.Term, ix.Lead, ix.Inline = term[:nt], lead[:nl], inl[:ni]
	// The document is treated as if it ended with a terminator.
	if n > 0 && src[n-1] != '\n' && src[n-1] != '\r' {
		ix.Term = append(ix.Term, uint32(n))
	}
	for len(ix.Lead) < len(ix.Term) {
		ix.Lead = append(ix.Lead, uint32(n))
	}
	return nul != 0
}

// grow returns s with room for at least n elements. Callers pass s[:0],
// so a short s is replaced rather than grown with slices.Grow, which
// appends to s[:cap(s)] and so would copy contents being discarded.
func grow[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, 0, n)
	}
	return s
}

func isASCIIPunct(c byte) bool {
	return c >= '!' && c <= '/' || c >= ':' && c <= '@' || c >= '[' && c <= '`' || c >= '{' && c <= '~'
}
