package escpos

import (
	"fmt"
	"log/slog"
	"sync"
)

// cp858Map maps Unicode runes to their CP858 byte sequences. Most entries
// are a single byte (the codepoint in CP858); a few are short ASCII
// transliterations for characters CP858 doesn't carry (e.g. … → "...").
//
// Codepoints verified against the Microsoft CP850 / CP858 reference;
// CP858 differs from CP850 only at 0xD5 (€ instead of ı).
var cp858Map = map[rune][]byte{
	// Lowercase French accented vowels and consonants
	'à': {0x85},
	'â': {0x83},
	'ä': {0x84},
	'ç': {0x87},
	'é': {0x82},
	'è': {0x8A},
	'ê': {0x88},
	'ë': {0x89},
	'î': {0x8C},
	'ï': {0x8B},
	'ô': {0x93},
	'ö': {0x94},
	'ù': {0x97},
	'û': {0x96},
	'ü': {0x81},
	'ÿ': {0x98},

	// Uppercase French accented letters
	'À': {0xB7},
	'Â': {0xB6},
	'Ç': {0x80},
	'É': {0x90},
	'È': {0xD4},
	'Ê': {0xD2},
	'Î': {0xD7},
	'Ô': {0xE2},
	'Ù': {0xEB},
	'Û': {0xEA},

	// Symbols present in CP858
	'€': {0xD5},
	'°': {0xF8},
	'«': {0xAE},
	'»': {0xAF},
	'£': {0x9C},

	// Transliterations for characters not in CP858
	' ': []byte(" "),   // non-breaking space → regular space
	'…':      []byte("..."), // U+2026 horizontal ellipsis
	'œ':      []byte("oe"),  // U+0153 — not in CP858
	'Œ':      []byte("OE"),  // U+0152 — not in CP858
}

// unknownRuneOnce ensures slog.Warn fires at most once per unrecognized rune.
var unknownRuneOnce sync.Map // map[rune]*sync.Once

// ToCP858 converts a UTF-8 string to a CP858 byte sequence. ASCII bytes
// (0x20–0x7E plus tab, LF, CR) pass through unchanged. Mapped runes use
// their CP858 codepoint or transliteration. Unknown runes become '?' and
// are logged once each at slog.Warn.
func ToCP858(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 0x20 && r <= 0x7E:
			out = append(out, byte(r))
		case r == '\n' || r == '\r' || r == '\t':
			out = append(out, byte(r))
		default:
			if b, ok := cp858Map[r]; ok {
				out = append(out, b...)
				continue
			}
			warnUnknownRune(r)
			out = append(out, '?')
		}
	}
	return out
}

func warnUnknownRune(r rune) {
	once, _ := unknownRuneOnce.LoadOrStore(r, &sync.Once{})
	once.(*sync.Once).Do(func() {
		slog.Warn("escpos: rune not representable in CP858, replaced with '?'",
			"rune", string(r),
			"codepoint", fmt.Sprintf("U+%04X", r),
		)
	})
}
