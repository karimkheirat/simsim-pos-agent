// Package jwt implements the narrow slice of JWT the POS agent needs:
// minting and verifying HMAC-SHA256 (HS256) tokens. It is intentionally
// NOT a general JWT library — it supports exactly one algorithm, a fixed
// claim set, and no key infrastructure beyond a single HMAC key.
//
// Rationale for hand-rolling instead of pulling a dependency: the agent's
// whole design philosophy is minimal deps (go.mod has three, all
// transitive-essential). HS256 mint+verify is ~80 lines of stdlib
// (crypto/hmac, crypto/sha256, crypto/subtle, encoding/base64,
// encoding/json). A JWT library would be a larger attack surface for a
// smaller job.
//
// Spec: M13_BUILD_SPEC.md §2.1, docs/agent-handshake-protocol.md §4-§5.
// The agent mints these in GET /handshake (signed with the terminal
// token as the HMAC key) and verifies them on /print + /test-print.
package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// MaxTTLSeconds is the hard upper bound on a token's lifetime. Verify
// rejects any token whose exp-iat exceeds this even when the signature
// is valid — defense against a forged over-long TTL. 900s = 15 minutes,
// the value fixed by M13's Phase 1 resolution.
const MaxTTLSeconds = 900

// Claims is the fixed payload shape for agent handshake tokens. No
// optional / dynamic claims — the agent mints exactly this set and
// Verify decodes exactly this set.
type Claims struct {
	// Iss — issuer. The agent's terminal_id. The verifier confirms it
	// matches its own terminal_id (caller-side check, not done here).
	Iss string `json:"iss"`
	// Aud — audience. Always "simsim-print" for agent handshake tokens.
	// The verifier confirms the expected value (caller-side check).
	Aud string `json:"aud"`
	// Iat — issued-at, Unix seconds.
	Iat int64 `json:"iat"`
	// Exp — expiration, Unix seconds. Verify rejects when exp <= now.
	Exp int64 `json:"exp"`
	// Scope — reserved for M14+ scope-restricted tokens. Always "print"
	// in M13. Emitted-but-unenforced: Verify does not gate on it.
	Scope string `json:"scope"`
}

// header is the fixed JWT header. alg is always HS256; typ always JWT.
type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// Verification error sentinels. The api middleware maps these to the
// wire error codes (JWT_INVALID / SIGNATURE_INVALID / JWT_EXPIRED).
var (
	// ErrMalformed — the token isn't three base64url segments, the
	// header isn't decodable, the header's alg/typ aren't HS256/JWT,
	// or the payload isn't decodable JSON. Maps to JWT_INVALID.
	ErrMalformed = errors.New("jwt: malformed token")
	// ErrBadSignature — the HMAC signature does not verify against the
	// supplied key. Maps to SIGNATURE_INVALID.
	ErrBadSignature = errors.New("jwt: signature verification failed")
	// ErrExpired — exp <= now, OR exp-iat exceeds MaxTTLSeconds (a
	// forged over-long TTL is treated as expired regardless of the
	// signature). Maps to JWT_EXPIRED.
	ErrExpired = errors.New("jwt: token expired")
)

// b64 is the URL-safe base64 encoding WITHOUT padding, per the JWT spec
// (RFC 7515 §2 — base64url, padding stripped).
var b64 = base64.RawURLEncoding

// Mint builds a signed HS256 token from the given claims and HMAC key.
// The key is the raw bytes of the agent's terminal token. The returned
// string is `base64url(header).base64url(payload).base64url(signature)`.
func Mint(claims Claims, key []byte) (string, error) {
	headerJSON, err := json.Marshal(header{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64.EncodeToString(headerJSON) + "." + b64.EncodeToString(payloadJSON)
	sig := sign(signingInput, key)
	return signingInput + "." + b64.EncodeToString(sig), nil
}

// Verify parses, signature-checks, and expiry-checks a token against the
// given HMAC key. `now` is injected (rather than read from time.Now
// internally) so tests can exercise expiry boundaries deterministically.
//
// Verify does NOT check `aud` or `iss` — those are application policy,
// checked by the caller (the api middleware) against its own expected
// audience and terminal_id. Verify owns the algorithm-agnostic integrity
// checks: structure, signature, expiry, TTL ceiling.
//
// On success returns the decoded Claims. On failure returns the zero
// Claims and one of ErrMalformed / ErrBadSignature / ErrExpired.
func Verify(token string, key []byte, now time.Time) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrMalformed
	}
	headerSeg, payloadSeg, sigSeg := parts[0], parts[1], parts[2]

	// Header — must be exactly {alg:HS256, typ:JWT}. Reject alg=none,
	// alg=RS256, anything else.
	headerJSON, err := b64.DecodeString(headerSeg)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var h header
	if err := json.Unmarshal(headerJSON, &h); err != nil {
		return Claims{}, ErrMalformed
	}
	if h.Alg != "HS256" || h.Typ != "JWT" {
		return Claims{}, ErrMalformed
	}

	// Signature — recompute over `header.payload` and constant-time
	// compare. This happens BEFORE decoding the payload so we never
	// act on unverified claim bytes.
	wantSig, err := b64.DecodeString(sigSeg)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	gotSig := sign(headerSeg+"."+payloadSeg, key)
	if subtle.ConstantTimeCompare(gotSig, wantSig) != 1 {
		return Claims{}, ErrBadSignature
	}

	// Payload — decode only after the signature verified.
	payloadJSON, err := b64.DecodeString(payloadSeg)
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var c Claims
	if err := json.Unmarshal(payloadJSON, &c); err != nil {
		return Claims{}, ErrMalformed
	}

	// Expiry — exp must be in the future.
	nowUnix := now.Unix()
	if c.Exp <= nowUnix {
		return Claims{}, ErrExpired
	}
	// Forged-TTL ceiling — even a validly-signed token with a TTL
	// beyond the spec ceiling is rejected. A compromised minting path
	// can't grant itself a long-lived token.
	if c.Exp-c.Iat > MaxTTLSeconds {
		return Claims{}, ErrExpired
	}

	return c, nil
}

// sign computes the HMAC-SHA256 of signingInput under key.
func sign(signingInput string, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingInput))
	return mac.Sum(nil)
}
