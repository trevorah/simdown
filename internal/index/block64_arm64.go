//go:build goexperiment.simd && arm64

package index

import (
	"math/bits"
	"simd/archsimd"
)

// Here is all the cool stuff from the README

// bitWeights gives byte i of a 16-byte lane group the weight 1<<(i%8).
var bitWeights = [16]uint8{
	0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80,
	0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80,
}

// The two nibble tables: class = loNibble[b&15] & hiNibble[b>>4].
// Bits 0-4 are ASCII punctuation, 5 space, 6 tab, 7 NUL.
var (
	loNibble = [16]uint8{0xa4, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x41, 0x03, 0x0b, 0x0b, 0x0b, 0x0b, 0x13}
	hiNibble = [16]uint8{0xc0, 0x00, 0x21, 0x02, 0x04, 0x18, 0x04, 0x08}
)

const (
	classPunct = 0x1f
	classBlank = 0x60
	classNUL   = 0x80
)

// movemask64 packs four 16-lane 0x00/0xFF vectors, already ANDed with
// bitWeights, into one bit per lane.
//
// NEON's usual movemask folds with four byte-wise ADDPs, which archsimd does
// not expose (yet).
func movemask64(v0, v1, v2, v3 archsimd.Uint8x16) uint64 {
	s := v0.ReshapeToUint16s().ConcatAddPairs(v1.ReshapeToUint16s()).
		ConcatAddPairs(v2.ReshapeToUint16s().ConcatAddPairs(v3.ReshapeToUint16s()))
	return s.Or(s.ShiftAllRight(8)).TruncToUint8().ReshapeToUint64s().GetElem(0)
}

func movemask128(a0, a1, a2, a3, b0, b1, b2, b3 archsimd.Uint8x16) (uint64, uint64) {
	sa := a0.ReshapeToUint16s().ConcatAddPairs(a1.ReshapeToUint16s()).
		ConcatAddPairs(a2.ReshapeToUint16s().ConcatAddPairs(a3.ReshapeToUint16s())).ReshapeToUint8s()
	sb := b0.ReshapeToUint16s().ConcatAddPairs(b1.ReshapeToUint16s()).
		ConcatAddPairs(b2.ReshapeToUint16s().ConcatAddPairs(b3.ReshapeToUint16s())).ReshapeToUint8s()
	r := sa.ConcatEven(sb).Or(sa.ConcatOdd(sb)).ReshapeToUint64s()
	return r.GetElem(0), r.GetElem(1)
}

func bitsOf(m archsimd.Mask8x16) archsimd.Uint8x16 { return m.ToInt8x16().ToBits() }

func classify64(in []byte) charMasks {
	c0 := archsimd.LoadUint8x16(in[0:16])
	c1 := archsimd.LoadUint8x16(in[16:32])
	c2 := archsimd.LoadUint8x16(in[32:48])
	c3 := archsimd.LoadUint8x16(in[48:64])
	w := archsimd.LoadUint8x16(bitWeights[:])

	// Classes. NEON's TBL zeroes out-of-range indexes rather than masking
	// them like vpshufb, so the low nibble is masked explicitly.
	lo := archsimd.LoadUint8x16(loNibble[:])
	hi := archsimd.LoadUint8x16(hiNibble[:])
	nib := archsimd.BroadcastUint8x16(0x0f)
	k0 := lo.LookupOrZero(c0.And(nib)).And(hi.LookupOrZero(c0.ShiftAllRight(4)))
	k1 := lo.LookupOrZero(c1.And(nib)).And(hi.LookupOrZero(c1.ShiftAllRight(4)))
	k2 := lo.LookupOrZero(c2.And(nib)).And(hi.LookupOrZero(c2.ShiftAllRight(4)))
	k3 := lo.LookupOrZero(c3.And(nib)).And(hi.LookupOrZero(c3.ShiftAllRight(4)))

	var m charMasks
	zero := archsimd.BroadcastUint8x16(0)

	// Punctuation, which also needs LF and CR, and blanks.
	lf := archsimd.BroadcastUint8x16('\n')
	cr := archsimd.BroadcastUint8x16('\r')
	pb := archsimd.BroadcastUint8x16(classPunct)
	bb := archsimd.BroadcastUint8x16(classBlank)
	lf0, lf1, lf2, lf3 := bitsOf(c0.Equal(lf)), bitsOf(c1.Equal(lf)), bitsOf(c2.Equal(lf)), bitsOf(c3.Equal(lf))
	cr0, cr1, cr2, cr3 := bitsOf(c0.Equal(cr)), bitsOf(c1.Equal(cr)), bitsOf(c2.Equal(cr)), bitsOf(c3.Equal(cr))
	m.lf, m.cr = movemask128(
		lf0.And(w), lf1.And(w), lf2.And(w), lf3.And(w),
		cr0.And(w), cr1.And(w), cr2.And(w), cr3.And(w))
	m.punctNL, m.blank = movemask128(
		bitsOf(k0.And(pb).NotEqual(zero)).Or(lf0).Or(cr0).And(w),
		bitsOf(k1.And(pb).NotEqual(zero)).Or(lf1).Or(cr1).And(w),
		bitsOf(k2.And(pb).NotEqual(zero)).Or(lf2).Or(cr2).And(w),
		bitsOf(k3.And(pb).NotEqual(zero)).Or(lf3).Or(cr3).And(w),
		bitsOf(k0.And(bb).NotEqual(zero)).And(w),
		bitsOf(k1.And(bb).NotEqual(zero)).And(w),
		bitsOf(k2.And(bb).NotEqual(zero)).And(w),
		bitsOf(k3.And(bb).NotEqual(zero)).And(w))

	// The inline specials. Most blocks of prose hold one or two, and some
	// none; testing for that first saves eight movemasks.
	bs := archsimd.BroadcastUint8x16('\\')
	bt := archsimd.BroadcastUint8x16('`')
	st := archsimd.BroadcastUint8x16('*')
	us := archsimd.BroadcastUint8x16('_')
	lb := archsimd.BroadcastUint8x16('[')
	bg := archsimd.BroadcastUint8x16('!')
	rb := archsimd.BroadcastUint8x16(']')
	lt := archsimd.BroadcastUint8x16('<')
	am := archsimd.BroadcastUint8x16('&')

	bs0, bs1, bs2, bs3 := bitsOf(c0.Equal(bs)), bitsOf(c1.Equal(bs)), bitsOf(c2.Equal(bs)), bitsOf(c3.Equal(bs))
	bt0, bt1, bt2, bt3 := bitsOf(c0.Equal(bt)), bitsOf(c1.Equal(bt)), bitsOf(c2.Equal(bt)), bitsOf(c3.Equal(bt))
	st0, st1, st2, st3 := bitsOf(c0.Equal(st)), bitsOf(c1.Equal(st)), bitsOf(c2.Equal(st)), bitsOf(c3.Equal(st))
	us0, us1, us2, us3 := bitsOf(c0.Equal(us)), bitsOf(c1.Equal(us)), bitsOf(c2.Equal(us)), bitsOf(c3.Equal(us))
	lb0, lb1, lb2, lb3 := bitsOf(c0.Equal(lb)), bitsOf(c1.Equal(lb)), bitsOf(c2.Equal(lb)), bitsOf(c3.Equal(lb))
	bg0, bg1, bg2, bg3 := bitsOf(c0.Equal(bg)), bitsOf(c1.Equal(bg)), bitsOf(c2.Equal(bg)), bitsOf(c3.Equal(bg))
	ot0 := bitsOf(c0.Equal(rb)).Or(bitsOf(c0.Equal(lt))).Or(bitsOf(c0.Equal(am)))
	ot1 := bitsOf(c1.Equal(rb)).Or(bitsOf(c1.Equal(lt))).Or(bitsOf(c1.Equal(am)))
	ot2 := bitsOf(c2.Equal(rb)).Or(bitsOf(c2.Equal(lt))).Or(bitsOf(c2.Equal(am)))
	ot3 := bitsOf(c3.Equal(rb)).Or(bitsOf(c3.Equal(lt))).Or(bitsOf(c3.Equal(am)))

	any := bs0.Or(bs1).Or(bs2).Or(bs3).
		Or(bt0).Or(bt1).Or(bt2).Or(bt3).
		Or(st0).Or(st1).Or(st2).Or(st3).
		Or(us0).Or(us1).Or(us2).Or(us3).
		Or(lb0).Or(lb1).Or(lb2).Or(lb3).
		Or(bg0).Or(bg1).Or(bg2).Or(bg3).
		Or(ot0).Or(ot1).Or(ot2).Or(ot3).ReshapeToUint64s()
	if any.GetElem(0)|any.GetElem(1) != 0 {
		m.backslash, m.backtick = movemask128(
			bs0.And(w), bs1.And(w), bs2.And(w), bs3.And(w),
			bt0.And(w), bt1.And(w), bt2.And(w), bt3.And(w))
		m.star, m.underscore = movemask128(
			st0.And(w), st1.And(w), st2.And(w), st3.And(w),
			us0.And(w), us1.And(w), us2.And(w), us3.And(w))
		m.lbracket, m.bang = movemask128(
			lb0.And(w), lb1.And(w), lb2.And(w), lb3.And(w),
			bg0.And(w), bg1.And(w), bg2.And(w), bg3.And(w))
		m.other = movemask64(ot0.And(w), ot1.And(w), ot2.And(w), ot3.And(w))
	}

	// rarely there, but the commonmark spec requires that we do something about it
	nb := archsimd.BroadcastUint8x16(classNUL)
	nulAny := k0.Or(k1).Or(k2).Or(k3).And(nb).ReshapeToUint64s()
	if nulAny.GetElem(0)|nulAny.GetElem(1) != 0 {
		m.nul = movemask64(
			bitsOf(k0.And(nb).NotEqual(zero)).And(w),
			bitsOf(k1.And(nb).NotEqual(zero)).And(w),
			bitsOf(k2.And(nb).NotEqual(zero)).And(w),
			bitsOf(k3.And(nb).NotEqual(zero)).And(w))
	}
	return m
}

// The nibble tables of the fast path, one class bit per group: 1 '`',
// 2 '*', 4 '_', 8 '[' or ']', 0x10 '<', 0x20 '&', 0x40 '!', 0x80 CR or NUL.
// Each group is a product of low and high nibbles, so the AND is exact.
var (
	fastLo = [16]uint8{0x81, 0x40, 0, 0, 0, 0, 0x20, 0, 0, 0, 0x02, 0x08, 0x10, 0x88, 0, 0x04}
	fastHi = [16]uint8{0x80, 0, 0x62, 0x10, 0, 0x0c, 0x01}
)

// fastRun runs stage 1 over the blocks from pos on, pos > 0, for as long
// as they hold no backslash, '!', CR or NUL, which is almost every block,
// and returns the offset of the first block that does, or of the tail. It
// also stops, with full set, where one of b's arrays needs to grow.
//
// Without those bytes there are no escapes, so the inline mask
// comes straight out of the classification: a run of '`', '*' or '_' starts
// where a byte differs from the one before it, which the vectors see by
// loading the block again one byte earlier, and every '[', ']', '<' and '&'
// is an entry. That is three movemasks where classify64 needs twelve. The
// loop lives whole in this file so that its vectors and constants stay in
// registers; the scalar algebra it shares with stage1.go inlines.
func fastRun(src []byte, pos int, f *fastState, b *indexBufs) (_ int, full bool) {
	term, lead, inl := b.term, b.lead, b.inl
	nt, nl, ni := b.nt, b.nl, b.ni
	prevLF, prevCR, carry, pend := f.prevLF, f.prevCR, f.leadCarry, f.pend

	lo := archsimd.LoadUint8x16(fastLo[:])
	hi := archsimd.LoadUint8x16(fastHi[:])
	nib := archsimd.BroadcastUint8x16(0x0f)
	bs := archsimd.BroadcastUint8x16('\\')
	rare := archsimd.BroadcastUint8x16(0xc0)
	w := archsimd.LoadUint8x16(bitWeights[:])
	lfv := archsimd.BroadcastUint8x16('\n')
	sp := archsimd.BroadcastUint8x16(' ')
	tab := archsimd.BroadcastUint8x16('\t')
	zero := archsimd.BroadcastUint8x16(0)
	runs := archsimd.BroadcastUint8x16(0x07)
	always := archsimd.BroadcastUint8x16(0x38)
	for ; pos+64 <= len(src); pos += 64 {
		if len(term)-nt < 64 || len(lead)-nl < 64 || len(inl)-ni < 64 {
			full = true
			break
		}
		p := src[pos-1 : pos+64]
		c0 := archsimd.LoadUint8x16(p[1:17])
		c1 := archsimd.LoadUint8x16(p[17:33])
		c2 := archsimd.LoadUint8x16(p[33:49])
		c3 := archsimd.LoadUint8x16(p[49:65])
		k0 := lo.LookupOrZero(c0.And(nib)).And(hi.LookupOrZero(c0.ShiftAllRight(4)))
		k1 := lo.LookupOrZero(c1.And(nib)).And(hi.LookupOrZero(c1.ShiftAllRight(4)))
		k2 := lo.LookupOrZero(c2.And(nib)).And(hi.LookupOrZero(c2.ShiftAllRight(4)))
		k3 := lo.LookupOrZero(c3.And(nib)).And(hi.LookupOrZero(c3.ShiftAllRight(4)))
		r := k0.Or(k1).Or(k2).Or(k3).And(rare).
			Or(bitsOf(c0.Equal(bs))).Or(bitsOf(c1.Equal(bs))).
			Or(bitsOf(c2.Equal(bs))).Or(bitsOf(c3.Equal(bs))).ReshapeToUint64s()
		if r.GetElem(0)|r.GetElem(1) != 0 {
			break
		}

		lf, blank := movemask128(
			bitsOf(c0.Equal(lfv)).And(w), bitsOf(c1.Equal(lfv)).And(w),
			bitsOf(c2.Equal(lfv)).And(w), bitsOf(c3.Equal(lfv)).And(w),
			bitsOf(c0.Equal(sp).Or(c0.Equal(tab))).And(w),
			bitsOf(c1.Equal(sp).Or(c1.Equal(tab))).And(w),
			bitsOf(c2.Equal(sp).Or(c2.Equal(tab))).And(w),
			bitsOf(c3.Equal(sp).Or(c3.Equal(tab))).And(w))

		// Greater than zero is one compare, where not equal would be two.
		p0 := archsimd.LoadUint8x16(p[0:16])
		p1 := archsimd.LoadUint8x16(p[16:32])
		p2 := archsimd.LoadUint8x16(p[32:48])
		p3 := archsimd.LoadUint8x16(p[48:64])
		spec := movemask64(
			bitsOf(k0.And(always).Or(k0.And(runs).AndNot(bitsOf(c0.Equal(p0)))).Greater(zero)).And(w),
			bitsOf(k1.And(always).Or(k1.And(runs).AndNot(bitsOf(c1.Equal(p1)))).Greater(zero)).And(w),
			bitsOf(k2.And(always).Or(k2.And(runs).AndNot(bitsOf(c2.Equal(p2)))).Greater(zero)).And(w),
			bitsOf(k3.And(always).Or(k3.And(runs).AndNot(bitsOf(c3.Equal(p3)))).Greater(zero)).And(w))

		tm, lm, c := fastLines(lf, blank, prevLF, prevCR, carry)
		prevLF, prevCR, carry = lf>>63, 0, c
		// put, with put4 inlined; put itself is too big to be.
		if b, rest := term[nt:nt+64], put4(term[nt:nt+64], uint32(pos), tm); rest != 0 {
			putRest(b, uint32(pos), rest)
		}
		if b, rest := lead[nl:nl+64], put4(lead[nl:nl+64], uint32(pos), lm); rest != 0 {
			putRest(b, uint32(pos), rest)
		}
		if b, rest := inl[ni:ni+64], put4(inl[ni:ni+64], uint32(pos-64), pend); rest != 0 {
			putRest(b, uint32(pos-64), rest)
		}
		nt += bits.OnesCount64(tm)
		nl += bits.OnesCount64(lm)
		ni += bits.OnesCount64(pend)
		pend = spec
	}
	b.nt, b.nl, b.ni = nt, nl, ni
	f.prevLF, f.prevCR, f.leadCarry, f.pend = prevLF, prevCR, carry, pend
	return pos, full
}
