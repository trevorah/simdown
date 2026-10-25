//go:build goexperiment.simd && arm64

package simdown

import (
	"encoding/binary"
	"math/bits"
	"simd/archsimd"
	"slices"
)

// escapeHTML appends s to out with &, <, > and " escaped.
func escapeHTML(out, s []byte) []byte { return escapeText(out, s, '&') }

// escapeCode is escapeHTML with line endings turned into spaces, for code
// spans.
func escapeCode(out, s []byte) []byte { return escapeText(out, s, '\n') }

// escapeText appends s to out with &, <, > and " escaped, and extra too,
// which is '\n' for code spans and otherwise '&' again.
//
// Like simdjson's string copy, it stores each 16-byte vector it loads
// straight to out and advances by the bytes that were really s's, so out
// keeps 16 bytes of room past its length, whose contents are undefined. To
// load whole vectors it also reads past len(s), up to cap(s): s is a range of
// src or aux, so those bytes exist, and the lanes past len(s) are masked off.
// Only a run within 16 bytes of the end of the backing array falls back to
// the byte loop.
//
// The whole loop lives here, not just its vector operations, so that the
// vectors stay in registers.
func escapeText(out, s []byte, extra byte) []byte {
	amp := archsimd.BroadcastUint8x16('&')
	lt := archsimd.BroadcastUint8x16('<')
	gt := archsimd.BroadcastUint8x16('>')
	qt := archsimd.BroadcastUint8x16('"')
	ex := archsimd.BroadcastUint8x16(extra)
	lane := archsimd.LoadUint8x16Array(&laneIndex)
	// Invariant: buf has room for the rest of s unescaped, plus a whole
	// vector store past it.
	if cap(out)-len(out) < len(s)+16 {
		out = slices.Grow(out, len(s)+16)
	}
	buf, n := out[:cap(out)], len(out)
	full := s[:cap(s)]
	i := 0
	for i < len(s) && i+16 <= len(full) {
		c := archsimd.LoadUint8x16(full[i : i+16])
		r := min(len(s)-i, 16)
		u := c.Equal(amp).Or(c.Equal(lt)).Or(c.Equal(gt)).Or(c.Equal(qt)).Or(c.Equal(ex)).
			And(archsimd.BroadcastUint8x16(uint8(r)).Greater(lane)).
			ToInt8x16().ToBits().ReshapeToUint64s()
		lo, hi := u.GetElem(0), u.GetElem(1)
		c.Store(buf[n : n+16])
		if lo|hi == 0 {
			n, i = n+r, i+r
			continue
		}
		k := bits.TrailingZeros64(lo) / 8
		if lo == 0 {
			k = 8 + bits.TrailingZeros64(hi)/8
		}
		e := s[i+k]
		n, i = n+k, i+k+1
		if len(buf)-n < len(s)-i+16+8 {
			buf = slices.Grow(buf[:n], len(s)-i+16+8)
			buf = buf[:cap(buf)]
		}
		binary.LittleEndian.PutUint64(buf[n:], escapeWords[e])
		n += int(escapeLens[e])
	}
	out = buf[:n]
	if i == len(s) {
		return out
	}
	// The last bytes of the document's address space, which cannot be
	// read 16 at a time.
	start := i
	for ; i < len(s); i++ {
		if c := s[i]; htmlEscapes[c] != "" || c == extra {
			out = append(out, s[start:i]...)
			out = append(out, textEscapes[c]...)
			start = i + 1
		}
	}
	return append(out, s[start:]...)
}

var laneIndex = [16]uint8{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// textEscapes is htmlEscapes plus the space a code span writes for '\n'.
// escapeWords and escapeLens hold each of them, all of which fit in eight
// bytes, as a little-endian word and its length, so that escapeText can
// write one with a single store.
var (
	textEscapes = func() [256]string {
		t := htmlEscapes
		t['\n'] = " "
		return t
	}()
	escapeWords, escapeLens = func() (w [256]uint64, l [256]uint8) {
		for c, e := range textEscapes {
			var b [8]byte
			copy(b[:], e)
			w[c], l[c] = binary.LittleEndian.Uint64(b[:]), uint8(len(e))
		}
		return
	}()
)

// skipDestPlain returns the first offset at or after j whose byte is not
// destPlain: a control character, space, 0x7f, '\\', '(' or ')'. URLs are
// long runs of plain bytes, which is handy
func skipDestPlain(t []byte, j int) int {
	sp := archsimd.BroadcastUint8x16(' ')
	del := archsimd.BroadcastUint8x16(0x7f)
	bs := archsimd.BroadcastUint8x16('\\')
	lp := archsimd.BroadcastUint8x16('(')
	rp := archsimd.BroadcastUint8x16(')')
	for ; j+16 <= len(t); j += 16 {
		c := archsimd.LoadUint8x16(t[j : j+16])
		u := c.LessEqual(sp).Or(c.Equal(del)).Or(c.Equal(bs)).Or(c.Equal(lp)).Or(c.Equal(rp)).
			ToInt8x16().ToBits().ReshapeToUint64s()
		if lo, hi := u.GetElem(0), u.GetElem(1); lo != 0 {
			return j + bits.TrailingZeros64(lo)/8
		} else if hi != 0 {
			return j + 8 + bits.TrailingZeros64(hi)/8
		}
	}
	for j < len(t) && destPlain[t[j]] {
		j++
	}
	return j
}
