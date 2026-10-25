//go:build !(goexperiment.simd && arm64)

package simdown

import (
	"encoding/binary"
	"math/bits"
)

// The scalar version of escape_arm64.go.

// escapeHTML appends s to out with &, <, > and " escaped.
func escapeHTML(out, s []byte) []byte {
	start := 0
	for i, c := range s {
		if e := htmlEscapes[c]; e != "" {
			out = append(out, s[start:i]...)
			out = append(out, e...)
			start = i + 1
		}
	}
	return append(out, s[start:]...)
}

// escapeCode is escapeHTML with line endings turned into spaces, for code
// spans.
func escapeCode(out, s []byte) []byte {
	start := 0
	for i, c := range s {
		if c == '\n' {
			out = escapeHTML(out, s[start:i])
			out = append(out, ' ')
			start = i + 1
		}
	}
	return escapeHTML(out, s[start:])
}

// skipDestPlain returns the first offset at or after j whose byte is not
// destPlain. URLs are long runs of such bytes, so it tests eight at a time,
// as one uint64: a byte below 0x21, or equal to 0x7f, '\\', '(' or ')', sets
// the high bit of its lane in one of the terms below. Borrows can only set
// lanes above a true one, so the lowest set lane is exact.
func skipDestPlain(t []byte, j int) int {
	const ones, highs = 0x0101010101010101, 0x8080808080808080
	for ; j+8 <= len(t); j += 8 {
		x := binary.LittleEndian.Uint64(t[j:])
		m := (x - 0x21*ones) & ^x
		for _, c := range [...]uint64{0x7f, '\\', '(', ')'} {
			y := x ^ c*ones
			m |= (y - ones) & ^y
		}
		if m &= highs; m != 0 {
			return j + bits.TrailingZeros64(m)/8
		}
	}
	for j < len(t) && destPlain[t[j]] {
		j++
	}
	return j
}
