package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/config"
	"github.com/karimkheirat/simsim-pos-agent/internal/jwt"
)

// handshakeTTL is the lifetime of a minted handshake JWT. 15 minutes,
// fixed by M13's Phase 1 resolution. Mirrors jwt.MaxTTLSeconds (the
// verifier's hard ceiling) — kept as a separate named constant here so
// the minting side reads intentionally rather than reusing the
// verifier's "maximum" as if it were the "target."
const handshakeTTL = 15 * time.Minute

// handshakeAudience is the `aud` claim stamped on every minted token
// and required by requireAuth on verification. Per the M13 A.1 task
// spec. (Note: docs/agent-handshake-protocol.md §4.2 currently says
// "simsim-pos-agent" — that doc drift is flagged for reconciliation;
// the A.1 task spec value wins here.)
const handshakeAudience = "simsim-print"

// handshakeResponse is the flat (non-enveloped) success body for
// GET /handshake. Flat to match the /health precedent — both are
// agent-discovery endpoints the POS web app reads directly. Error
// responses still use the standard {ok:false,error:{...}} envelope
// via writeError, consistent with every other error path in the
// package.
type handshakeResponse struct {
	JWT        string `json:"jwt"`
	ExpiresAt  string `json:"expires_at"` // ISO 8601 / RFC 3339, UTC
	TerminalID string `json:"terminal_id"`
}

// handleHandshake mints a short-lived JWT the POS web app uses to
// authenticate subsequent /print + /test-print calls.
//
// Auth: none at the handler level. The endpoint is reachable only via
// the loopback listener + checkLoopbackMiddleware — being able to
// reach 127.0.0.1:47291 at all IS the trust boundary for the bootstrap
// handshake. The minted token is then signed with the terminal token
// (which never leaves the agent), so a caller who can hit /handshake
// still can't forge tokens for a different terminal.
//
// 503 AGENT_UNPAIRED when the secret store has no terminal token to
// sign with — the agent hasn't completed pairing.
func (s *Server) handleHandshake(w http.ResponseWriter, r *http.Request) {
	if s.secrets == nil {
		s.logHandshake("", time.Time{}, false, "no secret store configured")
		writeError(w, http.StatusServiceUnavailable, CodeAgentUnpaired,
			"Agent non jumelé. Exécutez 'agentctl pair'.")
		return
	}

	secrets, err := s.secrets.Load()
	if errors.Is(err, config.ErrNoSecrets) {
		s.logHandshake("", time.Time{}, false, "agent unpaired")
		writeError(w, http.StatusServiceUnavailable, CodeAgentUnpaired,
			"Agent non jumelé. Exécutez 'agentctl pair'.")
		return
	}
	if err != nil {
		s.logHandshake("", time.Time{}, false, "secret store load failed: "+err.Error())
		s.logger.Error("handleHandshake: secret store load failed", "err", err.Error())
		writeError(w, http.StatusInternalServerError, CodeInternal,
			"Erreur d'accès aux secrets.")
		return
	}

	now := time.Now()
	exp := now.Add(handshakeTTL)
	claims := jwt.Claims{
		Iss:   secrets.TerminalID,
		Aud:   handshakeAudience,
		Iat:   now.Unix(),
		Exp:   exp.Unix(),
		Scope: "print",
	}
	// HMAC key = the terminal token's bytes. Both Mint here and Verify
	// in requireAuth derive the key identically — []byte(TerminalToken)
	// — so the round-trip is consistent regardless of how the token is
	// encoded on disk.
	token, err := jwt.Mint(claims, []byte(secrets.TerminalToken))
	if err != nil {
		s.logHandshake(secrets.TerminalID, exp, false, "jwt mint failed: "+err.Error())
		s.logger.Error("handleHandshake: jwt mint failed", "err", err.Error())
		writeError(w, http.StatusInternalServerError, CodeInternal,
			"Erreur de génération du jeton.")
		return
	}

	s.logHandshake(secrets.TerminalID, exp, true, "")
	writeJSON(w, http.StatusOK, handshakeResponse{
		JWT:        token,
		ExpiresAt:  exp.UTC().Format(time.RFC3339),
		TerminalID: secrets.TerminalID,
	})
}

// logHandshake emits one structured slog record per /handshake call —
// terminal_id, timestamp, the minted token's exp, success flag, and
// (on failure) the reason.
//
// M13 A.1 task item 4 also calls for forwarding these events to the
// cloud "via the existing heartbeat mechanism." That forwarding needs
// a heartbeat-payload schema change spanning the agent AND the cloud's
// /api/pos-agent/heartbeat endpoint — a cross-repo change outside the
// agent-only A.1 boundary, and docs/agent-handshake-protocol.md §9
// itself scopes handshake-log cloud-forwarding as deferred. A.1 ships
// the structured LOCAL log; cloud-forwarding is flagged as a follow-up.
func (s *Server) logHandshake(terminalID string, exp time.Time, success bool, reason string) {
	attrs := []any{
		"event", "handshake",
		"terminal_id", terminalID,
		"timestamp", time.Now().UTC().Format(time.RFC3339),
		"success", success,
	}
	if !exp.IsZero() {
		attrs = append(attrs, "jwt_exp", exp.UTC().Format(time.RFC3339))
	}
	if reason != "" {
		attrs = append(attrs, "reason", reason)
	}
	if success {
		s.logger.Info("handshake minted", attrs...)
	} else {
		s.logger.Warn("handshake refused", attrs...)
	}
}
