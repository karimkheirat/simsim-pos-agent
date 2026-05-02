package receipt

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/karimkheirat/simsim-pos-agent/internal/escpos"
)

// Layout constants for 80mm thermal paper at single-size font. Widths
// are measured in display columns (runes), not UTF-8 bytes.
const (
	receiptWidth  = 42
	nameColWidth  = 24
	qtyColWidth   = 8
	totalColWidth = 10 // 24 + 8 + 10 = 42
)

// ErrInvalidReceipt is returned (wrapped via fmt.Errorf "%w") when a
// Receipt is missing required fields. Use errors.Is to detect.
var ErrInvalidReceipt = errors.New("invalid receipt")

// RenderOptions controls side effects appended after the receipt body.
type RenderOptions struct {
	OpenDrawerAfter bool
}

// Render serializes a Receipt to an ESC/POS byte stream: init + codepage
// + body + paper feed + full cut + optional drawer kick. French strings
// are transcoded to CP858 via escpos.TextCP858; pure-ASCII fixed strings
// (separators, blank lines) use escpos.Text. Returns ErrInvalidReceipt
// (wrapped) for malformed input.
func Render(r Receipt, opts RenderOptions) ([]byte, error) {
	if err := validate(r); err != nil {
		return nil, err
	}

	b := escpos.New().Init().Codepage(escpos.CP858)

	// Header — centered. Store name in double-height for emphasis.
	// LF stays outside the double-height scope so the trailing line feed
	// advances at normal height (preserves vertical rhythm).
	b.Align(escpos.Center)
	b.DoubleHeight(true).TextCP858(r.Store.Name).DoubleHeight(false).Text("\n")
	if r.Store.AddressLine1 != "" {
		b.TextCP858(r.Store.AddressLine1).Text("\n")
	}
	if r.Store.AddressLine2 != "" {
		b.TextCP858(r.Store.AddressLine2).Text("\n")
	}
	if r.Store.Phone != "" {
		b.TextCP858(r.Store.Phone).Text("\n")
	}
	if r.Store.TaxID != "" {
		b.TextCP858(r.Store.TaxID).Text("\n")
	}
	b.Align(escpos.Left).Text("\n")

	// Receipt info.
	b.TextCP858("Ticket N°: " + r.ReceiptNumber + "\n")
	b.TextCP858(formatDate(r.IssuedAt) + "\n")
	if r.Cashier.Name != "" {
		b.TextCP858("Caissier: " + r.Cashier.Name + "\n")
	}
	if r.Terminal.Label != "" {
		b.TextCP858("Terminal: " + r.Terminal.Label + "\n")
	}

	// Items.
	b.Text(strings.Repeat("-", receiptWidth) + "\n")
	for _, l := range r.Lines {
		b.TextCP858(formatLine(l) + "\n")
	}

	// Top-level discounts (separate section per spec §7).
	if len(r.Discounts) > 0 {
		b.Text(strings.Repeat("-", receiptWidth) + "\n")
		for _, d := range r.Discounts {
			b.TextCP858(formatTotalLine(d.Label, d.Amount) + "\n")
		}
	}

	// Totals.
	b.Text(strings.Repeat("-", receiptWidth) + "\n")
	b.TextCP858(formatTotalLine("Sous-total", r.Totals.Subtotal) + "\n")
	if r.Totals.DiscountTotal != 0 {
		b.TextCP858(formatTotalLine("Remise", r.Totals.DiscountTotal) + "\n")
	}
	if r.Totals.TaxTotal != 0 {
		b.TextCP858(formatTotalLine("TVA", r.Totals.TaxTotal) + "\n")
	}
	// Grand total — double-height. LF placed outside the double-height
	// scope to keep the line feed at normal advance.
	b.DoubleHeight(true).
		TextCP858(formatGrandTotalLine("Total", r.Totals.GrandTotal, r.Currency)).
		DoubleHeight(false).
		Text("\n")

	// Payment block. v1 supports cash only.
	b.Text("\n")
	if r.Payment.Method == "cash" {
		b.TextCP858(formatTotalLine("Espèces", r.Payment.Tendered) + "\n")
		b.TextCP858(formatTotalLine("Rendu", r.Payment.Change) + "\n")
	} else {
		b.TextCP858("Paiement: " + r.Payment.Method + "\n")
	}

	// Footer — centered, normal weight.
	if len(r.FooterLines) > 0 {
		b.Text("\n").Align(escpos.Center)
		for _, fl := range r.FooterLines {
			b.TextCP858(fl + "\n")
		}
		b.Align(escpos.Left)
	}

	// Final feed + cut + optional drawer.
	b.Text("\n\n\n\n").CutFull()
	if opts.OpenDrawerAfter {
		b.DrawerKick()
	}

	return b.Bytes(), nil
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

// formatDate renders DD/MM/YYYY  HH:MM with two spaces between, in the
// timezone carried by t.
func formatDate(t time.Time) string {
	return t.Format("02/01/2006  15:04")
}

// formatLine lays out an item line in three fixed-width columns:
// name (24, left), qty (8, centered), line_total (10, right) = 42 cols.
func formatLine(l Line) string {
	name := truncatePad(l.Name, nameColWidth)
	qty := centerPad(fmt.Sprintf("%d", l.Qty), qtyColWidth)
	total := rightAlign(formatAmount(l.LineTotal), totalColWidth)
	return name + qty + total
}

// formatTotalLine lays out "label" + spaces + "amount" filling receiptWidth
// display columns (rune-counted, so French diacritics align correctly).
func formatTotalLine(label string, amount float64) string {
	a := formatAmount(amount)
	pad := receiptWidth - utf8.RuneCountInString(label) - utf8.RuneCountInString(a)
	if pad < 1 {
		pad = 1
	}
	return label + strings.Repeat(" ", pad) + a
}

// formatGrandTotalLine appends " <currency>" to the amount on the grand-total line.
func formatGrandTotalLine(label string, amount float64, currency string) string {
	a := formatAmount(amount) + " " + currency
	pad := receiptWidth - utf8.RuneCountInString(label) - utf8.RuneCountInString(a)
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
