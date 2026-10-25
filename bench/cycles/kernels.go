package main

import (
	"bytes"
	"encoding/binary"
	"math/bits"
)

// Candidate stage-1 kernels. Each produces (or, for the baselines, only
// counts) a bitmap of the bytes a CommonMark parser must look at: newlines
// plus the inline specials ` * _ [ ] ! < \ &.

var kernels = []struct {
	name string
	fn   func([]byte) int
}{
	{"bytes.Count newline (NEON asm)", func(b []byte) int { return bytes.Count(b, nl) }},
	{"per-byte loop, table lookup", scalarTable},
	{"per-byte bitmap, table lookup", scalarBitmap},
	{"SWAR bitmap (8 bytes/op)", swarBitmap},
}

var nl = []byte{'\n'}

var special = func() (t [256]bool) {
	for _, c := range "\n`*_[]!<\\&" {
		t[c] = true
	}
	return
}()

// scalarTable is the loop shape goldmark and cmark use: branch per byte.
func scalarTable(b []byte) int {
	n := 0
	for _, c := range b {
		if special[c] {
			n++
		}
	}
	return n
}

var bitmap = make([]uint64, 1<<20)

// scalarBitmap builds the same 1-bit-per-byte map simdjson's stage 1 emits,
// branch-free but one byte at a time.
func scalarBitmap(b []byte) int {
	words := bitmap[:(len(b)+63)/64]
	for w := range words {
		chunk := b[w*64 : min(w*64+64, len(b))]
		var m uint64
		for i, c := range chunk {
			var bit uint64
			if special[c] {
				bit = 1
			}
			m |= bit << i
		}
		words[w] = m
	}
	return popcount(words)
}

const lo = 0x0101010101010101
const hi = 0x8080808080808080

// eq returns 0x80 in each byte of x equal to c. Exact (no false positives
// from borrows) because the high bit of each lane is computed separately.
func eq(x uint64, c byte) uint64 {
	y := x ^ (lo * uint64(c))
	return ^(((y &^ hi) + ^uint64(hi)&^hi) | y) & hi
}

// swarBitmap classifies 8 bytes per step with SWAR equality tests and packs
// the lane high bits into the bitmap with a multiply.
func swarBitmap(b []byte) int {
	words := bitmap[:(len(b)+63)/64]
	full := len(b) / 64
	for w := 0; w < full; w++ {
		var m uint64
		for j := 0; j < 64; j += 8 {
			x := binary.LittleEndian.Uint64(b[w*64+j:])
			e := eq(x, '\n') | eq(x, '`') | eq(x, '*') | eq(x, '_') | eq(x, '[') |
				eq(x, ']') | eq(x, '!') | eq(x, '<') | eq(x, '\\') | eq(x, '&')
			// gather bit 7 of each lane into the low byte
			m |= ((e >> 7) * 0x0102040810204080) >> 56 << j
		}
		words[w] = m
	}
	if full < len(words) {
		var m uint64
		for i, c := range b[full*64:] {
			if special[c] {
				m |= 1 << i
			}
		}
		words[full] = m
	}
	return popcount(words)
}

func popcount(w []uint64) int {
	n := 0
	for _, x := range w {
		n += bits.OnesCount64(x)
	}
	return n
}
