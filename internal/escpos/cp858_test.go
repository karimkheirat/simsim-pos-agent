package escpos

import (
	"bytes"
	"testing"
)

func TestToCP858(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []byte
	}{
		{"empty", "", []byte{}},
		{"ascii passthrough", "Hello, World!", []byte("Hello, World!")},
		{"digits and punctuation", "256,50 DZD", []byte("256,50 DZD")},
		{"newline preserved", "a\nb", []byte{'a', 0x0A, 'b'}},
		{"tab preserved", "a\tb", []byte{'a', 0x09, 'b'}},

		// Single-rune mappings — French lowercase
		{"e acute", "é", []byte{0x82}},
		{"e grave", "è", []byte{0x8A}},
		{"e circumflex", "ê", []byte{0x88}},
		{"a grave", "à", []byte{0x85}},
		{"c cedilla", "ç", []byte{0x87}},
		{"u grave", "ù", []byte{0x97}},
		{"o circumflex", "ô", []byte{0x93}},
		{"i circumflex", "î", []byte{0x8C}},

		// Single-rune mappings — French uppercase
		{"caps E acute", "É", []byte{0x90}},
		{"caps E grave", "È", []byte{0xD4}},
		{"caps C cedilla", "Ç", []byte{0x80}},
		{"caps A grave", "À", []byte{0xB7}},

		// Symbols
		{"euro", "€", []byte{0xD5}},
		{"degree", "°", []byte{0xF8}},
		{"french guillemets", "«»", []byte{0xAE, 0xAF}},
		{"pound", "£", []byte{0x9C}},

		// Mixed words from the receipt
		{"word: Especes", "Espèces", []byte{'E', 's', 'p', 0x8A, 'c', 'e', 's'}},
		{"word: Caissier", "Caissier", []byte("Caissier")},
		{"prefix: Ticket No", "Ticket N°:", []byte{'T', 'i', 'c', 'k', 'e', 't', ' ', 'N', 0xF8, ':'}},

		// Transliterations for chars not in CP858
		{"ellipsis transliterates", "…", []byte("...")},
		{"nbsp transliterates", " ", []byte(" ")},
		{"oe ligature", "œuf", []byte("oeuf")},
		{"OE ligature", "Œuvre", []byte("OEuvre")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToCP858(tt.in)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ToCP858(%q) = % X, want % X", tt.in, got, tt.want)
			}
		})
	}
}

func TestToCP858_UnknownRuneFallback(t *testing.T) {
	// 你 (U+4F60) is a Han rune not in CP858. Expect '?' fallback.
	got := ToCP858("a你b")
	want := []byte{'a', '?', 'b'}
	if !bytes.Equal(got, want) {
		t.Errorf("ToCP858(\"a你b\") = % X, want % X", got, want)
	}

	// Second call with the same unknown rune must also produce '?'
	// (and must not panic on the once-keyed log path).
	got2 := ToCP858("你你")
	want2 := []byte{'?', '?'}
	if !bytes.Equal(got2, want2) {
		t.Errorf("ToCP858(\"你你\") = % X, want % X", got2, want2)
	}
}

func TestBuilder_TextCP858(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"ascii", "Hello"},
		{"french", "Espèces"},
		{"with newline", "Caissier: Amine\n"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New().TextCP858(tt.in).Bytes()
			want := ToCP858(tt.in)
			if !bytes.Equal(got, want) {
				t.Errorf("Builder.TextCP858(%q) = % X, want % X", tt.in, got, want)
			}
		})
	}
}

func TestBuilder_TextCP858ReturnsReceiver(t *testing.T) {
	b := New()
	if b.TextCP858("x") != b {
		t.Error("TextCP858() did not return receiver")
	}
}
