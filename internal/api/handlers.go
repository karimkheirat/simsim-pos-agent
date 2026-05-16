package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/capabilities"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
	"github.com/karimkheirat/simsim-pos-agent/internal/escpos"
	"github.com/karimkheirat/simsim-pos-agent/internal/receipt"
	"github.com/karimkheirat/simsim-pos-agent/internal/util"
)

// healthResponse mirrors POS_AGENT_SPEC.md §5.3. M2 (sub-task A5) wired
// the real paired/store_id/terminal_id from the secret store.
type healthResponse struct {
	OK         bool          `json:"ok"`
	Version    string        `json:"version"`
	Paired     bool          `json:"paired"`
	StoreID    string        `json:"store_id,omitempty"`
	TerminalID string        `json:"terminal_id,omitempty"`
	Printer    printerHealth `json:"printer"`
}

type printerHealth struct {
	Configured bool   `json:"configured"`
	Reachable  bool   `json:"reachable"`
	Name       string `json:"name"`
}

// handleHealth — flat response per spec §5.3 (not the standard envelope).
// Unauthenticated; the POS web app uses it to discover the agent and
// verify the bound terminal_id matches the cashier's session before
// sending /print requests.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		OK:      true,
		Version: s.cfg.Version,
		Printer: s.printerHealth(),
	}
	if s.secrets != nil {
		// Load failures (file IO, decryption error) are reported as
		// paired:false in the response (no secret-store detail leaks to
		// the wire) but are logged at warn level here so an operator
		// pulling agent.log on a pilot machine sees the cause directly,
		// rather than just "agent says unpaired." /health is loopback-only
		// so the leak concern is informational, not security.
		secrets, err := s.secrets.Load()
		switch {
		case err == nil:
			resp.Paired = true
			resp.StoreID = secrets.StoreID
			resp.TerminalID = secrets.TerminalID
		case errors.Is(err, config.ErrNoSecrets):
			// Genuinely unpaired — no warning, just paired:false.
		default:
			s.logger.Warn("/health: secret store load failed; reporting unpaired",
				"err", err.Error())
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// statusResponse extends the /health shape with auth-gated diagnostic
// fields. Returned only when the caller has a valid X-Terminal-Token,
// so we can include details that /health withholds.
type statusResponse struct {
	OK          bool          `json:"ok"`
	Version     string        `json:"version"`
	Paired      bool          `json:"paired"`
	StoreID     string        `json:"store_id"`
	TerminalID  string        `json:"terminal_id"`
	Printer     printerHealth `json:"printer"`
	LastPrintAt *time.Time    `json:"last_print_at"`
}

// handleStatus is the authenticated diagnostic endpoint. requireTerminalToken
// has already verified pairing + token, so secrets are guaranteed loadable
// here. M3 will extend with queue depths from the outbox.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	secrets, err := s.secrets.Load()
	if err != nil {
		// Would only happen if the store flipped between middleware and
		// handler — surface as INTERNAL.
		s.logger.Error("handleStatus: secret store load failed", "err", err.Error())
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erreur d'accès aux secrets.")
		return
	}

	var lastPrintAt *time.Time
	if t, ok := s.idem.LastSuccessAt(); ok {
		lastPrintAt = &t
	}

	writeJSON(w, http.StatusOK, statusResponse{
		OK:          true,
		Version:     s.cfg.Version,
		Paired:      true,
		StoreID:     secrets.StoreID,
		TerminalID:  secrets.TerminalID,
		Printer:     s.printerHealth(),
		LastPrintAt: lastPrintAt,
	})
}

func (s *Server) printerHealth() printerHealth {
	if s.printer == nil || s.printer.Name() == "" {
		return printerHealth{}
	}
	return printerHealth{
		Configured: true,
		Reachable:  s.printer.IsReachable(),
		Name:       s.printer.Name(),
	}
}

// capabilitiesForPrinter returns the PrinterCapabilities for the
// currently-configured printer, overlaying the agent's configured
// PaperWidthMM on top of the per-model lookup hint.
//
// M13 A.5a — the agent config is the source of truth for paper width
// (admins override the per-model default in config.json); the lookup
// table's PaperWidthMM is a hint for admin UI only. Cut + drawer + the
// barcode/codepage sets remain from the lookup.
//
// Returns the fallback capability set when the printer is unconfigured.
// Callers should gate on `s.printer == nil || s.printer.Name() == ""`
// BEFORE calling this if they want to surface PRINTER_NOT_CONFIGURED;
// passing through is correct for /print + /test-print which have
// their own printer-state checks.
func (s *Server) capabilitiesForPrinter() capabilities.PrinterCapabilities {
	var name string
	if s.printer != nil {
		name = s.printer.Name()
	}
	caps := capabilities.Lookup(name)
	caps.PaperWidthMM = s.cfg.PaperWidthMM
	return caps
}

// handleCapabilities — GET /capabilities. JWT-authed via requireAuth.
//
// Returns the PrinterCapabilities for the currently-configured printer,
// or 503 PRINTER_NOT_CONFIGURED when no printer is bound. The response
// is wrapped in the standard {ok:true, data:...} envelope; M13 A.5a
// matches /print + /status (auth-protected endpoints), not /health
// (the agent-discovery flat shape).
//
// Spec: M13_BUILD_SPEC.md §3.A.5 + docs/agent-handshake-protocol.md §3.6.
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.printer == nil || s.printer.Name() == "" {
		writeError(w, http.StatusServiceUnavailable, CodePrinterNotConfigured, "no printer configured")
		return
	}
	writeOK(w, s.capabilitiesForPrinter())
}

// printRequest is the body of POST /print.
type printRequest struct {
	JobID           string          `json:"job_id"`
	IdempotencyKey  string          `json:"idempotency_key"`
	Receipt         receipt.Receipt `json:"receipt"`
	OpenDrawerAfter bool            `json:"open_drawer_after"`
}

type printResponseData struct {
	JobID      string `json:"job_id"`
	BytesSent  int    `json:"bytes_sent"`
	DurationMs int64  `json:"duration_ms"`
}

// handlePrint renders a Receipt and submits it to the printer with
// idempotency keyed on job_id. Auth is enforced by requireTerminalToken,
// wired in api.go's mux registration.
func (s *Server) handlePrint(w http.ResponseWriter, r *http.Request) {
	var req printRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidReceipt, "invalid JSON: "+err.Error())
		return
	}
	if req.JobID == "" {
		writeError(w, http.StatusBadRequest, CodeInvalidReceipt, "job_id required")
		return
	}

	// Idempotency replay — return cached response verbatim.
	if cached, ok := s.idem.Get(req.JobID); ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(cached.Status)
		_, _ = w.Write(cached.Body)
		return
	}

	// Render before checking printer state — surfaces validation errors
	// (400) instead of pre-empting them with 503 if the printer is also
	// unreachable. Cheaper failure first.
	//
	// M13 A.5a — RenderOptions now carry per-render PaperWidthMM
	// (from agent config) and CutSupported (from the per-model
	// capabilities lookup). For an unconfigured printer the capability
	// fallback applies; the renderer's defaults preserve pre-A.5a
	// behavior for 80mm + cut.
	data, err := receipt.Render(req.Receipt, receipt.RenderOptions{
		OpenDrawerAfter: req.OpenDrawerAfter,
		PaperWidthMM:    s.cfg.PaperWidthMM,
		CutSupported:    s.capabilitiesForPrinter().CutSupported,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidReceipt, err.Error())
		return
	}

	if s.printer == nil {
		writeError(w, http.StatusServiceUnavailable, CodePrinterNotConfigured, "no printer configured")
		return
	}
	if !s.printer.IsReachable() {
		writeError(w, http.StatusServiceUnavailable, CodePrinterOffline, "printer not reachable")
		return
	}

	start := time.Now()
	if err := s.printer.Print(req.JobID, data); err != nil {
		writeError(w, http.StatusInternalServerError, CodePrintFailed, err.Error())
		return
	}
	duration := time.Since(start)

	respBody, _ := json.Marshal(envelopeOK{
		OK: true,
		Data: printResponseData{
			JobID:      req.JobID,
			BytesSent:  len(data),
			DurationMs: duration.Milliseconds(),
		},
	})

	// Cache BEFORE writing to wire — protects against double-print if the
	// wire write fails after the printer side effect has already occurred.
	s.idem.Set(req.JobID, Result{
		JobID:  req.JobID,
		Status: http.StatusOK,
		Body:   respBody,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBody)

	s.logger.Info("print success",
		"job_id", req.JobID,
		"bytes", len(data),
		"duration_ms", duration.Milliseconds(),
	)
}

// handleTestPrint renders the M1 fixture and prints it. open_drawer_after
// defaults to true so the cash-drawer kick path is exercised.
func (s *Server) handleTestPrint(w http.ResponseWriter, r *http.Request) {
	if s.printer == nil {
		writeError(w, http.StatusServiceUnavailable, CodePrinterNotConfigured, "no printer configured")
		return
	}
	if !s.printer.IsReachable() {
		writeError(w, http.StatusServiceUnavailable, CodePrinterOffline, "printer not reachable")
		return
	}

	// M13 A.5a — /test-print honors the same per-render options as
	// /print so the cashier's "test print" matches what a real receipt
	// would look like on the configured printer.
	data, err := receipt.Render(receiptFixture, receipt.RenderOptions{
		OpenDrawerAfter: true,
		PaperWidthMM:    s.cfg.PaperWidthMM,
		CutSupported:    s.capabilitiesForPrinter().CutSupported,
	})
	if err != nil {
		// Fixture is canonical; render failure is an internal bug.
		writeError(w, http.StatusInternalServerError, CodeInternal, "render fixture: "+err.Error())
		return
	}

	start := time.Now()
	if err := s.printer.Print("test-print", data); err != nil {
		writeError(w, http.StatusInternalServerError, CodePrintFailed, err.Error())
		return
	}
	duration := time.Since(start)

	writeOK(w, printResponseData{
		JobID:      "test-print",
		BytesSent:  len(data),
		DurationMs: duration.Milliseconds(),
	})

	s.logger.Info("test-print success",
		"bytes", len(data),
		"duration_ms", duration.Milliseconds(),
	)
}

// handleDrawerOpen sends a single ESC p pulse via the printer transport.
func (s *Server) handleDrawerOpen(w http.ResponseWriter, r *http.Request) {
	if s.printer == nil {
		writeError(w, http.StatusServiceUnavailable, CodePrinterNotConfigured, "no printer configured")
		return
	}
	if !s.printer.IsReachable() {
		writeError(w, http.StatusServiceUnavailable, CodePrinterOffline, "printer not reachable")
		return
	}

	id, err := util.NewUUIDv4()
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "generate job id: "+err.Error())
		return
	}
	jobName := "drawer-kick-" + id

	if err := s.printer.Print(jobName, escpos.DrawerKick()); err != nil {
		writeError(w, http.StatusInternalServerError, CodeDrawerFailed, err.Error())
		return
	}

	writeOK(w, struct{}{})

	s.logger.Info("drawer kick", "job", jobName)
}
