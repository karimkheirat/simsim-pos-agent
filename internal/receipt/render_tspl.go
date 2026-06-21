package receipt

import (
	"math"
	"strings"

	"github.com/karimkheirat/simsim-pos-agent/internal/tspl"
)

// render_tspl.go renders a Receipt to the TSPL2 label language so that
// TSPL-only thermal printers (which cannot parse ESC/POS) — e.g. the
// Gprinter GP-3150TN at the pilot store — can print customer receipts.
//
// It mirrors the ESC/POS layout in render.go SECTION-FOR-SECTION and
// reuses the same column/format helpers (formatLine, formatTotalLine,
// formatGrandTotalLine, formatDate, widthsFor, validate). The only
// difference is the output language: instead of ESC/POS control bytes
// it emits positioned TSPL TEXT commands via internal/tspl — the SAME
// TSPL engine the label path uses (we reuse the engine, not the
// label.Label document type, whose Validate caps height at 150mm).
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

	// Dialect is reserved (receipts carry no EAN-13 today, so the
	// standard/rongta split is unused). Defaults to DialectStandard.
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

// tsplOp is one positioned text element captured during pass 1.
type tsplOp struct {
	x, y, xMul, yMul int
	s                string
	cp               bool // transcode UTF-8 → CP1252 (French text)
}

// RenderTSPL serializes a Receipt to a TSPL2 byte stream for TSPL-only
// printers. Layout mirrors Render (ESC/POS) section-for-section. Returns
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
	w := widthsFor(opts.PaperWidthMM)

	// ── Pass 1: lay out, tracking the Y cursor in dots ──
	var ops []tsplOp
	y := tsplTopMarginDots

	// emit places a text row at the current cursor and advances Y.
	// centered=true horizontally centers within WidthDots; otherwise the
	// row starts at x=0 (the monospace column block, byte-identical to
	// the ESC/POS formatLine output). cp=true transcodes French text.
	emit := func(s string, xMul, yMul int, centered, cp bool) {
		x := 0
		if centered {
			textDots := len([]rune(s)) * cell.w * xMul
			if x = (opts.WidthDots - textDots) / 2; x < 0 {
				x = 0
			}
		}
		ops = append(ops, tsplOp{x: x, y: y, xMul: xMul, yMul: yMul, s: s, cp: cp})
		y += cell.h*yMul + tsplLineGapDots
	}
	// blank advances the cursor by one base row (mirrors a "\n" in the
	// ESC/POS renderer).
	blank := func() { y += cell.h + tsplLineGapDots }

	// Header — centered. Store name double-height (yMul=2), matching the
	// ESC/POS DoubleHeight in render.go.
	emit(r.Store.Name, 1, 2, true, true)
	if r.Store.AddressLine1 != "" {
		emit(r.Store.AddressLine1, 1, 1, true, true)
	}
	if r.Store.AddressLine2 != "" {
		emit(r.Store.AddressLine2, 1, 1, true, true)
	}
	if r.Store.Phone != "" {
		emit(r.Store.Phone, 1, 1, true, true)
	}
	if r.Store.TaxID != "" {
		emit(r.Store.TaxID, 1, 1, true, true)
	}
	blank()

	// Receipt info.
	emit("Ticket N°: "+r.ReceiptNumber, 1, 1, false, true)
	emit(formatDate(r.IssuedAt), 1, 1, false, true)
	if r.Cashier.Name != "" {
		emit("Caissier: "+r.Cashier.Name, 1, 1, false, true)
	}
	if r.Terminal.Label != "" {
		emit("Terminal: "+r.Terminal.Label, 1, 1, false, true)
	}

	// Items.
	sep := strings.Repeat("-", w.receipt)
	emit(sep, 1, 1, false, false)
	for _, l := range r.Lines {
		emit(formatLine(l, w), 1, 1, false, true)
	}

	// Top-level discounts (separate section per spec §7).
	if len(r.Discounts) > 0 {
		emit(sep, 1, 1, false, false)
		for _, d := range r.Discounts {
			emit(formatTotalLine(d.Label, d.Amount, w.receipt), 1, 1, false, true)
		}
	}

	// Totals.
	emit(sep, 1, 1, false, false)
	emit(formatTotalLine("Sous-total", r.Totals.Subtotal, w.receipt), 1, 1, false, true)
	if r.Totals.DiscountTotal != 0 {
		emit(formatTotalLine("Remise", r.Totals.DiscountTotal, w.receipt), 1, 1, false, true)
	}
	if r.Totals.TaxTotal != 0 {
		emit(formatTotalLine("TVA", r.Totals.TaxTotal, w.receipt), 1, 1, false, true)
	}
	// Grand total — double-height (yMul=2), matching render.go.
	emit(formatGrandTotalLine("Total", r.Totals.GrandTotal, r.Currency, w.receipt), 1, 2, false, true)

	// Payment block. v1 supports cash only.
	blank()
	if r.Payment.Method == "cash" {
		emit(formatTotalLine("Espèces", r.Payment.Tendered, w.receipt), 1, 1, false, true)
		emit(formatTotalLine("Rendu", r.Payment.Change, w.receipt), 1, 1, false, true)
	} else {
		emit("Paiement: "+r.Payment.Method, 1, 1, false, true)
	}

	// Footer — centered.
	if len(r.FooterLines) > 0 {
		blank()
		for _, fl := range r.FooterLines {
			emit(fl, 1, 1, true, true)
		}
	}

	// Tear clearance below the last line (no cut on TSPL).
	y += tsplBottomMarginDots

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

	for _, o := range ops {
		if o.cp {
			b.TextCP1252(o.x, o.y, opts.Font, 0, o.xMul, o.yMul, o.s)
		} else {
			b.Text(o.x, o.y, opts.Font, 0, o.xMul, o.yMul, o.s)
		}
	}
	b.Print(1, 1)

	return b.Bytes(), nil
}
