package receipt

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"
)

// TestPreview writes PREVIEW_PNG when set: the Arabic rows of a sample
// ticket decoded back to pixels, to look at by eye.
func TestPreview(t *testing.T) {
	out := os.Getenv("PREVIEW_PNG")
	if out == "" {
		t.Skip()
	}
	dl := "0,342 كغ × 380 DA/kg"
	r := Receipt{
		Store:         Store{Name: "ميني شوب شموما", AddressLine1: "مستغانم", Phone: "Tél. : 0550 12 34 56"},
		Terminal:      Terminal{Label: "Caisse 1"},
		Cashier:       Cashier{Name: "رشيد"},
		ReceiptNumber: "TC-S0417-P2-2026-0312",
		IssuedAt:      time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC),
		Currency:      "DZD",
		Lines: []Line{
			{Name: "حليب كانديا 1 لتر", Qty: 2, UnitPrice: 120, LineTotal: 240},
			{Name: "Savon Liquide 400 g", Qty: 1, UnitPrice: 250, LineTotal: 250},
			{Name: "جبن (لافاش كيري) 16 قطعة", Qty: 1, UnitPrice: 130, LineTotal: 130, DiscountLabel: &dl},
		},
		Totals:      Totals{Subtotal: 620, GrandTotal: 620},
		Payment:     Payment{Method: "cash", Tendered: 1000, Change: 380},
		Labels:      &Labels{Total: "المجموع", Cash: "نقدا", Change: "الباقي"},
		FooterLines: []string{"شكرا لزيارتكم"},
	}
	for _, mm := range []int{80, 58} {
		b, err := Render(r, RenderOptions{PaperWidthMM: mm, CutSupported: true})
		if err != nil {
			t.Fatal(err)
		}
		var imgs []*image.Gray
		for i := 0; i+8 < len(b); i++ {
			if b[i] == 0x1D && b[i+1] == 0x76 && b[i+2] == 0x30 && b[i+3] == 0 {
				bw := int(b[i+4]) | int(b[i+5])<<8
				h := int(b[i+6]) | int(b[i+7])<<8
				img := image.NewGray(image.Rect(0, 0, bw*8, h+6))
				for y := 0; y < h+6; y++ {
					for x := 0; x < bw*8; x++ {
						img.SetGray(x, y, color.Gray{255})
					}
				}
				data := b[i+8 : i+8+bw*h]
				for y := 0; y < h; y++ {
					for x := 0; x < bw*8; x++ {
						if data[y*bw+x/8]&(0x80>>(x%8)) != 0 {
							img.SetGray(x, y, color.Gray{0})
						}
					}
				}
				imgs = append(imgs, img)
				i += 8 + bw*h - 1
			}
		}
		W, H := 0, 0
		for _, im := range imgs {
			if im.Bounds().Dx() > W {
				W = im.Bounds().Dx()
			}
			H += im.Bounds().Dy()
		}
		all := image.NewGray(image.Rect(0, 0, W, H))
		y := 0
		for _, im := range imgs {
			for yy := 0; yy < im.Bounds().Dy(); yy++ {
				for x := 0; x < im.Bounds().Dx(); x++ {
					all.SetGray(x, y+yy, im.GrayAt(x, yy))
				}
			}
			y += im.Bounds().Dy()
		}
		f, _ := os.Create(out + "-" + map[int]string{80: "80", 58: "58"}[mm] + ".png")
		png.Encode(f, all)
		f.Close()
	}
}
