package api

import (
	"encoding/json"
	"net/http"
)

// Error codes returned in the response envelope. Defined as constants so
// callers (POS web app, agentctl) can switch on them.
const (
	CodeUnauthenticated      = "UNAUTHENTICATED"
	CodeNotPaired            = "NOT_PAIRED"
	CodePrinterNotConfigured = "PRINTER_NOT_CONFIGURED"
	CodePrinterOffline       = "PRINTER_OFFLINE"
	CodePrintFailed          = "PRINT_FAILED"
	CodeDrawerFailed         = "DRAWER_FAILED"
	CodeInvalidReceipt       = "INVALID_RECEIPT"
	CodeRateLimited          = "RATE_LIMITED" // unused until M3 rate-limit work
	CodeInternal             = "INTERNAL"
)

type envelopeOK struct {
	OK   bool `json:"ok"`
	Data any  `json:"data"`
}

type envelopeError struct {
	OK    bool         `json:"ok"`
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeOK serializes data inside an {ok:true,data:...} envelope.
func writeOK(w http.ResponseWriter, data any) {
	if data == nil {
		data = struct{}{}
	}
	writeJSON(w, http.StatusOK, envelopeOK{OK: true, Data: data})
}

// writeError serializes an {ok:false,error:{code,message}} envelope at status.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, envelopeError{
		OK:    false,
		Error: errorPayload{Code: code, Message: message},
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}
