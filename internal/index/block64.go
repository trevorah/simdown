//go:build !(goexperiment.simd && arm64)

package index

// The scalar version of block64_arm64.go so that we dont crash

// classify64 returns the masks of the 64 bytes starting at in[0].
func classify64(in []byte) charMasks {
	var m charMasks
	for i, c := range in[:64] {
		bit := uint64(1) << i
		switch c {
		case '\\':
			m.backslash |= bit
		case '`':
			m.backtick |= bit
		case '*':
			m.star |= bit
		case '_':
			m.underscore |= bit
		case '[':
			m.lbracket |= bit
		case '!':
			m.bang |= bit
		case ']', '<', '&':
			m.other |= bit
		case '\n':
			m.lf |= bit
		case '\r':
			m.cr |= bit
		case ' ', '\t':
			m.blank |= bit
		case 0:
			m.nul |= bit
		}
		if isASCIIPunct(c) || c == '\n' || c == '\r' {
			m.punctNL |= bit
		}
	}
	return m
}

// fastRun runs stage 1 over the blocks from pos on for as long as they
// hold no backslash, '!', CR or NUL.
func fastRun(src []byte, pos int, f *fastState, b *indexBufs) (_ int, full bool) {
	for ; pos+64 <= len(src); pos += 64 {
		lf, blank, spec, ok := classifyFast(src[pos-1 : pos+64])
		if !ok {
			break
		}
		tm, lm, c := fastLines(lf, blank, f.prevLF, f.prevCR, f.leadCarry)
		f.prevLF, f.prevCR, f.leadCarry = lf>>63, 0, c
		b.term, b.nt = extract(b.term, b.nt, uint32(pos), tm)
		b.lead, b.nl = extract(b.lead, b.nl, uint32(pos), lm)
		b.inl, b.ni = extract(b.inl, b.ni, uint32(pos-64), f.pend)
		f.pend = spec
	}
	return pos, false
}

// classifyFast returns fastRun's masks for the block p[1:65]: LF, blanks,
// and the inline mask, or ok false if the block holds a byte that needs the
// general path.
func classifyFast(p []byte) (lf, blank, spec uint64, ok bool) {
	p = p[:65]
	for i, c := range p[1:] {
		bit := uint64(1) << i
		switch c {
		case '\\', '!', '\r', 0:
			return 0, 0, 0, false
		case '\n':
			lf |= bit
		case ' ', '\t':
			blank |= bit
		case '[', ']', '<', '&':
			spec |= bit
		case '`', '*', '_':
			if p[i] != c {
				spec |= bit
			}
		}
	}
	return lf, blank, spec, true
}
