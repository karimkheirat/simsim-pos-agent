package receipt

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// -update regenerates the golden file from the current Render output.
// Use with care: re-run the full test suite without -update afterwards
// to confirm the new golden is intentional.
var update = flag.Bool("update", false, "update golden files")

// hamoudReceipt is the M1 hardcoded test receipt from POS_AGENT_SPEC.md §7.
func hamoudReceipt(t *testing.T) Receipt {
	t.Helper()
	issued, err := time.Parse(time.RFC3339, "2026-04-28T14:32:11+01:00")
	if err != nil {
		t.Fatalf("parse issued_at: %v", err)
	}
	return Receipt{
		Store: Store{
			Name:         "Hamoud Boualem - Centre Oran",
			AddressLine1: "12 Rue Larbi Ben M'hidi",
			AddressLine2: "Oran 31000",
			Phone:        "+213 41 ...",
			TaxID:        "NIF/RC line if applicable",
		},
		Terminal:      Terminal{ID: "trm_...", Label: "Caisse 1"},
		Cashier:       Cashier{Name: "Amine Benali"},
		ReceiptNumber: "2026-0428-0001",
		IssuedAt:      issued,
		Currency:      "DZD",
		Lines: []Line{
			{SKU: "HB-COLA-33", Name: "Hamoud Cola 33cl", Qty: 6, UnitPrice: 45, LineTotal: 270},
		},
		Discounts: []Discount{
			{Label: "Remise -5%", Amount: -13.50},
		},
		Totals: Totals{
			Subtotal:      270,
			DiscountTotal: -13.50,
			TaxTotal:      0,
			GrandTotal:    256.50,
		},
		Payment: Payment{
			Method:   "cash",
			Tendered: 300,
			Change:   43.50,
		},
		FooterLines: []string{
			"Merci de votre visite",
			"Conservez ce ticket",
		},
	}
}

// renderGolden is a shared helper: runs Render with the given options,
// compares the output against the named golden file under testdata/.
// Use the -update flag to regenerate (re-run without -update to confirm).
func renderGolden(t *testing.T, r Receipt, opts RenderOptions, goldenName string) {
	t.Helper()
	got, err := Render(r, opts)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	goldenPath := filepath.Join("testdata", goldenName)
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
		t.Errorf("rendered output differs from golden — run `go test -update` to regenerate.\n  got: %d bytes\n want: %d bytes", len(got), len(want))
	}
}

func TestRender_HamoudGolden(t *testing.T) {
	// Pre-A.5a baseline: 80mm + cut. The RenderOptions zero value for
	// PaperWidthMM defaults to 80 (back-compat), but pass explicitly
	// here for clarity — and pin CutSupported: true to lock the
	// pre-A.5a cut-emission behavior the golden was generated against.
	renderGolden(t, hamoudReceipt(t),
		RenderOptions{OpenDrawerAfter: true, PaperWidthMM: 80, CutSupported: true},
		"golden_hamoud_receipt.bin")
}

func TestRender_HamoudGolden_58mm(t *testing.T) {
	// M13 A.5a — 58mm-paper render of the same fixture. Same fixture
	// + drawer + cut, only the column widths shift. Golden file
	// regenerable via `go test -run TestRender_HamoudGolden_58mm -update`.
	renderGolden(t, hamoudReceipt(t),
		RenderOptions{OpenDrawerAfter: true, PaperWidthMM: 58, CutSupported: true},
		"golden_hamoud_receipt_58mm.bin")
}

func TestRender_HamoudGolden_NoCut(t *testing.T) {
	// M13 A.5a — 80mm-paper render with CutSupported=false. Used for
	// manual-tear printers (rare in Algeria-realistic pilot hardware,
	// but the spec mandates the path). Output should contain extra
	// feed lines (8 instead of 4+cut) and NO GS V 0 byte sequence.
	renderGolden(t, hamoudReceipt(t),
		RenderOptions{OpenDrawerAfter: true, PaperWidthMM: 80, CutSupported: false},
		"golden_hamoud_receipt_nocut.bin")
}

func TestRender_LineWidths_80mm(t *testing.T) {
	r := hamoudReceipt(t)
	got, err := Render(r, RenderOptions{PaperWidthMM: 80, CutSupported: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for i, line := range strings.Split(extractText(got), "\n") {
		if len(line) > widths80mm.receipt {
			t.Errorf("output line %d exceeds %d cols (got %d): %q", i, widths80mm.receipt, len(line), line)
		}
	}
}

func TestRender_LineWidths_58mm(t *testing.T) {
	r := hamoudReceipt(t)
	got, err := Render(r, RenderOptions{PaperWidthMM: 58, CutSupported: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for i, line := range strings.Split(extractText(got), "\n") {
		if len(line) > widths58mm.receipt {
			t.Errorf("output line %d exceeds %d cols (got %d): %q", i, widths58mm.receipt, len(line), line)
		}
	}
}

func TestRender_NoCut_EmitsExtraFeed_NotCutBytes(t *testing.T) {
	// CutSupported=false must NOT emit GS V 0 (0x1D 0x56 0x00). The
	// cut bytes appear in CutSupported=true output; their presence in
	// the no-cut output is a regression.
	r := hamoudReceipt(t)
	got, err := Render(r, RenderOptions{PaperWidthMM: 80, CutSupported: false})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	cutBytes := []byte{0x1D, 0x56, 0x00}
	if bytes.Contains(got, cutBytes) {
		t.Errorf("CutSupported=false output contains GS V 0 bytes (cut command) — must not")
	}
	// Sanity: CutSupported=true output DOES contain those bytes.
	withCut, _ := Render(r, RenderOptions{PaperWidthMM: 80, CutSupported: true})
	if !bytes.Contains(withCut, cutBytes) {
		t.Errorf("CutSupported=true output missing GS V 0 bytes — render regression")
	}
}

func TestRender_PaperWidthZero_DefaultsTo80(t *testing.T) {
	// Defence-in-depth: a misconfigured caller passing PaperWidthMM=0
	// (zero value) must produce 80mm output, not panic.
	r := hamoudReceipt(t)
	got, err := Render(r, RenderOptions{CutSupported: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for i, line := range strings.Split(extractText(got), "\n") {
		if len(line) > widths80mm.receipt {
			t.Errorf("output line %d exceeds 80mm width (got %d): %q — zero width should default to 80mm", i, len(line), line)
		}
	}
}

func TestRender_Validation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(r *Receipt)
		wantMsg string
	}{
		{
			name:    "empty store name",
			mutate:  func(r *Receipt) { r.Store.Name = "" },
			wantMsg: "store name",
		},
		{
			name:    "whitespace-only store name",
			mutate:  func(r *Receipt) { r.Store.Name = "   " },
			wantMsg: "store name",
		},
		{
			name:    "no line items",
			mutate:  func(r *Receipt) { r.Lines = nil },
			wantMsg: "line item",
		},
		{
			name:    "missing totals",
			mutate:  func(r *Receipt) { r.Totals = Totals{} },
			wantMsg: "totals",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := hamoudReceipt(t)
			tt.mutate(&r)
			_, err := Render(r, RenderOptions{})
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrInvalidReceipt) {
				t.Errorf("err = %v; want errors.Is(err, ErrInvalidReceipt) == true", err)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %q; want it to contain %q", err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestFormatAmount(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want string
	}{
		{"zero", 0, "0,00"},
		{"positive integer", 270, "270,00"},
		{"positive decimal", 256.5, "256,50"},
		{"negative", -13.5, "-13,50"},
		{"large", 1234567.89, "1234567,89"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAmount(tt.in)
			if got != tt.want {
				t.Errorf("formatAmount(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// extractText decodes ESC/POS bytes to printable text content, dropping
// command sequences. LF (0x0A) is preserved as a line separator.
func extractText(data []byte) string {
	var out []byte
	for i := 0; i < len(data); {
		c := data[i]
		switch c {
		case 0x1B: // ESC
			if i+1 >= len(data) {
				i++
				continue
			}
			switch data[i+1] {
			case 0x40: // ESC @ (Init) — 2 bytes
				i += 2
			case 0x74, 0x45, 0x61: // ESC t / E / a + 1-byte param — 3 bytes
				i += 3
			case 0x70: // ESC p m t1 t2 — 5 bytes
				i += 5
			default:
				i += 2
			}
		case 0x1D: // GS
			if i+1 >= len(data) {
				i++
				continue
			}
			switch data[i+1] {
			case 0x21, 0x56: // GS ! / GS V + 1-byte param — 3 bytes
				i += 3
			default:
				i += 2
			}
		default:
			out = append(out, c)
			i++
		}
	}
	return string(out)
}
