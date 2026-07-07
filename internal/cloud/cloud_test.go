package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// canned response helpers ----------------------------------------------------

func writeOKEnvelope(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}

func writeErrorEnvelope(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":    false,
		"error": map[string]any{"code": code, "message": message},
	})
}

// Pair ----------------------------------------------------------------------

func TestPair_HappyPath(t *testing.T) {
	var (
		seenPath        string
		seenAuth        string
		seenContentType string
		receivedBody    pairRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenAuth = r.Header.Get("X-Terminal-Token")
		seenContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		writeOKEnvelope(w, http.StatusOK, map[string]any{
			"terminal_id":    "trm_abc",
			"terminal_token": "tok_xyz_43chars",
			"store_id":       "store_123",
			"store_name":     "Hamoud Boualem - Centre Oran",
			"terminal_label": "Caisse 1",
		})
	}))
	defer server.Close()

	c := New(server.URL, "0.2.0")
	resp, err := c.Pair(context.Background(), "428193", "0.2.0", "machine-xyz")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if resp.TerminalID != "trm_abc" {
		t.Errorf("TerminalID = %q", resp.TerminalID)
	}
	if resp.TerminalToken != "tok_xyz_43chars" {
		t.Errorf("TerminalToken = %q", resp.TerminalToken)
	}
	if resp.StoreID != "store_123" {
		t.Errorf("StoreID = %q", resp.StoreID)
	}
	if resp.StoreName != "Hamoud Boualem - Centre Oran" {
		t.Errorf("StoreName = %q", resp.StoreName)
	}
	if resp.TerminalLabel != "Caisse 1" {
		t.Errorf("TerminalLabel = %q", resp.TerminalLabel)
	}

	// Wire checks
	if seenPath != pathPair {
		t.Errorf("path = %q, want %q", seenPath, pathPair)
	}
	if seenAuth != "" {
		t.Errorf("X-Terminal-Token sent on /pair: %q (must NOT be sent)", seenAuth)
	}
	if seenContentType != "application/json" {
		t.Errorf("Content-Type = %q", seenContentType)
	}
	if receivedBody.Code != "428193" || receivedBody.AgentVersion != "0.2.0" || receivedBody.MachineID != "machine-xyz" {
		t.Errorf("request body = %+v", receivedBody)
	}
}

func TestPair_InvalidCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErrorEnvelope(w, http.StatusUnauthorized, "INVALID_CODE", "Code invalide ou expiré")
	}))
	defer server.Close()

	c := New(server.URL, "0.2.0")
	_, err := c.Pair(context.Background(), "000000", "0.2.0", "m")
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("err = %v; want errors.Is(err, ErrInvalidCode)", err)
	}
	var ce *CloudError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v; want *CloudError", err)
	}
	if ce.Code() != "INVALID_CODE" {
		t.Errorf("Code() = %q", ce.Code())
	}
	if ce.Message() != "Code invalide ou expiré" {
		t.Errorf("Message() = %q", ce.Message())
	}
}

func TestPair_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErrorEnvelope(w, http.StatusTooManyRequests, "RATE_LIMITED", "Trop de tentatives")
	}))
	defer server.Close()

	c := New(server.URL, "0.2.0")
	_, err := c.Pair(context.Background(), "000000", "0.2.0", "m")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v; want errors.Is(err, ErrRateLimited)", err)
	}
}

// Heartbeat -----------------------------------------------------------------

func TestHeartbeat_Success(t *testing.T) {
	var (
		seenPath  string
		seenToken string
		body      HeartbeatRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenToken = r.Header.Get("X-Terminal-Token")
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeOKEnvelope(w, http.StatusOK, map[string]any{"received_at": "2026-05-02T14:30:00Z"})
	}))
	defer server.Close()

	c := New(server.URL, "0.2.0")
	err := c.Heartbeat(context.Background(), "good-token", HeartbeatRequest{
		AgentVersion:  "0.2.0",
		OSVersion:     "Windows 11 23H2",
		UptimeSeconds: 12345,
		Printer:       PrinterStatus{Configured: true, Reachable: true, Name: "SP-331"},
	})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if seenPath != pathHeartbeat {
		t.Errorf("path = %q", seenPath)
	}
	if seenToken != "good-token" {
		t.Errorf("X-Terminal-Token = %q, want good-token", seenToken)
	}
	if body.AgentVersion != "0.2.0" || body.UptimeSeconds != 12345 {
		t.Errorf("body = %+v", body)
	}
	if !body.Printer.Configured || !body.Printer.Reachable || body.Printer.Name != "SP-331" {
		t.Errorf("body.Printer = %+v", body.Printer)
	}
	if body.Printer.LastError != nil {
		t.Errorf("body.Printer.LastError = %v, want nil", body.Printer.LastError)
	}
}

func TestHeartbeat_Unauthenticated(t *testing.T) {
	var seenToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenToken = r.Header.Get("X-Terminal-Token")
		writeErrorEnvelope(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Token invalide")
	}))
	defer server.Close()

	c := New(server.URL, "0.2.0")
	err := c.Heartbeat(context.Background(), "bad-token", HeartbeatRequest{AgentVersion: "0.2.0"})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v; want errors.Is(err, ErrUnauthenticated)", err)
	}
	if seenToken != "bad-token" {
		t.Errorf("X-Terminal-Token = %q", seenToken)
	}
}

// Unpair --------------------------------------------------------------------

func TestUnpair_Success(t *testing.T) {
	var (
		seenPath  string
		seenToken string
		bodyBytes []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenToken = r.Header.Get("X-Terminal-Token")
		bodyBytes, _ = io.ReadAll(r.Body)
		writeOKEnvelope(w, http.StatusOK, map[string]any{})
	}))
	defer server.Close()

	c := New(server.URL, "0.2.0")
	err := c.Unpair(context.Background(), "good-token")
	if err != nil {
		t.Fatalf("Unpair: %v", err)
	}
	if seenPath != pathUnpair {
		t.Errorf("path = %q", seenPath)
	}
	if seenToken != "good-token" {
		t.Errorf("X-Terminal-Token = %q", seenToken)
	}
	if strings.TrimSpace(string(bodyBytes)) != "{}" {
		t.Errorf("body = %q, want {}", bodyBytes)
	}
}

// ReportPrintVerified -------------------------------------------------------

func TestReportPrintVerified_Success(t *testing.T) {
	var (
		seenPath  string
		seenToken string
		body      PrintVerifiedRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenToken = r.Header.Get("X-Terminal-Token")
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeOKEnvelope(w, http.StatusOK, map[string]any{
			"verified": true, "recorded": true,
		})
	}))
	defer server.Close()

	c := New(server.URL, "0.4.0")
	err := c.ReportPrintVerified(context.Background(), "good-token", PrintVerifiedRequest{
		Verified: true,
	})
	if err != nil {
		t.Fatalf("ReportPrintVerified: %v", err)
	}
	if seenPath != pathPrintVerified {
		t.Errorf("path = %q, want %q", seenPath, pathPrintVerified)
	}
	if seenToken != "good-token" {
		t.Errorf("X-Terminal-Token = %q", seenToken)
	}
	if body.Verified != true {
		t.Errorf("body.Verified = %v, want true", body.Verified)
	}
	if body.ErrorClass != "" {
		t.Errorf("body.ErrorClass = %q, want empty for verified=true", body.ErrorClass)
	}
}

func TestReportPrintVerified_FailedRoundTripsErrorClass(t *testing.T) {
	var body PrintVerifiedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeOKEnvelope(w, http.StatusOK, map[string]any{
			"verified": false, "recorded": true,
		})
	}))
	defer server.Close()

	c := New(server.URL, "0.4.0")
	err := c.ReportPrintVerified(context.Background(), "good-token", PrintVerifiedRequest{
		Verified:   false,
		ErrorClass: "OPERATOR_REJECTED",
	})
	if err != nil {
		t.Fatalf("ReportPrintVerified: %v", err)
	}
	if body.Verified != false {
		t.Errorf("body.Verified = %v, want false", body.Verified)
	}
	if body.ErrorClass != "OPERATOR_REJECTED" {
		t.Errorf("body.ErrorClass = %q, want OPERATOR_REJECTED", body.ErrorClass)
	}
}

func TestReportPrintVerified_Unauthenticated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErrorEnvelope(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Token invalide")
	}))
	defer server.Close()

	c := New(server.URL, "0.4.0")
	err := c.ReportPrintVerified(context.Background(), "bad-token", PrintVerifiedRequest{
		Verified: true,
	})
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v; want errors.Is(err, ErrUnauthenticated)", err)
	}
}

// User-Agent / headers ------------------------------------------------------

func TestUserAgentSentOnEveryRequest(t *testing.T) {
	tests := []struct {
		name string
		call func(c *Client) error
	}{
		{"pair", func(c *Client) error { _, err := c.Pair(context.Background(), "x", "v", "m"); return err }},
		{"heartbeat", func(c *Client) error { return c.Heartbeat(context.Background(), "t", HeartbeatRequest{}) }},
		{"unpair", func(c *Client) error { return c.Unpair(context.Background(), "t") }},
		{"report_verified", func(c *Client) error { return c.ReportPrintVerified(context.Background(), "t", PrintVerifiedRequest{Verified: true}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seenUA string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seenUA = r.Header.Get("User-Agent")
				writeOKEnvelope(w, http.StatusOK, map[string]any{
					"terminal_id": "x", "terminal_token": "x",
					"store_id": "x", "store_name": "x", "terminal_label": "x",
				})
			}))
			defer server.Close()
			c := New(server.URL, "9.9.9")
			if err := tt.call(c); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if seenUA != "simsim-pos-agent/9.9.9" {
				t.Errorf("User-Agent = %q, want simsim-pos-agent/9.9.9", seenUA)
			}
		})
	}
}

// Network / context errors --------------------------------------------------

func TestNetworkError_ServerClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close() // dial will fail

	c := New(server.URL, "0.2.0")
	_, err := c.Pair(context.Background(), "428193", "0.2.0", "m")
	if !errors.Is(err, ErrNetwork) {
		t.Fatalf("err = %v; want errors.Is(err, ErrNetwork)", err)
	}
}

func TestContextCancellationHonored(t *testing.T) {
	// Handler blocks ~1s with a fallback timer — r.Context().Done() does
	// not always fire promptly under httptest, so the time.After branch
	// guarantees the handler returns even if the client disconnect isn't
	// observed. The client should return in ~50ms regardless.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(1 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	c := New(server.URL, "0.2.0")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Pair(ctx, "428193", "0.2.0", "m")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
	if !errors.Is(err, ErrNetwork) {
		t.Errorf("err = %v; want errors.Is(err, ErrNetwork)", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("client took %v to return; want <500ms (ctx timeout was 50ms)", elapsed)
	}
}

// Defensive: non-JSON 5xx ----------------------------------------------------

func TestNonJSON_5xx_MapsToInternal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	}))
	defer server.Close()

	c := New(server.URL, "0.2.0")
	err := c.Heartbeat(context.Background(), "t", HeartbeatRequest{})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("err = %v; want errors.Is(err, ErrInternal)", err)
	}
}

// Sanity: errors.As for CloudError preserves message --------------------------

func TestCloudError_PreservesMessage(t *testing.T) {
	err := mapError(&errorPayload{Code: "INVALID_CODE", Message: "Code invalide ou expiré"})
	if err.Error() != "cloud: pairing code invalid or expired: Code invalide ou expiré" {
		t.Errorf("Error() = %q", err.Error())
	}
	var ce *CloudError
	if !errors.As(err, &ce) {
		t.Fatal("errors.As failed")
	}
	if ce.Code() != "INVALID_CODE" || ce.Message() != "Code invalide ou expiré" {
		t.Errorf("Code()=%q Message()=%q", ce.Code(), ce.Message())
	}
}

// Unknown error code falls back to ErrInternal but preserves the original code/message.
func TestCloudError_UnknownCodeFallsBackToInternal(t *testing.T) {
	err := mapError(&errorPayload{Code: "BANANA_TIME", Message: "le serveur a glissé"})
	if !errors.Is(err, ErrInternal) {
		t.Errorf("err = %v; want errors.Is(err, ErrInternal)", err)
	}
	var ce *CloudError
	if !errors.As(err, &ce) {
		t.Fatal("errors.As failed")
	}
	if ce.Code() != "BANANA_TIME" {
		t.Errorf("Code() = %q, want BANANA_TIME (preserved)", ce.Code())
	}
}

// FetchScalePLUFile ----------------------------------------------------------

func TestFetchScalePLUFile_HappyPath(t *testing.T) {
	var (
		seenPath   string
		seenMethod string
		seenAuth   string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenMethod = r.Method
		seenAuth = r.Header.Get("X-Terminal-Token")
		writeOKEnvelope(w, http.StatusOK, map[string]any{
			"format":      "link69_plu_v2",
			"encoding":    "utf-16le-bom",
			"path_hint":   `C:\ProgramData\Simsim\balance\PLU.txt`,
			"content":     "ID\tName1\t\r\nPLU FILE BODY\t\r\n",
			"sha256":      "abc123",
			"entry_count": 42,
			"generated":   []map[string]any{{"product_id": "p1", "plu": "10001"}},
			"skipped":     []map[string]any{{"product_id": "p2", "reason": "missing_price"}},
		})
	}))
	defer server.Close()

	c := New(server.URL, "0.4.0")
	resp, err := c.FetchScalePLUFile(context.Background(), "tok_abc")
	if err != nil {
		t.Fatalf("FetchScalePLUFile: %v", err)
	}

	if seenPath != "/api/pos-agent/scale-plu-file" {
		t.Errorf("path = %q", seenPath)
	}
	if seenMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", seenMethod)
	}
	if seenAuth != "tok_abc" {
		t.Errorf("X-Terminal-Token = %q, want tok_abc", seenAuth)
	}
	if resp.Format != ScalePLUFileFormat {
		t.Errorf("format = %q, want %q", resp.Format, ScalePLUFileFormat)
	}
	if resp.Encoding != ScalePLUFileEncoding {
		t.Errorf("encoding = %q, want %q", resp.Encoding, ScalePLUFileEncoding)
	}
	if resp.Content != "ID\tName1\t\r\nPLU FILE BODY\t\r\n" || resp.SHA256 != "abc123" || resp.EntryCount != 42 {
		t.Errorf("payload = %+v", resp)
	}
	if resp.PathHint != `C:\ProgramData\Simsim\balance\PLU.txt` {
		t.Errorf("path_hint = %q", resp.PathHint)
	}
	if len(resp.Generated) != 1 || resp.Generated[0].ProductID != "p1" || resp.Generated[0].PLU != "10001" {
		t.Errorf("generated = %+v", resp.Generated)
	}
	if len(resp.Skipped) != 1 || resp.Skipped[0].ProductID != "p2" || resp.Skipped[0].Reason != "missing_price" {
		t.Errorf("skipped = %+v", resp.Skipped)
	}
}

func TestFetchScalePLUFile_Unauthenticated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErrorEnvelope(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Jeton invalide.")
	}))
	defer server.Close()

	c := New(server.URL, "0.4.0")
	_, err := c.FetchScalePLUFile(context.Background(), "tok_revoked")
	if !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
}
