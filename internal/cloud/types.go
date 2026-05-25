package cloud

// PairResponse is the decoded payload of POST /api/pos-agent/pair on success.
// Contract §4.2 — the terminal_token is shown by the cloud only here.
type PairResponse struct {
	TerminalID    string `json:"terminal_id"`
	TerminalToken string `json:"terminal_token"`
	StoreID       string `json:"store_id"`
	StoreName     string `json:"store_name"`
	TerminalLabel string `json:"terminal_label"`
}

// HeartbeatRequest is the body of POST /api/pos-agent/heartbeat per contract §4.3.
type HeartbeatRequest struct {
	AgentVersion  string        `json:"agent_version"`
	OSVersion     string        `json:"os_version"`
	UptimeSeconds int64         `json:"uptime_seconds"`
	Printer       PrinterStatus `json:"printer"`
}

// PrinterStatus captures the printer transport state surfaced in heartbeat
// payloads. LastError is a pointer so the JSON `null` round-trips faithfully
// — the cloud distinguishes "no error" (null) from "" (an error of empty string).
type PrinterStatus struct {
	Configured bool    `json:"configured"`
	Reachable  bool    `json:"reachable"`
	Name       string  `json:"name"`
	LastError  *string `json:"last_error"`
}

// pairRequest is the wire body for POST /api/pos-agent/pair. Internal to
// keep the public Pair() signature positional per A1 spec.
type pairRequest struct {
	Code         string `json:"code"`
	AgentVersion string `json:"agent_version"`
	MachineID    string `json:"machine_id"`
}

// PrintVerifiedRequest is the body of POST /api/pos-agent/print-verified
// — operator-confirmed test-print outcome reported back to the cloud.
//
//   - Verified=true  → cloud stamps pos_terminals.last_print_verified_at = NOW().
//   - Verified=false → cloud CLEARS pos_terminals.last_print_verified_at to NULL.
//     A confirmed bad test print downgrades a previously-verified
//     terminal — the cloud can no longer assert this terminal prints
//     correctly.
//
// ErrorClass is an optional free-form tag the agent/web client uses to
// classify the failure mode (e.g. "OPERATOR_REJECTED",
// "AGENT_UNREACHABLE", "MAX_RETRIES_EXCEEDED"). The cloud logs it for
// ops visibility but does not branch on its value.
type PrintVerifiedRequest struct {
	Verified   bool   `json:"verified"`
	ErrorClass string `json:"error_class,omitempty"`
}
