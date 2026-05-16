package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
)

// newTestServerTwo wires both a receipt printer and a label printer.
// Mirrors newTestServer but exercises the NewTwo constructor introduced
// in M13 Track B PR 1. Either receipt or label may be nil to test the
// missing-printer branches of /capabilities + /health.
func newTestServerTwo(t *testing.T, receipt, label printer.Printer) (*Server, *httptest.Server) {
	t.Helper()
	cfg := Config{
		ListenAddr:               "127.0.0.1:0",
		AllowedOrigins:           []string{"https://allowed.example"},
		Version:                  "test-1.0.0",
		Logger:                   discardLogger(),
		Secrets:                  pairedSecrets(),
		IdempotencyTTL:           time.Hour,
		IdempotencySweepInterval: time.Hour,
	}
	srv, err := NewTwo(cfg, receipt, label)
	if err != nil {
		t.Fatalf("NewTwo: %v", err)
	}
	ts := httptest.NewServer(srv.handler)
	t.Cleanup(ts.Close)
	return srv, ts
}

// ── /health: Label sibling ────────────────────────────────────────────

func TestHealth_LabelKey_Absent_WhenNoLabelPrinter(t *testing.T) {
	// Single-printer config (back-compat): /health must NOT carry a
	// `label` key. omitempty drops the nil pointer.
	_, ts := newTestServerTwo(t, &fakePrinter{name: "SP-331", reachable: true}, nil)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["label"]; ok {
		t.Errorf("label key present in single-printer /health response: %s", raw)
	}
}

func TestHealth_LabelKey_Present_WhenLabelPrinterConfigured(t *testing.T) {
	receipt := &fakePrinter{name: "SP-331", reachable: true}
	label := &fakePrinter{name: "Xprinter XP-DT426B", reachable: true}
	_, ts := newTestServerTwo(t, receipt, label)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	lbl, ok := body["label"].(map[string]any)
	if !ok {
		t.Fatalf("label key missing or wrong type: %v", body["label"])
	}
	if lbl["configured"] != true {
		t.Errorf("label.configured = %v, want true", lbl["configured"])
	}
	if lbl["reachable"] != true {
		t.Errorf("label.reachable = %v, want true", lbl["reachable"])
	}
	if lbl["name"] != "Xprinter XP-DT426B" {
		t.Errorf("label.name = %v, want Xprinter XP-DT426B", lbl["name"])
	}
}

// ── /capabilities: additive label sibling ─────────────────────────────

func TestCapabilities_LabelKey_NullWhenNoLabelPrinter(t *testing.T) {
	receipt := &fakePrinter{name: "Star SP-331", reachable: true}
	_, ts := newTestServerTwo(t, receipt, nil)
	token := mintTestJWT(t, nil)
	resp := callCaps(t, ts, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	_, data, _, _ := decodeEnvelope(t, resp.Body)
	if data == nil {
		t.Fatal("decodeEnvelope returned nil data")
	}
	// `label` key MUST be present (additive contract) and null.
	if v, ok := data["label"]; !ok {
		t.Errorf("label key missing in /capabilities response")
	} else if v != nil {
		t.Errorf("label = %v, want null", v)
	}
	// Top-level receipt fields stay intact.
	if data["paper_width_mm"].(float64) != 80 {
		t.Errorf("paper_width_mm = %v, want 80 (receipt at top level)", data["paper_width_mm"])
	}
}

func TestCapabilities_LabelKey_PopulatedWhenLabelPrinterConfigured(t *testing.T) {
	receipt := &fakePrinter{name: "Star SP-331", reachable: true}
	label := &fakePrinter{name: "Xprinter XP-DT426B", reachable: true}
	_, ts := newTestServerTwo(t, receipt, label)
	token := mintTestJWT(t, nil)
	resp := callCaps(t, ts, token)
	defer resp.Body.Close()
	_, data, _, _ := decodeEnvelope(t, resp.Body)

	labelRow, ok := data["label"].(map[string]any)
	if !ok {
		t.Fatalf("label = %v (%T), want object", data["label"], data["label"])
	}
	if labelRow["paper_width_mm"].(float64) != 60 {
		t.Errorf("label.paper_width_mm = %v, want 60", labelRow["paper_width_mm"])
	}
	if labelRow["cut_supported"] != false {
		t.Errorf("label.cut_supported = %v, want false", labelRow["cut_supported"])
	}
	if labelRow["drawer_supported"] != false {
		t.Errorf("label.drawer_supported = %v, want false", labelRow["drawer_supported"])
	}
	if labelRow["qr_supported"] != true {
		t.Errorf("label.qr_supported = %v, want true", labelRow["qr_supported"])
	}
	if labelRow["source"] != "model_lookup" {
		t.Errorf("label.source = %v, want model_lookup", labelRow["source"])
	}
	if labelRow["tspl_dialect"] != "standard" {
		t.Errorf("label.tspl_dialect = %v, want standard", labelRow["tspl_dialect"])
	}
}

func TestCapabilities_LabelDialect_RongtaSurfaced(t *testing.T) {
	receipt := &fakePrinter{name: "Star SP-331", reachable: true}
	label := &fakePrinter{name: "Rongta RP-410", reachable: true}
	cfg := Config{
		ListenAddr:     "127.0.0.1:0",
		AllowedOrigins: []string{"https://allowed.example"},
		Version:        "test-1.0.0",
		Logger:         discardLogger(),
		Secrets:        pairedSecrets(),
		IdempotencyTTL: time.Hour,
		TSPLDialect:    "rongta",
	}
	srv, err := NewTwo(cfg, receipt, label)
	if err != nil {
		t.Fatalf("NewTwo: %v", err)
	}
	ts := httptest.NewServer(srv.handler)
	t.Cleanup(ts.Close)

	token := mintTestJWT(t, nil)
	resp := callCaps(t, ts, token)
	defer resp.Body.Close()
	_, data, _, _ := decodeEnvelope(t, resp.Body)
	labelRow := data["label"].(map[string]any)
	if labelRow["tspl_dialect"] != "rongta" {
		t.Errorf("label.tspl_dialect = %v, want rongta", labelRow["tspl_dialect"])
	}
}

func TestCapabilities_BackCompatTopLevelShape(t *testing.T) {
	// A pre-Track-B client unmarshals the response directly into
	// capabilities.PrinterCapabilities — those clients ignore unknown
	// keys (standard json.Unmarshal). Pin that the top-level shape
	// still parses correctly (i.e. we didn't move the receipt fields
	// into a sibling).
	receipt := &fakePrinter{name: "Star SP-331", reachable: true}
	_, ts := newTestServerTwo(t, receipt, nil)
	token := mintTestJWT(t, nil)
	resp := callCaps(t, ts, token)
	defer resp.Body.Close()

	type preTrackBCaps struct {
		PaperWidthMM    int      `json:"paper_width_mm"`
		CutSupported    bool     `json:"cut_supported"`
		DrawerSupported bool     `json:"drawer_supported"`
		BarcodeTypes    []string `json:"barcode_types"`
		Codepages       []string `json:"codepages"`
		QRSupported     bool     `json:"qr_supported"`
		Source          string   `json:"source"`
	}
	var env struct {
		OK   bool          `json:"ok"`
		Data preTrackBCaps `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("pre-track-B shape decode failed: %v", err)
	}
	if !env.OK {
		t.Errorf("ok = false")
	}
	if env.Data.PaperWidthMM != 80 {
		t.Errorf("Data.PaperWidthMM = %d, want 80 (pre-track-B unmarshal)", env.Data.PaperWidthMM)
	}
	if env.Data.Source != "model_lookup" {
		t.Errorf("Data.Source = %q, want model_lookup", env.Data.Source)
	}
}

// ── printerForIntent ──────────────────────────────────────────────────

func TestPrinterForIntent_Receipt(t *testing.T) {
	receipt := &fakePrinter{name: "SP-331", reachable: true}
	srv, _ := newTestServerTwo(t, receipt, nil)

	for _, intent := range []string{"receipt", "", "unknown-defaults-receipt"} {
		t.Run(intent, func(t *testing.T) {
			p, status, code, _ := srv.printerForIntent(intent)
			if status != 0 {
				t.Errorf("status = %d, want 0 (printer present)", status)
			}
			if code != "" {
				t.Errorf("code = %q, want empty", code)
			}
			if p == nil || p.Name() != "SP-331" {
				t.Errorf("printer = %v, want SP-331", p)
			}
		})
	}
}

func TestPrinterForIntent_Label_Missing_503(t *testing.T) {
	receipt := &fakePrinter{name: "SP-331", reachable: true}
	srv, _ := newTestServerTwo(t, receipt, nil)
	p, status, code, _ := srv.printerForIntent("label")
	if p != nil {
		t.Errorf("printer = %v, want nil", p)
	}
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if code != CodeNoLabelPrinterConfigured {
		t.Errorf("code = %q, want %q", code, CodeNoLabelPrinterConfigured)
	}
}

func TestPrinterForIntent_Label_Present(t *testing.T) {
	receipt := &fakePrinter{name: "SP-331", reachable: true}
	label := &fakePrinter{name: "RP-410", reachable: true}
	srv, _ := newTestServerTwo(t, receipt, label)
	p, status, code, _ := srv.printerForIntent("label")
	if status != 0 {
		t.Errorf("status = %d, want 0", status)
	}
	if code != "" {
		t.Errorf("code = %q, want empty", code)
	}
	if p == nil || p.Name() != "RP-410" {
		t.Errorf("printer = %v, want RP-410", p)
	}
}

func TestPrinterForIntent_Receipt_Missing_503(t *testing.T) {
	srv, _ := newTestServerTwo(t, nil, &fakePrinter{name: "RP-410", reachable: true})
	p, status, code, _ := srv.printerForIntent("receipt")
	if p != nil {
		t.Errorf("printer = %v, want nil", p)
	}
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if code != CodePrinterNotConfigured {
		t.Errorf("code = %q, want %q", code, CodePrinterNotConfigured)
	}
}

// ── /test-print: intent query param ───────────────────────────────────

func TestTestPrint_NoIntent_DefaultsToReceipt(t *testing.T) {
	// Existing behaviour: POST /test-print with no query param prints
	// to the receipt printer. Pin that the default path is unchanged
	// (back-compat for cashier UI).
	fp := &fakePrinter{name: "SP-331", reachable: true}
	_, ts := newTestServerTwo(t, fp, nil)
	resp := authPost(t, ts, "/test-print", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(fp.Printed()) != 1 {
		t.Errorf("receipt printer received %d jobs, want 1", len(fp.Printed()))
	}
}

func TestTestPrint_IntentReceipt_Explicit(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true}
	_, ts := newTestServerTwo(t, fp, nil)
	resp := authPost(t, ts, "/test-print?intent=receipt", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(fp.Printed()) != 1 {
		t.Errorf("receipt printer received %d jobs, want 1", len(fp.Printed()))
	}
}

func TestTestPrint_IntentLabel_NotImplementedInPR1(t *testing.T) {
	// PR 1 reserves intent=label for PR 2; until then it returns 501.
	// Pin the not-implemented surface so PR 2's wire shape can land
	// without inventing a new endpoint.
	receipt := &fakePrinter{name: "SP-331", reachable: true}
	label := &fakePrinter{name: "RP-410", reachable: true}
	_, ts := newTestServerTwo(t, receipt, label)
	resp := authPost(t, ts, "/test-print?intent=label", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", resp.StatusCode)
	}
	// Receipt printer must NOT have been called.
	if len(receipt.Printed()) != 0 {
		t.Errorf("receipt printer received %d jobs on intent=label; want 0", len(receipt.Printed()))
	}
	if len(label.Printed()) != 0 {
		t.Errorf("label printer received %d jobs on intent=label; want 0 (PR 1 stops at 501)", len(label.Printed()))
	}
}
