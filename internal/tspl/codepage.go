package tspl

import (
	"fmt"
	"log/slog"
	"sync"
)

// cp1252Map maps Unicode runes to their Windows-1252 byte sequences.
// CP1252 is the TSPL printer default codepage (CODEPAGE 1252 in the
// command stream) and the natural fit for French diacritics on label
// templates.
//
// CP1252 differs from ISO-8859-1 only in the 0x80–0x9F range (where
// ISO has C1 control codes, CP1252 has printable glyphs like the Euro
// sign at 0x80 and curly quotes at 0x91–0x94). All entries here are
// single bytes (CP1252 is a strict 8-bit codepage).
//
// Arabic (and any rune not in CP1252) falls back to '?' per the
// per-rune-once warning policy in warnUnknownRune.
var cp1252Map = map[rune][]byte{
	// Lowercase French accented vowels and consonants
	'à': {0xE0},
	'á': {0xE1},
	'â': {0xE2},
	'ã': {0xE3},
	'ä': {0xE4},
	'å': {0xE5},
	'æ': {0xE6},
	'ç': {0xE7},
	'è': {0xE8},
	'é': {0xE9},
	'ê': {0xEA},
	'ë': {0xEB},
	'ì': {0xEC},
	'í': {0xED},
	'î': {0xEE},
	'ï': {0xEF},
	'ñ': {0xF1},
	'ò': {0xF2},
	'ó': {0xF3},
	'ô': {0xF4},
	'õ': {0xF5},
	'ö': {0xF6},
	'ø': {0xF8},
	'ù': {0xF9},
	'ú': {0xFA},
	'û': {0xFB},
	'ü': {0xFC},
	'ý': {0xFD},
	'ÿ': {0xFF},

	// Uppercase French accented letters
	'À': {0xC0},
	'Á': {0xC1},
	'Â': {0xC2},
	'Ã': {0xC3},
	'Ä': {0xC4},
	'Å': {0xC5},
	'Æ': {0xC6},
	'Ç': {0xC7},
	'È': {0xC8},
	'É': {0xC9},
	'Ê': {0xCA},
	'Ë': {0xCB},
	'Ì': {0xCC},
	'Í': {0xCD},
	'Î': {0xCE},
	'Ï': {0xCF},
	'Ñ': {0xD1},
	'Ò': {0xD2},
	'Ó': {0xD3},
	'Ô': {0xD4},
	'Õ': {0xD5},
	'Ö': {0xD6},
	'Ø': {0xD8},
	'Ù': {0xD9},
	'Ú': {0xDA},
	'Û': {0xDB},
	'Ü': {0xDC},
	'Ý': {0xDD},

	// French ligatures (CP1252 carries œ/Œ; CP858 does not)
	'œ': {0x9C},
	'Œ': {0x8C},

	// Symbols carried by CP1252 in the 0x80–0x9F windows-extension band
	'€': {0x80},
	'‚': {0x82},
	'ƒ': {0x83},
	'„': {0x84},
	'…': {0x85},
	'†': {0x86},
	'‡': {0x87},
	'ˆ': {0x88},
	'‰': {0x89},
	'Š': {0x8A},
	'‹': {0x8B},
	'Ž': {0x8E},
	'‘': {0x91},
	'’': {0x92},
	'“': {0x93},
	'”': {0x94},
	'•': {0x95},
	'–': {0x96},
	'—': {0x97},
	'˜': {0x98},
	'™': {0x99},
	'š': {0x9A},
	'›': {0x9B},
	'ž': {0x9E},
	'Ÿ': {0x9F},

	// Latin-1 punctuation and symbols (passed through above 0xA0)
	' ': {0xA0}, // non-breaking space
	'¡':      {0xA1},
	'¢':      {0xA2},
	'£':      {0xA3},
	'¤':      {0xA4},
	'¥':      {0xA5},
	'¦':      {0xA6},
	'§':      {0xA7},
	'¨':      {0xA8},
	'©':      {0xA9},
	'ª':      {0xAA},
	'«':      {0xAB},
	'¬':      {0xAC},
	'­':      {0xAD},
	'®':      {0xAE},
	'¯':      {0xAF},
	'°':      {0xB0},
	'±':      {0xB1},
	'²':      {0xB2},
	'³':      {0xB3},
	'´':      {0xB4},
	'µ':      {0xB5},
	'¶':      {0xB6},
	'·':      {0xB7},
	'¸':      {0xB8},
	'¹':      {0xB9},
	'º':      {0xBA},
	'»':      {0xBB},
	'¼':      {0xBC},
	'½':      {0xBD},
	'¾':      {0xBE},
	'¿':      {0xBF},
	'×':      {0xD7},
	'÷':      {0xF7},
	'ß':      {0xDF},
	'Þ':      {0xDE},
	'þ':      {0xFE},
	'Ð':      {0xD0},
	'ð':      {0xF0},
}

// unknownRuneOnce ensures slog.Warn fires at most once per unrecognized
// rune (matches the escpos/cp858.go pattern).
var unknownRuneOnce sync.Map // map[rune]*sync.Once

// ToCP1252 converts a UTF-8 string to a CP1252 byte sequence. ASCII
// bytes (0x20–0x7E plus tab, LF, CR) pass through unchanged. Mapped
// runes use their CP1252 codepoint. Unknown runes (Arabic, CJK, etc.)
// become '?' and are logged once each at slog.Warn.
//
// Arabic glyphs are NOT representable in CP1252 — the v1 label
// templates handle Arabic content out-of-band (RTL is a known
// limitation documented in M13 Track B's spec). This function makes
// the fallback explicit + loggable, not silent.
func ToCP1252(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 0x20 && r <= 0x7E:
			out = append(out, byte(r))
		case r == '\n' || r == '\r' || r == '\t':
			// CR/LF in TEXT payloads should be sanitized upstream
			// (see sanitizeArg in tspl.go) — preserved here so any
			// transcoded raw payload still represents them faithfully.
			out = append(out, byte(r))
		default:
			if b, ok := cp1252Map[r]; ok {
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
		slog.Warn("tspl: rune not representable in CP1252, replaced with '?'",
			"rune", string(r),
			"codepoint", fmt.Sprintf("U+%04X", r),
		)
	})
}
