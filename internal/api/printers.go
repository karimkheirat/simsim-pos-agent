package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// printersResponse is GET /printers: the printers Windows knows (virtual ones
// dropped) and the one this agent prints receipts on now ("" when none).
type printersResponse struct {
	Printers any    `json:"printers"`
	Current  string `json:"current"`
}

// handlePrinters lists the printers for the web app's first setup (simsim
// États G 14, 10 Nov 2026): "Recherche…", then the shop picks one.
func (s *Server) handlePrinters(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.ListPrinters == nil {
		writeError(w, http.StatusServiceUnavailable, CodePrinterListUnavailable, "printer listing unavailable")
		return
	}
	list, err := s.cfg.ListPrinters()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, CodePrinterListUnavailable, err.Error())
		return
	}
	current := ""
	if s.receiptPrinter != nil {
		current = s.receiptPrinter.Name()
	}
	writeOK(w, printersResponse{Printers: list, Current: current})
}

type choosePrinterRequest struct {
	Name string `json:"name"`
}

// handleChoosePrinter records the shop's pick and restarts the agent so it
// prints there. Only a printer Windows lists is accepted. The answer goes out
// before the restart; the web app waits for /health, then prints its test.
func (s *Server) handleChoosePrinter(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ChooseReceiptPrinter == nil || s.cfg.ListPrinters == nil {
		writeError(w, http.StatusServiceUnavailable, CodePrinterChoiceUnavailable, "printer choice unavailable")
		return
	}
	var req choosePrinterRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, CodePrinterUnknown, "a printer name is required")
		return
	}
	list, err := s.cfg.ListPrinters()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, CodePrinterListUnavailable, err.Error())
		return
	}
	known := false
	for _, p := range list {
		if p.Name == req.Name {
			known = true
			break
		}
	}
	if !known {
		writeError(w, http.StatusBadRequest, CodePrinterUnknown, "not a printer Windows lists")
		return
	}
	if err := s.cfg.ChooseReceiptPrinter(req.Name); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	writeOK(w, map[string]any{"chosen": req.Name, "restarting": true})
}
