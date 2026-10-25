package main

import (
	"slices"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// normalize is a port of the spec's test/normalize.py, which its test runner
// applies to both sides before comparing. It drops differences that do not
// change the rendered document: whitespace around block tags, runs of
// whitespace outside <pre>, attribute order, self-closing slashes and the
// choice of character reference.
func normalize(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var n normalizer
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.TrimSpace(n.out.String())
		case html.TextToken:
			n.data(string(z.Raw()))
		case html.StartTagToken:
			n.startTag(z.Token())
		case html.SelfClosingTagToken:
			n.startTag(z.Token())
			n.last = "endtag"
		case html.EndTagToken:
			n.endTag(z.Token().Data)
		case html.CommentToken:
			n.out.WriteString("<!--" + string(z.Text()) + "-->")
			n.last = "comment"
		case html.DoctypeToken:
			n.out.WriteString("<!" + string(z.Text()) + ">")
			n.last = "decl"
		}
	}
}

type normalizer struct {
	out     strings.Builder
	last    string
	lastTag string
	inPre   bool
}

func (n *normalizer) data(d string) {
	afterTag := n.last == "endtag" || n.last == "starttag"
	afterBlockTag := afterTag && isBlockTag(n.lastTag)
	if afterTag && n.lastTag == "br" {
		d = strings.TrimLeft(d, "\n")
	}
	if !n.inPre {
		d = collapseSpace(d)
	}
	if afterBlockTag && !n.inPre {
		if n.last == "starttag" {
			d = strings.TrimLeftFunc(d, unicode.IsSpace)
		} else {
			d = strings.TrimSpace(d)
		}
	}
	// Character references are decoded after whitespace is collapsed, as
	// normalize.py sees them as separate tokens.
	n.out.WriteString(escape(html.UnescapeString(d)))
	n.last = "data"
}

func (n *normalizer) startTag(t html.Token) {
	if t.Data == "pre" {
		n.inPre = true
	}
	if isBlockTag(t.Data) {
		n.rstrip()
	}
	n.out.WriteString("<" + t.Data)
	slices.SortFunc(t.Attr, func(a, b html.Attribute) int {
		return strings.Compare(a.Key+"\x00"+a.Val, b.Key+"\x00"+b.Val)
	})
	for _, a := range t.Attr {
		n.out.WriteString(" " + a.Key + `="` + html.EscapeString(a.Val) + `"`)
	}
	n.out.WriteString(">")
	n.lastTag = t.Data
	n.last = "starttag"
}

func (n *normalizer) endTag(tag string) {
	if tag == "pre" {
		n.inPre = false
	} else if isBlockTag(tag) {
		n.rstrip()
	}
	n.out.WriteString("</" + tag + ">")
	n.lastTag = tag
	n.last = "endtag"
}

func (n *normalizer) rstrip() {
	s := strings.TrimRightFunc(n.out.String(), unicode.IsSpace)
	n.out.Reset()
	n.out.WriteString(s)
}

// escape writes the four characters normalize.py escapes in text; every
// other character reference becomes its character.
func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// collapseSpace replaces each run of whitespace with one space, as Python's
// \s+ does on a str.
func collapseSpace(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	if space {
		b.WriteByte(' ')
	}
	return b.String()
}

var blockTags = strings.Fields(`article header aside hgroup blockquote hr iframe
	body li map button object canvas ol caption output col p colgroup pre dd
	progress div section dl table td dt tbody embed textarea fieldset tfoot
	figcaption th figure thead footer tr form ul h1 h2 h3 h4 h5 h6 video
	script style`)

func isBlockTag(tag string) bool {
	return slices.Contains(blockTags, tag)
}
