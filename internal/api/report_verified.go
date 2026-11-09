package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

// reportVerifiedRequest is the body of POST /report-verified.
//
// The handler forwards verified + errorClass to the cloud's
// /api/pos-agent/print-verified using the agent's stored terminal
// token (loaded from s.secrets). Operator-facing UIs (the installer
// dialog and the web POS "Test print" button) only ever need to
// supply the operator's Oui/Non answer; the agent owns the cloud
// identity.
type reportVerifiedRequest struct {
	Verified   bool   `json:"verified"`
	ErrorClass string `json:"error_class,omitempty"`
}

// reportVerifiedResponseData mirrors the cloud's recorded-status
// shape so the loopback caller can react without a separate cloud
// fetch.
type reportVerifiedResponseData struct {
	Verified   bool   `json:"verified"`
	Recorded   bool   `json:"recorded"`
	ErrorClass string `json:"error_class,omitempty"`
}

// reportVerifiedTimeout caps the outbound cloud call. The handler
// MUST return promptly — the operator is staring at a wizard dialog
// or a confirmation modal. A stuck cloud should surface as
// CLOUD_UNREACHABLE within seconds, not minutes.
const reportVerifiedTimeout = 8 * time.Second

// handleReportVerified bridges the operator's test-print confirmation
// to the cloud's /api/pos-agent/print-verified endpoint. JWT-authed
// via requireAuth.
//
// Flow:
//  1. Parse the body — { verified: bool, error_class?: string }.
//  2. Load the agent's stored terminal token from s.secrets.
//     Missing secrets → 503 NOT_PAIRED (the operator can't have
//     confirmed a test print on an unpaired agent; this is a
//     defensive guard against client misuse).
//  3. Call s.cloud.ReportPrintVerified with the token + body.
//     Network / cloud 5xx / cloud auth failures → 502 CLOUD_UNREACHABLE.
//  4. Success → 200 { verified, recorded: true, error_class? }.
//
// No idempotency cache. Repeated calls to /report-verified hit the
// cloud each time; the cloud's UPDATE is itself idempotent
// (verified=true stamps NOW(); verified=false clears to NULL).
// Re-stamping with the current time is a feature (re-verifications
// after a printer swap are meaningful).
func (s *Server) handleReportVerified(w http.ResponseWriter, r *http.Request) {
	var req reportVerifiedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidReceipt, "invalid JSON: "+err.Error())
		return
	}

	if s.cloud == nil {
		// Production main always wires this; tests that don't need
		// the endpoint just don't wire it. Defensive 503 keeps the
		// signal honest if a future caller hits the route on a
		// half-configured Server.
		writeError(w, http.StatusServiceUnavailable, CodeCloudUnreachable,
			"cloud reporter not configured on this agent")
		return
	}

	secrets, err := s.secrets.Load()
	if err != nil {
		if errors.Is(err, config.ErrNoSecrets) {
			writeError(w, http.StatusServiceUnavailable, CodeNotPaired,
				"agent has no terminal token")
			return
		}
		s.logger.Error("report-verified: secret store load failed", "err", err.Error())
		writeError(w, http.StatusInternalServerError, CodeInternal,
			"secret store error")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), reportVerifiedTimeout)
	defer cancel()

	if err := s.cloud.ReportPrintVerified(ctx, secrets.TerminalToken, req.Verified, req.ErrorClass); err != nil {
		s.logger.Warn("report-verified: cloud call failed",
			"err", err.Error(),
			"verified", req.Verified,
			"error_class", req.ErrorClass,
		)
		writeError(w, http.StatusBadGateway, CodeCloudUnreachable, err.Error())
		return
	}

	s.logger.Info("report-verified: cloud accepted",
		"verified", req.Verified,
		"error_class", req.ErrorClass,
		"terminal_id", secrets.TerminalID,
	)

	writeOK(w, reportVerifiedResponseData{
		Verified:   req.Verified,
		Recorded:   true,
		ErrorClass: req.ErrorClass,
	})
}
