package receipt

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/tspl"
)

// tsplOpts returns explicit, stable render options for the given paper
// width so the golden is independent of any future default drift. The
// width-dots/dpi/font values mirror the agent's defaults and are the
// knobs to recalibrate against the real GP-3150TN.
func tsplOpts(paperMM int) TSPLReceiptOptions {
	widthDots := 576
	if paperMM == 58 {
		widthDots = 384
	}
	return TSPLReceiptOptions{
		PaperWidthMM: paperMM,
		WidthDots:    widthDots,
		DPI:          203,
		Font:         tspl.Font2,
		Density:      8,
		Speed:        4,
		Dialect:      tspl.DialectStandard,
	}
}

// TestRenderTSPL_HamoudGolden golden-tests the TSPL render of the shared
// hamoudReceipt fixture (defined in render_test.go). Reuses the package
// -update flag: `go test -run TestRenderTSPL_HamoudGolden -update`.
func TestRenderTSPL_HamoudGolden(t *testing.T) {
	got, err := RenderTSPL(hamoudReceipt(t), tsplOpts(80))
	if err != nil {
		t.Fatalf("RenderTSPL: %v", err)
	}
	goldenPath := filepath.Join("testdata", "golden_tspl_receipt_hamoud.bin")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden: %s (%d bytes)", goldenPath, len(got))
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run `go test -update` to create)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("TSPL output differs from golden — run `go test -update` to regenerate.\n  got: %d bytes\n want: %d bytes", len(got), len(want))
	}
}

// TestRenderTSPL_Structure asserts the language-level invariants without
// pinning exact bytes: TSPL pre-amble/post-amble, continuous-media GAP 0,
// CP1252 codepage, mirrored receipt content, and the absence of any
// ESC/POS control bytes (proving it's a real language switch).
func TestRenderTSPL_Structure(t *testing.T) {
	got, err := RenderTSPL(hamoudReceipt(t), tsplOpts(80))
	if err != nil {
		t.Fatalf("RenderTSPL: %v", err)
	}
	s := string(got)

	if !strings.HasPrefix(s, "CLS\r\n") {
		t.Errorf("should start with CLS, got %q", s[:min(16, len(s))])
	}
	if !strings.HasSuffix(s, "PRINT 1,1\r\n") {
		t.Error("should end with PRINT 1,1")
	}
	for _, want := range []string{"SIZE 80 mm,", "GAP 0 mm,0 mm", "DIRECTION 1", "CODEPAGE 1252"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing pre-amble command %q", want)
		}
	}
	// Content is the paper's (paper.go), as on ESC/POS: the store in
	// capitals, the document's name, the TOTAL.
	for _, want := range []string{"HAMOUD BOUALEM", "TICKET DE CAISSE", "TOTAL"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing receipt content %q", want)
		}
	}
	// TSPL is ASCII command lines — no ESC/POS control bytes may appear.
	if bytes.ContainsRune(got, 0x1B) || bytes.ContainsRune(got, 0x1D) {
		t.Error("TSPL stream contains an ESC/POS control byte (0x1B/0x1D)")
	}
}

// TestRenderTSPL_58mmSize confirms the SIZE width tracks PaperWidthMM.
func TestRenderTSPL_58mmSize(t *testing.T) {
	got, err := RenderTSPL(hamoudReceipt(t), tsplOpts(58))
	if err != nil {
		t.Fatalf("RenderTSPL: %v", err)
	}
	if !strings.Contains(string(got), "SIZE 58 mm,") {
		t.Error("58mm render should emit SIZE 58 mm,")
	}
}

// TestRenderTSPL_InvalidReceipt confirms the TSPL path enforces the same
// validation as the ESC/POS path.
func TestRenderTSPL_InvalidReceipt(t *testing.T) {
	if _, err := RenderTSPL(Receipt{}, tsplOpts(80)); !errors.Is(err, ErrInvalidReceipt) {
		t.Errorf("empty receipt err = %v, want ErrInvalidReceipt", err)
	}
}

// A line's working (the web app's unit of sale, "Pack · 6 × 2") prints under the line on a TSPL printer, as it
// does on the ESC/POS paper; a line without one prints exactly as before.
func TestRenderTSPL_LineDetail(t *testing.T) {
	r := hamoudReceipt(t)
	r.Lines[0].Detail = "Pack · 6 × 2"
	got, err := RenderTSPL(r, tsplOpts(80))
	if err != nil {
		t.Fatalf("RenderTSPL: %v", err)
	}
	if !bytes.Contains(got, []byte("Pack")) {
		t.Fatalf("the line's detail is not on the label")
	}
	plain, err := RenderTSPL(hamoudReceipt(t), tsplOpts(80))
	if err != nil {
		t.Fatalf("RenderTSPL: %v", err)
	}
	if bytes.Contains(plain, []byte("Pack ·")) {
		t.Fatalf("a line without detail must print as before")
	}
}

// rachidTicket is a ticket as the web app sends it for a weighed line: the
// line collapsed to qty 1, its working in discount_label, currency "DZD".
func rachidTicket() Receipt {
	work := "0,750 kg × 150,00 DA/kg"
	return Receipt{
		Store:         Store{Name: "Mini Shop Chemouma", AddressLine1: "Mostaganem", Phone: "Tél. : 0550 12 34 56"},
		Terminal:      Terminal{Label: "Caisse 1"},
		Cashier:       Cashier{Name: "Rachid"},
		ReceiptNumber: "TC-S0417-P1-2026-0312",
		IssuedAt:      time.Date(2026, 11, 22, 11, 42, 0, 0, time.FixedZone("CET", 3600)),
		Currency:      "DZD",
		Lines: []Line{
			{Name: "LENTILLES VRAC", Qty: 1, UnitPrice: 112.5, LineTotal: 112.5, DiscountLabel: &work},
			{Name: "Lait Candia 1 L", Qty: 2, UnitPrice: 120, LineTotal: 240},
		},
		Totals:      Totals{Subtotal: 352.5, GrandTotal: 352.5},
		Payment:     Payment{Method: "cash", Tendered: 500, Change: 147.5},
		FooterLines: []string{"Merci de votre visite !", "TC-S0417-P1-2026-0312"},
	}
}

// The three things Rachid's printer did not print (v0.3.16): the weighed
// line's working, "DA" rather than "DZD", and the ticket's barcode.
func TestRenderTSPL_RachidTicket(t *testing.T) {
	got, err := RenderTSPL(rachidTicket(), tsplOpts(80))
	if err != nil {
		t.Fatalf("RenderTSPL: %v", err)
	}
	s := string(got)
	// CP1252 "×" is 0xD7.
	if !strings.Contains(s, "0,750 kg \xd7 150,00 DA/kg") {
		t.Error("the weighed line's working is not printed")
	}
	if strings.Contains(s, "DZD") {
		t.Error("an amount still prints DZD")
	}
	if !strings.Contains(s, "352,50 DA") {
		t.Error("the total does not print in DA")
	}
	// The barcode: a bitmap, then the number in clear beneath it, once.
	if !strings.Contains(s, "BITMAP ") {
		t.Fatal("no barcode bitmap")
	}
	if strings.Count(s, "TC-S0417-P1-2026-0312") != 2 {
		t.Errorf("the number prints in the head and once under the barcode; got %d", strings.Count(s, "TC-S0417-P1-2026-0312"))
	}
	if strings.Index(s, "BITMAP ") > strings.LastIndex(s, "TC-S0417-P1-2026-0312") {
		t.Error("the number in clear belongs under the barcode")
	}
}

// A rapport de caisse carries no barcode.
func TestRenderTSPL_RapportHasNoBarcode(t *testing.T) {
	r := rachidTicket()
	r.Payment = Payment{Method: "report"}
	r.FooterLines = []string{"RAPPORT DE CAISSE"}
	got, err := RenderTSPL(r, tsplOpts(80))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "BITMAP ") {
		t.Error("a rapport must not print a barcode")
	}
}

// The shopper QR prints only when the web app sends it (it is switched off
// there until the shopper app ships), between the thanks and the ticket's
// barcode, on both printer kinds; never on a rapport.
func TestShopperInvite_OnlyWhenSent(t *testing.T) {
	r := rachidTicket()
	plain, err := RenderTSPL(r, tsplOpts(80))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(plain), "BITMAP ") != 1 {
		t.Fatalf("without an invite only the barcode is a picture")
	}
	r.ShopperInvite = &ShopperInvite{URL: "https://app.molshi.com/shopper", Text: "Vos tickets sur l'appli Molshi"}
	got, err := RenderTSPL(r, tsplOpts(80))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Count(s, "BITMAP ") != 2 {
		t.Fatalf("want the QR and the barcode, got %d pictures", strings.Count(s, "BITMAP "))
	}
	if i, j := strings.Index(s, "Vos tickets"), strings.Index(s, "Merci"); i < j {
		t.Error("the invitation comes after the thanks")
	}
	esc, err := Render(r, RenderOptions{PaperWidthMM: 80})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(esc, []byte{0x1D, 0x76, 0x30, 0x00}) {
		t.Error("ESC/POS prints the QR as a raster picture")
	}
	r.Payment = Payment{Method: "report"}
	rep, _ := RenderTSPL(r, tsplOpts(80))
	if strings.Contains(string(rep), "BITMAP ") {
		t.Error("a rapport carries no QR")
	}
}

func TestQRImage_WidthIsWholeBytes(t *testing.T) {
	img, err := qrImage("https://app.molshi.com/shopper", shopperQRDots)
	if err != nil {
		t.Fatal(err)
	}
	if w := img.Bounds().Dx(); w%8 != 0 || w > shopperQRDots+8 {
		t.Errorf("width %d", w)
	}
}
