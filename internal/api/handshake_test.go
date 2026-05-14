package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/jwt"
)

// ----- helpers -----

// decodeHandshake decodes the flat (non-enveloped) GET /handshake
// success body.
func decodeHandshake(t *testing.T, resp *http.Response) handshakeResponse {
	t.Helper()
	var hr handshakeResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		t.Fatalf("decode handshake response: %v", err)
	}
	return hr
}

// mintTestJWT mints a JWT signed with `testToken` (the token
// pairedSecrets() stores), using the given claim overrides applied to a
// valid baseline. Lets each test tweak exactly one thing.
func mintTestJWT(t *testing.T, mutate func(*jwt.Claims)) string {
	t.Helper()
	now := time.Now()
	claims := jwt.Claims{
		Iss:   "trm_test", // matches pairedSecrets()
		Aud:   handshakeAudience,
		Iat:   now.Unix(),
		Exp:   now.Add(15 * time.Minute).Unix(),
		Scope: "print",
	}
	if mutate != nil {
		mutate(&claims)
	}
	token, err := jwt.Mint(claims, []byte(testToken))
	if err != nil {
		t.Fatalf("mintTestJWT: %v", err)
	}
	return token
}

// jwtPost issues a POST with Authorization: Bearer <token>.
func jwtPost(t *testing.T, ts *httptest.Server, path, token string, body []byte) *http.Response {
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

// ----- GET /handshake -----

func TestHandshake_Paired_Returns200WithClaims(t *testing.T) {
	_, ts := newTestServer(t, nil)

	resp, err := http.Get(ts.URL + "/handshake")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	hr := decodeHandshake(t, resp)
	if hr.JWT == "" {
		t.Fatal("response jwt is empty")
	}
	if hr.TerminalID != "trm_test" {
		t.Errorf("terminal_id = %q, want trm_test", hr.TerminalID)
	}
	if _, err := time.Parse(time.RFC3339, hr.ExpiresAt); err != nil {
		t.Errorf("expires_at %q is not RFC3339: %v", hr.ExpiresAt, err)
	}

	// The minted JWT must verify against the terminal token and carry
	// the expected claims.
	claims, err := jwt.Verify(hr.JWT, []byte(testToken), time.Now())
	if err != nil {
		t.Fatalf("minted JWT failed verification: %v", err)
	}
	if claims.Iss != "trm_test" {
		t.Errorf("claim iss = %q, want trm_test", claims.Iss)
	}
	if claims.Aud != "simsim-print" {
		t.Errorf("claim aud = %q, want simsim-print", claims.Aud)
	}
	if claims.Scope != "print" {
		t.Errorf("claim scope = %q, want print", claims.Scope)
	}
	// exp must be ~15 minutes out, within the TTL ceiling.
	ttl := claims.Exp - claims.Iat
	if ttl <= 0 || ttl > jwt.MaxTTLSeconds {
		t.Errorf("claim TTL = %ds, want 0 < ttl <= %d", ttl, jwt.MaxTTLSeconds)
	}
}

func TestHandshake_Unpaired_Returns503AgentUnpaired(t *testing.T) {
	_, ts := newTestServerWith(t, nil, Config{
		ListenAddr:               "127.0.0.1:0",
		AllowedOrigins:           []string{"https://allowed.example"},
		Version:                  "test-1.0.0",
		Logger:                   discardLogger(),
		Secrets:                  unpairedSecrets(),
		IdempotencyTTL:           time.Hour,
		IdempotencySweepInterval: time.Hour,
	})

	resp, err := http.Get(ts.URL + "/handshake")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	ok, _, code, _ := decodeEnvelope(t, resp.Body)
	if ok {
		t.Error("envelope ok = true, want false")
	}
	if code != CodeAgentUnpaired {
		t.Errorf("error code = %q, want %q", code, CodeAgentUnpaired)
	}
}

// ----- requireAuth: JWT verification on /print -----

func TestPrint_ValidJWT_Succeeds(t *testing.T) {
	fp := &fakePrinter{name: "fake", reachable: true}
	_, ts := newTestServer(t, fp)

	token := mintTestJWT(t, nil)
	resp := jwtPost(t, ts, "/print", token, validPrintBody("job-jwt-ok", false))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		ok, _, code, msg := decodeEnvelope(t, resp.Body)
		t.Fatalf("status = %d (ok=%v code=%q msg=%q), want 200", resp.StatusCode, ok, code, msg)
	}
}

func TestPrint_ValidXTerminalToken_Succeeds_BackwardCompat(t *testing.T) {
	fp := &fakePrinter{name: "fake", reachable: true}
	_, ts := newTestServer(t, fp)

	// The legacy X-Terminal-Token path must keep working alongside JWT
	// until A.3 removes it.
	resp := authPost(t, ts, "/print", bytes.NewReader(validPrintBody("job-xtt-ok", false)))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		ok, _, code, msg := decodeEnvelope(t, resp.Body)
		t.Fatalf("status = %d (ok=%v code=%q msg=%q), want 200", resp.StatusCode, ok, code, msg)
	}
}

func TestPrint_NoAuthAtAll_RejectsUnauthenticated(t *testing.T) {
	fp := &fakePrinter{name: "fake", reachable: true}
	_, ts := newTestServer(t, fp)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/print",
		bytes.NewReader(validPrintBody("job-noauth", false)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeUnauthenticated {
		t.Errorf("error code = %q, want %q", code, CodeUnauthenticated)
	}
}

func TestPrint_TamperedSignature_RejectsSignatureInvalid(t *testing.T) {
	fp := &fakePrinter{name: "fake", reachable: true}
	_, ts := newTestServer(t, fp)

	// A JWT signed with a different key — the agent's stored token
	// won't verify it.
	wrongKeyToken, err := jwt.Mint(jwt.Claims{
		Iss:   "trm_test",
		Aud:   handshakeAudience,
		Iat:   time.Now().Unix(),
		Exp:   time.Now().Add(15 * time.Minute).Unix(),
		Scope: "print",
	}, []byte("a-different-terminal-token-entirely-xxxxxxx"))
	if err != nil {
		t.Fatal(err)
	}

	resp := jwtPost(t, ts, "/print", wrongKeyToken, validPrintBody("job-badsig", false))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeSignatureInvalid {
		t.Errorf("error code = %q, want %q", code, CodeSignatureInvalid)
	}
}

func TestPrint_ExpiredJWT_RejectsJWTExpired(t *testing.T) {
	fp := &fakePrinter{name: "fake", reachable: true}
	_, ts := newTestServer(t, fp)

	token := mintTestJWT(t, func(c *jwt.Claims) {
		issued := time.Now().Add(-30 * time.Minute)
		c.Iat = issued.Unix()
		c.Exp = issued.Add(15 * time.Minute).Unix() // 15min in the past
	})

	resp := jwtPost(t, ts, "/print", token, validPrintBody("job-expired", false))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeJWTExpired {
		t.Errorf("error code = %q, want %q", code, CodeJWTExpired)
	}
}

func TestPrint_WrongAudience_RejectsAudienceMismatch(t *testing.T) {
	fp := &fakePrinter{name: "fake", reachable: true}
	_, ts := newTestServer(t, fp)

	token := mintTestJWT(t, func(c *jwt.Claims) {
		c.Aud = "some-other-audience"
	})

	resp := jwtPost(t, ts, "/print", token, validPrintBody("job-badaud", false))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeAudienceMismatch {
		t.Errorf("error code = %q, want %q", code, CodeAudienceMismatch)
	}
}

func TestPrint_WrongIssuer_RejectsIssuerMismatch(t *testing.T) {
	fp := &fakePrinter{name: "fake", reachable: true}
	_, ts := newTestServer(t, fp)

	// Signed with the correct key (so the signature verifies) but
	// carrying a different terminal_id — the agent must reject a token
	// that wasn't minted for THIS terminal.
	token := mintTestJWT(t, func(c *jwt.Claims) {
		c.Iss = "trm_some_other_terminal"
	})

	resp := jwtPost(t, ts, "/print", token, validPrintBody("job-badiss", false))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeIssuerMismatch {
		t.Errorf("error code = %q, want %q", code, CodeIssuerMismatch)
	}
}

func TestPrint_MalformedJWT_RejectsJWTInvalid(t *testing.T) {
	fp := &fakePrinter{name: "fake", reachable: true}
	_, ts := newTestServer(t, fp)

	resp := jwtPost(t, ts, "/print", "not-a-real-jwt", validPrintBody("job-malformed", false))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeJWTInvalid {
		t.Errorf("error code = %q, want %q", code, CodeJWTInvalid)
	}
}

// TestPrint_BadJWT_DoesNotFallThroughToXTerminalToken confirms the
// header-precedence rule: a present-but-invalid Authorization: Bearer
// header fails with its specific JWT code — it does NOT fall through
// to the X-Terminal-Token path, even when a valid X-Terminal-Token is
// ALSO supplied on the same request.
func TestPrint_BadJWT_DoesNotFallThroughToXTerminalToken(t *testing.T) {
	fp := &fakePrinter{name: "fake", reachable: true}
	_, ts := newTestServer(t, fp)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/print",
		bytes.NewReader(validPrintBody("job-precedence", false)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer garbage-token")
	req.Header.Set("X-Terminal-Token", testToken) // valid legacy token, but ignored
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (bad JWT must not fall through)", resp.StatusCode)
	}
	_, _, code, _ := decodeEnvelope(t, resp.Body)
	if code != CodeJWTInvalid {
		t.Errorf("error code = %q, want %q (the JWT failure, not the legacy path)", code, CodeJWTInvalid)
	}
}

// TestTestPrint_ValidJWT_Succeeds confirms /test-print is also gated by
// requireAuth (A.1 applies the JWT gate to both /print and /test-print).
func TestTestPrint_ValidJWT_Succeeds(t *testing.T) {
	fp := &fakePrinter{name: "fake", reachable: true}
	_, ts := newTestServer(t, fp)

	token := mintTestJWT(t, nil)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/test-print", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		ok, _, code, msg := decodeEnvelope(t, resp.Body)
		t.Fatalf("status = %d (ok=%v code=%q msg=%q), want 200", resp.StatusCode, ok, code, msg)
	}
}
