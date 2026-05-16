package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/capabilities"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
	"github.com/karimkheirat/simsim-pos-agent/internal/escpos"
	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
	"github.com/karimkheirat/simsim-pos-agent/internal/receipt"
	"github.com/karimkheirat/simsim-pos-agent/internal/util"
)

// healthResponse mirrors POS_AGENT_SPEC.md §5.3. M2 (sub-task A5) wired
// the real paired/store_id/terminal_id from the secret store.
//
// M13 Track B PR 1 — additive Label sibling. Existing `printer` field
// is the receipt printer (back-compat for pre-B clients); new `label`
// is null when no label printer is wired, populated when one is.
type healthResponse struct {
	OK         bool           `json:"ok"`
	Version    string         `json:"version"`
	Paired     bool           `json:"paired"`
	StoreID    string         `json:"store_id,omitempty"`
	TerminalID string         `json:"terminal_id,omitempty"`
	Printer    printerHealth  `json:"printer"`
	Label      *printerHealth `json:"label,omitempty"`
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
		Printer: s.receiptPrinterHealth(),
		Label:   s.labelHealth(),
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
		Printer:     s.receiptPrinterHealth(),
		LastPrintAt: lastPrintAt,
	})
}

// receiptPrinterHealth reports the receipt (ESC/POS) printer's status.
// M13 Track B PR 1 — renamed from printerHealth as part of the
// two-printer split; labelHealth is the counterpart for the TSPL
// label printer.
func (s *Server) receiptPrinterHealth() printerHealth {
	return statusFor(s.receiptPrinter)
}

// labelHealth reports the TSPL label printer's status, or nil when no
// label printer is configured. Returned via the additive `label` key
// in /health (omitempty drops the nil for back-compat).
func (s *Server) labelHealth() *printerHealth {
	if s.labelPrinter == nil || s.labelPrinter.Name() == "" {
		return nil
	}
	h := statusFor(s.labelPrinter)
	return &h
}

// statusFor is the shared shape used by receiptPrinterHealth and
// labelHealth. Returns the zero printerHealth (configured=false) for
// a nil or unnamed printer; otherwise reports Reachable + Name.
func statusFor(p printer.Printer) printerHealth {
	if p == nil || p.Name() == "" {
		return printerHealth{}
	}
	return printerHealth{
		Configured: true,
		Reachable:  p.IsReachable(),
		Name:       p.Name(),
	}
}

// capabilitiesForPrinter returns the PrinterCapabilities for the
// currently-configured RECEIPT printer, overlaying the agent's
// configured PaperWidthMM on top of the per-model lookup hint.
//
// M13 A.5a — the agent config is the source of truth for paper width
// (admins override the per-model default in config.json); the lookup
// table's PaperWidthMM is a hint for admin UI only. Cut + drawer + the
// barcode/codepage sets remain from the lookup.
//
// Returns the fallback capability set when the printer is unconfigured.
// Callers should gate on `s.receiptPrinter == nil || s.receiptPrinter.Name() == ""`
// BEFORE calling this if they want to surface PRINTER_NOT_CONFIGURED;
// passing through is correct for /print + /test-print which have
// their own printer-state checks.
func (s *Server) capabilitiesForPrinter() capabilities.PrinterCapabilities {
	var name string
	if s.receiptPrinter != nil {
		name = s.receiptPrinter.Name()
	}
	caps := capabilities.Lookup(name)
	caps.PaperWidthMM = s.cfg.PaperWidthMM
	return caps
}

// labelCapabilities is the wire-shape extension of the per-model TSPL
// capabilities row with the agent's tspl_dialect choice attached. The
// web client uses dialect to mirror the agent's EAN-13 hyphen choice
// in any preview UI.
type labelCapabilities struct {
	capabilities.PrinterCapabilities
	TSPLDialect string `json:"tspl_dialect"`
}

// capabilitiesForLabelPrinter returns the labelCapabilities for the
// currently-configured LABEL printer. Returns nil when no label
// printer is wired (sibling key in the additive /capabilities shape
// becomes null per the additive contract — Q2 decision).
func (s *Server) capabilitiesForLabelPrinter() *labelCapabilities {
	if s.labelPrinter == nil || s.labelPrinter.Name() == "" {
		return nil
	}
	caps := capabilities.LookupLabel(s.labelPrinter.Name())
	return &labelCapabilities{
		PrinterCapabilities: caps,
		TSPLDialect:         s.cfg.TSPLDialect,
	}
}

// capabilitiesResponse is the wire shape of GET /capabilities's data
// payload. M13 Track B PR 1 additive shape (Q2 decision):
//   - All pre-existing receipt fields stay at the top level for
//     back-compat with pre-B web clients (they unmarshal the row
//     directly into PrinterCapabilities).
//   - A new `label` sibling carries the TSPL label printer's caps,
//     or null if none is wired.
//
// Pre-B clients that ignore unknown keys (the standard JSON behaviour)
// see the original shape unchanged. Post-B clients that know to look
// for `label` get the extended view.
type capabilitiesResponse struct {
	capabilities.PrinterCapabilities
	Label *labelCapabilities `json:"label"`
}

// handleCapabilities — GET /capabilities. JWT-authed via requireAuth.
//
// Returns the PrinterCapabilities for the currently-configured RECEIPT
// printer + the additive `label` sibling for the configured label
// printer (null when no label printer is wired). Returns 503
// PRINTER_NOT_CONFIGURED when NO receipt printer is bound — a label-
// only deployment isn't a valid v1 shape (receipt is the primary
// surface; label is optional).
//
// Spec: M13_BUILD_SPEC.md §3.A.5 + docs/agent-handshake-protocol.md §3.6.
// M13 Track B PR 1 — additive label sibling per Q2 decision.
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.receiptPrinter == nil || s.receiptPrinter.Name() == "" {
		writeError(w, http.StatusServiceUnavailable, CodePrinterNotConfigured, "no printer configured")
		return
	}
	writeOK(w, capabilitiesResponse{
		PrinterCapabilities: s.capabilitiesForPrinter(),
		Label:               s.capabilitiesForLabelPrinter(),
	})
}

// printerForIntent returns the printer for the requested intent and a
// status / error tuple suitable for HTTP responses when the requested
// printer is not configured.
//
// Intents:
//   - "receipt" (default) → s.receiptPrinter, 503 PRINTER_NOT_CONFIGURED on absence
//   - "label"             → s.labelPrinter,   503 NO_LABEL_PRINTER_CONFIGURED on absence
//
// Unknown intent strings collapse to "receipt" — defensive default
// matching the pre-Track-B behaviour.
//
// Callers receive printer, http status, error code, error message;
// `p` is nil when status > 0 (caller should writeError with the
// returned status/code/message and return).
func (s *Server) printerForIntent(intent string) (p printer.Printer, status int, code, message string) {
	switch intent {
	case "label":
		if s.labelPrinter == nil || s.labelPrinter.Name() == "" {
			return nil, http.StatusServiceUnavailable, CodeNoLabelPrinterConfigured, "no label printer configured"
		}
		return s.labelPrinter, 0, "", ""
	default: // "receipt", "", anything else
		if s.receiptPrinter == nil || s.receiptPrinter.Name() == "" {
			return nil, http.StatusServiceUnavailable, CodePrinterNotConfigured, "no printer configured"
		}
		return s.receiptPrinter, 0, "", ""
	}
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

	if s.receiptPrinter == nil {
		writeError(w, http.StatusServiceUnavailable, CodePrinterNotConfigured, "no printer configured")
		return
	}
	if !s.receiptPrinter.IsReachable() {
		writeError(w, http.StatusServiceUnavailable, CodePrinterOffline, "printer not reachable")
		return
	}

	start := time.Now()
	if err := s.receiptPrinter.Print(req.JobID, data); err != nil {
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
//
// M13 Track B PR 1 — accepts ?intent=receipt|label. Default (receipt)
// renders the M1 ESC/POS fixture and prints to the receipt printer
// (existing behaviour, byte-identical). intent=label is reserved for
// PR 2 (label test-print pipeline); for PR 1 it returns 501 to surface
// the not-yet-implemented contract without ever sending random bytes
// to a label printer.
func (s *Server) handleTestPrint(w http.ResponseWriter, r *http.Request) {
	intent := r.URL.Query().Get("intent")
	if intent == "" {
		intent = "receipt"
	}

	if intent == "label" {
		// Label test-print pipeline ships in M13 Track B PR 2 alongside
		// the /print-label endpoint and the renderable label fixtures.
		// Until then, surface the contract intent without producing
		// random TSPL bytes (which a misconfigured printer might
		// interpret as a setup command).
		writeError(w, http.StatusNotImplemented, CodeInternal, "intent=label not implemented in PR 1 (ships PR 2)")
		return
	}

	p, status, code, message := s.printerForIntent(intent)
	if status != 0 {
		writeError(w, status, code, message)
		return
	}
	if !p.IsReachable() {
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
	if err := p.Print("test-print", data); err != nil {
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
		"intent", intent,
		"bytes", len(data),
		"duration_ms", duration.Milliseconds(),
	)
}

// handleDrawerOpen sends a single ESC p pulse via the printer transport.
func (s *Server) handleDrawerOpen(w http.ResponseWriter, r *http.Request) {
	if s.receiptPrinter == nil {
		writeError(w, http.StatusServiceUnavailable, CodePrinterNotConfigured, "no printer configured")
		return
	}
	if !s.receiptPrinter.IsReachable() {
		writeError(w, http.StatusServiceUnavailable, CodePrinterOffline, "printer not reachable")
		return
	}

	id, err := util.NewUUIDv4()
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "generate job id: "+err.Error())
		return
	}
	jobName := "drawer-kick-" + id

	if err := s.receiptPrinter.Print(jobName, escpos.DrawerKick()); err != nil {
		writeError(w, http.StatusInternalServerError, CodeDrawerFailed, err.Error())
		return
	}

	writeOK(w, struct{}{})

	s.logger.Info("drawer kick", "job", jobName)
}
