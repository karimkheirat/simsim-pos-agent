package tspl

import (
	"bytes"
	"testing"
)

func TestToCP1252_AsciiPassthrough(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []byte
	}{
		{"empty", "", []byte{}},
		{"basic ascii", "Hello, World!", []byte("Hello, World!")},
		{"digits and punctuation", "256,50 DZD", []byte("256,50 DZD")},
		{"newline preserved", "a\nb", []byte{'a', 0x0A, 'b'}},
		{"tab preserved", "a\tb", []byte{'a', 0x09, 'b'}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToCP1252(tt.in)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ToCP1252(%q) = % X, want % X", tt.in, got, tt.want)
			}
		})
	}
}

func TestToCP1252_FrenchDiacritics(t *testing.T) {
	// CP1252 places French letters in the standard Latin-1 range
	// (0xC0–0xFF). These pin the most common French glyphs on labels.
	tests := []struct {
		name string
		in   string
		want []byte
	}{
		{"e acute", "é", []byte{0xE9}},
		{"e grave", "è", []byte{0xE8}},
		{"e circumflex", "ê", []byte{0xEA}},
		{"a grave", "à", []byte{0xE0}},
		{"c cedilla", "ç", []byte{0xE7}},
		{"u grave", "ù", []byte{0xF9}},
		{"o circumflex", "ô", []byte{0xF4}},
		{"i circumflex", "î", []byte{0xEE}},
		{"caps E acute", "É", []byte{0xC9}},
		{"caps C cedilla", "Ç", []byte{0xC7}},
		{"caps A grave", "À", []byte{0xC0}},
		{"oe ligature", "œ", []byte{0x9C}},
		{"OE ligature", "Œ", []byte{0x8C}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToCP1252(tt.in)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ToCP1252(%q) = % X, want % X", tt.in, got, tt.want)
			}
		})
	}
}

func TestToCP1252_Symbols(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []byte
	}{
		{"euro", "€", []byte{0x80}},
		{"degree", "°", []byte{0xB0}},
		{"french guillemets", "«»", []byte{0xAB, 0xBB}},
		{"pound", "£", []byte{0xA3}},
		{"copyright", "©", []byte{0xA9}},
		{"ellipsis", "…", []byte{0x85}},
		{"em dash", "—", []byte{0x97}},
		{"curly single quotes", "‘’", []byte{0x91, 0x92}},
		{"curly double quotes", "“”", []byte{0x93, 0x94}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToCP1252(tt.in)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ToCP1252(%q) = % X, want % X", tt.in, got, tt.want)
			}
		})
	}
}

func TestToCP1252_LabelTextRoundTrip(t *testing.T) {
	// Representative label payloads — every byte must be a CP1252-
	// representable byte (no UTF-8 multibyte sequences).
	tests := []struct {
		name string
		in   string
		want []byte
	}{
		{"price tag", "Hamoud Boualem 1L — 150 DZD",
			[]byte{'H', 'a', 'm', 'o', 'u', 'd', ' ', 'B', 'o', 'u', 'a', 'l', 'e', 'm', ' ', '1', 'L', ' ', 0x97, ' ', '1', '5', '0', ' ', 'D', 'Z', 'D'}},
		{"shelf label", "Café Bistro: 250 DZD",
			[]byte{'C', 'a', 'f', 0xE9, ' ', 'B', 'i', 's', 't', 'r', 'o', ':', ' ', '2', '5', '0', ' ', 'D', 'Z', 'D'}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToCP1252(tt.in)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ToCP1252(%q) = % X, want % X", tt.in, got, tt.want)
			}
		})
	}
}

func TestToCP1252_ArabicFallback(t *testing.T) {
	// TSPL CP1252 cannot represent Arabic glyphs; v1 falls back to '?'.
	// This pins the known-limitation behaviour documented in M13 Track
	// B's spec (RTL Arabic deferred — fallback must not panic, must be
	// silent on the wire beyond the warning log).
	tests := []struct {
		name string
		in   string
		want []byte
	}{
		{"single arabic", "ا", []byte{'?'}},
		{"arabic word: store", "متجر", []byte{'?', '?', '?', '?'}},
		{"arabic mixed with ascii", "Store متجر 1L", []byte{'S', 't', 'o', 'r', 'e', ' ', '?', '?', '?', '?', ' ', '1', 'L'}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToCP1252(tt.in)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ToCP1252(%q) = % X, want % X", tt.in, got, tt.want)
			}
		})
	}

	// Second call with the same unknown rune must also produce '?'
	// (and must not panic on the once-keyed log path).
	got2 := ToCP1252("اا")
	if !bytes.Equal(got2, []byte{'?', '?'}) {
		t.Errorf("ToCP1252(\"اا\") = % X, want '?' '?'", got2)
	}
}

func TestToCP1252_CJKFallback(t *testing.T) {
	got := ToCP1252("a你b")
	want := []byte{'a', '?', 'b'}
	if !bytes.Equal(got, want) {
		t.Errorf("ToCP1252(\"a你b\") = % X, want % X", got, want)
	}
}

func TestBuilder_TextCP1252_Roundtrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"ascii", "Hello"},
		{"french", "Café"},
		{"euro", "150€"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// TextCP1252 should embed the transcoded bytes inside the
			// TEXT quoted argument; verify each expected byte appears.
			got := New().TextCP1252(0, 0, Font2, 0, 1, 1, tt.in).Bytes()
			want := ToCP1252(tt.in)
			for _, b := range want {
				if b >= 0x80 && bytes.IndexByte(got, b) < 0 {
					// For high bytes, presence in the line is sufficient.
					t.Errorf("TextCP1252(%q) buffer missing byte 0x%02X: % X", tt.in, b, got)
				}
			}
		})
	}
}
