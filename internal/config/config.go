// Package config loads, validates, and supplies defaults for the agent's
// runtime configuration. Mirrors POS_AGENT_SPEC.md §5.2.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Config is the on-disk JSON shape stored at %ProgramData%\Simsim\POSAgent\config.json
// (Windows) or ./config.json (other). All fields are optional; missing
// fields fall back to Defaults().
type Config struct {
	Version        string   `json:"version"`
	ListenPort     int      `json:"listen_port"`
	PrinterName    string   `json:"printer_name"`
	LogLevel       string   `json:"log_level"`
	AllowedOrigins []string `json:"allowed_origins"`
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
		Version:     "0.1.0",
		ListenPort:  47291,
		PrinterName: "",
		LogLevel:    "info",
		AllowedOrigins: []string{
			"https://web-production-6bb4d.up.railway.app",
			"https://opensimsim.co",
		},
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
	return cfg, nil
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

// programDataDir returns %ProgramData% with a sensible fallback. Only
// meaningful on Windows; callers gate via runtime.GOOS.
func programDataDir() string {
	if v := os.Getenv("ProgramData"); v != "" {
		return v
	}
	return `C:\ProgramData`
}
