package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/escpos"
	"github.com/karimkheirat/simsim-pos-agent/internal/receipt"
	"github.com/karimkheirat/simsim-pos-agent/internal/util"
)

// healthResponse mirrors POS_AGENT_SPEC.md §5.3. paired/store_id/terminal_id
// are M2; in M1 paired is hardcoded false and the binding fields are omitted.
type healthResponse struct {
	OK      bool          `json:"ok"`
	Version string        `json:"version"`
	Paired  bool          `json:"paired"`
	Printer printerHealth `json:"printer"`
}

type printerHealth struct {
	Configured bool   `json:"configured"`
	Reachable  bool   `json:"reachable"`
	Name       string `json:"name"`
}

// handleHealth — flat response per spec §5.3 (not the standard envelope).
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		OK:      true,
		Version: s.cfg.Version,
		Paired:  false, // M1: hardcoded
		Printer: s.printerHealth(),
	}
	writeJSON(w, http.StatusOK, resp)
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
// idempotency keyed on job_id.
//
// TODO M2: token auth — wrap this handler with auth middleware that
// requires X-Terminal-Token matching the stored terminal token before
// calling through to the body below.
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
	data, err := receipt.Render(req.Receipt, receipt.RenderOptions{OpenDrawerAfter: req.OpenDrawerAfter})
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

	data, err := receipt.Render(receiptFixture, receipt.RenderOptions{OpenDrawerAfter: true})
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
