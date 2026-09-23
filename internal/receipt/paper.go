package receipt

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/escpos"
)

// THE TILL'S PAPER on ESC/POS — the ticket de caisse and the rapport de
// caisse laid out per the web app's DESIGN_SYSTEM §67.2 (head) and §67.5
// (thermal sizes), so paper printed through the agent matches paper printed
// through the browser (simsim src/lib/pos/paper.ts, receipt.tsx).
//
// Type sizes. A thermal printer has no point sizes: it has one bitmap font
// scaled by whole numbers (GS ! — x1 to x8 wide and tall) and a bold
// switch. At 203 dpi Font A is 12x24 dots, a 1.5 x 3.0 mm cell, about
// 8.5 pt, so §67.5 maps as:
//
//	body 9 pt            -> Font A x1 (42 columns on 80 mm, 32 on 58 mm)
//	store 11 pt bold     -> bold, x2 tall x1 wide: the smallest step up;
//	                        x1 wide keeps a long name on its row
//	document name 9 pt   -> bold, UPPER, +1 dot letter spacing (1/12, the
//	  bold +6 % UPPER       nearest to +6 % tracking)
//	TOTAL 13 pt bold     -> bold, x2 tall x2 wide, the biggest line; x2 tall
//	                        x1 wide when the amount would not fit at x2
//	dashed dividers      -> a full row of "-"
//	black only           -> no red-ribbon or reverse-print command is sent
const (
	storeHeight = 2
	totalScale  = 2
	// barcodeHeightDots is 8 mm; the browser draws 32 px, about 8.5 mm.
	barcodeHeightDots = 64
)

// dotsFor is the printable width in dots at 203 dpi: 72 mm = 576 dots on
// the 80 mm roll, 48 mm = 384 dots on the 58 mm roll.
func dotsFor(paperWidthMM int) int {
	if paperWidthMM == 58 {
		return 384
	}
	return 576
}

// defaultLabels are the French words for what the web app does not send.
// It sends the store phone already worded ("Tél. : ..."), the tenders'
// names, the shopper, the thanks and the rapport's rows; Receipt.Labels
// overrides these.
var defaultLabels = Labels{
	Subtotal:  "Sous-total",
	Discounts: "Remises",
	Tax:       "TVA",
	Total:     "TOTAL",
	Cash:      "Espèces",
	Change:    "Monnaie",
}

const (
	titleSale   = "TICKET DE CAISSE"
	titleRefund = "TICKET DE REMBOURSEMENT"
	titleReport = "RAPPORT DE CAISSE"
)

// labelsFor merges the request's words over the French defaults.
func labelsFor(r Receipt) Labels {
	l := defaultLabels
	if r.Labels == nil {
		return l
	}
	pick := func(dst *string, v string) {
		if strings.TrimSpace(v) != "" {
			*dst = v
		}
	}
	pick(&l.Subtotal, r.Labels.Subtotal)
	pick(&l.Discounts, r.Labels.Discounts)
	pick(&l.Tax, r.Labels.Tax)
	pick(&l.Total, r.Labels.Total)
	pick(&l.Cash, r.Labels.Cash)
	pick(&l.Change, r.Labels.Change)
	return l
}

// document is what the head and the foot print, worked out from the request.
type document struct {
	report bool
	title  string
	number string
	footer []string // footer_lines minus what the head or the barcode already prints
}

// documentOf reads the document's name and number from the request.
//
// The web app (simsim PR #622) sends no title field: the rapport is
// payment.method "report" with its name as footer_lines[0]; a ticket's
// number is receipt_number (its TC/TR number, or the raw transaction id for
// a ticket made before the device had a number) and is repeated as the last
// footer line. So: a footer line equal to the number (or "#"+number) is
// dropped, since it prints under the barcode; a rapport's first remaining
// footer line is its title; a ticket numbered "TR-..." is a refund. An
// explicit document_title wins over all of that.
func documentOf(r Receipt) document {
	d := document{
		report: strings.EqualFold(strings.TrimSpace(r.Payment.Method), "report"),
		number: strings.TrimSpace(r.ReceiptNumber),
		title:  strings.TrimSpace(r.DocumentTitle),
	}
	for _, fl := range r.FooterLines {
		t := strings.TrimSpace(fl)
		if t == "" {
			continue
		}
		if d.number != "" && (t == d.number || t == "#"+d.number) {
			continue
		}
		if d.title != "" && strings.EqualFold(t, d.title) {
			continue
		}
		d.footer = append(d.footer, t)
	}
	if d.title == "" {
		switch {
		case d.report && len(d.footer) > 0:
			d.title, d.footer = d.footer[0], d.footer[1:]
		case d.report:
			d.title = titleReport
		case strings.HasPrefix(strings.ToUpper(d.number), "TR-"):
			d.title = titleRefund
		default:
			d.title = titleSale
		}
	}
	return d
}

// Render serializes a Receipt to an ESC/POS byte stream: init + codepage +
// head (§67.2) + body + foot (+ Code 128 on a ticket) + paper feed + (cut
// OR extra feed) + optional drawer kick. Text is transcoded to CP858.
// Returns ErrInvalidReceipt (wrapped) for malformed input.
func Render(r Receipt, opts RenderOptions) ([]byte, error) {
	if err := validate(r); err != nil {
		return nil, err
	}

	p := page{
		b:      escpos.New().Init().Codepage(escpos.CP858),
		width:  widthsFor(opts.PaperWidthMM).receipt,
		narrow: opts.PaperWidthMM == 58,
		dots:   dotsFor(opts.PaperWidthMM),
	}
	doc := documentOf(r)
	cur := currencyWord(r.Currency)

	// Head: store · document name · number · date and time · terminal ·
	// cashier · shopper.
	p.b.Align(escpos.Center).Bold(true)
	for _, s := range wrap(strings.ToUpper(strings.TrimSpace(r.Store.Name)), p.width) {
		// The LF stays outside the tall scope so the feed advances at
		// normal height.
		p.b.Size(1, storeHeight).TextCP858(s).Size(1, 1).Text("\n")
	}
	p.b.Bold(false)
	for _, s := range []string{r.Store.AddressLine1, r.Store.AddressLine2, r.Store.Phone, r.Store.TaxID} {
		p.text(s)
	}
	p.b.Align(escpos.Left)
	p.rule()

	p.b.Bold(true).CharSpacing(1)
	for _, s := range wrap(strings.ToUpper(doc.title), p.width-p.width/12) {
		p.b.TextCP858(s + "\n")
	}
	p.b.CharSpacing(0).Bold(false)
	p.text(doc.number)
	p.text(paperDateTime(r.IssuedAt, opts.Location))
	if p.narrow {
		p.text(r.Terminal.Label)
		p.text(r.Cashier.Name)
	} else {
		p.text(joinDot(r.Terminal.Label, r.Cashier.Name))
	}
	p.text(r.Shopper)
	p.rule()

	if doc.report {
		p.reportRows(r.Lines, cur)
	} else {
		p.ticketBody(r, labelsFor(r), cur)
	}

	// Foot: the words sent (the thanks; the shopper on the current wire),
	// then on a ticket the Code 128 with its number in clear beneath.
	if len(doc.footer) > 0 {
		p.rule()
		p.b.Align(escpos.Center)
		for _, fl := range doc.footer {
			p.text(fl)
		}
		p.b.Align(escpos.Left)
	}
	if !doc.report && doc.number != "" {
		p.b.Align(escpos.Center)
		p.barcode(doc.number)
		p.b.Align(escpos.Left)
	}

	// Final feed + cut/no-cut + optional drawer.
	//
	// Cut-supported (default for SP-331, TM-T20, etc.) — 4 feed lines to
	// clear the print head + GS V 0 full cut.
	//
	// Cut-unsupported (manual-tear printers) — 8 feed lines (no cut) so
	// the cashier has visible perforation distance to tear the receipt
	// cleanly. 8 lines is roughly the receipt-paper standoff + tear-bar
	// offset on common Algeria-realistic no-cut models.
	if opts.CutSupported {
		p.b.Text("\n\n\n\n").CutFull()
	} else {
		p.b.Text("\n\n\n\n\n\n\n\n")
	}
	if opts.OpenDrawerAfter {
		p.b.DrawerKick()
	}

	return p.b.Bytes(), nil
}

// page is one render in progress: the byte builder plus the roll's geometry.
type page struct {
	b      *escpos.Builder
	width  int  // columns at body size
	narrow bool // 58 mm reflow
	dots   int  // printable dots across
}

// text writes s wrapped to the page width, one LF per row, in whatever
// alignment is current. Blank → nothing.
func (p *page) text(s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	for _, r := range wrap(s, p.width) {
		p.b.TextCP858(r + "\n")
	}
}

// rule is a dashed full-width divider.
func (p *page) rule() {
	p.b.Text(strings.Repeat("-", p.width) + "\n")
}

// row writes label left and value right.
func (p *page) row(label, value string) {
	for _, r := range row(label, value, p.width) {
		p.b.TextCP858(r + "\n")
	}
}

// ticketBody prints a sale or refund ticket's lines, totals and tenders.
func (p *page) ticketBody(r Receipt, words Labels, cur string) {
	for _, l := range r.Lines {
		amount := paperMoney(l.LineTotal, cur)
		detail, extra := lineWorking(l, cur)
		if p.narrow {
			// 58 mm: the name on its own row; "q × prix" and the total
			// share the next when both fit with a two-column gap, else the
			// working takes the row and pushes the total, right-aligned,
			// below.
			p.text(l.Name)
			if runeLen(detail)+2+runeLen(amount) <= p.width {
				p.b.TextCP858(padBetween(detail, amount, p.width) + "\n")
			} else {
				p.text(detail)
				p.b.TextCP858(rightAlign(amount, p.width) + "\n")
			}
		} else {
			p.row(l.Name, amount)
			p.text(detail)
		}
		p.text(extra)
	}

	// Top-level discounts (older clients; the web app now sends none).
	for _, d := range r.Discounts {
		p.row(d.Label, paperMoney(d.Amount, cur))
	}

	p.rule()
	if r.Totals.DiscountTotal != 0 {
		p.row(words.Subtotal, paperMoney(r.Totals.Subtotal, cur))
		p.row(words.Discounts, paperMoney(-math.Abs(r.Totals.DiscountTotal), cur))
	}
	if r.Totals.TaxTotal != 0 {
		p.row(words.Tax, paperMoney(r.Totals.TaxTotal, cur))
	}
	p.total(words.Total, paperMoney(r.Totals.GrandTotal, cur))

	// Tenders. A lone cash leg travels as "cash" and prints the cash word;
	// any other method travels as its own word (legacy rails keep their
	// names, a split reads "Espèces + Électronique") and prints as sent.
	method := strings.TrimSpace(r.Payment.Method)
	if method == "" {
		return
	}
	if method == "cash" {
		method = words.Cash
	}
	p.rule()
	p.row(method, paperMoney(r.Payment.Tendered, cur))
	if r.Payment.Change > 0 {
		p.row(words.Change, paperMoney(r.Payment.Change, cur))
	}
}

// total prints the TOTAL row at the largest size that fits the roll.
func (p *page) total(label, amount string) {
	p.b.Bold(true)
	if cols := p.width / totalScale; runeLen(label)+1+runeLen(amount) <= cols {
		p.b.Size(totalScale, totalScale).TextCP858(padBetween(label, amount, cols))
	} else {
		p.b.Size(1, totalScale).TextCP858(padBetween(label, amount, p.width))
	}
	p.b.Size(1, 1).Bold(false).Text("\n")
}

// reportRows prints the rapport's rows as sent, in order — the CAISSE rows
// and the no-sale count included: label left, value right; where a row
// names a new section, a rule and the section's title in bold.
func (p *page) reportRows(lines []Line, cur string) {
	section := ""
	for i, l := range lines {
		if s := strings.TrimSpace(l.Section); s != "" && s != section {
			if i > 0 {
				p.rule()
			}
			p.b.Bold(true)
			p.text(s)
			p.b.Bold(false)
			section = s
		}
		// An unmarked row prints a bare number: the current wire does not
		// say which rows are counts, and "7 DA" for 7 sales would be false.
		value := paperNumber(l.LineTotal)
		if l.Kind == "money" {
			value = paperMoney(l.LineTotal, cur)
		}
		if l.Bold {
			p.b.Bold(true)
		}
		p.row(l.Name, value)
		if l.Bold {
			p.b.Bold(false)
		}
	}
}

// barcode prints data as a Code 128 (set B), with data in clear beneath.
// The module (narrowest bar) is the widest of 3, 2 or 1 dots at which the
// symbol fits the printable width; a symbol that fits at none, or holds a
// non-ASCII character, is skipped and only the number prints.
func (p *page) barcode(data string) {
	modules := escpos.Code128Modules(data)
	for _, m := range []int{3, 2, 1} {
		if modules*m > p.dots {
			continue
		}
		if cmd, err := escpos.Code128(data, barcodeHeightDots, byte(m), escpos.HRINone); err == nil {
			p.b.Write(cmd)
		}
		break
	}
	p.text(data)
}

// lineWorking returns the working printed under a ticket line, and any
// extra row (the line's discount).
//
// The web app collapses a weighed or denominated line to qty 1, unit_price
// = line_total, and sends its working ("0,342 kg × 380 DA/kg") in
// discount_label; otherwise discount_label is the line's discount
// ("Remise 10 %"). An explicit Detail wins over both.
func lineWorking(l Line, cur string) (detail, extra string) {
	dl := ""
	if l.DiscountLabel != nil {
		dl = strings.TrimSpace(*l.DiscountLabel)
	}
	if d := strings.TrimSpace(l.Detail); d != "" {
		return d, dl
	}
	if dl != "" && l.Qty == 1 && l.UnitPrice == l.LineTotal {
		return dl, ""
	}
	return fmt.Sprintf("%d × %s", l.Qty, paperMoney(l.UnitPrice, cur)), dl
}

// currencyWord is what prints after an amount: the dinar as "DA", as on the
// browser sheet; any other code as sent.
func currencyWord(code string) string {
	switch c := strings.TrimSpace(code); strings.ToUpper(c) {
	case "", "DZD", "DA":
		return "DA"
	default:
		return c
	}
}

// paperMoney mirrors the web's paperMoney: "1 000 DA", "-62 DA", "12,50 DA"
// — whole amounts without decimals, a fractional one with two, thousands
// grouped by a space, an ASCII minus (CP858 has no U+2212).
func paperMoney(v float64, cur string) string {
	return paperNumber(v) + " " + cur
}

// paperNumber is paperMoney without the currency.
func paperNumber(v float64) string {
	cents := int64(math.Round(math.Abs(v) * 100))
	body := group(strconv.FormatInt(cents/100, 10))
	if cents%100 != 0 {
		body += fmt.Sprintf(",%02d", cents%100)
	}
	if v < 0 && cents != 0 {
		return "-" + body
	}
	return body
}

// group puts a space every three digits from the right.
func group(digits string) string {
	var sb strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			sb.WriteByte(' ')
		}
		sb.WriteRune(c)
	}
	return sb.String()
}

// paperDateTime renders "23/09/2026 14:32". A UTC timestamp shows in loc
// (nil → the machine's zone, the shop's clock); any other offset is kept.
func paperDateTime(t time.Time, loc *time.Location) string {
	if _, off := t.Zone(); off == 0 {
		if loc == nil {
			loc = time.Local
		}
		t = t.In(loc)
	}
	return t.Format("02/01/2006 15:04")
}

// joinDot joins the non-blank parts with " · " ("Caisse 02 · Amine B.").
func joinDot(parts ...string) string {
	var kept []string
	for _, s := range parts {
		if s = strings.TrimSpace(s); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, " · ")
}

func runeLen(s string) int { return len([]rune(s)) }

// padBetween puts left and right at the two ends of a width-column row,
// at least one space apart.
func padBetween(left, right string, width int) string {
	pad := width - runeLen(left) - runeLen(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right
}

// row lays out label and value on one row. A label too long for the room
// beside the value wraps, the value staying on the first row; when the
// value leaves under a third of the row, the label takes full rows and the
// value goes right-aligned below.
func row(label, value string, width int) []string {
	label = strings.TrimSpace(label)
	if runeLen(label)+1+runeLen(value) <= width {
		return []string{padBetween(label, value, width)}
	}
	avail := width - runeLen(value) - 1
	if avail < width/3 {
		return append(wrap(label, width), rightAlign(value, width))
	}
	rows := wrap(label, avail)
	out := []string{padBetween(rows[0], value, width)}
	if len(rows) > 1 {
		out = append(out, wrap(strings.Join(rows[1:], " "), width)...)
	}
	return out
}

// wrap breaks s into rows of at most width columns, at spaces where it can
// and mid-word where a word is longer than a row.
func wrap(s string, width int) []string {
	s = strings.TrimSpace(s)
	if width < 1 || runeLen(s) <= width {
		return []string{s}
	}
	var rows []string
	cur := ""
	for _, word := range strings.Fields(s) {
		for runeLen(word) > width {
			if cur != "" {
				rows = append(rows, cur)
				cur = ""
			}
			rw := []rune(word)
			rows = append(rows, string(rw[:width]))
			word = string(rw[width:])
		}
		switch {
		case word == "":
		case cur == "":
			cur = word
		case runeLen(cur)+1+runeLen(word) <= width:
			cur += " " + word
		default:
			rows = append(rows, cur)
			cur = word
		}
	}
	if cur != "" {
		rows = append(rows, cur)
	}
	return rows
}
