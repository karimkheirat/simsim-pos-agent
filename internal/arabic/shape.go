// Package arabic prints Arabic text on ESC/POS thermal printers.
//
// A thermal printer's built-in fonts carry Latin code pages only (the agent
// sends CP858), so an Arabic letter used to print as "?". Arabic also needs
// two things no code page gives: each letter takes one of four shapes
// depending on its neighbours, and the text runs right to left. So a row
// holding Arabic is drawn here as a picture — the letters joined, the row
// laid out right to left — and sent as an ESC/POS raster image (GS v 0),
// which every thermal printer understands whatever its code pages.
//
// Shaping uses the Unicode Arabic Presentation Forms (FE70–FEFF, FB50–FDFF):
// each letter is replaced by its isolated, final, initial or medial form, and
// lam + alef becomes its ligature. The embedded DejaVu Sans carries every one
// of those forms. Diacritics (harakat) are dropped: shop and product names
// rarely carry them and a bitmap font draws them badly at receipt size.
package arabic

import "unicode"

// forms holds a letter's presentation forms: isolated, final, initial,
// medial. A zero initial/medial means the letter only joins to the letter
// before it (alef, dal, reh, waw…).
type forms [4]rune

var letters = map[rune]forms{
	0x0621: {0xFE80, 0, 0, 0},
	0x0622: {0xFE81, 0xFE82, 0, 0},
	0x0623: {0xFE83, 0xFE84, 0, 0},
	0x0624: {0xFE85, 0xFE86, 0, 0},
	0x0625: {0xFE87, 0xFE88, 0, 0},
	0x0626: {0xFE89, 0xFE8A, 0xFE8B, 0xFE8C},
	0x0627: {0xFE8D, 0xFE8E, 0, 0},
	0x0628: {0xFE8F, 0xFE90, 0xFE91, 0xFE92},
	0x0629: {0xFE93, 0xFE94, 0, 0},
	0x062A: {0xFE95, 0xFE96, 0xFE97, 0xFE98},
	0x062B: {0xFE99, 0xFE9A, 0xFE9B, 0xFE9C},
	0x062C: {0xFE9D, 0xFE9E, 0xFE9F, 0xFEA0},
	0x062D: {0xFEA1, 0xFEA2, 0xFEA3, 0xFEA4},
	0x062E: {0xFEA5, 0xFEA6, 0xFEA7, 0xFEA8},
	0x062F: {0xFEA9, 0xFEAA, 0, 0},
	0x0630: {0xFEAB, 0xFEAC, 0, 0},
	0x0631: {0xFEAD, 0xFEAE, 0, 0},
	0x0632: {0xFEAF, 0xFEB0, 0, 0},
	0x0633: {0xFEB1, 0xFEB2, 0xFEB3, 0xFEB4},
	0x0634: {0xFEB5, 0xFEB6, 0xFEB7, 0xFEB8},
	0x0635: {0xFEB9, 0xFEBA, 0xFEBB, 0xFEBC},
	0x0636: {0xFEBD, 0xFEBE, 0xFEBF, 0xFEC0},
	0x0637: {0xFEC1, 0xFEC2, 0xFEC3, 0xFEC4},
	0x0638: {0xFEC5, 0xFEC6, 0xFEC7, 0xFEC8},
	0x0639: {0xFEC9, 0xFECA, 0xFECB, 0xFECC},
	0x063A: {0xFECD, 0xFECE, 0xFECF, 0xFED0},
	0x0640: {0x0640, 0x0640, 0x0640, 0x0640}, // tatweel
	0x0641: {0xFED1, 0xFED2, 0xFED3, 0xFED4},
	0x0642: {0xFED5, 0xFED6, 0xFED7, 0xFED8},
	0x0643: {0xFED9, 0xFEDA, 0xFEDB, 0xFEDC},
	0x0644: {0xFEDD, 0xFEDE, 0xFEDF, 0xFEE0},
	0x0645: {0xFEE1, 0xFEE2, 0xFEE3, 0xFEE4},
	0x0646: {0xFEE5, 0xFEE6, 0xFEE7, 0xFEE8},
	0x0647: {0xFEE9, 0xFEEA, 0xFEEB, 0xFEEC},
	0x0648: {0xFEED, 0xFEEE, 0, 0},
	0x0649: {0xFEEF, 0xFEF0, 0, 0},
	0x064A: {0xFEF1, 0xFEF2, 0xFEF3, 0xFEF4},
	// Letters used in Maghrebi and Persian spellings.
	0x067E: {0xFB56, 0xFB57, 0xFB58, 0xFB59}, // peh
	0x0686: {0xFB7A, 0xFB7B, 0xFB7C, 0xFB7D}, // tcheh
	0x0698: {0xFB8A, 0xFB8B, 0, 0},           // jeh
	0x06A4: {0xFB6A, 0xFB6B, 0xFB6C, 0xFB6D}, // veh (ڤ)
	0x06A9: {0xFB8E, 0xFB8F, 0xFB90, 0xFB91}, // keheh
	0x06AF: {0xFB92, 0xFB93, 0xFB94, 0xFB95}, // gaf
	0x06CC: {0xFBFC, 0xFBFD, 0xFBFE, 0xFBFF}, // farsi yeh
}

// lamAlef maps the alef that follows a lam to the ligature's isolated and
// final forms.
var lamAlef = map[rune][2]rune{
	0x0622: {0xFEF5, 0xFEF6},
	0x0623: {0xFEF7, 0xFEF8},
	0x0625: {0xFEF9, 0xFEFA},
	0x0627: {0xFEFB, 0xFEFC},
}

// IsArabic reports whether r is a letter or sign of the Arabic script.
func IsArabic(r rune) bool {
	return unicode.Is(unicode.Arabic, r) && !isArabicDigit(r)
}

// HasArabic reports whether s holds any Arabic.
func HasArabic(s string) bool {
	for _, r := range s {
		if IsArabic(r) {
			return true
		}
	}
	return false
}

func isArabicDigit(r rune) bool {
	return (r >= 0x0660 && r <= 0x0669) || (r >= 0x06F0 && r <= 0x06F9)
}

// isMark is a combining sign (harakat, shadda, sukun, superscript alef…).
func isMark(r rune) bool {
	return unicode.Is(unicode.Mn, r)
}

func dualJoining(r rune) bool {
	f, ok := letters[r]
	return ok && f[2] != 0
}

func joins(r rune) bool {
	f, ok := letters[r]
	return ok && f[1] != 0
}

// Shape replaces each Arabic letter of s, in logical order, by the form its
// neighbours call for, and fuses lam + alef. Marks are dropped. Everything
// that is not an Arabic letter passes through.
func Shape(s string) []rune {
	var in []rune
	for _, r := range s {
		if isMark(r) {
			continue
		}
		in = append(in, r)
	}
	out := make([]rune, 0, len(in))
	for i := 0; i < len(in); i++ {
		r := in[i]
		f, ok := letters[r]
		if !ok {
			out = append(out, r)
			continue
		}
		prevJoins := i > 0 && dualJoining(in[i-1]) && joins(r)
		// Lam followed by alef: one ligature, which joins only backwards.
		if r == 0x0644 && i+1 < len(in) {
			if lig, ok := lamAlef[in[i+1]]; ok {
				if prevJoins {
					out = append(out, lig[1])
				} else {
					out = append(out, lig[0])
				}
				i++
				continue
			}
		}
		nextJoins := f[2] != 0 && i+1 < len(in) && joins(in[i+1])
		switch {
		case prevJoins && nextJoins:
			out = append(out, f[3])
		case prevJoins:
			out = append(out, f[1])
		case nextJoins:
			out = append(out, f[2])
		default:
			out = append(out, f[0])
		}
	}
	return out
}

type class int

const (
	neutral class = iota
	ltr
	rtl
)

func classOf(r rune) class {
	switch {
	case IsArabic(r) || (r >= 0xFB50 && r <= 0xFEFF):
		return rtl
	case unicode.IsLetter(r) || unicode.IsDigit(r):
		return ltr
	default:
		return neutral
	}
}

var mirror = map[rune]rune{'(': ')', ')': '(', '[': ']', ']': '[', '{': '}', '}': '{', '<': '>', '>': '<', '«': '»', '»': '«'}

// Visual shapes s and returns it in the order it is drawn left to right.
//
// A simplified bidi: the row reads right to left when its first strong
// character is Arabic. Latin words and numbers stay left to right inside
// it; a space or a sign between two runs of the same direction takes that
// direction, otherwise the row's. Brackets inside right-to-left runs are
// mirrored.
func Visual(s string) []rune {
	rs := Shape(s)
	if len(rs) == 0 {
		return rs
	}
	base := ltr
	for _, r := range rs {
		if c := classOf(r); c != neutral {
			base = c
			break
		}
	}
	cls := make([]class, len(rs))
	for i, r := range rs {
		cls[i] = classOf(r)
	}
	// Resolve neutrals.
	for i := 0; i < len(rs); {
		if cls[i] != neutral {
			i++
			continue
		}
		j := i
		for j < len(rs) && cls[j] == neutral {
			j++
		}
		before, after := base, base
		if i > 0 {
			before = cls[i-1]
		}
		if j < len(rs) {
			after = cls[j]
		}
		c := base
		if before == after {
			c = before
		}
		for k := i; k < j; k++ {
			cls[k] = c
		}
		i = j
	}
	// Cut into runs.
	type run struct {
		c  class
		rs []rune
	}
	var runs []run
	for i, r := range rs {
		if len(runs) == 0 || runs[len(runs)-1].c != cls[i] {
			runs = append(runs, run{c: cls[i]})
		}
		runs[len(runs)-1].rs = append(runs[len(runs)-1].rs, r)
	}
	for _, ru := range runs {
		if ru.c == rtl {
			for a, b := 0, len(ru.rs)-1; a < b; a, b = a+1, b-1 {
				ru.rs[a], ru.rs[b] = ru.rs[b], ru.rs[a]
			}
			for k, r := range ru.rs {
				if m, ok := mirror[r]; ok {
					ru.rs[k] = m
				}
			}
		}
	}
	if base == rtl {
		for a, b := 0, len(runs)-1; a < b; a, b = a+1, b-1 {
			runs[a], runs[b] = runs[b], runs[a]
		}
	}
	out := make([]rune, 0, len(rs))
	for _, ru := range runs {
		out = append(out, ru.rs...)
	}
	return out
}
