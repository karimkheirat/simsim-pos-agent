package receipt

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/arabic"
	"github.com/karimkheirat/simsim-pos-agent/internal/escpos"
	"github.com/karimkheirat/simsim-pos-agent/internal/tspl"
)

// mixedLine is the kind of line a Darija shop prints: an Arabic name, a
// Latin size and quantity, a price.
const mixedLine = "حليب كانديا 1L ×2   240,00 DA"

// productReceipt is a one-line ticket whose product is called name.
func productReceipt(name string) Receipt {
	return Receipt{
		Store:         Store{Name: "Superette El Amel"},
		Terminal:      Terminal{Label: "Caisse 1"},
		Cashier:       Cashier{Name: "Amine"},
		ReceiptNumber: "TC-S0417-P2-2026-0312",
		IssuedAt:      time.Date(2026, 9, 23, 14, 32, 0, 0, time.FixedZone("", 3600)),
		Currency:      "DZD",
		Lines:         []Line{{Name: name, Qty: 2, UnitPrice: 120, LineTotal: 240}},
		Totals:        Totals{Subtotal: 240, GrandTotal: 240},
		Payment:       Payment{Method: "cash", Tendered: 300, Change: 60},
		FooterLines:   []string{"Merci de votre visite !"},
	}
}

// spliced checks that b is a with exactly one span replaced: a's row
// (found by its bytes) gives way to b's picture. It returns the picture.
func spliced(t *testing.T, a, b, row []byte) []byte {
	t.Helper()
	i := bytes.Index(a, row)
	if i < 0 {
		t.Fatalf("text row %q not found in the Latin render", row)
	}
	if bytes.Count(a, row) != 1 {
		t.Fatalf("text row %q appears more than once", row)
	}
	pre, post := a[:i], a[i+len(row):]
	if !bytes.HasPrefix(b, pre) {
		t.Fatalf("the bytes before the Arabic line changed")
	}
	if !bytes.HasSuffix(b, post) {
		t.Fatalf("the bytes after the Arabic line changed")
	}
	if len(b) < len(pre)+len(post) {
		t.Fatalf("Arabic render is shorter than its surroundings")
	}
	return b[len(pre) : len(b)-len(post)]
}

func TestArabic_ESCPOS_ProductLineIsAPicture(t *testing.T) {
	for _, mm := range []int{80, 58} {
		t.Run(strconv.Itoa(mm), func(t *testing.T) {
			opts := RenderOptions{PaperWidthMM: mm, CutSupported: true}
			latin, err := Render(productReceipt("Lait Candia 1L"), opts)
			if err != nil {
				t.Fatal(err)
			}
			ar, err := Render(productReceipt("حليب كانديا 1L"), opts)
			if err != nil {
				t.Fatal(err)
			}
			// 80 mm prints name and price on one row; 58 mm the name alone.
			row := escpos.ToCP858(padBetween("Lait Candia 1L", "240 DA", 42) + "\n")
			if mm == 58 {
				row = escpos.ToCP858("Lait Candia 1L\n")
			}
			pic := spliced(t, latin, ar, row)
			wb := dotsFor(mm) / 8
			head := []byte{0x1D, 0x76, 0x30, 0x00, byte(wb), 0, escposRowDots, 0}
			if !bytes.HasPrefix(pic, head) {
				t.Fatalf("picture header % X, want % X", pic[:8], head)
			}
			if len(pic) != 8+wb*escposRowDots {
				t.Fatalf("picture is %d bytes, want %d", len(pic), 8+wb*escposRowDots)
			}
			if bytes.Count(ar, []byte{0x1D, 0x76, 0x30}) != 1 {
				t.Fatal("more than one picture: a Latin line was drawn as one")
			}
		})
	}
}

// The store name, bold and double height, becomes a double-height picture.
func TestArabic_ESCPOS_StoreNameTall(t *testing.T) {
	r := productReceipt("Lait")
	r.Store.Name = "بقالة السلام"
	out, err := Render(r, RenderOptions{PaperWidthMM: 80, CutSupported: true})
	if err != nil {
		t.Fatal(err)
	}
	head := []byte{0x1D, 0x76, 0x30, 0x00, 72, 0, escposRowDots * storeHeight, 0}
	if !bytes.Contains(out, head) {
		t.Fatal("no double-height picture for the store name")
	}
}

func TestArabic_ESCPOS_NoArabicUnchanged(t *testing.T) {
	out, err := Render(productReceipt("Lait Candia 1L"), RenderOptions{PaperWidthMM: 80, CutSupported: true})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte{0x1D, 0x76, 0x30}) {
		t.Fatal("a receipt with no Arabic carries a raster image")
	}
}

func TestArabic_TSPL_ProductLineIsABitmap(t *testing.T) {
	latin, err := RenderTSPL(productReceipt("Lait Candia 1L"), tsplOpts(80))
	if err != nil {
		t.Fatal(err)
	}
	ar, err := RenderTSPL(productReceipt("حليب كانديا 1L"), tsplOpts(80))
	if err != nil {
		t.Fatal(err)
	}
	l := Line{Name: "Lait Candia 1L", Qty: 2, LineTotal: 240}
	i := bytes.Index(latin, []byte(formatLine(l, widths80mm)))
	if i < 0 {
		t.Fatal("Latin product TEXT not found")
	}
	start := bytes.LastIndex(latin[:i], []byte("\r\n")) + 2
	end := i + bytes.Index(latin[i:], []byte("\r\n")) + 2
	pic := spliced(t, latin, ar, latin[start:end])
	if !bytes.HasPrefix(pic, []byte("BITMAP ")) || !bytes.HasSuffix(pic, []byte("\r\n")) {
		t.Fatalf("replacement is not a BITMAP line: %q", pic[:min(40, len(pic))])
	}
	// Same y as the TEXT it replaces.
	var tx, ty, bx, by, wb, h, mode int
	fmt.Sscanf(string(latin[start:end]), "TEXT %d,%d", &tx, &ty)
	fmt.Sscanf(string(pic), "BITMAP %d,%d,%d,%d,%d,", &bx, &by, &wb, &h, &mode)
	if by != ty || mode != 0 || h != tsplFontCells[tspl.Font2].h+tsplLineGapDots {
		t.Fatalf("BITMAP at y=%d h=%d mode=%d, TEXT at y=%d", by, h, mode, ty)
	}
	hdr := fmt.Sprintf("BITMAP %d,%d,%d,%d,%d,", bx, by, wb, h, mode)
	if len(pic) != len(hdr)+wb*h+2 {
		t.Fatalf("BITMAP carries %d bytes, want %d", len(pic)-len(hdr)-2, wb*h)
	}
}

// TestArabic_WritePNGs renders mixed lines through the real renderers and
// writes the pictures they would print to $ARABIC_PNG_DIR, so a person
// can look at them before a printer is at hand. Skipped when unset:
//
//	ARABIC_PNG_DIR=/tmp/arabic go test ./internal/receipt -run WritePNGs
func TestArabic_WritePNGs(t *testing.T) {
	dir := os.Getenv("ARABIC_PNG_DIR")
	if dir == "" {
		t.Skip("set ARABIC_PNG_DIR to write the pictures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := productReceipt("حليب كانديا 1L ×2")
	r.Store.Name = "بقالة السلام"
	r.Lines[0].LineTotal = 240.5
	r.FooterLines = []string{mixedLine}
	for _, mm := range []int{80, 58} {
		out, err := Render(r, RenderOptions{PaperWidthMM: mm, CutSupported: true})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for {
			i := bytes.Index(out, []byte{0x1D, 0x76, 0x30, 0x00})
			if i < 0 {
				break
			}
			wb := int(out[i+4]) | int(out[i+5])<<8
			h := int(out[i+6]) | int(out[i+7])<<8
			data := out[i+8 : i+8+wb*h]
			n++
			writePNG(t, filepath.Join(dir, fmt.Sprintf("escpos_%dmm_%d.png", mm, n)), arabic.Unpack(wb, h, data, 1))
			out = out[i+8+wb*h:]
		}
	}
	tsp, err := RenderTSPL(r, tsplOpts(80))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		i := bytes.Index(tsp, []byte("BITMAP "))
		if i < 0 {
			break
		}
		var x, y, wb, h, mode int
		fmt.Sscanf(string(tsp[i:]), "BITMAP %d,%d,%d,%d,%d,", &x, &y, &wb, &h, &mode)
		hdr := len(fmt.Sprintf("BITMAP %d,%d,%d,%d,%d,", x, y, wb, h, mode))
		data := tsp[i+hdr : i+hdr+wb*h]
		n++
		writePNG(t, filepath.Join(dir, fmt.Sprintf("tspl_receipt_80mm_%d_x%d.png", n, x)), arabic.Unpack(wb, h, data, tspl.BitmapInkBit))
		tsp = tsp[i+hdr+wb*h:]
	}
}

func writePNG(t *testing.T, path string, img *image.Gray) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%dx%d)", path, img.Bounds().Dx(), img.Bounds().Dy())
}
