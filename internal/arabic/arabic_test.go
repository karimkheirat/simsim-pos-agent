package arabic

import (
	"testing"

	"golang.org/x/image/font/sfnt"
)

func TestShapeJoinsAndLigates(t *testing.T) {
	// سلام: seen initial, lam-alef final ligature, meem isolated.
	got := string(Shape("سلام"))
	want := string([]rune{0xFEB3, 0xFEFC, 0xFEE1})
	if got != want {
		t.Fatalf("Shape(سلام) = %U, want %U", []rune(got), []rune(want))
	}
}

func TestVisualKeepsNumbersLeftToRight(t *testing.T) {
	// "حليب 1 لتر" reads right to left: حليب at the right end, then 1,
	// then لتر at the left end.
	got := Visual("حليب 1 لتر")
	if got[len(got)-1] != 0xFEA3 { // ح initial, drawn last (rightmost)
		t.Fatalf("ح should sit at the right end, got %U", got)
	}
	if string(got[len(got)-7:len(got)-4]) != " 1 " {
		t.Fatalf("the number should sit between the words, got %U", got)
	}
	if got[0] != 0xFEAE { // ر final, drawn first (leftmost)
		t.Fatalf("ر should sit at the left end, got %U", got)
	}
}

func TestFontCoversEveryForm(t *testing.T) {
	for name, data := range map[string][]byte{"regular": regularTTF, "bold": boldTTF} {
		f, err := sfnt.Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		var buf sfnt.Buffer
		check := func(r rune) {
			if r == 0 {
				return
			}
			if g, err := f.GlyphIndex(&buf, r); err != nil || g == 0 {
				t.Errorf("%s font has no glyph for %U", name, r)
			}
		}
		for _, fm := range letters {
			for _, r := range fm {
				check(r)
			}
		}
		for _, l := range lamAlef {
			check(l[0])
			check(l[1])
		}
		for r := rune(0x0660); r <= 0x0669; r++ {
			check(r)
		}
	}
}

func TestRowIsAFullWidthRaster(t *testing.T) {
	b, err := Row("حليب كانديا 1 لتر   120 DA", Style{Dots: 576, Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 8 || b[0] != 0x1D || b[1] != 0x76 || b[2] != 0x30 {
		t.Fatalf("not a GS v 0 image: % X", b[:8])
	}
	if bw := int(b[4]) | int(b[5])<<8; bw != 72 {
		t.Fatalf("width %d bytes, want 72", bw)
	}
	h := int(b[6]) | int(b[7])<<8
	if len(b) != 8+72*h {
		t.Fatalf("payload %d, want %d", len(b)-8, 72*h)
	}
}
