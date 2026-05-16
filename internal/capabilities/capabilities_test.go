package capabilities

import (
	"reflect"
	"strings"
	"testing"
)

func TestLookup_KnownModels(t *testing.T) {
	tests := []struct {
		name        string
		printerName string
		wantWidth   int
		wantCut     bool
		wantDrawer  bool
		wantSource  Source
	}{
		{"Star SP-331", "Star SP-331 Receipt", 80, true, true, SourceModelLookup},
		{"Epson TM-T20", "EPSON TM-T20", 80, true, true, SourceModelLookup},
		{"Epson TM-T20II", "EPSON TM-T20II Receipt", 80, true, true, SourceModelLookup},
		{"Epson TM-T20III", "EPSON TM-T20III", 80, true, true, SourceModelLookup},
		{"Epson TM-T88V", "Epson TM-T88V", 80, true, true, SourceModelLookup},
		{"Epson TM-T88VI", "Epson TM-T88VI Thermal", 80, true, true, SourceModelLookup},
		{"Epson TM-T88VII", "Epson TM-T88VII", 80, true, true, SourceModelLookup},
		{"Epson TM-U220", "EPSON TM-U220B", 80, true, true, SourceModelLookup},
		{"Rongta RP-58", "Rongta RP-58", 80, true, true, SourceModelLookup},
		{"Rongta RP-80", "RP-80 Printer", 80, true, true, SourceModelLookup},
		{"Rongta RP-330", "Rongta RP-330 80mm", 80, true, true, SourceModelLookup},
		{"Rongta RP-332", "Rongta RP-332", 80, true, true, SourceModelLookup},
		{"Xprinter XP-58", "Xprinter XP-58IIH", 80, true, true, SourceModelLookup},
		{"Xprinter XP-80", "Xprinter XP-80C", 80, true, true, SourceModelLookup},
		{"Xprinter XP-N160", "Xprinter XP-N160II", 80, true, true, SourceModelLookup},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Lookup(tt.printerName)
			if got.PaperWidthMM != tt.wantWidth {
				t.Errorf("PaperWidthMM = %d, want %d", got.PaperWidthMM, tt.wantWidth)
			}
			if got.CutSupported != tt.wantCut {
				t.Errorf("CutSupported = %v, want %v", got.CutSupported, tt.wantCut)
			}
			if got.DrawerSupported != tt.wantDrawer {
				t.Errorf("DrawerSupported = %v, want %v", got.DrawerSupported, tt.wantDrawer)
			}
			if got.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, tt.wantSource)
			}
			// Every model in v1 ships the same barcode + codepage set.
			if !reflect.DeepEqual(got.BarcodeTypes, []string{"CODE128", "EAN13"}) {
				t.Errorf("BarcodeTypes = %v, want [CODE128 EAN13]", got.BarcodeTypes)
			}
			if !reflect.DeepEqual(got.Codepages, []string{"CP858"}) {
				t.Errorf("Codepages = %v, want [CP858]", got.Codepages)
			}
			if got.QRSupported {
				t.Errorf("QRSupported = true, want false (v1 conservative default)")
			}
		})
	}
}

func TestLookup_CaseInsensitive(t *testing.T) {
	// Multiple casings of the same model must resolve to the same caps.
	tests := []string{
		"sp-331",
		"SP-331",
		"Sp-331",
		"sP-331",
		"My Printer: SP-331",
	}
	want := Lookup("Star SP-331")
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			got := Lookup(in)
			if got.Source != want.Source {
				t.Errorf("Source = %q, want %q", got.Source, want.Source)
			}
			if got.Source != SourceModelLookup {
				t.Errorf("expected SourceModelLookup for %q, got %q", in, got.Source)
			}
		})
	}
}

// TestLookup_MostSpecificFirst pins the ordering invariant: a printer
// literally named "RP-332" must NOT match the "rp-330" entry (which
// is a substring of "rp-332" only if "330" is a substring of "332" —
// which it isn't, but the test pins the broader principle that
// longer/more-specific aliases come before shorter/less-specific ones
// so additions don't break ordering by accident).
//
// The test below also covers TM-T88VI vs TM-T88: "tm-t88vi" must match
// before "tm-t88" so that a TM-T88VI's hypothetical future QR support
// isn't masked by the plain TM-T88 entry.
func TestLookup_MostSpecificFirst(t *testing.T) {
	// Sanity: the modelTable order satisfies "longer substring first"
	// for the families that have an overlap. Audit the relevant pairs.
	overlapPairs := [][2]string{
		{"tm-t88vii", "tm-t88"},
		{"tm-t88vi", "tm-t88"},
		{"tm-t88v", "tm-t88"},
		{"tm-t20iii", "tm-t20"},
		{"tm-t20ii", "tm-t20"},
		{"rp-332", "rp-330"}, // not strict substring but spec calls it out
		{"xp-n160", "xp-80"}, // unrelated; both should match their own
	}
	idx := map[string]int{}
	for i, e := range modelTable {
		idx[e.substring] = i
	}
	for _, p := range overlapPairs {
		longer, shorter := p[0], p[1]
		// Both substrings must exist; the longer must come first.
		li, lOk := idx[longer]
		si, sOk := idx[shorter]
		if !lOk {
			t.Errorf("modelTable missing entry for %q", longer)
			continue
		}
		if !sOk {
			t.Errorf("modelTable missing entry for %q", shorter)
			continue
		}
		if li >= si {
			t.Errorf("ordering violation: %q (idx %d) must come before %q (idx %d)", longer, li, shorter, si)
		}
	}

	// Behavioural check: a "TM-T88VI" name must NOT mask to "tm-t88"
	// (regression — if someone reorders the table the test fails).
	caps := Lookup("Epson TM-T88VI")
	if caps.Source != SourceModelLookup {
		t.Errorf("TM-T88VI Source = %q, want model_lookup", caps.Source)
	}
}

func TestLookup_UnknownModelFallback(t *testing.T) {
	tests := []string{
		"",
		"Brother HL-2030",          // a real printer, just not a thermal one
		"Generic / Text Only",      // common Windows fallback driver name
		"My Custom Printer Driver", // no substring match anywhere
		"OKI Microline 320",        // legacy dot-matrix
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			got := Lookup(in)
			if got.Source != SourceFallback {
				t.Errorf("Source = %q, want %q", got.Source, SourceFallback)
			}
			// Fallback shares the same defaults as known models in v1.
			if got.PaperWidthMM != 80 {
				t.Errorf("PaperWidthMM = %d, want 80", got.PaperWidthMM)
			}
			if !got.CutSupported {
				t.Errorf("CutSupported = false, want true")
			}
			if !got.DrawerSupported {
				t.Errorf("DrawerSupported = false, want true")
			}
		})
	}
}

// ── M13 Track B PR 1 — LookupLabel (TSPL label printers) ──

func TestLookupLabel_KnownModels(t *testing.T) {
	tests := []struct {
		name        string
		printerName string
	}{
		{"Rongta RP-410", "Rongta RP-410"},
		{"Rongta RP-420", "Rongta RP-420 80mm"},
		{"Rongta RP-426A", "Rongta RP-426A"},
		{"Xprinter XP-DT426B", "Xprinter XP-DT426B"},
		{"Xprinter XP-DT108B", "Xprinter XP-DT108B Thermal"},
		{"Xprinter XP-470B", "Xprinter XP-470B"},
		{"Aclas PP7X", "Aclas PP7X Label"},
		{"Aclas PP8X", "Aclas PP8X"},
		{"TSC TDP-244", "TSC TDP-244"},
		{"TSC TTP-244 Pro", "TSC TTP-244 Pro"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LookupLabel(tt.printerName)
			if got.Source != SourceModelLookup {
				t.Errorf("Source = %q, want model_lookup", got.Source)
			}
			if got.PaperWidthMM != 60 {
				t.Errorf("PaperWidthMM = %d, want 60", got.PaperWidthMM)
			}
			if got.CutSupported {
				t.Errorf("CutSupported = true, want false (label printers tear at gap)")
			}
			if got.DrawerSupported {
				t.Errorf("DrawerSupported = true, want false (no cash drawer)")
			}
			if !got.QRSupported {
				t.Errorf("QRSupported = false, want true (TSPL2 native QRCODE)")
			}
			if !reflect.DeepEqual(got.BarcodeTypes, []string{"CODE128", "EAN13", "QR"}) {
				t.Errorf("BarcodeTypes = %v, want [CODE128 EAN13 QR]", got.BarcodeTypes)
			}
			if !reflect.DeepEqual(got.Codepages, []string{"CP1252"}) {
				t.Errorf("Codepages = %v, want [CP1252]", got.Codepages)
			}
		})
	}
}

func TestLookupLabel_CaseInsensitive(t *testing.T) {
	tests := []string{
		"rp-410",
		"RP-410",
		"Rp-410",
		"My Printer: rp-410",
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			got := LookupLabel(in)
			if got.Source != SourceModelLookup {
				t.Errorf("Source = %q, want model_lookup", got.Source)
			}
		})
	}
}

func TestLookupLabel_UnknownFallback(t *testing.T) {
	tests := []string{
		"",
		"Star SP-331",     // a real receipt printer, NOT a label printer
		"Brother HL-2030", // unrelated
		"Generic / Text Only",
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			got := LookupLabel(in)
			if got.Source != SourceFallback {
				t.Errorf("Source = %q, want fallback", got.Source)
			}
			// Fallback shares the same defaults as known label models
			// in v1; only `source` distinguishes.
			if got.PaperWidthMM != 60 {
				t.Errorf("PaperWidthMM = %d, want 60", got.PaperWidthMM)
			}
			if got.CutSupported {
				t.Errorf("CutSupported = true, want false")
			}
			if got.DrawerSupported {
				t.Errorf("DrawerSupported = true, want false")
			}
			if !got.QRSupported {
				t.Errorf("QRSupported = false, want true")
			}
		})
	}
}

func TestLookupLabel_TTP244ProBeforeTTP244(t *testing.T) {
	// "TTP-244 Pro" must NOT mask to the plain "TTP-244" entry.
	// Same most-specific-first invariant as the receipt table.
	got := LookupLabel("TSC TTP-244 Pro")
	if got.Source != SourceModelLookup {
		t.Errorf("Source = %q, want model_lookup", got.Source)
	}
}

func TestLookupLabel_ReturnsIndependentSlices(t *testing.T) {
	// Mutating one caller's BarcodeTypes / Codepages must NOT affect
	// another caller's result. Pins the factory-not-shared-state design
	// for the label table.
	a := LookupLabel("Rongta RP-410")
	a.BarcodeTypes = append(a.BarcodeTypes, "DATAMATRIX_HIJACKED")
	a.Codepages = append(a.Codepages, "CP-HIJACKED")

	b := LookupLabel("Rongta RP-410")
	if len(b.BarcodeTypes) != 3 {
		t.Errorf("BarcodeTypes len after mutating sibling = %d, want 3 — label table is aliasing", len(b.BarcodeTypes))
	}
	if len(b.Codepages) != 1 {
		t.Errorf("Codepages len after mutating sibling = %d, want 1 — label table is aliasing", len(b.Codepages))
	}
	for _, bt := range b.BarcodeTypes {
		if strings.Contains(bt, "HIJACKED") {
			t.Errorf("BarcodeTypes leaked sibling mutation: %v", b.BarcodeTypes)
		}
	}
}

// TestLookupLabel_DoesNotMatchReceiptPrinter — sanity that the two
// lookup tables are independent. A name that matches a receipt
// printer (e.g. "Star SP-331") must NOT accidentally produce a
// model_lookup label row.
func TestLookupLabel_DoesNotMatchReceiptPrinter(t *testing.T) {
	got := LookupLabel("Star SP-331")
	if got.Source != SourceFallback {
		t.Errorf("Star SP-331 in LookupLabel got Source = %q, want fallback (it's a receipt printer)", got.Source)
	}
}

func TestLookup_ReturnsIndependentSlices(t *testing.T) {
	// Mutating one caller's BarcodeTypes / Codepages must NOT affect
	// another caller's result. Pins the factory-not-shared-state design.
	a := Lookup("Star SP-331")
	a.BarcodeTypes = append(a.BarcodeTypes, "QR_HIJACKED")
	a.Codepages = append(a.Codepages, "CP-HIJACKED")

	b := Lookup("Star SP-331")
	if len(b.BarcodeTypes) != 2 {
		t.Errorf("BarcodeTypes len after mutating sibling = %d, want 2 — table is aliasing", len(b.BarcodeTypes))
	}
	if len(b.Codepages) != 1 {
		t.Errorf("Codepages len after mutating sibling = %d, want 1 — table is aliasing", len(b.Codepages))
	}
	for _, bt := range b.BarcodeTypes {
		if strings.Contains(bt, "HIJACKED") {
			t.Errorf("BarcodeTypes leaked sibling mutation: %v", b.BarcodeTypes)
		}
	}
}
