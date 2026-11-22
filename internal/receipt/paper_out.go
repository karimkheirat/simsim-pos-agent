package receipt

import (
	"image"

	"github.com/karimkheirat/simsim-pos-agent/internal/arabic"
	"github.com/karimkheirat/simsim-pos-agent/internal/escpos"
)

// paperOut is where the till's paper is written: the ESC/POS byte stream,
// or the TSPL page (render_tspl.go). The layout in paper.go is written once
// against it, so a ticket reads the same on both kinds of printer — the
// head, the lines with their working, "DA", the totals, the tenders, the
// thanks and the Code 128 of the ticket's number.
type paperOut interface {
	setBold(on bool)
	setAlign(a escpos.Alignment)
	// setSpacing is extra dots between characters (the document name's
	// tracking). A printer without it ignores it.
	setSpacing(dots byte)
	// text writes one laid-out row at xMul × yMul size and ends it.
	// cp says to transcode it (French text); a rule is sent as is.
	text(s string, xMul, yMul int, cp bool)
	// arabic draws one row holding Arabic as a picture and reports whether
	// it did (false: the caller prints it as text).
	arabic(s string, st arabic.Style) bool
	// barcode prints data as a Code 128 (set B), centred, moduleDots wide a
	// module, and reports whether it did.
	barcode(data string, moduleDots, heightDots int) bool
	// picture prints a black-and-white picture centred on its own rows
	// (the shopper QR) and reports whether it did.
	picture(img *image.Gray) bool
}

// escposOut writes to an ESC/POS builder, byte for byte as paper.go always
// has.
type escposOut struct {
	b     *escpos.Builder
	align escpos.Alignment
}

func (o *escposOut) setBold(on bool) { o.b.Bold(on) }

func (o *escposOut) setAlign(a escpos.Alignment) {
	o.align = a
	o.b.Align(a)
}

func (o *escposOut) setSpacing(dots byte) { o.b.CharSpacing(dots) }

func (o *escposOut) text(s string, xMul, yMul int, cp bool) {
	if xMul == 1 && yMul == 1 {
		if cp {
			o.b.TextCP858(s + "\n")
		} else {
			o.b.Text(s + "\n")
		}
		return
	}
	// The LF stays outside the scaled scope so the feed advances at normal
	// height.
	o.b.Size(xMul, yMul)
	if cp {
		o.b.TextCP858(s)
	} else {
		o.b.Text(s)
	}
	o.b.Size(1, 1).Text("\n")
}

func (o *escposOut) arabic(s string, st arabic.Style) bool {
	img, err := arabic.Row(s, st)
	if err != nil {
		return false
	}
	// The image is the full width; justification would only shift it.
	o.b.Align(escpos.Left)
	o.b.Write(img)
	o.b.Align(o.align)
	return true
}

func (o *escposOut) barcode(data string, moduleDots, heightDots int) bool {
	cmd, err := escpos.Code128(data, byte(heightDots), byte(moduleDots), escpos.HRINone)
	if err != nil {
		return false
	}
	o.b.Write(cmd)
	return true
}

func (o *escposOut) picture(img *image.Gray) bool {
	o.b.Align(escpos.Center)
	o.b.Write(arabic.Raster(img))
	o.b.Align(o.align)
	return true
}
