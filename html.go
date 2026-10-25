package simdown

import "strconv"

// Rendering: a walk over the block tape that renders each
// leaf's range of the inline tape. The last part of the parser!

func (p *parser) render(out []byte) []byte {
	blocks := p.blocks
	for i := 1; i < len(blocks)-1; i++ {
		b := &blocks[i]
		switch b.kind {
		case kBlockQuote:
			out = cr(out)
			out = append(out, "<blockquote>\n"...)
		case kList:
			out = cr(out)
			if b.flags&fOrdered != 0 {
				out = append(out, "<ol"...)
				if b.a != 1 {
					out = append(out, ` start="`...)
					out = strconv.AppendUint(out, uint64(b.a), 10)
					out = append(out, '"')
				}
				out = append(out, ">\n"...)
			} else {
				out = append(out, "<ul>\n"...)
			}
		case kItem:
			out = append(out, "<li>"...)
		case kClose:
			o := &blocks[b.link]
			switch o.kind {
			case kBlockQuote:
				out = cr(out)
				out = append(out, "</blockquote>\n"...)
			case kList:
				out = cr(out)
				if o.flags&fOrdered != 0 {
					out = append(out, "</ol>\n"...)
				} else {
					out = append(out, "</ul>\n"...)
				}
			case kItem:
				out = append(out, "</li>\n"...)
			}
		case kParagraph:
			if b.flags&fTight != 0 {
				out = p.renderInlines(out, b.a, b.b)
				continue
			}
			out = cr(out)
			out = append(out, "<p>"...)
			out = p.renderInlines(out, b.a, b.b)
			out = append(out, "</p>\n"...)
		case kHeading:
			out = cr(out)
			out = append(out, "<h"...)
			out = append(out, byte('0'+b.extra), '>')
			out = p.renderInlines(out, b.a, b.b)
			out = append(out, "</h"...)
			out = append(out, byte('0'+b.extra), '>', '\n')
		case kCodeBlock:
			out = cr(out)
			out = append(out, "<pre><code"...)
			if b.flags&fFenced != 0 {
				info := p.text(p.infos[b.link])
				n := 0
				for n < len(info) && !isSpace(info[n]) {
					n++
				}
				if n > 0 {
					out = append(out, ` class="language-`...)
					out = escapeHTML(out, info[:n])
					out = append(out, '"')
				}
			}
			out = append(out, '>')
			out = p.appendLines(out, p.spans[b.a:b.b], true)
			out = append(out, "</code></pre>\n"...)
		case kHTMLBlock:
			out = cr(out)
			out = p.appendLines(out, p.spans[b.a:b.b], false)
		case kThematicBreak:
			out = cr(out)
			out = append(out, "<hr />\n"...)
		}
	}
	return out
}

func cr(out []byte) []byte {
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out
}

// appendLines writes the lines of a code block (escaped) or an HTML block
// (as they are), each followed by '\n'. Lines that follow one another in src
// with only a '\n' between them, as most do, are copied as one range, since
// that '\n' is the one the output wants. The fenced block "```\na\nb\n```"
// is written with one copy of "a\nb", but the indented block "    a\n    b"
// with two, as the second line's indentation lies between them.
func (p *parser) appendLines(out []byte, spans []span, escape bool) []byte {
	for i := 0; i < len(spans); {
		start, end := spans[i].start, spans[i].end
		// vspace is what is left of a tab, at most tabStop columns.
		out = append(out, "    "[:spans[i].vspace]...)
		for i++; i < len(spans) && spans[i].start == end+1 && spans[i].vspace == 0 && p.src[end] == '\n'; i++ {
			end = spans[i].end
		}
		if escape {
			out = escapeHTML(out, p.src[start:end])
		} else {
			out = append(out, p.src[start:end]...)
		}
		out = append(out, '\n')
	}
	return out
}

func (p *parser) renderInlines(out []byte, from, to uint32) []byte {
	inl := p.inl
	for i := from; i < to; i++ {
		e := &inl[i]
		switch e.kind {
		case iText:
			out = escapeHTML(out, p.textAt(e.a, e.b))
		case iSoftBreak:
			out = append(out, '\n')
		case iHardBreak:
			out = append(out, "<br />\n"...)
		case iCode:
			out = append(out, "<code>"...)
			out = escapeCode(out, p.textAt(e.a, e.b))
			out = append(out, "</code>"...)
		case iHTML:
			out = append(out, p.textAt(e.a, e.b)...)
		case iEmphOpen:
			out = append(out, "<em>"...)
		case iEmphClose:
			out = append(out, "</em>"...)
		case iStrongOpen:
			out = append(out, "<strong>"...)
		case iStrongClose:
			out = append(out, "</strong>"...)
		case iLinkOpen:
			li := &p.links[e.a]
			out = append(out, `<a href="`...)
			out = p.appendHref(out, li)
			out = append(out, '"')
			if li.title.end > li.title.start {
				out = append(out, ` title="`...)
				out = escapeHTML(out, p.text(li.title))
				out = append(out, '"')
			}
			out = append(out, '>')
		case iLinkClose:
			out = append(out, "</a>"...)
		case iImageOpen:
			li := &p.links[e.a]
			out = append(out, `<img src="`...)
			out = p.appendHref(out, li)
			out = append(out, `" alt="`...)
			out = p.renderPlain(out, i+1, e.link)
			out = append(out, '"')
			if li.title.end > li.title.start {
				out = append(out, ` title="`...)
				out = escapeHTML(out, p.text(li.title))
				out = append(out, '"')
			}
			out = append(out, " />"...)
			i = e.link
		}
	}
	return out
}

// renderPlain renders inlines as an image's alt text (i.e only their text).
func (p *parser) renderPlain(out []byte, from, to uint32) []byte {
	for i := from; i < to; i++ {
		e := &p.inl[i]
		switch e.kind {
		case iText, iHTML:
			out = escapeHTML(out, p.textAt(e.a, e.b))
		case iCode:
			out = escapeCode(out, p.textAt(e.a, e.b))
		case iSoftBreak, iHardBreak:
			out = append(out, '\n')
		}
	}
	return out
}

func (p *parser) appendHref(out []byte, li *linkInfo) []byte {
	if li.mailto {
		out = append(out, "mailto:"...)
	}
	return escapeHref(out, p.text(li.dest))
}

// htmlEscapes maps each byte to its escape, or "" if it needs none.
// escapeHTML and escapeCode, which apply it, are in escape_arm64.go and
// escape.go.
var htmlEscapes = [256]string{'&': "&amp;", '<': "&lt;", '>': "&gt;", '"': "&quot;"}

// hrefSafe marks the bytes a link destination keeps as they are; the rest
// are percent-encoded, apart from & and ', which are HTML-escaped. This is
// just like cmark's houdini_escape_href.
var hrefSafe = func() (t [256]bool) {
	for c := 0; c < 128; c++ {
		t[c] = isAlnum(byte(c))
	}
	for _, c := range []byte("-_.!~*();/?:@=+$,%#") {
		t[c] = true
	}
	return
}()

func escapeHref(out, s []byte) []byte {
	const hex = "0123456789ABCDEF"
	for _, c := range s {
		switch {
		case hrefSafe[c]:
			out = append(out, c)
		case c == '&':
			out = append(out, "&amp;"...)
		case c == '\'':
			out = append(out, "&#x27;"...)
		default:
			out = append(out, '%', hex[c>>4], hex[c&15])
		}
	}
	return out
}
