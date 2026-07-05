// Package config loads, validates, and supplies defaults for the agent's
// runtime configuration. Mirrors POS_AGENT_SPEC.md §5.2.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// Config is the on-disk JSON shape stored at %ProgramData%\Simsim\POSAgent\config.json
// (Windows) or ./config.json (other). All fields are optional; missing
// fields fall back to Defaults().
type Config struct {
	Version    string `json:"version"`
	ListenPort int    `json:"listen_port"`

	// CloudBaseURL is the base for /api/pos-agent/* calls. Added in M2
	// for pair/heartbeat/unpair; M1 had no cloud-side dependencies.
	CloudBaseURL string `json:"cloud_base_url"`

	// HeartbeatSeconds is the interval (seconds) between cloud
	// heartbeats. Default 300 (5min). Added in M2 (sub-task A7).
	HeartbeatSeconds int `json:"heartbeat_seconds"`

	// PrinterName is the LEGACY receipt-printer-only field. Kept for
	// back-compat with deployed config.json files; new fields are
	// ReceiptPrinterName + LabelPrinterName below.
	//
	// Back-compat mapping (in Load):
	//   - If PrinterName is set AND ReceiptPrinterName is empty,
	//     PrinterName's value is mirrored into ReceiptPrinterName.
	//   - If both are set, ReceiptPrinterName wins (operator was
	//     explicit about the two-printer split).
	//
	// Operators migrating to the two-printer config (M13 Track B) should
	// drop PrinterName and use ReceiptPrinterName + LabelPrinterName.
	PrinterName string `json:"printer_name"`

	// ReceiptPrinterName is the ESC/POS receipt printer name (the
	// thermal 80mm/58mm printer). Added in M13 Track B PR 1 alongside
	// the two-printer architecture split. Empty = no receipt printer
	// configured (agent reports PRINTER_NOT_CONFIGURED on /print).
	ReceiptPrinterName string `json:"receipt_printer_name"`

	// LabelPrinterName is the TSPL label printer name (the small
	// 40x30mm / 50x40mm / 60x40mm thermal label printer). Empty = no
	// label printer configured (agent will report
	// NO_LABEL_PRINTER_CONFIGURED on /print-label, added in PR 2).
	LabelPrinterName string `json:"label_printer_name"`

	// TSPLDialect selects the EAN-13 command variant for the label
	// printer. Most TSPL2 printers use "EAN13" (no hyphen); Rongta
	// firmware requires "EAN-13" (hyphen). Valid values: "standard"
	// (default) or "rongta". Anything else is rejected by Validate.
	//
	// Other TSPL commands are dialect-agnostic — this field only
	// affects the BarcodeEAN13 builder in internal/tspl.
	TSPLDialect string `json:"tspl_dialect"`

	LogLevel       string   `json:"log_level"`
	AllowedOrigins []string `json:"allowed_origins"`

	// PaperWidthMM is the thermal paper width the renderer formats
	// for. Added in M13 A.5a. Valid values: 58 or 80 (millimetres).
	// Maps to receiptWidth=32 (58mm) or receiptWidth=42 (80mm) in
	// internal/receipt/render.go. Defaults to 80.
	//
	// Cap-aware: the value reported via GET /capabilities is this
	// configured width, NOT the per-model lookup hint in
	// internal/capabilities — admins override hardware defaults here.
	PaperWidthMM int `json:"paper_width_mm"`

	// ReceiptPrinterLanguage selects the command language the receipt
	// renderer emits: "escpos" (default) for ESC/POS receipt printers,
	// "tspl" for TSPL-only label printers (e.g. the Gprinter GP-3150TN)
	// that cannot parse ESC/POS. Set per-printer by the setup UI's guided
	// test, NOT a user-facing toggle. Validated to {escpos, tspl}.
	ReceiptPrinterLanguage string `json:"receipt_printer_language"`

	// ReceiptWidthDots is the printable receipt width in dots, used by the
	// TSPL receipt renderer for centering + right-alignment and the SIZE
	// width. 0 = derive from PaperWidthMM (58mm→384, 80mm→576 at 203dpi).
	// TUNABLE against real hardware; ignored by the ESC/POS path.
	ReceiptWidthDots int `json:"receipt_width_dots"`

	// DPI is the printer resolution (dots/inch) used for mm↔dots math in
	// the TSPL receipt renderer. Default 203 (the thermal standard); the
	// GP-3150TN may be 203 or 300 — TUNABLE. Ignored by the ESC/POS path.
	DPI int `json:"dpi"`

	// ScaleIP + ScalePort locate the Aclas LS2-series label scale on the
	// store LAN for PLU sync (POST /scale/sync-plu). Both empty/zero =
	// no scale configured (endpoint returns NO_SCALE_CONFIGURED). When
	// one is set the other is required — Validate enforces the pairing
	// so a half-configured scale fails loudly at startup, not at the
	// first sync. There is no default port: the LS2 manual documents no
	// fixed listening port, so the operator supplies what the scale's
	// network setup screen shows.
	ScaleIP   string `json:"scale_ip"`
	ScalePort int    `json:"scale_port"`
}

// EffectiveReceiptWidthDots returns the printable receipt width in dots
// for the TSPL receipt renderer: the explicit ReceiptWidthDots when set
// (>0), otherwise derived from PaperWidthMM (58mm→384, 80mm→576). The
// derived values assume the 203dpi thermal standard and a ~full-width
// printable area; calibrate ReceiptWidthDots against the real printer if
// the margins are off.
func (c Config) EffectiveReceiptWidthDots() int {
	if c.ReceiptWidthDots > 0 {
		return c.ReceiptWidthDots
	}
	if c.PaperWidthMM == 58 {
		return 384
	}
	return 576
}

// Sentinel errors returned by Load. Callers can detect via errors.Is and
// decide how loud to be — ErrConfigMissing is informational (defaults
// kick in), ErrConfigMalformed is fatal.
var (
	ErrConfigMissing   = errors.New("config: file not found")
	ErrConfigMalformed = errors.New("config: malformed JSON")
)

// Defaults returns the M1 defaults from the spec.
func Defaults() Config {
	return Config{
		Version:          "0.1.0", // informational only; build-time Version constant in main() overrides
		ListenPort:       47291,
		CloudBaseURL:     "https://opensimsim.co",
		HeartbeatSeconds: 300,
		PrinterName:      "",
		// M13 Track B PR 1 — new two-printer fields default empty;
		// operators set one or both. Defaults to DialectStandard for
		// label printers since the majority of pilot-targeted TSPL
		// printers (Xprinter, Aclas, TSC) are standard-dialect.
		ReceiptPrinterName: "",
		LabelPrinterName:   "",
		TSPLDialect:        "standard",
		LogLevel:           "info",
		AllowedOrigins: []string{
			"https://web-production-6bb4d.up.railway.app",
			"https://opensimsim.co",
		},
		// M13 A.5a — 80mm is the Algeria-realistic default. The vast
		// majority of pilot-targeted printers ship as 80mm; 58mm is
		// for smaller-format retail (newsstand, deli labels) which
		// the agent supports but doesn't default to.
		PaperWidthMM: 80,
		// TSPL receipt path — escpos is the default receipt language;
		// tspl is opted into per-printer by the setup UI. ReceiptWidthDots
		// 0 means "derive from PaperWidthMM"; DPI 203 is the thermal
		// standard. Both are TUNABLE for real-printer calibration.
		ReceiptPrinterLanguage: "escpos",
		ReceiptWidthDots:       0,
		DPI:                    203,
		// Scale sync — no scale by default; operators with an LS2 set
		// both fields (agent write-config --scale-ip/--scale-port).
		ScaleIP:   "",
		ScalePort: 0,
	}
}

// Load reads the config file at path. If the file does not exist, Load
// returns Defaults() and an error wrapping ErrConfigMissing — callers
// should treat that as a warning, not a fatal. Malformed JSON or unknown
// fields return ErrConfigMalformed.
//
// Present JSON fields override the corresponding default; absent fields
// retain their default value (standard json.Decode-into-prefilled-struct
// semantics).
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), fmt.Errorf("%w: %s", ErrConfigMissing, path)
	}
	if err != nil {
		return Defaults(), fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	cfg := Defaults()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Defaults(), fmt.Errorf("%w: %s: %v", ErrConfigMalformed, path, err)
	}
	applyPrinterBackCompat(&cfg)
	return cfg, nil
}

// applyPrinterBackCompat mirrors the legacy PrinterName field into
// ReceiptPrinterName when only the legacy field is set in the loaded
// config. Pre-M13-B deployments wrote `"printer_name": "<receipt>"`;
// those configs MUST keep working after the two-printer refactor.
//
// Mapping (per Karim Q1 decision):
//   - PrinterName set, ReceiptPrinterName empty → mirror.
//   - Both set                                  → ReceiptPrinterName wins.
//   - Neither set                                → both stay empty.
func applyPrinterBackCompat(c *Config) {
	if c.ReceiptPrinterName == "" && c.PrinterName != "" {
		c.ReceiptPrinterName = c.PrinterName
	}
}

// Validate enforces invariants the rest of the agent assumes:
//   - Version non-empty.
//   - ListenPort in [1, 65535].
//   - LogLevel in {debug, info, warn, error}.
//   - Every allowed origin is non-empty.
func Validate(c Config) error {
	if c.Version == "" {
		return errors.New("config: version is required")
	}
	if c.ListenPort < 1 || c.ListenPort > 65535 {
		return fmt.Errorf("config: listen_port %d out of range (1..65535)", c.ListenPort)
	}
	if c.HeartbeatSeconds <= 0 {
		return fmt.Errorf("config: heartbeat_seconds %d must be > 0", c.HeartbeatSeconds)
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: invalid log_level %q (want debug|info|warn|error)", c.LogLevel)
	}
	for i, o := range c.AllowedOrigins {
		if o == "" {
			return fmt.Errorf("config: allowed_origins[%d] is empty", i)
		}
	}
	// M13 A.5a — only 58 or 80 are valid widths. Anything else (76mm,
	// 112mm, etc.) is rejected loudly rather than silently rendering
	// at the wrong width. If we ever support more widths, this is the
	// one place to extend.
	if c.PaperWidthMM != 58 && c.PaperWidthMM != 80 {
		return fmt.Errorf("config: paper_width_mm %d invalid (want 58 or 80)", c.PaperWidthMM)
	}
	// M13 Track B PR 1 — tspl_dialect gates the BarcodeEAN13 hyphen split.
	// "standard" is the TSC/Xprinter/Aclas baseline; "rongta" is the
	// Rongta-firmware variant. Anything else is rejected so a typo can't
	// silently print broken barcodes.
	switch c.TSPLDialect {
	case "standard", "rongta":
	default:
		return fmt.Errorf("config: tspl_dialect %q invalid (want standard or rongta)", c.TSPLDialect)
	}
	// TSPL receipt path — receipt_printer_language gates ESC/POS vs TSPL
	// receipt rendering. dpi must be positive (mm↔dots math); width_dots
	// 0 is valid and means "derive from paper_width_mm".
	switch c.ReceiptPrinterLanguage {
	case "escpos", "tspl":
	default:
		return fmt.Errorf("config: receipt_printer_language %q invalid (want escpos or tspl)", c.ReceiptPrinterLanguage)
	}
	if c.DPI <= 0 {
		return fmt.Errorf("config: dpi %d must be > 0", c.DPI)
	}
	if c.ReceiptWidthDots < 0 {
		return fmt.Errorf("config: receipt_width_dots %d must be >= 0 (0 = derive from paper_width_mm)", c.ReceiptWidthDots)
	}
	// Scale sync — scale_ip and scale_port travel together. A lone IP
	// (or lone port) is a half-configured scale: reject at startup
	// rather than surfacing as a confusing dial error mid-sync.
	if (c.ScaleIP == "") != (c.ScalePort == 0) {
		return fmt.Errorf("config: scale_ip and scale_port must be set together (got ip %q, port %d)", c.ScaleIP, c.ScalePort)
	}
	if c.ScaleIP != "" {
		if net.ParseIP(c.ScaleIP) == nil {
			return fmt.Errorf("config: scale_ip %q is not a valid IP address", c.ScaleIP)
		}
		if c.ScalePort < 1 || c.ScalePort > 65535 {
			return fmt.Errorf("config: scale_port %d out of range (1..65535)", c.ScalePort)
		}
	}
	return nil
}

// DefaultConfigPath returns the OS-appropriate config path. On Windows,
// %ProgramData%\Simsim\POSAgent\config.json (with a sensible fallback if
// %ProgramData% is unset). Elsewhere, ./config.json.
func DefaultConfigPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(programDataDir(), "Simsim", "POSAgent", "config.json")
	}
	return "./config.json"
}

// DefaultSecretsPath returns the OS-appropriate secrets file path. On
// Windows, %ProgramData%\Simsim\POSAgent\secrets.dat (DPAPI ciphertext).
// Elsewhere, ./secrets.json (plaintext, dev-only).
func DefaultSecretsPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(programDataDir(), "Simsim", "POSAgent", "secrets.dat")
	}
	return "./secrets.json"
}

// DefaultMachineIDPath returns the OS-appropriate cache path for the
// machine_id token computed by agentctl. Cached across runs so the
// pairing identifier the cloud sees is stable. Added in M2 for the
// pairing flow; M1 had no machine-identity concept.
func DefaultMachineIDPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(programDataDir(), "Simsim", "POSAgent", "machine_id")
	}
	return "./machine_id"
}

// DefaultLogPath returns the OS-appropriate path for the agent's structured
// log file. Used in Windows service mode where stdout is discarded by SCM.
// Added in M2 (sub-task A6).
//
// TODO M3: callers should wrap output via gopkg.in/natefinch/lumberjack.v2
// — currently the file is opened with O_APPEND and grows unbounded.
func DefaultLogPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(programDataDir(), "Simsim", "POSAgent", "logs", "agent.log")
	}
	return "./agent.log"
}

// programDataDir returns %ProgramData% with a sensible fallback. Only
// meaningful on Windows; callers gate via runtime.GOOS.
func programDataDir() string {
	if v := os.Getenv("ProgramData"); v != "" {
		return v
	}
	return `C:\ProgramData`
}
