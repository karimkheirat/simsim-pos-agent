package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// callCaps issues an authenticated GET /capabilities. Centralizes the
// `http.NewRequest + Authorization + DefaultClient.Do` boilerplate.
func callCaps(t *testing.T, ts *httptest.Server, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/capabilities", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// ----- GET /capabilities -----

func TestCapabilities_NoPrinter_503(t *testing.T) {
	// printer=nil → 503 PRINTER_NOT_CONFIGURED. JWT still required;
	// the path is "valid JWT, no printer" — handler short-circuits.
	_, ts := newTestServer(t, nil)
	token := mintTestJWT(t, nil)

	resp := callCaps(t, ts, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	ok, _, code, _ := decodeEnvelope(t, resp.Body)
	if ok {
		t.Errorf("envelope ok = true, want false")
	}
	if code != CodePrinterNotConfigured {
		t.Errorf("code = %q, want %q", code, CodePrinterNotConfigured)
	}
}

func TestCapabilities_EmptyPrinterName_503(t *testing.T) {
	// A printer constructed with name="" is treated as unconfigured
	// (matches printerHealth's predicate).
	p := &fakePrinter{name: "", reachable: true}
	_, ts := newTestServer(t, p)
	token := mintTestJWT(t, nil)

	resp := callCaps(t, ts, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodePrinterNotConfigured {
		t.Errorf("code = %q, want %q", code, CodePrinterNotConfigured)
	}
}

func TestCapabilities_KnownModel_ReturnsCapsAndConfiguredWidth(t *testing.T) {
	// Star SP-331 — should match the model_lookup entry. PaperWidthMM
	// comes from the agent config (80mm here), NOT from the lookup
	// hint. Cut + drawer remain from the lookup.
	p := &fakePrinter{name: "Star SP-331", reachable: true}
	_, ts := newTestServerWith(t, p, Config{
		ListenAddr:     "127.0.0.1:0",
		AllowedOrigins: []string{"https://allowed.example"},
		Version:        "test-1.0.0",
		Logger:         discardLogger(),
		Secrets:        pairedSecrets(),
		IdempotencyTTL: time.Hour,
		PaperWidthMM:   80,
	})
	token := mintTestJWT(t, nil)

	resp := callCaps(t, ts, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ok, data, _, _ := decodeEnvelope(t, resp.Body)
	if !ok {
		t.Fatal("envelope ok = false, want true")
	}
	if data["paper_width_mm"].(float64) != 80 {
		t.Errorf("paper_width_mm = %v, want 80", data["paper_width_mm"])
	}
	if data["cut_supported"] != true {
		t.Errorf("cut_supported = %v, want true", data["cut_supported"])
	}
	if data["drawer_supported"] != true {
		t.Errorf("drawer_supported = %v, want true", data["drawer_supported"])
	}
	if data["qr_supported"] != false {
		t.Errorf("qr_supported = %v, want false", data["qr_supported"])
	}
	if data["source"] != "model_lookup" {
		t.Errorf("source = %v, want model_lookup", data["source"])
	}
	// firmware_version + raw_status are omitempty → absent from JSON.
	if _, present := data["firmware_version"]; present {
		t.Errorf("firmware_version present in response; should be omitted via omitempty")
	}
	if _, present := data["raw_status"]; present {
		t.Errorf("raw_status present in response; should be omitted via omitempty")
	}
}

func TestCapabilities_RespectsConfigured58mm(t *testing.T) {
	// Same printer, but PaperWidthMM=58 in config → response carries
	// 58. The lookup hint says 80; agent config wins. Pins the
	// "config is source of truth" invariant from the task spec.
	p := &fakePrinter{name: "Star SP-331", reachable: true}
	_, ts := newTestServerWith(t, p, Config{
		ListenAddr:     "127.0.0.1:0",
		AllowedOrigins: []string{"https://allowed.example"},
		Version:        "test-1.0.0",
		Logger:         discardLogger(),
		Secrets:        pairedSecrets(),
		IdempotencyTTL: time.Hour,
		PaperWidthMM:   58,
	})
	token := mintTestJWT(t, nil)

	resp := callCaps(t, ts, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	_, data, _, _ := decodeEnvelope(t, resp.Body)
	if data["paper_width_mm"].(float64) != 58 {
		t.Errorf("paper_width_mm = %v, want 58", data["paper_width_mm"])
	}
}

func TestCapabilities_UnknownPrinter_FallbackSource(t *testing.T) {
	// Unknown model → fallback source. Defaults still 80/cut/drawer
	// because v1 conservative defaults match the model_lookup
	// happy-path defaults; only `source` distinguishes.
	p := &fakePrinter{name: "Brother HL-2030 Driver", reachable: true}
	_, ts := newTestServer(t, p)
	token := mintTestJWT(t, nil)

	resp := callCaps(t, ts, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	_, data, _, _ := decodeEnvelope(t, resp.Body)
	if data["source"] != "fallback" {
		t.Errorf("source = %v, want fallback", data["source"])
	}
}

func TestCapabilities_NoJWT_401(t *testing.T) {
	// Bare GET without Authorization → requireAuth returns 401.
	p := &fakePrinter{name: "Star SP-331", reachable: true}
	_, ts := newTestServer(t, p)

	resp := callCaps(t, ts, "") // no token
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestCapabilities_UnpairedSecrets_401NotPaired(t *testing.T) {
	// Auth middleware short-circuits BEFORE looking at the printer
	// when secrets are absent — same behaviour as /print + /test-print.
	p := &fakePrinter{name: "Star SP-331", reachable: true}
	_, ts := newTestServerWith(t, p, Config{
		ListenAddr:     "127.0.0.1:0",
		AllowedOrigins: []string{"https://allowed.example"},
		Version:        "test-1.0.0",
		Logger:         discardLogger(),
		Secrets:        unpairedSecrets(),
		IdempotencyTTL: time.Hour,
	})
	token := mintTestJWT(t, nil)

	resp := callCaps(t, ts, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeNotPaired {
		t.Errorf("code = %q, want %q", code, CodeNotPaired)
	}
}

func TestCapabilities_BarcodesAndCodepages_AreLists(t *testing.T) {
	// Wire-format sanity — barcode_types and codepages must round-trip
	// as JSON arrays (not strings or single values). Web clients
	// pattern-match on the lists.
	p := &fakePrinter{name: "Star SP-331", reachable: true}
	_, ts := newTestServer(t, p)
	token := mintTestJWT(t, nil)

	resp := callCaps(t, ts, token)
	defer resp.Body.Close()
	_, data, _, _ := decodeEnvelope(t, resp.Body)
	bts, ok := data["barcode_types"].([]any)
	if !ok {
		t.Fatalf("barcode_types is %T, want []any", data["barcode_types"])
	}
	if len(bts) < 2 {
		t.Errorf("barcode_types len = %d, want >= 2 (CODE128 + EAN13)", len(bts))
	}
	cps, ok := data["codepages"].([]any)
	if !ok {
		t.Fatalf("codepages is %T, want []any", data["codepages"])
	}
	if len(cps) < 1 {
		t.Errorf("codepages len = %d, want >= 1 (CP858)", len(cps))
	}
}
