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
	// M13 Track B PR 1 — distinguishes "no LABEL printer wired" from
	// the receipt-printer PRINTER_NOT_CONFIGURED case. Surfaced by
	// printerForIntent("label") and by handler intent='label' branches.
	CodeNoLabelPrinterConfigured = "NO_LABEL_PRINTER_CONFIGURED"
	CodePrinterOffline           = "PRINTER_OFFLINE"
	CodePrintFailed              = "PRINT_FAILED"
	CodeDrawerFailed             = "DRAWER_FAILED"
	CodeInvalidReceipt       = "INVALID_RECEIPT"
	CodeRateLimited          = "RATE_LIMITED" // unused until M3 rate-limit work
	CodeInternal             = "INTERNAL"

	// M13 A.1 — JWT handshake auth codes.
	//
	// CodeAgentUnpaired is distinct from CodeNotPaired: NOT_PAIRED is
	// returned by the legacy requireTerminalToken middleware; AGENT_UNPAIRED
	// is the GET /handshake response when no terminal token is on disk to
	// sign with. Same root cause, separate code per the M13 A.1 task spec
	// so the web client can branch on "the handshake endpoint itself says
	// unpaired" vs "a protected endpoint rejected me."
	CodeAgentUnpaired = "AGENT_UNPAIRED"
	// The four JWT-verification failure codes. The requireAuth middleware
	// maps jwt.ErrMalformed → JWT_INVALID, jwt.ErrBadSignature →
	// SIGNATURE_INVALID, jwt.ErrExpired → JWT_EXPIRED, and does the
	// aud / iss policy checks itself → AUDIENCE_MISMATCH / ISSUER_MISMATCH.
	CodeJWTInvalid       = "JWT_INVALID"
	CodeJWTExpired       = "JWT_EXPIRED"
	CodeSignatureInvalid = "SIGNATURE_INVALID"
	CodeAudienceMismatch = "AUDIENCE_MISMATCH"
	CodeIssuerMismatch   = "ISSUER_MISMATCH"
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
