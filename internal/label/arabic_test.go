package label

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/karimkheirat/simsim-pos-agent/internal/arabic"
	"github.com/karimkheirat/simsim-pos-agent/internal/tspl"
)

// shelfLabelNamed is the shelf-label fixture with its product name (the
// first text element) replaced.
func shelfLabelNamed(name string) Label {
	l := shelfLabelFixture()
	for i := range l.Elements {
		if l.Elements[i].Type == ElementText {
			l.Elements[i].Value = name
			break
		}
	}
	return l
}

func TestArabic_LabelTextIsABitmap(t *testing.T) {
	opts := RenderOptions{Dialect: tspl.DialectStandard, QRSupported: true}
	latinLabel := shelfLabelNamed("Lait Candia 1L")
	latin, err := Render(latinLabel, opts)
	if err != nil {
		t.Fatal(err)
	}
	ar, err := Render(shelfLabelNamed("حليب كانديا 1L ×2   240,00 DA"), opts)
	if err != nil {
		t.Fatal(err)
	}
	var e Element
	for _, el := range latinLabel.Elements {
		if el.Type == ElementText {
			e = el
			break
		}
	}
	text := tspl.Text(e.X, e.Y, tspl.FontName(e.Font), e.Rotation, e.XScale, e.YScale, string(tspl.ToCP1252(e.Value)))
	i := bytes.Index(latin, text)
	if i < 0 {
		t.Fatalf("TEXT %q not found", text)
	}
	pre, post := latin[:i], latin[i+len(text):]
	if !bytes.HasPrefix(ar, pre) || !bytes.HasSuffix(ar, post) {
		t.Fatal("the rest of the label changed")
	}
	pic := ar[len(pre) : len(ar)-len(post)]
	var x, y, wb, h, mode int
	if _, err := fmt.Sscanf(string(pic), "BITMAP %d,%d,%d,%d,%d,", &x, &y, &wb, &h, &mode); err != nil {
		t.Fatalf("not a BITMAP: %q (%v)", pic[:min(40, len(pic))], err)
	}
	cell := fontCells[e.Font]
	if y != e.Y || x < e.X-7 || x > e.X || h != cell[1]*e.YScale || mode != 0 {
		t.Fatalf("BITMAP x=%d y=%d h=%d mode=%d for TEXT at %d,%d", x, y, h, mode, e.X, e.Y)
	}
	if x+wb*8 > latinLabel.Size.Width*dotsPerMM+7 {
		t.Fatalf("picture runs off the label: x=%d width=%d", x, wb*8)
	}
	if dir := os.Getenv("ARABIC_PNG_DIR"); dir != "" {
		hdr := len(fmt.Sprintf("BITMAP %d,%d,%d,%d,%d,", x, y, wb, h, mode))
		img := arabic.Unpack(wb, h, pic[hdr:hdr+wb*h], tspl.BitmapInkBit)
		path := filepath.Join(dir, "tspl_label_font"+e.Font+".png")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
	}
}
