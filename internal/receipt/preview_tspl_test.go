package receipt

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/encoding/charmap"
)

// TestPreviewTSPL writes PREVIEW_TSPL_PNG when set: Rachid's ticket as
// RenderTSPL sends it to his XP-420B, drawn back from the TSPL bytes — TEXT
// in a monospace face sized to the printer's 12x20 cell, BITMAPs (Arabic
// rows, the barcode) dot for dot. It reads the stream, not the layout, so
// what it shows is what the printer is told.
func TestPreviewTSPL(t *testing.T) {
	out := os.Getenv("PREVIEW_TSPL_PNG")
	if out == "" {
		t.Skip()
	}
	r := rachidTicket()
	w := func(s string) *string { return &s }
	r.Lines = []Line{
		{Name: "LENTILLES VRAC", Qty: 1, UnitPrice: 112.5, LineTotal: 112.5, DiscountLabel: w("0,750\u00a0kg × 150,00\u00a0DA/kg")},
		{Name: "RIZ BASMATI VRAC", Qty: 1, UnitPrice: 204, LineTotal: 204, DiscountLabel: w("1,200\u00a0kg × 170,00\u00a0DA/kg")},
		{Name: "OLIVES NOIR", Qty: 1, UnitPrice: 157.5, LineTotal: 157.5, DiscountLabel: w("0,350\u00a0kg × 450,00\u00a0DA/kg")},
		{Name: "Lait Candia 1 L", Qty: 2, UnitPrice: 120, LineTotal: 240},
		{Name: "حليب الصومام 1 لتر", Qty: 1, UnitPrice: 110, LineTotal: 110},
	}
	r.Totals = Totals{Subtotal: 824, GrandTotal: 824}
	r.Payment = Payment{Method: "cash", Tendered: 1000, Change: 176}
	if os.Getenv("PREVIEW_INVITE") != "" {
		r.ShopperInvite = &ShopperInvite{URL: "https://app.molshi.com/shopper", Text: os.Getenv("PREVIEW_INVITE")}
	}
	b, err := RenderTSPL(r, tsplOpts(80))
	if err != nil {
		t.Fatal(err)
	}
	img := drawTSPL(t, b, 576)
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// drawTSPL plays a TSPL stream onto a white page widthDots across.
func drawTSPL(t *testing.T, b []byte, widthDots int) *image.Gray {
	t.Helper()
	ft, err := opentype.Parse(gomonobold.TTF)
	if err != nil {
		t.Fatal(err)
	}
	face, err := opentype.NewFace(ft, &opentype.FaceOptions{Size: 19, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		t.Fatal(err)
	}
	dec := charmap.Windows1252.NewDecoder()

	heightDots := 0
	if i := bytes.Index(b, []byte("SIZE ")); i >= 0 {
		line := string(b[i : i+bytes.Index(b[i:], []byte("\r\n"))])
		mm, _ := strconv.Atoi(strings.TrimSuffix(strings.Fields(strings.Split(line, ",")[1])[0], " mm"))
		heightDots = mm * 8
	}
	page := image.NewGray(image.Rect(0, 0, widthDots, heightDots))
	draw.Draw(page, page.Bounds(), image.White, image.Point{}, draw.Src)

	for len(b) > 0 {
		switch {
		case bytes.HasPrefix(b, []byte("BITMAP ")):
			hdrEnd := 0
			for n, commas := 0, 0; n < len(b); n++ {
				if b[n] == ',' {
					commas++
					if commas == 5 {
						hdrEnd = n + 1
						break
					}
				}
			}
			f := strings.Split(string(b[7:hdrEnd-1]), ",")
			x, _ := strconv.Atoi(f[0])
			y, _ := strconv.Atoi(f[1])
			wb, _ := strconv.Atoi(f[2])
			h, _ := strconv.Atoi(f[3])
			data := b[hdrEnd : hdrEnd+wb*h]
			for yy := 0; yy < h; yy++ {
				for xx := 0; xx < wb*8; xx++ {
					if data[yy*wb+xx/8]&(0x80>>(xx%8)) == 0 {
						page.SetGray(x+xx, y+yy, color.Gray{})
					}
				}
			}
			b = b[hdrEnd+wb*h+2:]
		case bytes.HasPrefix(b, []byte("TEXT ")):
			end := bytes.Index(b, []byte("\r\n"))
			line := string(b[5:end])
			q := strings.Index(line, "\"")
			nums := strings.Split(line[:q], ",")
			rest := line[q+1:]
			parts := strings.SplitN(rest, "\",", 2)
			tail := strings.Split(parts[1], ",")
			x, _ := strconv.Atoi(nums[0])
			y, _ := strconv.Atoi(nums[1])
			xm, _ := strconv.Atoi(tail[1])
			ym, _ := strconv.Atoi(tail[2])
			content := strings.Join(tail[3:], ",")
			content = strings.TrimSuffix(strings.TrimPrefix(content, "\""), "\"")
			content = strings.ReplaceAll(content, "\\[\"]", "\"")
			s, _ := dec.String(content)
			drawCells(page, face, x, y, xm, ym, s)
			b = b[end+2:]
		default:
			end := bytes.Index(b, []byte("\r\n"))
			if end < 0 {
				b = nil
			} else {
				b = b[end+2:]
			}
		}
	}
	return page
}

// drawCells draws s one character a 12x20 cell, scaled xm x ym.
func drawCells(page *image.Gray, face font.Face, x, y, xm, ym int, s string) {
	for i, r := range []rune(s) {
		cell := image.NewGray(image.Rect(0, 0, 12, 20))
		draw.Draw(cell, cell.Bounds(), image.White, image.Point{}, draw.Src)
		d := font.Drawer{Dst: cell, Src: image.Black, Face: face, Dot: fixed.P(0, 16)}
		d.DrawString(string(r))
		for cy := 0; cy < 20*ym; cy++ {
			for cx := 0; cx < 12*xm; cx++ {
				if cell.GrayAt(cx/xm, cy/ym).Y < 128 {
					page.SetGray(x+i*12*xm+cx, y+cy, color.Gray{})
				}
			}
		}
	}
}
