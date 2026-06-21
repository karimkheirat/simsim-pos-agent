package receipt

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	// Content mirrors the ESC/POS receipt sections.
	for _, want := range []string{"Hamoud Boualem", "Sous-total", "Total"} {
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
