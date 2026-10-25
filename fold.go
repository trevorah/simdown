package simdown

import (
	"slices"
	"unicode"
	"unicode/utf8"
)

// foldRune appends the Unicode case fold of r (spec §6.3) in a form that
// matches when full case folding does, though it need not spell it the
// same. Go's unicode package has only simple case mappings, and
// ToLower(ToUpper(r)) puts r in the right class for every rune but those in
// foldSpecial. TestFoldRuneMatchesCaseFolding checks this against
// CaseFolding.txt.
func foldRune(dst []byte, r rune) []byte {
	if i, ok := slices.BinarySearchFunc(foldSpecial, r, func(e foldEntry, r rune) int {
		return int(e.r - r)
	}); ok {
		return append(dst, foldSpecial[i].s...)
	}
	return utf8.AppendRune(dst, unicode.ToLower(unicode.ToUpper(r)))
}

type foldEntry struct {
	r rune
	s string
}

// foldSpecial holds, sorted, the full case folds of CaseFolding.txt (status
// F), which fold one rune to several, and the dotless i, which full folding
// leaves alone but ToLower(ToUpper(r)) turns into i.
var foldSpecial = []foldEntry{
	{0x00DF, "ss"},                 // latin small letter sharp s
	{0x0130, "i\u0307"},            // latin capital letter i with dot above
	{0x0131, "\u0131"},             // latin small letter dotless i
	{0x0149, "\u02bcn"},            // latin small letter n preceded by apostrophe
	{0x01F0, "j\u030c"},            // latin small letter j with caron
	{0x0390, "\u03b9\u0308\u0301"}, // greek small letter iota with dialytika and tonos
	{0x03B0, "\u03c5\u0308\u0301"}, // greek small letter upsilon with dialytika and tonos
	{0x0587, "\u0565\u0582"},       // armenian small ligature ech yiwn
	{0x1E96, "h\u0331"},            // latin small letter h with line below
	{0x1E97, "t\u0308"},            // latin small letter t with diaeresis
	{0x1E98, "w\u030a"},            // latin small letter w with ring above
	{0x1E99, "y\u030a"},            // latin small letter y with ring above
	{0x1E9A, "a\u02be"},            // latin small letter a with right half ring
	{0x1E9E, "ss"},                 // latin capital letter sharp s
	{0x1F50, "\u03c5\u0313"},       // greek small letter upsilon with psili
	{0x1F52, "\u03c5\u0313\u0300"}, // greek small letter upsilon with psili and varia
	{0x1F54, "\u03c5\u0313\u0301"}, // greek small letter upsilon with psili and oxia
	{0x1F56, "\u03c5\u0313\u0342"}, // greek small letter upsilon with psili and perispomeni
	{0x1F80, "\u1f00\u03b9"},       // greek small letter alpha with psili and ypogegrammeni
	{0x1F81, "\u1f01\u03b9"},       // greek small letter alpha with dasia and ypogegrammeni
	{0x1F82, "\u1f02\u03b9"},       // greek small letter alpha with psili and varia and ypogegrammeni
	{0x1F83, "\u1f03\u03b9"},       // greek small letter alpha with dasia and varia and ypogegrammeni
	{0x1F84, "\u1f04\u03b9"},       // greek small letter alpha with psili and oxia and ypogegrammeni
	{0x1F85, "\u1f05\u03b9"},       // greek small letter alpha with dasia and oxia and ypogegrammeni
	{0x1F86, "\u1f06\u03b9"},       // greek small letter alpha with psili and perispomeni and ypogegrammeni
	{0x1F87, "\u1f07\u03b9"},       // greek small letter alpha with dasia and perispomeni and ypogegrammeni
	{0x1F88, "\u1f00\u03b9"},       // greek capital letter alpha with psili and prosgegrammeni
	{0x1F89, "\u1f01\u03b9"},       // greek capital letter alpha with dasia and prosgegrammeni
	{0x1F8A, "\u1f02\u03b9"},       // greek capital letter alpha with psili and varia and prosgegrammeni
	{0x1F8B, "\u1f03\u03b9"},       // greek capital letter alpha with dasia and varia and prosgegrammeni
	{0x1F8C, "\u1f04\u03b9"},       // greek capital letter alpha with psili and oxia and prosgegrammeni
	{0x1F8D, "\u1f05\u03b9"},       // greek capital letter alpha with dasia and oxia and prosgegrammeni
	{0x1F8E, "\u1f06\u03b9"},       // greek capital letter alpha with psili and perispomeni and prosgegrammeni
	{0x1F8F, "\u1f07\u03b9"},       // greek capital letter alpha with dasia and perispomeni and prosgegrammeni
	{0x1F90, "\u1f20\u03b9"},       // greek small letter eta with psili and ypogegrammeni
	{0x1F91, "\u1f21\u03b9"},       // greek small letter eta with dasia and ypogegrammeni
	{0x1F92, "\u1f22\u03b9"},       // greek small letter eta with psili and varia and ypogegrammeni
	{0x1F93, "\u1f23\u03b9"},       // greek small letter eta with dasia and varia and ypogegrammeni
	{0x1F94, "\u1f24\u03b9"},       // greek small letter eta with psili and oxia and ypogegrammeni
	{0x1F95, "\u1f25\u03b9"},       // greek small letter eta with dasia and oxia and ypogegrammeni
	{0x1F96, "\u1f26\u03b9"},       // greek small letter eta with psili and perispomeni and ypogegrammeni
	{0x1F97, "\u1f27\u03b9"},       // greek small letter eta with dasia and perispomeni and ypogegrammeni
	{0x1F98, "\u1f20\u03b9"},       // greek capital letter eta with psili and prosgegrammeni
	{0x1F99, "\u1f21\u03b9"},       // greek capital letter eta with dasia and prosgegrammeni
	{0x1F9A, "\u1f22\u03b9"},       // greek capital letter eta with psili and varia and prosgegrammeni
	{0x1F9B, "\u1f23\u03b9"},       // greek capital letter eta with dasia and varia and prosgegrammeni
	{0x1F9C, "\u1f24\u03b9"},       // greek capital letter eta with psili and oxia and prosgegrammeni
	{0x1F9D, "\u1f25\u03b9"},       // greek capital letter eta with dasia and oxia and prosgegrammeni
	{0x1F9E, "\u1f26\u03b9"},       // greek capital letter eta with psili and perispomeni and prosgegrammeni
	{0x1F9F, "\u1f27\u03b9"},       // greek capital letter eta with dasia and perispomeni and prosgegrammeni
	{0x1FA0, "\u1f60\u03b9"},       // greek small letter omega with psili and ypogegrammeni
	{0x1FA1, "\u1f61\u03b9"},       // greek small letter omega with dasia and ypogegrammeni
	{0x1FA2, "\u1f62\u03b9"},       // greek small letter omega with psili and varia and ypogegrammeni
	{0x1FA3, "\u1f63\u03b9"},       // greek small letter omega with dasia and varia and ypogegrammeni
	{0x1FA4, "\u1f64\u03b9"},       // greek small letter omega with psili and oxia and ypogegrammeni
	{0x1FA5, "\u1f65\u03b9"},       // greek small letter omega with dasia and oxia and ypogegrammeni
	{0x1FA6, "\u1f66\u03b9"},       // greek small letter omega with psili and perispomeni and ypogegrammeni
	{0x1FA7, "\u1f67\u03b9"},       // greek small letter omega with dasia and perispomeni and ypogegrammeni
	{0x1FA8, "\u1f60\u03b9"},       // greek capital letter omega with psili and prosgegrammeni
	{0x1FA9, "\u1f61\u03b9"},       // greek capital letter omega with dasia and prosgegrammeni
	{0x1FAA, "\u1f62\u03b9"},       // greek capital letter omega with psili and varia and prosgegrammeni
	{0x1FAB, "\u1f63\u03b9"},       // greek capital letter omega with dasia and varia and prosgegrammeni
	{0x1FAC, "\u1f64\u03b9"},       // greek capital letter omega with psili and oxia and prosgegrammeni
	{0x1FAD, "\u1f65\u03b9"},       // greek capital letter omega with dasia and oxia and prosgegrammeni
	{0x1FAE, "\u1f66\u03b9"},       // greek capital letter omega with psili and perispomeni and prosgegrammeni
	{0x1FAF, "\u1f67\u03b9"},       // greek capital letter omega with dasia and perispomeni and prosgegrammeni
	{0x1FB2, "\u1f70\u03b9"},       // greek small letter alpha with varia and ypogegrammeni
	{0x1FB3, "\u03b1\u03b9"},       // greek small letter alpha with ypogegrammeni
	{0x1FB4, "\u03ac\u03b9"},       // greek small letter alpha with oxia and ypogegrammeni
	{0x1FB6, "\u03b1\u0342"},       // greek small letter alpha with perispomeni
	{0x1FB7, "\u03b1\u0342\u03b9"}, // greek small letter alpha with perispomeni and ypogegrammeni
	{0x1FBC, "\u03b1\u03b9"},       // greek capital letter alpha with prosgegrammeni
	{0x1FC2, "\u1f74\u03b9"},       // greek small letter eta with varia and ypogegrammeni
	{0x1FC3, "\u03b7\u03b9"},       // greek small letter eta with ypogegrammeni
	{0x1FC4, "\u03ae\u03b9"},       // greek small letter eta with oxia and ypogegrammeni
	{0x1FC6, "\u03b7\u0342"},       // greek small letter eta with perispomeni
	{0x1FC7, "\u03b7\u0342\u03b9"}, // greek small letter eta with perispomeni and ypogegrammeni
	{0x1FCC, "\u03b7\u03b9"},       // greek capital letter eta with prosgegrammeni
	{0x1FD2, "\u03b9\u0308\u0300"}, // greek small letter iota with dialytika and varia
	{0x1FD3, "\u03b9\u0308\u0301"}, // greek small letter iota with dialytika and oxia
	{0x1FD6, "\u03b9\u0342"},       // greek small letter iota with perispomeni
	{0x1FD7, "\u03b9\u0308\u0342"}, // greek small letter iota with dialytika and perispomeni
	{0x1FE2, "\u03c5\u0308\u0300"}, // greek small letter upsilon with dialytika and varia
	{0x1FE3, "\u03c5\u0308\u0301"}, // greek small letter upsilon with dialytika and oxia
	{0x1FE4, "\u03c1\u0313"},       // greek small letter rho with psili
	{0x1FE6, "\u03c5\u0342"},       // greek small letter upsilon with perispomeni
	{0x1FE7, "\u03c5\u0308\u0342"}, // greek small letter upsilon with dialytika and perispomeni
	{0x1FF2, "\u1f7c\u03b9"},       // greek small letter omega with varia and ypogegrammeni
	{0x1FF3, "\u03c9\u03b9"},       // greek small letter omega with ypogegrammeni
	{0x1FF4, "\u03ce\u03b9"},       // greek small letter omega with oxia and ypogegrammeni
	{0x1FF6, "\u03c9\u0342"},       // greek small letter omega with perispomeni
	{0x1FF7, "\u03c9\u0342\u03b9"}, // greek small letter omega with perispomeni and ypogegrammeni
	{0x1FFC, "\u03c9\u03b9"},       // greek capital letter omega with prosgegrammeni
	{0xFB00, "ff"},                 // latin small ligature ff
	{0xFB01, "fi"},                 // latin small ligature fi
	{0xFB02, "fl"},                 // latin small ligature fl
	{0xFB03, "ffi"},                // latin small ligature ffi
	{0xFB04, "ffl"},                // latin small ligature ffl
	{0xFB05, "st"},                 // latin small ligature long s t
	{0xFB06, "st"},                 // latin small ligature st
	{0xFB13, "\u0574\u0576"},       // armenian small ligature men now
	{0xFB14, "\u0574\u0565"},       // armenian small ligature men ech
	{0xFB15, "\u0574\u056b"},       // armenian small ligature men ini
	{0xFB16, "\u057e\u0576"},       // armenian small ligature vew now
	{0xFB17, "\u0574\u056d"},       // armenian small ligature men xeh
}
