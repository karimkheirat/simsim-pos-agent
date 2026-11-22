package receipt

import (
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/arabic"
	"github.com/karimkheirat/simsim-pos-agent/internal/code128"
	"github.com/karimkheirat/simsim-pos-agent/internal/escpos"
	"github.com/karimkheirat/simsim-pos-agent/internal/tspl"
)

// render_tspl.go renders a Receipt to the TSPL2 label language so that
// TSPL-only thermal printers (which cannot parse ESC/POS) — e.g. the
// Gprinter GP-3150TN at the pilot store — can print customer receipts.
//
// The layout is NOT its own: it is paper.go's, written once against
// paperOut, so a ticket on a TSPL printer reads exactly as on an ESC/POS
// one — the document's name and number, each line's working under it
// ("0,750 kg × 150,00 DA/kg" for a weighed line), "DA" after every amount,
// and the Code 128 of the ticket's number that the till scans to reopen the
// sale. (Until v0.3.17 this file had a layout of its own, which printed none
// of the three.) Only the output language differs: positioned TSPL TEXT
// commands via internal/tspl — the SAME TSPL engine the label path uses —
// with Arabic rows and the barcode drawn as BITMAPs.
//
// Continuous media: TSPL is page-oriented (SIZE w,h before any drawing),
// but a receipt's height is unknown until laid out. So this is a
// two-pass render — pass 1 lays out every line and accumulates the Y
// cursor (dots); pass 2 emits SIZE width,<computed height mm> + GAP 0,0
// (gapless/continuous) and the positioned elements. GAP 0 + a computed
// SIZE height is the standard "continuous roll" idiom.
//
// What has no TSPL equivalent and is therefore dropped vs ESC/POS:
//   - paper cut (GS V 0): label printers tear at the gap; the bottom
//     margin below provides tear clearance.
//   - cash-drawer kick (ESC p): TSPL label printers have no drawer port,
//     so open_drawer_after is accepted-and-ignored on this path (matches
//     capabilities.DrawerSupported=false for label hardware).

// ── TUNABLE constants (calibrate on the real GP-3150TN) ───────────────
//
// These are deliberately collected here so tomorrow's physical
// calibration is a one-file change. dpi/width-dots/font are passed via
// TSPLReceiptOptions; the spacing below is fixed for now.
const (
	// tsplTopMarginDots is the blank space above the first printed line.
	tsplTopMarginDots = 16
	// tsplLineGapDots is the vertical gap added after each text row, on
	// top of the font's cell height.
	tsplLineGapDots = 4
	// tsplBottomMarginDots is the tear/feed clearance added below the
	// last line (TSPL has no cut; this is the standoff for the tear bar).
	tsplBottomMarginDots = 48
)

// tsplFontCell is the dot footprint of a TSPL internal bitmap font at
// 1x magnification. Values from the TSPL2 Programming Manual font table
// (also documented in internal/tspl/tspl.go). Used for layout math —
// column widths, centering, and row advance. TUNABLE: confirm the
// chosen font's real cell size on the GP-3150TN firmware.
type tsplFontCell struct{ w, h int }

var tsplFontCells = map[tspl.FontName]tsplFontCell{
	tspl.Font1: {8, 12},
	tspl.Font2: {12, 20},
	tspl.Font3: {16, 24},
	tspl.Font4: {24, 32},
	tspl.Font5: {32, 48},
}

// TSPLReceiptOptions controls the TSPL receipt render. Every field has a
// sane zero-value default via withDefaults; WidthDots/DPI/Font are the
// knobs we expect to calibrate against the real printer.
type TSPLReceiptOptions struct {
	// PaperWidthMM selects the column set (mirrors RenderOptions) AND the
	// TSPL SIZE width. 58 or 80; defaults to 80.
	PaperWidthMM int

	// WidthDots is the printable width in dots, used for centering and
	// right-alignment. Defaults to 384 (58mm) / 576 (80mm) at 203dpi.
	// TUNABLE.
	WidthDots int

	// DPI is the printer resolution, used for the dots→mm SIZE-height
	// conversion. Defaults to 203. TUNABLE (GP-3150TN may be 203 or 300).
	DPI int

	// Font is the primary bitmap font for all text. Defaults to Font2
	// (12x20 dots) — narrow enough that the 80mm 42-column layout fits
	// (42*12=504 ≤ 576) and the 58mm 32-column layout fits exactly
	// (32*12=384). TUNABLE.
	Font tspl.FontName

	// Density (DENSITY 0..15) and Speed (SPEED ips) are the TSPL print
	// parameters. Default 8 / 4. TUNABLE.
	Density int
	Speed   int

	// Location is the shop's clock for a UTC issued_at (nil: the
	// machine's), as RenderOptions.Location.
	Location *time.Location

	// Dialect is reserved (receipts carry no EAN-13, and the Code 128 is
	// drawn as a bitmap, so the standard/rongta split is unused). Defaults
	// to DialectStandard.
	Dialect tspl.Dialect
}

func (o TSPLReceiptOptions) withDefaults() TSPLReceiptOptions {
	if o.PaperWidthMM == 0 {
		o.PaperWidthMM = 80
	}
	if o.WidthDots == 0 {
		if o.PaperWidthMM == 58 {
			o.WidthDots = 384
		} else {
			o.WidthDots = 576
		}
	}
	if o.DPI == 0 {
		o.DPI = 203
	}
	if o.Font == "" {
		o.Font = tspl.Font2
	}
	if o.Density == 0 {
		o.Density = 8
	}
	if o.Speed == 0 {
		o.Speed = 4
	}
	if o.Dialect == "" {
		o.Dialect = tspl.DialectStandard
	}
	return o
}

// tsplOp is one positioned element captured during pass 1.
type tsplOp struct {
	x, y, xMul, yMul int
	s                string
	cp               bool // transcode UTF-8 → CP1252 (French text)

	// bitmap, when set, replaces the text: a row holding Arabic, or the
	// Code 128, drawn as a picture.
	bitmap       []byte
	bitmapWidthB int
	bitmapHeight int
}

// tsplOut is paperOut on a TSPL page: pass 1 of the render. It lays every
// row out at a Y cursor in dots; RenderTSPL then emits them.
type tsplOut struct {
	opts  TSPLReceiptOptions
	cell  tsplFontCell
	align escpos.Alignment
	y     int
	ops   []tsplOp
}

// TSPL bitmap fonts have no bold and no character spacing; the document
// name keeps its weight from being UPPER, as the browser sheet does on a
// printer without bold.
func (o *tsplOut) setBold(bool)                {}
func (o *tsplOut) setSpacing(byte)             {}
func (o *tsplOut) setAlign(a escpos.Alignment) { o.align = a }

// xFor is where a row of w dots starts under the current alignment.
func (o *tsplOut) xFor(w int) int {
	x := 0
	switch o.align {
	case escpos.Center:
		x = (o.opts.WidthDots - w) / 2
	case escpos.Right:
		x = o.opts.WidthDots - w
	}
	if x < 0 {
		x = 0
	}
	return x
}

func (o *tsplOut) text(s string, xMul, yMul int, cp bool) {
	// The web app keeps a figure and its unit together with a no-break
	// space ("0,750 kg"); a TSPL bitmap font has no glyph it can be trusted
	// to draw for one, and the row is laid out already.
	s = strings.NewReplacer("\u00a0", " ", "\u202f", " ").Replace(s)
	o.ops = append(o.ops, tsplOp{x: o.xFor(runeLen(s) * o.cell.w * xMul), y: o.y, xMul: xMul, yMul: yMul, s: s, cp: cp})
	o.y += o.cell.h*yMul + tsplLineGapDots
}

func (o *tsplOut) arabic(s string, st arabic.Style) bool {
	st.Dots = o.opts.WidthDots
	st.CellW = o.cell.w * st.Scale
	img, err := arabic.RowImage(s, st)
	if err != nil {
		return false
	}
	data, wb, h := arabic.TSPLBitmap(img)
	o.ops = append(o.ops, tsplOp{y: o.y, bitmap: data, bitmapWidthB: wb, bitmapHeight: h})
	o.y += h + tsplLineGapDots
	return true
}

// barcode draws the Code 128 as a BITMAP rather than a BARCODE command:
// the symbol is then exactly the set-B symbol the ESC/POS path asks for,
// of a width known here (so it centres), whatever the printer's firmware
// would choose for "128".
func (o *tsplOut) barcode(data string, moduleDots, heightDots int) bool {
	mods, err := code128.Modules(data)
	if err != nil {
		return false
	}
	// The picture is the whole row, so the bars sit centred with their
	// quiet zones white on each side, and the bitmap starts at x 0 (a
	// bitmap's x is in whole bytes on some firmware).
	width := (o.opts.WidthDots + 7) &^ 7
	left := (width - len(mods)*moduleDots) / 2
	if left < 0 {
		return false
	}
	img := image.NewGray(image.Rect(0, 0, width, heightDots))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	for i, bar := range mods {
		if !bar {
			continue
		}
		for dx := 0; dx < moduleDots; dx++ {
			for y := 0; y < heightDots; y++ {
				img.SetGray(left+i*moduleDots+dx, y, color.Gray{})
			}
		}
	}
	bits, wb, h := arabic.TSPLBitmap(img)
	x := 0
	o.y += tsplLineGapDots * 2
	o.ops = append(o.ops, tsplOp{x: x, y: o.y, bitmap: bits, bitmapWidthB: wb, bitmapHeight: h})
	o.y += h + tsplLineGapDots
	return true
}

func (o *tsplOut) picture(img *image.Gray) bool {
	bits, wb, h := arabic.TSPLBitmap(img)
	x := o.xFor(wb*8) &^ 7
	o.ops = append(o.ops, tsplOp{x: x, y: o.y, bitmap: bits, bitmapWidthB: wb, bitmapHeight: h})
	o.y += h + tsplLineGapDots
	return true
}

// RenderTSPL serializes a Receipt to a TSPL2 byte stream for TSPL-only
// printers, laid out by paper.go exactly as Render lays it out. Returns
// ErrInvalidReceipt (wrapped) for malformed input — same validation as
// the ESC/POS path.
func RenderTSPL(r Receipt, opts TSPLReceiptOptions) ([]byte, error) {
	if err := validate(r); err != nil {
		return nil, err
	}
	opts = opts.withDefaults()

	cell, ok := tsplFontCells[opts.Font]
	if !ok {
		cell = tsplFontCells[tspl.Font2]
	}

	// ── Pass 1: lay out, tracking the Y cursor in dots ──
	out := &tsplOut{opts: opts, cell: cell, y: tsplTopMarginDots}
	p := page{
		out:    out,
		width:  widthsFor(opts.PaperWidthMM).receipt,
		narrow: opts.PaperWidthMM == 58,
		dots:   opts.WidthDots,
		loc:    opts.Location,
	}
	p.write(r)

	// Tear clearance below the last line (no cut on TSPL).
	y := out.y + tsplBottomMarginDots

	// ── Pass 2: emit the pre-amble with the computed height, then the
	// positioned elements, then PRINT. ──
	//
	// SIZE height is in mm — convert the dot cursor via dots-per-mm
	// (DPI/25.4) and round up so nothing is clipped (≤1mm of bottom
	// padding). SIZE width is the physical paper width in mm.
	dotsPerMM := float64(opts.DPI) / 25.4
	heightMM := int(math.Ceil(float64(y) / dotsPerMM))
	if heightMM < 1 {
		heightMM = 1
	}

	b := tspl.NewWithDialect(opts.Dialect)
	b.CLS().
		Size(opts.PaperWidthMM, heightMM).
		Gap(0, 0). // continuous / gapless media
		Direction(tspl.Direction1).
		Density(opts.Density).
		Speed(opts.Speed).
		Codepage("1252")

	for _, o := range out.ops {
		if o.bitmap != nil {
			b.Bitmap(o.x, o.y, o.bitmapWidthB, o.bitmapHeight, o.bitmap)
			continue
		}
		if o.cp {
			b.TextCP1252(o.x, o.y, opts.Font, 0, o.xMul, o.yMul, o.s)
		} else {
			b.Text(o.x, o.y, opts.Font, 0, o.xMul, o.yMul, o.s)
		}
	}
	b.Print(1, 1)

	return b.Bytes(), nil
}
