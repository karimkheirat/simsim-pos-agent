// Package arabic draws a printed line that holds Arabic as a 1-bit
// picture, so thermal printers whose built-in fonts have no Arabic
// (ESC/POS on CP858, TSPL on codepage 1252) still print it readably.
//
// A line with no Arabic-script rune is never touched: callers ask Has
// first and keep sending text exactly as before. Only a line that holds
// Arabic goes through RenderLine, which shapes it (joined letter forms,
// the lam-alef ligature) with go-text/typesetting (a Go port of
// HarfBuzz), orders it right-to-left with the Unicode bidi algorithm,
// and draws it at the column positions the text renderer would have used.
package arabic

// ranges are the Unicode blocks of the Arabic script.
var ranges = [][2]rune{
	{0x0600, 0x06FF}, // Arabic
	{0x0750, 0x077F}, // Arabic Supplement
	{0x0870, 0x089F}, // Arabic Extended-B
	{0x08A0, 0x08FF}, // Arabic Extended-A
	{0xFB50, 0xFDFF}, // Arabic Presentation Forms-A
	{0xFE70, 0xFEFF}, // Arabic Presentation Forms-B (U+FEFF, the BOM, excluded below)
}

// IsArabic reports whether r belongs to an Arabic-script block.
func IsArabic(r rune) bool {
	if r == 0xFEFF { // ZERO WIDTH NO-BREAK SPACE / byte order mark
		return false
	}
	for _, rg := range ranges {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}

// Has reports whether s holds at least one Arabic-script rune.
func Has(s string) bool {
	for _, r := range s {
		if IsArabic(r) {
			return true
		}
	}
	return false
}
