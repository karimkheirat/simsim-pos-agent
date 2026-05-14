package jwt

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var testKey = []byte("tok_test_43chars_xxxxxxxxxxxxxxxxxxxxxxxxxxx")

func baseClaims(now time.Time) Claims {
	return Claims{
		Iss:   "trm_test",
		Aud:   "simsim-print",
		Iat:   now.Unix(),
		Exp:   now.Add(15 * time.Minute).Unix(),
		Scope: "print",
	}
}

func TestMintVerify_RoundTrip(t *testing.T) {
	now := time.Now()
	claims := baseClaims(now)

	token, err := Mint(claims, testKey)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if strings.Count(token, ".") != 2 {
		t.Fatalf("token should have 3 dot-separated segments, got %q", token)
	}

	got, err := Verify(token, testKey, now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Iss != claims.Iss {
		t.Errorf("Iss = %q, want %q", got.Iss, claims.Iss)
	}
	if got.Aud != claims.Aud {
		t.Errorf("Aud = %q, want %q", got.Aud, claims.Aud)
	}
	if got.Iat != claims.Iat {
		t.Errorf("Iat = %d, want %d", got.Iat, claims.Iat)
	}
	if got.Exp != claims.Exp {
		t.Errorf("Exp = %d, want %d", got.Exp, claims.Exp)
	}
	if got.Scope != claims.Scope {
		t.Errorf("Scope = %q, want %q", got.Scope, claims.Scope)
	}
}

func TestMint_HeaderIsHS256(t *testing.T) {
	now := time.Now()
	token, err := Mint(baseClaims(now), testKey)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	headerSeg := strings.Split(token, ".")[0]
	raw, err := base64.RawURLEncoding.DecodeString(headerSeg)
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var h header
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if h.Alg != "HS256" {
		t.Errorf("header alg = %q, want HS256", h.Alg)
	}
	if h.Typ != "JWT" {
		t.Errorf("header typ = %q, want JWT", h.Typ)
	}
}

func TestVerify_RejectsTamperedSignature(t *testing.T) {
	now := time.Now()
	token, err := Mint(baseClaims(now), testKey)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// Flip the last character of the signature segment.
	parts := strings.Split(token, ".")
	sig := []byte(parts[2])
	if sig[len(sig)-1] == 'A' {
		sig[len(sig)-1] = 'B'
	} else {
		sig[len(sig)-1] = 'A'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(sig)

	_, err = Verify(tampered, testKey, now)
	if !errors.Is(err, ErrBadSignature) {
		t.Errorf("Verify(tampered sig) error = %v, want ErrBadSignature", err)
	}
}

func TestVerify_RejectsTamperedPayload(t *testing.T) {
	now := time.Now()
	token, err := Mint(baseClaims(now), testKey)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// Re-encode the payload with a different issuer; signature now mismatches.
	parts := strings.Split(token, ".")
	forged := baseClaims(now)
	forged.Iss = "trm_attacker"
	forgedJSON, _ := json.Marshal(forged)
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(forgedJSON) + "." + parts[2]

	_, err = Verify(tampered, testKey, now)
	if !errors.Is(err, ErrBadSignature) {
		t.Errorf("Verify(tampered payload) error = %v, want ErrBadSignature", err)
	}
}

func TestVerify_RejectsWrongKey(t *testing.T) {
	now := time.Now()
	token, err := Mint(baseClaims(now), testKey)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	_, err = Verify(token, []byte("a-completely-different-terminal-token-value"), now)
	if !errors.Is(err, ErrBadSignature) {
		t.Errorf("Verify(wrong key) error = %v, want ErrBadSignature", err)
	}
}

func TestVerify_RejectsExpired(t *testing.T) {
	issued := time.Now().Add(-30 * time.Minute)
	claims := baseClaims(issued) // exp = issued + 15min, so 15min in the past
	token, err := Mint(claims, testKey)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	_, err = Verify(token, testKey, time.Now())
	if !errors.Is(err, ErrExpired) {
		t.Errorf("Verify(expired) error = %v, want ErrExpired", err)
	}
}

func TestVerify_RejectsForgedOverLongTTL(t *testing.T) {
	now := time.Now()
	claims := baseClaims(now)
	// A validly-signed token whose TTL exceeds the 15-min ceiling.
	claims.Exp = claims.Iat + MaxTTLSeconds + 60
	token, err := Mint(claims, testKey)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	_, err = Verify(token, testKey, now)
	if !errors.Is(err, ErrExpired) {
		t.Errorf("Verify(forged over-long TTL) error = %v, want ErrExpired", err)
	}
}

func TestVerify_AcceptsAtTTLCeiling(t *testing.T) {
	now := time.Now()
	claims := baseClaims(now)
	claims.Exp = claims.Iat + MaxTTLSeconds // exactly at the ceiling — allowed
	token, err := Mint(claims, testKey)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if _, err := Verify(token, testKey, now); err != nil {
		t.Errorf("Verify(exactly at TTL ceiling) error = %v, want nil", err)
	}
}

func TestVerify_RejectsMalformed(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"one segment", "abc"},
		{"two segments", "abc.def"},
		{"four segments", "a.b.c.d"},
		{"non-base64 header", "!!!.eyJhIjoxfQ.sig"},
		{"non-base64 payload", "eyJhbGciOiJIUzI1NiJ9.!!!.sig"},
		{"non-base64 signature", "eyJhbGciOiJIUzI1NiJ9.eyJhIjoxfQ.!!!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Verify(tc.token, testKey, now)
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("Verify(%q) error = %v, want ErrMalformed", tc.name, err)
			}
		})
	}
}

func TestVerify_RejectsWrongAlg(t *testing.T) {
	now := time.Now()
	// Hand-build a token with alg=none.
	noneHeader, _ := json.Marshal(header{Alg: "none", Typ: "JWT"})
	payload, _ := json.Marshal(baseClaims(now))
	signingInput := base64.RawURLEncoding.EncodeToString(noneHeader) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	sig := sign(signingInput, testKey)
	token := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	_, err := Verify(token, testKey, now)
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("Verify(alg=none) error = %v, want ErrMalformed", err)
	}
}
