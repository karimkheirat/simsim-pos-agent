package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCloudReporter is a CloudReporter test double. Captures every
// ReportPrintVerified call + lets each test inject a return error.
type fakeCloudReporter struct {
	mu        sync.Mutex
	returnErr error
	calls     []fakeCloudReporterCall
}

type fakeCloudReporterCall struct {
	token      string
	verified   bool
	errorClass string
}

func (f *fakeCloudReporter) ReportPrintVerified(
	_ context.Context, token string, verified bool, errorClass string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCloudReporterCall{
		token: token, verified: verified, errorClass: errorClass,
	})
	return f.returnErr
}

func (f *fakeCloudReporter) Calls() []fakeCloudReporterCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeCloudReporterCall, len(f.calls))
	copy(out, f.calls)
	return out
}

var _ CloudReporter = (*fakeCloudReporter)(nil)

// newReportVerifiedTestServer wires a Server with a fakeCloudReporter
// and pairedSecrets so /report-verified can authenticate + forward.
func newReportVerifiedTestServer(t *testing.T, rep *fakeCloudReporter) *httptest.Server {
	t.Helper()
	_, ts := newTestServerWith(t, nil, Config{
		ListenAddr:               "127.0.0.1:0",
		AllowedOrigins:           []string{"https://allowed.example"},
		Version:                  "test-1.0.0",
		Logger:                   discardLogger(),
		Secrets:                  pairedSecrets(),
		IdempotencyTTL:           time.Hour,
		IdempotencySweepInterval: time.Hour,
		CloudReporter:            rep,
	})
	return ts
}

// jwtPostJSON is a copy of handshake_test.go's jwtPost specialized to
// /report-verified, keyed for tests that already have a minted JWT
// rather than a token header.
func jwtPostJSON(t *testing.T, ts *httptest.Server, path, token string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// ── Happy paths ────────────────────────────────────────────────────

func TestReportVerified_HappyTrue(t *testing.T) {
	rep := &fakeCloudReporter{}
	ts := newReportVerifiedTestServer(t, rep)
	token := mintTestJWT(t, nil)

	resp := jwtPostJSON(t, ts, "/report-verified", token,
		[]byte(`{"verified": true}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	calls := rep.Calls()
	if len(calls) != 1 {
		t.Fatalf("cloud reporter calls = %d, want 1", len(calls))
	}
	if calls[0].verified != true {
		t.Errorf("verified = %v, want true", calls[0].verified)
	}
	if calls[0].errorClass != "" {
		t.Errorf("errorClass = %q, want empty", calls[0].errorClass)
	}
	// Cloud call MUST use the paired-secret terminal token, not the
	// caller's JWT or anything else.
	if calls[0].token != testToken {
		t.Errorf("forwarded token = %q, want testToken", calls[0].token)
	}
}

func TestReportVerified_HappyFalseRoundTripsErrorClass(t *testing.T) {
	rep := &fakeCloudReporter{}
	ts := newReportVerifiedTestServer(t, rep)
	token := mintTestJWT(t, nil)

	resp := jwtPostJSON(t, ts, "/report-verified", token,
		[]byte(`{"verified": false, "error_class": "OPERATOR_REJECTED"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	calls := rep.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].verified != false {
		t.Errorf("verified = %v, want false", calls[0].verified)
	}
	if calls[0].errorClass != "OPERATOR_REJECTED" {
		t.Errorf("errorClass = %q, want OPERATOR_REJECTED", calls[0].errorClass)
	}

	// Envelope echoes back to caller.
	ok, data, _, _ := decodeEnvelope(t, resp.Body)
	if !ok {
		t.Fatal("envelope ok = false")
	}
	if data["verified"] != false {
		t.Errorf("data.verified = %v, want false", data["verified"])
	}
	if data["recorded"] != true {
		t.Errorf("data.recorded = %v, want true", data["recorded"])
	}
	if data["error_class"] != "OPERATOR_REJECTED" {
		t.Errorf("data.error_class = %v, want OPERATOR_REJECTED", data["error_class"])
	}
}

// ── Auth ───────────────────────────────────────────────────────────

func TestReportVerified_NoJWT_401(t *testing.T) {
	rep := &fakeCloudReporter{}
	ts := newReportVerifiedTestServer(t, rep)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/report-verified",
		bytes.NewReader([]byte(`{"verified": true}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	// Cloud must NOT have been reached on a 401.
	if calls := rep.Calls(); len(calls) != 0 {
		t.Errorf("calls = %d, want 0 on 401", len(calls))
	}
}

// ── Validation ─────────────────────────────────────────────────────

func TestReportVerified_BadJSON_400(t *testing.T) {
	rep := &fakeCloudReporter{}
	ts := newReportVerifiedTestServer(t, rep)
	token := mintTestJWT(t, nil)

	resp := jwtPostJSON(t, ts, "/report-verified", token, []byte(`{not json`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if calls := rep.Calls(); len(calls) != 0 {
		t.Errorf("calls = %d, want 0", len(calls))
	}
}

// ── No CloudReporter wired → 503 ───────────────────────────────────

func TestReportVerified_NoCloudReporter_503(t *testing.T) {
	// Construct a server without CloudReporter (production main always
	// wires it, but the defensive guard is worth pinning).
	_, ts := newTestServerWith(t, nil, Config{
		ListenAddr:               "127.0.0.1:0",
		AllowedOrigins:           []string{"https://allowed.example"},
		Version:                  "test-1.0.0",
		Logger:                   discardLogger(),
		Secrets:                  pairedSecrets(),
		IdempotencyTTL:           time.Hour,
		IdempotencySweepInterval: time.Hour,
		// CloudReporter intentionally nil.
	})
	token := mintTestJWT(t, nil)

	resp := jwtPostJSON(t, ts, "/report-verified", token,
		[]byte(`{"verified": true}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeCloudUnreachable {
		t.Errorf("code = %q, want %q", code, CodeCloudUnreachable)
	}
}

// ── Cloud call fails → 502 CLOUD_UNREACHABLE ───────────────────────

func TestReportVerified_CloudError_502(t *testing.T) {
	rep := &fakeCloudReporter{returnErr: errors.New("network: connection refused")}
	ts := newReportVerifiedTestServer(t, rep)
	token := mintTestJWT(t, nil)

	resp := jwtPostJSON(t, ts, "/report-verified", token,
		[]byte(`{"verified": true}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	_, _, code, msg := decodeEnvelope(t, resp.Body)
	if code != CodeCloudUnreachable {
		t.Errorf("code = %q, want %q", code, CodeCloudUnreachable)
	}
	// Error message threads through for forensics (operator sees the
	// retry option, devs see the cloud failure shape).
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("message = %q, want substring 'connection refused'", msg)
	}
}

// ── Unpaired secrets → 503 NOT_PAIRED ──────────────────────────────

func TestReportVerified_UnpairedSecrets_503(t *testing.T) {
	rep := &fakeCloudReporter{}
	_, ts := newTestServerWith(t, nil, Config{
		ListenAddr:               "127.0.0.1:0",
		AllowedOrigins:           []string{"https://allowed.example"},
		Version:                  "test-1.0.0",
		Logger:                   discardLogger(),
		Secrets:                  unpairedSecrets(),
		IdempotencyTTL:           time.Hour,
		IdempotencySweepInterval: time.Hour,
		CloudReporter:            rep,
	})
	token := mintTestJWT(t, nil)

	// The requireAuth middleware should reject before /report-verified
	// even runs (unpaired secrets break JWT verification too).
	resp := jwtPostJSON(t, ts, "/report-verified", token,
		[]byte(`{"verified": true}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if calls := rep.Calls(); len(calls) != 0 {
		t.Errorf("calls = %d, want 0", len(calls))
	}
}
