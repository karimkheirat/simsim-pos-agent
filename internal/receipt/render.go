package receipt

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Layout column counts. M13 A.5a — width is no longer a single constant;
// it's selected per render via RenderOptions.PaperWidthMM.
//
// At single-size font:
//   - 80mm thermal paper → 42 columns (24 name + 8 qty + 10 total)
//   - 58mm thermal paper → 32 columns (18 name + 6 qty + 8 total)
//
// The breakdown for 58mm is the same proportion as 80mm, scaled by
// 32/42 and rounded to fit (24*32/42 ≈ 18.3, 8*32/42 ≈ 6.1, 10*32/42
// ≈ 7.6 → use 8 to keep amount column readable). Sums: 18+6+8 = 32. ✓
//
// All values are in display COLUMNS (runes), not UTF-8 bytes.
type widthSet struct {
	receipt int // line width
	name    int // formatLine: name column
	qty     int // formatLine: qty column
	total   int // formatLine: total column
}

var (
	widths80mm = widthSet{receipt: 42, name: 24, qty: 8, total: 10}
	widths58mm = widthSet{receipt: 32, name: 18, qty: 6, total: 8}
)

// widthsFor returns the column set for the given paper width. Defaults
// to 80mm for any value other than 58 (the only other supported width
// in v1). Defaulting to 80mm (not panicking) preserves render success
// for a misconfigured caller — the agent's config.Validate rejects
// invalid widths at startup, so this branch is defence-in-depth.
func widthsFor(paperWidthMM int) widthSet {
	if paperWidthMM == 58 {
		return widths58mm
	}
	return widths80mm
}

// ErrInvalidReceipt is returned (wrapped via fmt.Errorf "%w") when a
// Receipt is missing required fields. Use errors.Is to detect.
var ErrInvalidReceipt = errors.New("invalid receipt")

// RenderOptions controls per-render output knobs. M13 A.5a added
// PaperWidthMM + CutSupported so a single Render call can target
// either an 80mm cut-capable printer or a 58mm no-cut printer without
// changing the function signature.
//
// Zero values keep pre-M13 behavior on the happy path:
//   - PaperWidthMM 0 → defaults to 80 in widthsFor (back-compat)
//   - CutSupported false BUT the constructor in handlers populates it
//     from capabilities, so the only code path that hits the zero
//     value is the "render to a no-cut printer" branch.
//
// Callers building options directly (tests) MUST set CutSupported
// explicitly — see TestRender_NoCut for the no-cut golden.
type RenderOptions struct {
	OpenDrawerAfter bool

	// PaperWidthMM is 58 or 80. Zero defaults to 80 (back-compat with
	// every pre-A.5a caller; new callers wire this from agent config).
	PaperWidthMM int

	// CutSupported gates the trailing GS V 0 (full cut). When false,
	// the renderer emits extra feed lines instead so the cashier can
	// tear the receipt manually.
	CutSupported bool

	// Location is the clock the date and time print in when issued_at
	// arrives in UTC (the web app sends "...Z"). Nil means the machine's
	// local zone, i.e. the shop's clock, as the browser prints it. A
	// timestamp sent with a non-zero offset prints in that offset.
	Location *time.Location
}

// validate enforces the minimum required fields per the sub-task contract.
func validate(r Receipt) error {
	if strings.TrimSpace(r.Store.Name) == "" {
		return fmt.Errorf("%w: store name is required", ErrInvalidReceipt)
	}
	if len(r.Lines) == 0 {
		return fmt.Errorf("%w: at least one line item is required", ErrInvalidReceipt)
	}
	// "Missing totals" — interpret as GrandTotal being zero while line
	// items have non-zero totals (caller forgot to populate Totals).
	if r.Totals.GrandTotal == 0 {
		var sum float64
		for _, l := range r.Lines {
			sum += l.LineTotal
		}
		if sum != 0 {
			return fmt.Errorf("%w: totals missing or zero (lines sum to %s)", ErrInvalidReceipt, formatAmount(sum))
		}
	}
	return nil
}

// formatAmount renders a value with French decimal comma and two decimals.
// No thousand separator yet (per sub-task 3 contract).
func formatAmount(v float64) string {
	return strings.Replace(fmt.Sprintf("%.2f", v), ".", ",", 1)
}

// formatDate (TSPL path) renders DD/MM/YYYY  HH:MM with two spaces between, in the
// timezone carried by t.
func formatDate(t time.Time) string {
	return t.Format("02/01/2006  15:04")
}

// formatLine lays out an item line in three fixed-width columns per the
// supplied widthSet. Pre-A.5a was hardcoded to 24/8/10 = 42 (80mm).
func formatLine(l Line, w widthSet) string {
	name := truncatePad(l.Name, w.name)
	qty := centerPad(fmt.Sprintf("%d", l.Qty), w.qty)
	total := rightAlign(formatAmount(l.LineTotal), w.total)
	return name + qty + total
}

// formatTotalLine lays out "label" + spaces + "amount" filling width
// display columns (rune-counted, so French diacritics align correctly).
func formatTotalLine(label string, amount float64, width int) string {
	a := formatAmount(amount)
	pad := width - utf8.RuneCountInString(label) - utf8.RuneCountInString(a)
	if pad < 1 {
		pad = 1
	}
	return label + strings.Repeat(" ", pad) + a
}

// formatGrandTotalLine appends " <currency>" to the amount on the grand-total line.
func formatGrandTotalLine(label string, amount float64, currency string, width int) string {
	a := formatAmount(amount) + " " + currency
	pad := width - utf8.RuneCountInString(label) - utf8.RuneCountInString(a)
	if pad < 1 {
		pad = 1
	}
	return label + strings.Repeat(" ", pad) + a
}

// truncatePad truncates s to width display columns and right-pads with
// spaces. If truncation leaves a trailing space, that space is trimmed
// before padding back out to width.
func truncatePad(s string, width int) string {
	runes := []rune(s)
	if len(runes) > width {
		s = strings.TrimRight(string(runes[:width]), " ")
		runes = []rune(s)
	}
	if len(runes) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(runes))
}

// centerPad centers s within width display columns. Truncates if longer.
func centerPad(s string, width int) string {
	runes := []rune(s)
	if len(runes) >= width {
		return string(runes[:width])
	}
	pad := width - len(runes)
	left := pad / 2
	right := pad - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

// rightAlign right-aligns s within width display columns.
func rightAlign(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return strings.Repeat(" ", width-n) + s
}
