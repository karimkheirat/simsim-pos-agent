package receipt

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/escpos"
)

// The two wire bodies below are what simsim's buildReceiptRequest /
// buildEodRequest send (PR #622, src/lib/pos-agent/__tests__/ticket-wire.test.ts
// fixtures, words from paper-words.fixture.ts). NBSPs in the web's money
// strings arrive as U+00A0.
const wireTicketJSON = `{
  "store": {"name": "Supérette El Amel", "address_line_1": "12 rue Didouche Mourad, Alger", "address_line_2": "", "phone": "Tél. : 023 45 67 89", "tax_id": ""},
  "terminal": {"id": "", "label": "Caisse 02"},
  "cashier": {"name": "Amine B."},
  "receipt_number": "TC-S0417-P2-2026-0312",
  "issued_at": "2026-09-23T13:32:00.000Z",
  "currency": "DZD",
  "lines": [
    {"sku": "", "name": "Lait Candia — demi-écrémé 1 L", "qty": 2, "unit_price": 120, "line_total": 240, "discount_label": null},
    {"sku": "", "name": "Olives noires vrac", "qty": 1, "unit_price": 130, "line_total": 130, "discount_label": "0,342\u00a0kg × 380\u00a0DA/kg"}
  ],
  "discounts": [],
  "totals": {"subtotal": 370, "discount_total": 0, "tax_total": 0, "grand_total": 370},
  "payment": {"method": "cash", "tendered": 400, "change": 30},
  "footer_lines": ["Client K7M2QX · Karim B.", "Merci de votre visite !", "TC-S0417-P2-2026-0312"]
}`

const wireRapportJSON = `{
  "store": {"name": "Supérette El Amel", "address_line_1": "", "address_line_2": "", "phone": "", "tax_id": ""},
  "terminal": {"id": "", "label": "Caisse 02"},
  "cashier": {"name": "Amine B."},
  "receipt_number": "RC-S0417-P2-2026-0214",
  "issued_at": "2026-09-23T17:31:00.000Z",
  "currency": "DZD",
  "lines": [
    {"sku": "", "name": "Transactions", "qty": 1, "unit_price": 7, "line_total": 7, "discount_label": null},
    {"sku": "", "name": "Articles vendus", "qty": 1, "unit_price": 41, "line_total": 41, "discount_label": null},
    {"sku": "", "name": "Total ventes", "qty": 1, "unit_price": 18378, "line_total": 18378, "discount_label": null},
    {"sku": "", "name": "Panier moyen", "qty": 1, "unit_price": 2625, "line_total": 2625, "discount_label": null},
    {"sku": "", "name": "Annulées", "qty": 1, "unit_price": 1, "line_total": 1, "discount_label": null},
    {"sku": "", "name": "Remboursées", "qty": 1, "unit_price": 1, "line_total": 1, "discount_label": null},
    {"sku": "", "name": "Espèces (5)", "qty": 1, "unit_price": 13918, "line_total": 13918, "discount_label": null},
    {"sku": "", "name": "Carnet (1)", "qty": 1, "unit_price": 1000, "line_total": 1000, "discount_label": null},
    {"sku": "", "name": "Fond de caisse", "qty": 1, "unit_price": 10000, "line_total": 10000, "discount_label": null},
    {"sku": "", "name": "Ventes espèces", "qty": 1, "unit_price": 8878, "line_total": 8878, "discount_label": null},
    {"sku": "", "name": "Remboursements", "qty": 1, "unit_price": -2528, "line_total": -2528, "discount_label": null},
    {"sku": "", "name": "Entrées", "qty": 1, "unit_price": 500, "line_total": 500, "discount_label": null},
    {"sku": "", "name": "Sorties", "qty": 1, "unit_price": -1500, "line_total": -1500, "discount_label": null},
    {"sku": "", "name": "Attendu", "qty": 1, "unit_price": 15350, "line_total": 15350, "discount_label": null},
    {"sku": "", "name": "Compté", "qty": 1, "unit_price": 14150, "line_total": 14150, "discount_label": null},
    {"sku": "", "name": "Écart", "qty": 1, "unit_price": -1200, "line_total": -1200, "discount_label": null},
    {"sku": "", "name": "Remise au coffre", "qty": 1, "unit_price": 4150, "line_total": 4150, "discount_label": null},
    {"sku": "", "name": "Fond conservé", "qty": 1, "unit_price": 10000, "line_total": 10000, "discount_label": null},
    {"sku": "", "name": "Ouvertures sans vente", "qty": 1, "unit_price": 2, "line_total": 2, "discount_label": null}
  ],
  "discounts": [],
  "totals": {"subtotal": 18378, "discount_total": 0, "tax_total": 0, "grand_total": 18378},
  "payment": {"method": "report", "tendered": 0, "change": 0},
  "footer_lines": ["RAPPORT DE CAISSE", "RC-S0417-P2-2026-0214"]
}`

// algiers is UTC+1, the shop's clock the tests print in.
var algiers = time.FixedZone("CET", 3600)

func decodeWire(t *testing.T, body string) Receipt {
	t.Helper()
	var r Receipt
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}
	return r
}

func renderText(t *testing.T, r Receipt, mm int) (string, []byte) {
	t.Helper()
	out, err := Render(r, RenderOptions{PaperWidthMM: mm, CutSupported: true, Location: algiers})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return decodeCP858(extractText(out)), out
}

// decodeCP858 maps the few CP858 bytes the fixtures use back to UTF-8 so
// assertions read as the paper does.
func decodeCP858(s string) string {
	back := map[byte]string{0x82: "é", 0x90: "É", 0x8A: "è", 0x9E: "×", 0xFA: "·", 0xD4: "È"}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if u, ok := back[s[i]]; ok {
			sb.WriteString(u)
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

func lines(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

func TestPaper_Ticket80_HeadBodyFoot(t *testing.T) {
	text, raw := renderText(t, decodeWire(t, wireTicketJSON), 80)
	t.Logf("\n%s", text)
	want := []string{
		"SUPÉRETTE EL AMEL",
		"12 rue Didouche Mourad, Alger",
		"Tél. : 023 45 67 89",
		strings.Repeat("-", 42),
		"TICKET DE CAISSE",
		"TC-S0417-P2-2026-0312",
		"23/09/2026 14:32", // 13:32Z at the shop's UTC+1
		"Caisse 02 · Amine B.",
		strings.Repeat("-", 42),
		"Lait Candia - demi-écrémé 1 L       240 DA",
		"2 × 120 DA",
		"Olives noires vrac                  130 DA",
		"0,342 kg × 380 DA/kg",
		strings.Repeat("-", 42),
		"TOTAL          370 DA", // double width: 21 columns
		strings.Repeat("-", 42),
		"Espèces                             400 DA",
		"Monnaie                              30 DA",
		strings.Repeat("-", 42),
		"Client K7M2QX · Karim B.",
		"Merci de votre visite !",
		"TC-S0417-P2-2026-0312",
	}
	got := lines(text)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ticket text:\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The barcode encodes the TC number, 2-dot modules (266 modules = 532
	// of 576 dots), no printer-drawn text (the number prints as our own row).
	bc, _ := escpos.Code128("TC-S0417-P2-2026-0312", 64, 2, escpos.HRINone)
	if !bytes.Contains(raw, bc) {
		t.Errorf("Code 128 of the TC number missing or at the wrong module width")
	}
	// Store name bold + double height; TOTAL bold + double width & height.
	if !bytes.Contains(raw, append(escpos.Size(1, 2), escpos.ToCP858("SUPÉRETTE EL AMEL")...)) {
		t.Errorf("store name is not double height")
	}
	if !bytes.Contains(raw, append(escpos.Size(2, 2), []byte("TOTAL")...)) {
		t.Errorf("TOTAL is not double size")
	}
	if !bytes.Contains(raw, append(escpos.CharSpacing(1), []byte("TICKET DE CAISSE")...)) {
		t.Errorf("document name has no letter spacing")
	}
}

func TestPaper_Ticket58_Reflow(t *testing.T) {
	r := decodeWire(t, wireTicketJSON)
	r.Lines = append(r.Lines, Line{Name: "Fromage", Qty: 1, UnitPrice: 1234.5, LineTotal: 1234.5,
		Detail: "0,450 kg × 2 743,33 DA/kg · promo"})
	text, raw := renderText(t, r, 58)
	t.Logf("\n%s", text)
	got := strings.Join(lines(text), "\n")
	for _, want := range []string{
		// terminal and cashier on their own rows at 58 mm
		"Caisse 02\nAmine B.\n",
		// the name alone, then q × prix sharing a row with the amount
		"Lait Candia - demi-écrémé 1 L\n2 × 120 DA                240 DA\n",
		"Olives noires vrac\n0,342 kg × 380 DA/kg      130 DA\n",
		// a long working pushes the amount, right-aligned, below
		"Fromage\n0,450 kg × 2 743,33 DA/kg ·\npromo\n                     1 234,50 DA\n",
		"TOTAL     370 DA\n", // 16 columns at double width
	} {
		if !strings.Contains(got, want) {
			t.Errorf("58 mm ticket missing %q\n%s", want, got)
		}
	}
	for i, l := range lines(text) {
		if n := len([]rune(l)); n > 32 {
			t.Errorf("row %d is %d columns: %q", i, n, l)
		}
	}
	// 266 modules do not fit 384 dots at 2 → 1-dot modules.
	bc, _ := escpos.Code128("TC-S0417-P2-2026-0312", 64, 1, escpos.HRINone)
	if !bytes.Contains(raw, bc) {
		t.Errorf("58 mm barcode not at 1-dot modules")
	}
}

func TestPaper_RefundTitle_And_WordsFromRequest(t *testing.T) {
	r := decodeWire(t, wireTicketJSON)
	r.ReceiptNumber = "TR-S0417-P2-2026-0007"
	r.FooterLines = []string{"TR-S0417-P2-2026-0007"}
	text, _ := renderText(t, r, 80)
	if !strings.Contains(text, "\nTICKET DE REMBOURSEMENT\n") {
		t.Errorf("TR- ticket not titled as a refund:\n%s", text)
	}

	// Words sent by the web app win over the French defaults.
	r.DocumentTitle = "RECEIPT"
	r.Shopper = "Client K7M2QX · Karim B."
	r.Labels = &Labels{Total: "TOTAL DUE", Cash: "Cash", Change: "Change"}
	text, _ = renderText(t, r, 80)
	for _, want := range []string{"\nRECEIPT\n", "\nClient K7M2QX · Karim B.\n" + strings.Repeat("-", 42), "Cash ", "Change ", "TOTAL DUE"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Monnaie") || strings.Contains(text, "Espèces") {
		t.Errorf("French default printed although the words were sent:\n%s", text)
	}
}

func TestPaper_TenderWords_AsSent(t *testing.T) {
	r := decodeWire(t, wireTicketJSON)
	r.Payment = Payment{Method: "Espèces + Électronique", Tendered: 400, Change: 30}
	text, _ := renderText(t, r, 80)
	if !strings.Contains(text, "Espèces + ") {
		t.Errorf("split tender not printed as sent:\n%s", text)
	}
	if !strings.Contains(text, "Monnaie ") {
		t.Errorf("change missing on a split with cash:\n%s", text)
	}
	r.Payment = Payment{Method: "BaridiMob", Tendered: 370}
	text, _ = renderText(t, r, 80)
	if !strings.Contains(text, "BaridiMob") || strings.Contains(text, "Monnaie") {
		t.Errorf("legacy tender: want its name and no change row:\n%s", text)
	}
}

func TestPaper_LineDiscount(t *testing.T) {
	r := decodeWire(t, wireTicketJSON)
	lbl := "Remise 10 %"
	r.Lines = []Line{{Name: "Café", Qty: 1, UnitPrice: 200, LineTotal: 180, DiscountLabel: &lbl}}
	r.Totals = Totals{Subtotal: 180, GrandTotal: 180}
	text, _ := renderText(t, r, 80)
	if !strings.Contains(text, "\n1 × 200 DA\nRemise 10 %\n") {
		t.Errorf("line discount: want the working then the discount:\n%s", text)
	}
}

func TestPaper_Rapport_AsSent(t *testing.T) {
	text, raw := renderText(t, decodeWire(t, wireRapportJSON), 80)
	t.Logf("\n%s", text)
	got := strings.Join(lines(text), "\n")
	for _, want := range []string{
		"\nRAPPORT DE CAISSE\nRC-S0417-P2-2026-0214\n23/09/2026 18:31\nCaisse 02 · Amine B.\n",
		"\nTransactions                             7\n",
		"\nFond de caisse                      10 000\n",
		"\nRemboursements                      -2 528\n",
		"\nÉcart                               -1 200\n",
		"\nRemise au coffre                     4 150\n",
		"\nFond conservé                       10 000\n",
		"\nOuvertures sans vente                    2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rapport missing %q\n%s", want, got)
		}
	}
	// A rapport has no TOTAL, no tender block, no barcode; its title and
	// number are not repeated at the foot.
	for _, bad := range []string{"TOTAL", "report", "Monnaie"} {
		if strings.Contains(got, bad) {
			t.Errorf("rapport prints %q:\n%s", bad, got)
		}
	}
	if strings.Count(got, "RAPPORT DE CAISSE") != 1 || strings.Count(got, "RC-S0417-P2-2026-0214") != 1 {
		t.Errorf("title or number repeated:\n%s", got)
	}
	if bytes.Contains(raw, []byte{0x1D, 0x6B}) {
		t.Errorf("rapport carries a barcode")
	}
}

func TestPaper_Rapport_SectionsKindsBold(t *testing.T) {
	r := decodeWire(t, wireRapportJSON)
	r.Lines = []Line{
		{Name: "Transactions", LineTotal: 7, Kind: "count", Section: "VENTES"},
		{Name: "Total ventes", LineTotal: 18378, Kind: "money", Section: "VENTES"},
		{Name: "Fond de caisse", LineTotal: 10000, Kind: "money", Section: "CAISSE"},
		{Name: "Écart", LineTotal: -1200, Kind: "money", Section: "CAISSE", Bold: true},
	}
	text, raw := renderText(t, r, 80)
	got := strings.Join(lines(text), "\n")
	for _, want := range []string{
		"\nVENTES\nTransactions                             7\nTotal ventes                     18 378 DA\n" + strings.Repeat("-", 42) + "\nCAISSE\n",
		"Écart                            -1 200 DA",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n%s", want, got)
		}
	}
	if !bytes.Contains(raw, append(escpos.Bold(true), escpos.ToCP858("Écart")...)) {
		t.Errorf("Écart not bold")
	}
}

func TestPaper_OldClient_StillPrints(t *testing.T) {
	// The pre-SIM-170 web wire: raw id number repeated as "#id" in the
	// foot, an old rapport titled in its footer.
	r := hamoudReceipt(t)
	r.ReceiptNumber = "4bd381ce-93aa-4e01-b1c2-7d90e2a41f55"
	r.FooterLines = []string{"Merci de votre visite !", "#4bd381ce-93aa-4e01-b1c2-7d90e2a41f55"}
	text, raw := renderText(t, r, 80)
	if strings.Count(text, "4bd381ce-93aa-4e01-b1c2-7d90e2a41f55") != 2 { // head + under the barcode
		t.Errorf("id should print in the head and under the barcode only:\n%s", text)
	}
	// 36 chars = 431 modules: 1-dot modules on 80 mm.
	bc, _ := escpos.Code128("4bd381ce-93aa-4e01-b1c2-7d90e2a41f55", 64, 1, escpos.HRINone)
	if !bytes.Contains(raw, bc) {
		t.Errorf("raw-id barcode missing")
	}
	// On 58 mm the id's symbol is wider than the roll: no barcode, the id
	// still prints in clear.
	_, raw58 := renderText(t, r, 58)
	if bytes.Contains(raw58, []byte{0x1D, 0x6B}) {
		t.Errorf("a symbol wider than 384 dots was sent")
	}

	rep := hamoudReceipt(t)
	rep.Payment = Payment{Method: "report"}
	rep.ReceiptNumber = "EOD-sess-1"
	rep.FooterLines = []string{"RAPPORT DE FIN DE JOURNEE", "Session: #sess-1"}
	text, _ = renderText(t, rep, 80)
	if !strings.Contains(text, "\nRAPPORT DE FIN DE JOURNEE\nEOD-sess-1\n") || !strings.Contains(text, "Session: #sess-1") {
		t.Errorf("old rapport head/foot:\n%s", text)
	}
}

func TestPaperMoney(t *testing.T) {
	for _, tt := range []struct {
		in   float64
		want string
	}{
		{0, "0 DA"}, {370, "370 DA"}, {1000, "1 000 DA"}, {-62, "-62 DA"},
		{12.5, "12,50 DA"}, {1234567.891, "1 234 567,89 DA"}, {-0.001, "0 DA"},
	} {
		if got := paperMoney(tt.in, "DA"); got != tt.want {
			t.Errorf("paperMoney(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPaperDateTime_Zones(t *testing.T) {
	utc := time.Date(2026, 9, 23, 13, 32, 0, 0, time.UTC)
	if got := paperDateTime(utc, algiers); got != "23/09/2026 14:32" {
		t.Errorf("UTC → shop clock: %q", got)
	}
	sent := time.Date(2026, 9, 23, 14, 32, 0, 0, time.FixedZone("", 3600))
	if got := paperDateTime(sent, time.UTC); got != "23/09/2026 14:32" {
		t.Errorf("an explicit offset is kept: %q", got)
	}
}
