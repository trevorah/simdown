// Package simdown is a CommonMark parser built on a simdjson-style structural
// index.
package simdown

import (
	"bytes"
	"sync"

	"github.com/trevorah/simdown/internal/index"
)

// ToHTML converts CommonMark to HTML. For example,
//
//	ToHTML([]byte("# Hello\n\nSome *emphasis*.\n"))
//
// returns
//
//	<h1>Hello</h1>
//	<p>Some <em>emphasis</em>.</p>
func ToHTML(src []byte) []byte {
	p := parserPool.Get().(*parser)
	defer p.release()
	p.parse(src)
	return p.render(make([]byte, 0, p.sizeHint()))
}

// parser is everything one call of ToHTML works in, kept between calls to
// cut down on allocations
type parser struct {
	src []byte
	aux []byte
	ix  index.Index

	// The parsed document: what render reads.
	blocks []block
	spans  []span
	infos  []rng // fenced code info strings
	inl    []inline
	links  []linkInfo

	// Collected while parsing blocks, for parsing inlines once they are
	// all known: a link may use a definition that comes after it.
	refs   map[string]refDef
	leaves []uint32 // tape indexes of the paragraphs and headings, in order

	blockState
	inlineState
}

var parserPool = sync.Pool{New: func() any { return new(parser) }}

// maxPooled bounds the memory a pooled parser may keep after an unusually
// large document.
const maxPooled = 1 << 26

func (p *parser) parse(src []byte) {
	if p.ix.Build(src) {
		// The spec replaces U+0000 with U+FFFD. NUL is rare enough that
		// doing it up front, and indexing again, costs nothing in practice.
		src = bytes.ReplaceAll(src, []byte{0}, []byte("\xef\xbf\xbd"))
		p.ix.Build(src)
	}
	p.reset(src)
	p.parseBlocks()
	p.parseInlines()
}

// release returns p to parserPool, dropping its references to the
// caller's src.
func (p *parser) release() {
	p.src, p.t = nil, nil
	if cap(p.aux) > maxPooled || cap(p.inl)*16 > maxPooled || cap(p.ix.Inline)*4 > maxPooled {
		return
	}
	parserPool.Put(p)
}

// resets so that the parser can be reused by the pool
func (p *parser) reset(src []byte) {
	ix := &p.ix
	lines := len(ix.Term)
	p.src = src
	p.aux = p.aux[:0]
	p.blocks = grow(p.blocks[:0], lines/2+2)
	p.spans = grow(p.spans[:0], lines+1)
	p.infos = p.infos[:0]
	p.leaves = p.leaves[:0]
	p.inl = grow(p.inl[:0], 2*len(ix.Inline)+lines/2+1)
	p.links = p.links[:0]
	clear(p.refs)
}

// sizeHint estimates the length of the HTML. The output is about the input
// plus the tags, and the tapes give the number of tags. This is too small
// for 1.4% of the spec examples and 0.6% of the corpus documents.
// len(src)*1.25, which it replaces, was too small for 85% of the examples,
// and each of those grew the buffer at least once. Counting the bytes
// exactly would save little more.
func (p *parser) sizeHint() int {
	return len(p.src) + 6*len(p.inl) + 10*len(p.blocks)
}

func (p *parser) textAt(a, b uint32) []byte {
	if n := uint32(len(p.src)); a >= n {
		return p.aux[a-n : b-n]
	}
	return p.src[a:b]
}

func (p *parser) text(r rng) []byte { return p.textAt(r.start, r.end) }

// unescapedAt returns t, which is at address at, with backslash escapes and
// entities resolved: in place when it has neither, as most link
// destinations and titles do, and otherwise written to aux.
func (p *parser) unescapedAt(t []byte, at uint32) rng {
	if bytes.IndexByte(t, '\\') < 0 && bytes.IndexByte(t, '&') < 0 {
		return rng{at, at + uint32(len(t))}
	}
	return p.unescapeRange(t)
}

// unescapeRange writes t to aux with backslash escapes and entities resolved.
func (p *parser) unescapeRange(t []byte) rng {
	start := uint32(len(p.src) + len(p.aux))
	p.aux = unescapeInto(p.aux, t)
	return rng{start, uint32(len(p.src) + len(p.aux))}
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
