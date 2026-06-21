package api

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

// tsplLanguageConfig is newTestServer's config with the receipt language
// flipped to TSPL — used to prove the handler branches on
// receipt_printer_language.
func tsplLanguageConfig() Config {
	return Config{
		ListenAddr:               "127.0.0.1:0",
		AllowedOrigins:           []string{"https://allowed.example"},
		Version:                  "test-1.0.0",
		Logger:                   discardLogger(),
		Secrets:                  pairedSecrets(),
		IdempotencyTTL:           time.Hour,
		IdempotencySweepInterval: time.Hour,
		ReceiptPrinterLanguage:   "tspl",
	}
}

// TestTestPrint_TSPLLanguage — with receipt_printer_language=tspl, the
// /test-print handler must emit a TSPL stream to the printer (CLS … PRINT
// 1,1, continuous-media GAP 0, no ESC/POS bytes).
func TestTestPrint_TSPLLanguage(t *testing.T) {
	fp := &fakePrinter{name: "GP-3150TN", reachable: true}
	_, ts := newTestServerWith(t, fp, tsplLanguageConfig())

	resp := authPost(t, ts, "/test-print", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	jobs := fp.Printed()
	if len(jobs) != 1 {
		t.Fatalf("printed %d jobs, want 1", len(jobs))
	}
	data := jobs[0].data
	s := string(data)
	if !strings.HasPrefix(s, "CLS\r\n") {
		t.Errorf("TSPL stream should start with CLS, got %q", s[:min(16, len(s))])
	}
	if !strings.Contains(s, "PRINT 1,1") {
		t.Error("TSPL stream missing PRINT 1,1")
	}
	if !strings.Contains(s, "GAP 0 mm,0 mm") {
		t.Error("TSPL stream missing GAP 0 (continuous media)")
	}
	if bytes.ContainsRune(data, 0x1B) || bytes.ContainsRune(data, 0x1D) {
		t.Error("TSPL stream contains an ESC/POS control byte (0x1B/0x1D)")
	}
}

// TestTestPrint_DefaultESCPOS — the default config (no language set →
// "escpos") must still emit ESC/POS, starting with ESC @ (1B 40). Guards
// against the TSPL branch leaking into the default path.
func TestTestPrint_DefaultESCPOS(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true}
	_, ts := newTestServer(t, fp)

	resp := authPost(t, ts, "/test-print", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	jobs := fp.Printed()
	if len(jobs) != 1 {
		t.Fatalf("printed %d jobs, want 1", len(jobs))
	}
	data := jobs[0].data
	if len(data) < 2 || data[0] != 0x1B || data[1] != 0x40 {
		got := data
		if len(got) > 4 {
			got = got[:4]
		}
		t.Errorf("ESC/POS stream should start with ESC @ (1B 40), got % X", got)
	}
}
