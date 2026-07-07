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

// ScalePLUFileFormat is the only `format` value this agent understands
// in a scale-plu-file response. Anything else means the cloud moved to
// a newer file format before this agent was updated — the worker skips
// the write rather than feeding an unknown format to the scale.
const ScalePLUFileFormat = "link69_plu_v1"

// ScalePLUFileResponse is the decoded payload of
// GET /api/pos-agent/scale-plu-file — the rendered PLU file the
// scale-sync worker mirrors to the local balance directory.
type ScalePLUFileResponse struct {
	// Format identifies the file dialect; see ScalePLUFileFormat.
	Format string `json:"format"`
	// PathHint is the destination path the cloud shows retailers in the
	// web UI. The worker writes to its own fixed path and warns if the
	// hint ever drifts from it.
	PathHint string `json:"path_hint"`
	// Content is the full PLU file body to write verbatim.
	Content string `json:"content"`
	// SHA256 is the hex digest of Content — used both to verify the
	// transfer and to skip rewrites of unchanged content.
	SHA256 string `json:"sha256"`
	// EntryCount is the number of PLU entries in Content.
	EntryCount int `json:"entry_count"`
	// Generated lists products whose scale code was auto-assigned
	// during this build (the route's write-on-read behavior).
	Generated []ScalePLUGenerated `json:"generated"`
	// Skipped lists products excluded from the file, so the agent can
	// log why the file shrank.
	Skipped []ScalePLUSkipped `json:"skipped"`
}

// ScalePLUGenerated is one product that received an auto-assigned PLU
// code during the file build. Wire shape per the route's explicit
// snake_case mapping (src/app/api/pos-agent/scale-plu-file/route.ts:
// generated.map(g => ({ product_id: g.productId, plu: g.plu }))).
type ScalePLUGenerated struct {
	ProductID string `json:"product_id"`
	PLU       string `json:"plu"`
}

// ScalePLUSkipped is one product the cloud could not render into the
// PLU file (missing price, missing code, ...). Wire shape per the
// route's explicit snake_case mapping (skipped.map(s =>
// ({ product_id: s.productId, reason: s.reason }))) — NOT the camelCase
// of the internal SkippedScaleRow type it derives from.
type ScalePLUSkipped struct {
	ProductID string `json:"product_id"`
	Reason    string `json:"reason"`
}
