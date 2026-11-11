package arabic

import (
	"bytes"
	"image"
	"testing"
)

func TestIsArabic(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
	}{
		{'a', false}, {'é', false}, {'0', false}, {' ', false}, {'€', false},
		{'א', false}, // Hebrew is right to left but not Arabic script
		{0x05FF, false}, {0x0600, true}, {'ب', true}, {'٣', true}, {0x06FF, true}, {0x0700, false},
		{0x074F, false}, {0x0750, true}, {0x077F, true}, {0x0780, false}, // Arabic Supplement
		{0x0870, true}, {0x089F, true}, // Arabic Extended-B
		{0x08A0, true}, {0x08FF, true}, {0x0900, false}, // Arabic Extended-A
		{0xFB4F, false}, {0xFB50, true}, {0xFDFF, true}, // Presentation Forms-A
		{0xFE6F, false}, {0xFE70, true}, {0xFEFC, true}, // Presentation Forms-B (lam-alef)
		{0xFEFF, false}, // byte order mark, not a letter
	}
	for _, c := range cases {
		if got := IsArabic(c.r); got != c.want {
			t.Errorf("IsArabic(%U) = %v, want %v", c.r, got, c.want)
		}
	}
	if Has("Hamoud Cola 33cl 270,00 DA") {
		t.Error("Has: a French line holds no Arabic")
	}
	if !Has("Lait حليب 1L") {
		t.Error("Has: a mixed line holds Arabic")
	}
}

func mustFaces(t *testing.T) *faceSet {
	t.Helper()
	fs, err := loadFaces()
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func gids(gs []glyph) []uint32 {
	out := make([]uint32, len(gs))
	for i, g := range gs {
		out[i] = uint32(g.gid)
	}
	return out
}

// Shaping must join the letters: a word's glyphs are not the isolated
// forms its letters take one by one.
func TestShaping_JoinedForms(t *testing.T) {
	fs := mustFaces(t)
	sc := scalesFor(fs, Layout{Width: 576, CellW: 12, CellH: 24, RowH: 32})
	word, _ := shapeField(fs, []rune("سلام"), false, sc)

	var isolated []glyph
	for _, r := range "سلام" {
		g, _ := shapeField(fs, []rune{r}, false, sc)
		isolated = append(isolated, g...)
	}
	if len(word) == 0 || len(isolated) != 4 {
		t.Fatalf("word %v, isolated %v", gids(word), gids(isolated))
	}
	// Visual order is left to right, so the word's glyphs run from the
	// last letter (م) to the first (س); compare against the reversed list.
	same := 0
	for i := range isolated {
		j := len(isolated) - 1 - i
		if j < len(word) && word[i].gid == isolated[j].gid {
			same++
		}
	}
	if same == len(isolated) && len(word) == len(isolated) {
		t.Fatalf("سلام shaped to its isolated forms %v; no joining happened", gids(word))
	}
	// The first letter, drawn rightmost, must be س's initial form, not its isolated one.
	if word[len(word)-1].gid == isolated[0].gid {
		t.Errorf("س kept its isolated form %d", isolated[0].gid)
	}
}

// lam followed by alef takes the lam-alef form. Noto Sans Arabic draws it
// as a pair of contextual glyphs (or one ligature, in other fonts): either
// way neither the lam nor the alef is the form it takes before or after
// an ordinary letter.
func TestShaping_LamAlef(t *testing.T) {
	fs := mustFaces(t)
	sc := scalesFor(fs, Layout{Width: 576, CellW: 12, CellH: 24, RowH: 32})
	la, _ := shapeField(fs, []rune("لا"), false, sc) // lam-alef
	lm, _ := shapeField(fs, []rune("لم"), false, sc) // lam initial + meem
	ba, _ := shapeField(fs, []rune("با"), false, sc) // beh + alef final
	if len(la) == 0 || len(lm) != 2 || len(ba) < 2 {
		t.Fatalf("la %v lm %v ba %v", gids(la), gids(lm), gids(ba))
	}
	if len(la) == 1 {
		return // a single ligature glyph
	}
	// Visual order: index 0 is the leftmost (the second letter).
	if la[1].gid == lm[1].gid {
		t.Errorf("lam before alef kept its ordinary initial form %d", lm[1].gid)
	}
	if la[0].gid == ba[0].gid {
		t.Errorf("alef after lam kept its ordinary final form %d", ba[0].gid)
	}
}

// A field whose first letter is Arabic reads right to left, with "1L"
// and "×2" kept whole on its left.
func TestBidi_VisualOrder(t *testing.T) {
	text := []rune("حليب 1L ×2")
	var got []string
	for _, l := range visualOrder(bidiLevels(text)) {
		got = append(got, string(text[l.start:l.end]))
	}
	want := []string{"1L ×2", "حليب "}
	if len(got) != len(want) {
		t.Fatalf("visual runs %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("visual runs %q, want %q", got, want)
		}
	}

	// A French field keeps its words in order; the Arabic word inside
	// it is the only thing turned around.
	text = []rune("Lait حليب 1L")
	got = got[:0]
	for _, l := range visualOrder(bidiLevels(text)) {
		got = append(got, string(text[l.start:l.end]))
	}
	if got[0] != "Lait " || got[len(got)-1] != "L" {
		t.Fatalf("visual runs %q", got)
	}
}

func TestSplitFields(t *testing.T) {
	got := splitFields([]rune("حليب كانديا 1L       240 DA"))
	if len(got) != 2 || got[0] != (field{0, 14}) || got[1] != (field{21, 27}) {
		t.Fatalf("fields %v", got)
	}
}

// inkColumns returns the first and last x with ink in [x0, x1).
func inkColumns(img *image.Gray, x0, x1 int) (first, last int) {
	first, last = -1, -1
	for x := x0; x < x1; x++ {
		for y := 0; y < img.Bounds().Dy(); y++ {
			if img.GrayAt(x, y).Y < 128 {
				if first < 0 {
					first = x
				}
				last = x
				break
			}
		}
	}
	return first, last
}

// The price on an Arabic line sits in the columns the text renderer gave
// it, so it lines up with the prices on the text lines around it.
func TestRenderLine_KeepsColumns(t *testing.T) {
	line := "حليب كانديا 1L                   240 DA" // the price starts at column 33
	if got := []rune(line); string(got[33:]) != "240 DA" {
		t.Fatalf("fixture: price at %q", string(got[33:]))
	}
	img, err := RenderLine(line, Layout{Width: 576, CellW: 12, CellH: 24, RowH: 32})
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 576 || img.Bounds().Dy() != 32 {
		t.Fatalf("picture %v, want 576x32", img.Bounds())
	}
	first, last := inkColumns(img, 33*12-6, 576)
	if first < 33*12-1 || first > 33*12+4 {
		t.Errorf("price ink starts at x=%d, want about %d", first, 33*12)
	}
	if last > 39*12 {
		t.Errorf("price ink ends at x=%d, past its six columns (%d)", last, 39*12)
	}
	// The name stays in its own columns, left of the price.
	nf, nl := inkColumns(img, 0, 33*12-6)
	if nf < 0 || nf > 4 || nl >= 32*12 {
		t.Errorf("name ink spans x=%d..%d, want from the left edge and before the price", nf, nl)
	}
}

func TestRenderLine_Centered(t *testing.T) {
	img, err := RenderLine("بقالة السلام", Layout{Width: 384, CellW: 12, CellH: 48, RowH: 64, Align: Center, Bold: true})
	if err != nil {
		t.Fatal(err)
	}
	first, last := inkColumns(img, 0, 384)
	if first < 0 {
		t.Fatal("no ink")
	}
	if mid := (first + last) / 2; mid < 192-8 || mid > 192+8 {
		t.Errorf("ink centred on x=%d, want about 192", mid)
	}
}

func TestPackUnpack(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 10, 2))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	img.Pix[0] = 0            // (0,0)
	img.Pix[9] = 0            // (9,0)
	img.Pix[img.Stride+3] = 0 // (3,1)
	wb, data := Pack(img, 1)
	if wb != 2 || !bytes.Equal(data, []byte{0x80, 0x40, 0x10, 0x00}) {
		t.Fatalf("Pack ink=1: wb=%d % X", wb, data)
	}
	wb, data = Pack(img, 0)
	if wb != 2 || !bytes.Equal(data, []byte{0x7F, 0xBF, 0xEF, 0xFF}) {
		t.Fatalf("Pack ink=0: wb=%d % X", wb, data)
	}
	back := Unpack(wb, 2, data, 0)
	if back.GrayAt(9, 0).Y != 0 || back.GrayAt(3, 1).Y != 0 || back.GrayAt(1, 0).Y != 255 {
		t.Fatal("Unpack does not invert Pack")
	}
	xb, wb, data := PackCropped(Rotate(img, 0), 1)
	if xb != 0 || wb != 2 || len(data) != 4 {
		t.Fatalf("PackCropped: %d %d % X", xb, wb, data)
	}
}

func TestRotate(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 3, 2))
	img.Pix[0] = 7 // (0,0)
	r := Rotate(img, 90)
	if r.Bounds().Dx() != 2 || r.Bounds().Dy() != 3 || r.GrayAt(1, 0).Y != 7 {
		t.Fatalf("90: %v", r.Bounds())
	}
	r = Rotate(img, 180)
	if r.GrayAt(2, 1).Y != 7 {
		t.Fatal("180")
	}
	r = Rotate(img, 270)
	if r.GrayAt(0, 2).Y != 7 {
		t.Fatal("270")
	}
}
