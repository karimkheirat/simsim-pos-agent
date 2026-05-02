package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/escpos"
	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
	"github.com/karimkheirat/simsim-pos-agent/internal/receipt"
)

// ----- fakePrinter -----

type printedJob struct {
	jobName string
	data    []byte
}

type fakePrinter struct {
	name      string
	reachable bool
	printErr  error

	mu      sync.Mutex
	printed []printedJob
}

func (f *fakePrinter) Name() string      { return f.name }
func (f *fakePrinter) IsReachable() bool { return f.reachable }
func (f *fakePrinter) Print(jobName string, data []byte) error {
	if f.printErr != nil {
		return f.printErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	f.printed = append(f.printed, printedJob{jobName, cp})
	return nil
}

func (f *fakePrinter) Printed() []printedJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]printedJob, len(f.printed))
	copy(out, f.printed)
	return out
}

// Compile-time assertion that *fakePrinter satisfies the Printer interface.
var _ printer.Printer = (*fakePrinter)(nil)

// ----- helpers -----

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func newTestServer(t *testing.T, p printer.Printer) (*Server, *httptest.Server) {
	t.Helper()
	return newTestServerWith(t, p, Config{
		ListenAddr:               "127.0.0.1:0",
		AllowedOrigins:           []string{"https://allowed.example"},
		Version:                  "test-1.0.0",
		Logger:                   discardLogger(),
		IdempotencyTTL:           time.Hour,
		IdempotencySweepInterval: time.Hour, // effectively disabled for most tests
	})
}

func newTestServerWith(t *testing.T, p printer.Printer, cfg Config) (*Server, *httptest.Server) {
	t.Helper()
	srv, err := New(cfg, p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.handler)
	t.Cleanup(ts.Close)
	return srv, ts
}

// validReceiptJSON returns a JSON request body for /print using the M1 fixture.
func validPrintBody(jobID string, openDrawer bool) []byte {
	type req struct {
		JobID           string          `json:"job_id"`
		IdempotencyKey  string          `json:"idempotency_key"`
		Receipt         receipt.Receipt `json:"receipt"`
		OpenDrawerAfter bool            `json:"open_drawer_after"`
	}
	body, err := json.Marshal(req{
		JobID:           jobID,
		IdempotencyKey:  jobID,
		Receipt:         receiptFixture,
		OpenDrawerAfter: openDrawer,
	})
	if err != nil {
		panic(err)
	}
	return body
}

func decodeEnvelope(t *testing.T, body io.Reader) (ok bool, data map[string]any, code, message string) {
	t.Helper()
	var raw map[string]any
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	ok, _ = raw["ok"].(bool)
	if d, dok := raw["data"].(map[string]any); dok {
		data = d
	}
	if errObj, eok := raw["error"].(map[string]any); eok {
		code, _ = errObj["code"].(string)
		message, _ = errObj["message"].(string)
	}
	return
}

// ----- /health -----

func TestHealth_NoPrinter(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.OK {
		t.Errorf("ok = false")
	}
	if body.Version != "test-1.0.0" {
		t.Errorf("version = %q, want test-1.0.0", body.Version)
	}
	if body.Paired {
		t.Errorf("paired = true; want false in M1")
	}
	if body.Printer.Configured {
		t.Errorf("printer.configured = true; want false when no printer")
	}
	if body.Printer.Reachable {
		t.Errorf("printer.reachable = true; want false")
	}
}

func TestHealth_WithReachablePrinter(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true}
	_, ts := newTestServer(t, fp)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body healthResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if !body.Printer.Configured {
		t.Errorf("printer.configured = false, want true")
	}
	if !body.Printer.Reachable {
		t.Errorf("printer.reachable = false, want true")
	}
	if body.Printer.Name != "SP-331" {
		t.Errorf("printer.name = %q, want SP-331", body.Printer.Name)
	}
}

func TestHealth_PrinterUnreachable(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: false}
	_, ts := newTestServer(t, fp)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body healthResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if !body.Printer.Configured {
		t.Errorf("printer.configured = false, want true")
	}
	if body.Printer.Reachable {
		t.Errorf("printer.reachable = true, want false")
	}
}

// ----- CORS -----

func TestCORS_AllowedOrigin(t *testing.T) {
	_, ts := newTestServer(t, nil)
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/print", nil)
	req.Header.Set("Origin", "https://allowed.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://allowed.example" {
		t.Errorf("ACAO = %q, want allowed origin", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Errorf("ACAM = %q, want POST present", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-Terminal-Token") {
		t.Errorf("ACAH = %q, want X-Terminal-Token present", got)
	}
}

func TestCORS_DisallowedOrigin(t *testing.T) {
	_, ts := newTestServer(t, nil)
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/print", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO = %q, want empty for disallowed origin", got)
	}
}

func TestCORS_NoOriginHeader(t *testing.T) {
	_, ts := newTestServer(t, nil)
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/print", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO = %q, want empty when no Origin header", got)
	}
}

// ----- /print -----

func TestPrint_HappyPath(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true}
	_, ts := newTestServer(t, fp)
	resp, err := http.Post(ts.URL+"/print", "application/json", bytes.NewReader(validPrintBody("job-1", true)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ok, data, _, _ := decodeEnvelope(t, resp.Body)
	if !ok {
		t.Errorf("ok = false")
	}
	if got := data["job_id"]; got != "job-1" {
		t.Errorf("job_id = %v, want job-1", got)
	}
	jobs := fp.Printed()
	if len(jobs) != 1 {
		t.Fatalf("printed jobs = %d, want 1", len(jobs))
	}
	bytesSent, _ := data["bytes_sent"].(float64)
	if int(bytesSent) != len(jobs[0].data) {
		t.Errorf("bytes_sent = %d, want %d (printer received)", int(bytesSent), len(jobs[0].data))
	}
	if jobs[0].jobName != "job-1" {
		t.Errorf("printer jobName = %q, want job-1", jobs[0].jobName)
	}
}

func TestPrint_EmptyJobID(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true}
	_, ts := newTestServer(t, fp)
	resp, err := http.Post(ts.URL+"/print", "application/json", bytes.NewReader(validPrintBody("", false)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeInvalidReceipt {
		t.Errorf("code = %q, want %q", code, CodeInvalidReceipt)
	}
	if len(fp.Printed()) != 0 {
		t.Errorf("printer was called despite 400")
	}
}

func TestPrint_InvalidReceipt(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true}
	_, ts := newTestServer(t, fp)

	// Build a body whose receipt has empty store name → receipt.Render rejects.
	type req struct {
		JobID           string          `json:"job_id"`
		Receipt         receipt.Receipt `json:"receipt"`
		OpenDrawerAfter bool            `json:"open_drawer_after"`
	}
	bad := receiptFixture
	bad.Store.Name = ""
	body, _ := json.Marshal(req{JobID: "job-bad", Receipt: bad})

	resp, err := http.Post(ts.URL+"/print", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeInvalidReceipt {
		t.Errorf("code = %q, want %q", code, CodeInvalidReceipt)
	}
	if len(fp.Printed()) != 0 {
		t.Errorf("printer was called despite invalid receipt")
	}
}

func TestPrint_NoPrinter(t *testing.T) {
	_, ts := newTestServer(t, nil)
	resp, err := http.Post(ts.URL+"/print", "application/json", bytes.NewReader(validPrintBody("job-x", false)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodePrinterNotConfigured {
		t.Errorf("code = %q, want %q", code, CodePrinterNotConfigured)
	}
}

func TestPrint_PrinterUnreachable(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: false}
	_, ts := newTestServer(t, fp)
	resp, err := http.Post(ts.URL+"/print", "application/json", bytes.NewReader(validPrintBody("job-y", false)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodePrinterOffline {
		t.Errorf("code = %q, want %q", code, CodePrinterOffline)
	}
	if len(fp.Printed()) != 0 {
		t.Errorf("printer was called despite unreachable")
	}
}

func TestPrint_PrintError(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true, printErr: errors.New("spooler exploded")}
	_, ts := newTestServer(t, fp)
	resp, err := http.Post(ts.URL+"/print", "application/json", bytes.NewReader(validPrintBody("job-z", false)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodePrintFailed {
		t.Errorf("code = %q, want %q", code, CodePrintFailed)
	}
}

func TestPrint_Idempotency_SameJobID(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true}
	_, ts := newTestServer(t, fp)
	body := validPrintBody("job-idem", false)

	resp1, err := http.Post(ts.URL+"/print", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if resp1.StatusCode != 200 {
		t.Fatalf("first call status = %d, want 200", resp1.StatusCode)
	}

	resp2, err := http.Post(ts.URL+"/print", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("second call status = %d, want 200", resp2.StatusCode)
	}

	if !bytes.Equal(body1, body2) {
		t.Errorf("second call body differs from first\n first: %s\nsecond: %s", body1, body2)
	}
	if got := len(fp.Printed()); got != 1 {
		t.Errorf("printer called %d times, want 1 (idempotency)", got)
	}
}

func TestPrint_Idempotency_Expiry(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true}
	_, ts := newTestServerWith(t, fp, Config{
		ListenAddr:               "127.0.0.1:0",
		Version:                  "test",
		Logger:                   discardLogger(),
		IdempotencyTTL:           30 * time.Millisecond,
		IdempotencySweepInterval: 10 * time.Millisecond,
	})
	body := validPrintBody("job-exp", false)

	resp1, err := http.Post(ts.URL+"/print", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()

	// Wait past TTL — entry should expire on next Get.
	time.Sleep(80 * time.Millisecond)

	resp2, err := http.Post(ts.URL+"/print", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	if got := len(fp.Printed()); got != 2 {
		t.Errorf("printer called %d times after expiry, want 2", got)
	}
}

// ----- /test-print -----

func TestTestPrint(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true}
	_, ts := newTestServer(t, fp)
	resp, err := http.Post(ts.URL+"/test-print", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	jobs := fp.Printed()
	if len(jobs) != 1 {
		t.Fatalf("printed jobs = %d, want 1", len(jobs))
	}
	if len(jobs[0].data) < 2 || jobs[0].data[0] != 0x1B || jobs[0].data[1] != 0x40 {
		t.Errorf("job data does not start with ESC @ (1B 40): % X...", jobs[0].data[:min(8, len(jobs[0].data))])
	}
}

// ----- /drawer/open -----

func TestDrawerOpen_Success(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true}
	_, ts := newTestServer(t, fp)
	resp, err := http.Post(ts.URL+"/drawer/open", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	jobs := fp.Printed()
	if len(jobs) != 1 {
		t.Fatalf("printed jobs = %d, want 1", len(jobs))
	}
	want := escpos.DrawerKick()
	if !bytes.Equal(jobs[0].data, want) {
		t.Errorf("data = % X, want % X (escpos.DrawerKick())", jobs[0].data, want)
	}
	if !strings.HasPrefix(jobs[0].jobName, "drawer-kick-") {
		t.Errorf("jobName = %q, want drawer-kick-<uuid>", jobs[0].jobName)
	}
}

func TestDrawerOpen_PrinterError(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true, printErr: errors.New("drawer fault")}
	_, ts := newTestServer(t, fp)
	resp, err := http.Post(ts.URL+"/drawer/open", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeDrawerFailed {
		t.Errorf("code = %q, want %q", code, CodeDrawerFailed)
	}
}

// ----- loopback middleware (unit) -----

func TestCheckLoopbackMiddleware(t *testing.T) {
	s := &Server{logger: discardLogger()}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := s.checkLoopbackMiddleware(inner)

	tests := []struct {
		name       string
		remoteAddr string
		wantStatus int
	}{
		{"loopback v4", "127.0.0.1:54321", http.StatusOK},
		{"loopback v6", "[::1]:54321", http.StatusOK},
		{"public v4", "8.8.8.8:1234", http.StatusForbidden},
		{"private v4", "192.168.1.10:8080", http.StatusForbidden},
		{"unparseable", "not-a-host", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			req.RemoteAddr = tt.remoteAddr
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// ----- Run / lifecycle -----

func TestRun_GracefulShutdown(t *testing.T) {
	srv, err := New(Config{
		ListenAddr: "127.0.0.1:0",
		Version:    "test",
		Logger:     discardLogger(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	// Give the listener time to come up before canceling.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Run did not return within 6s of ctx cancel")
	}
}
