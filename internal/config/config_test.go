package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	c := Defaults()
	if c.Version != "0.1.0" {
		t.Errorf("Version = %q, want 0.1.0", c.Version)
	}
	if c.ListenPort != 47291 {
		t.Errorf("ListenPort = %d, want 47291", c.ListenPort)
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", c.LogLevel)
	}
	if c.PrinterName != "" {
		t.Errorf("PrinterName = %q, want empty", c.PrinterName)
	}
	if c.CloudBaseURL != "https://opensimsim.co" {
		t.Errorf("CloudBaseURL = %q, want opensimsim.co prod URL", c.CloudBaseURL)
	}
	if c.HeartbeatSeconds != 300 {
		t.Errorf("HeartbeatSeconds = %d, want 300", c.HeartbeatSeconds)
	}
	if len(c.AllowedOrigins) != 2 {
		t.Errorf("AllowedOrigins len = %d, want 2", len(c.AllowedOrigins))
	}
	if c.PaperWidthMM != 80 {
		t.Errorf("PaperWidthMM = %d, want 80", c.PaperWidthMM)
	}
	if c.ReceiptPrinterLanguage != "escpos" {
		t.Errorf("ReceiptPrinterLanguage = %q, want escpos", c.ReceiptPrinterLanguage)
	}
	if c.DPI != 203 {
		t.Errorf("DPI = %d, want 203", c.DPI)
	}
	if c.ReceiptWidthDots != 0 {
		t.Errorf("ReceiptWidthDots = %d, want 0 (derive)", c.ReceiptWidthDots)
	}
}

func TestValidate_ReceiptPrinterLanguage(t *testing.T) {
	base := Defaults()
	if err := Validate(base); err != nil {
		t.Fatalf("defaults should validate: %v", err)
	}

	base.ReceiptPrinterLanguage = "tspl"
	if err := Validate(base); err != nil {
		t.Errorf("tspl language should validate: %v", err)
	}

	base.ReceiptPrinterLanguage = "zpl"
	if err := Validate(base); err == nil {
		t.Error("expected error for invalid receipt_printer_language=zpl")
	}
}

func TestValidate_DPI(t *testing.T) {
	c := Defaults()
	c.DPI = 0
	if err := Validate(c); err == nil {
		t.Error("expected error for dpi=0")
	}
	c.DPI = 300
	if err := Validate(c); err != nil {
		t.Errorf("dpi=300 should validate: %v", err)
	}
}

func TestEffectiveReceiptWidthDots(t *testing.T) {
	c := Defaults() // PaperWidthMM 80, ReceiptWidthDots 0
	if got := c.EffectiveReceiptWidthDots(); got != 576 {
		t.Errorf("80mm derive = %d, want 576", got)
	}
	c.PaperWidthMM = 58
	if got := c.EffectiveReceiptWidthDots(); got != 384 {
		t.Errorf("58mm derive = %d, want 384", got)
	}
	c.ReceiptWidthDots = 600 // explicit override wins
	if got := c.EffectiveReceiptWidthDots(); got != 600 {
		t.Errorf("explicit override = %d, want 600", got)
	}
}

func TestDefaultMachineIDPath(t *testing.T) {
	got := DefaultMachineIDPath()
	if runtime.GOOS == "windows" {
		if !strings.Contains(got, "Simsim") || !strings.HasSuffix(got, "machine_id") {
			t.Errorf("windows DefaultMachineIDPath = %q, want containing Simsim and ending machine_id", got)
		}
	} else {
		if got != "./machine_id" {
			t.Errorf("non-windows DefaultMachineIDPath = %q, want ./machine_id", got)
		}
	}
}

func TestLoad_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	cfg, err := Load(path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrConfigMissing) {
		t.Errorf("err = %v; want errors.Is(err, ErrConfigMissing)", err)
	}
	if cfg.ListenPort != 47291 {
		t.Errorf("Defaults() not returned on missing file: ListenPort = %d", cfg.ListenPort)
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("err = %v; want errors.Is(err, ErrConfigMalformed)", err)
	}
	if cfg.ListenPort != 47291 {
		t.Errorf("Defaults() not returned on malformed JSON: ListenPort = %d", cfg.ListenPort)
	}
}

func TestLoad_UnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(path, []byte(`{"listen_port": 8080, "mystery_field": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !errors.Is(err, ErrConfigMalformed) {
		t.Errorf("err = %v; want errors.Is(err, ErrConfigMalformed)", err)
	}
}

func TestLoad_PartialOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.json")
	// Only listen_port is overridden; other fields keep defaults.
	if err := os.WriteFile(path, []byte(`{"listen_port": 9000}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenPort != 9000 {
		t.Errorf("ListenPort = %d, want 9000", cfg.ListenPort)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info (default)", cfg.LogLevel)
	}
	if cfg.Version != "0.1.0" {
		t.Errorf("Version = %q, want 0.1.0 (default)", cfg.Version)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Errorf("AllowedOrigins len = %d, want 2 (default)", len(cfg.AllowedOrigins))
	}
	if cfg.PaperWidthMM != 80 {
		t.Errorf("PaperWidthMM = %d, want 80 (default)", cfg.PaperWidthMM)
	}
}

func TestLoad_PaperWidth58_OK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paper58.json")
	if err := os.WriteFile(path, []byte(`{"paper_width_mm": 58}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PaperWidthMM != 58 {
		t.Errorf("PaperWidthMM = %d, want 58", cfg.PaperWidthMM)
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestValidate_Success(t *testing.T) {
	if err := Validate(Defaults()); err != nil {
		t.Errorf("Validate(Defaults()) = %v, want nil", err)
	}
}

func TestValidate_InvalidFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"empty version", func(c *Config) { c.Version = "" }, "version"},
		{"port zero", func(c *Config) { c.ListenPort = 0 }, "listen_port"},
		{"port negative", func(c *Config) { c.ListenPort = -1 }, "listen_port"},
		{"port too high", func(c *Config) { c.ListenPort = 70000 }, "listen_port"},
		{"bad log level", func(c *Config) { c.LogLevel = "verbose" }, "log_level"},
		{"empty log level", func(c *Config) { c.LogLevel = "" }, "log_level"},
		{"empty origin in list", func(c *Config) { c.AllowedOrigins = []string{"https://ok.example", ""} }, "allowed_origins"},
		{"heartbeat_seconds zero", func(c *Config) { c.HeartbeatSeconds = 0 }, "heartbeat_seconds"},
		{"heartbeat_seconds negative", func(c *Config) { c.HeartbeatSeconds = -5 }, "heartbeat_seconds"},
		// M13 A.5a — only 58 and 80 are valid; everything else rejected.
		{"paper_width_mm zero", func(c *Config) { c.PaperWidthMM = 0 }, "paper_width_mm"},
		{"paper_width_mm 76 (non-spec)", func(c *Config) { c.PaperWidthMM = 76 }, "paper_width_mm"},
		{"paper_width_mm 112 (non-spec)", func(c *Config) { c.PaperWidthMM = 112 }, "paper_width_mm"},
		{"paper_width_mm negative", func(c *Config) { c.PaperWidthMM = -1 }, "paper_width_mm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			tt.mutate(&c)
			err := Validate(c)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q; want substring %q", err.Error(), tt.want)
			}
		})
	}
}

// ── M13 Track B PR 1 — two-printer fields + tspl_dialect + back-compat ──

func TestDefaults_TwoPrinterFields(t *testing.T) {
	c := Defaults()
	if c.ReceiptPrinterName != "" {
		t.Errorf("ReceiptPrinterName = %q, want empty", c.ReceiptPrinterName)
	}
	if c.LabelPrinterName != "" {
		t.Errorf("LabelPrinterName = %q, want empty", c.LabelPrinterName)
	}
	if c.TSPLDialect != "standard" {
		t.Errorf("TSPLDialect = %q, want standard", c.TSPLDialect)
	}
}

func TestLoad_BackCompat_LegacyPrinterNameMaps(t *testing.T) {
	// Pre-M13-B deployed configs only set printer_name; that value
	// must be mirrored into receipt_printer_name so the agent keeps
	// driving the receipt printer.
	path := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(path, []byte(`{"printer_name": "SP-331"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PrinterName != "SP-331" {
		t.Errorf("PrinterName = %q, want SP-331", cfg.PrinterName)
	}
	if cfg.ReceiptPrinterName != "SP-331" {
		t.Errorf("ReceiptPrinterName = %q, want SP-331 (back-compat)", cfg.ReceiptPrinterName)
	}
}

func TestLoad_BackCompat_NewFieldWinsOverLegacy(t *testing.T) {
	// If the operator set BOTH the new field AND the legacy field,
	// the explicit new field wins. Pins the Q1 decision.
	path := filepath.Join(t.TempDir(), "both.json")
	body := `{"printer_name": "legacy-name", "receipt_printer_name": "new-name"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ReceiptPrinterName != "new-name" {
		t.Errorf("ReceiptPrinterName = %q, want new-name (new field wins)", cfg.ReceiptPrinterName)
	}
}

func TestLoad_TwoPrinterConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "two-printer.json")
	body := `{
		"receipt_printer_name": "Star SP-331",
		"label_printer_name": "Xprinter XP-DT426B",
		"tspl_dialect": "standard"
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ReceiptPrinterName != "Star SP-331" {
		t.Errorf("ReceiptPrinterName = %q, want Star SP-331", cfg.ReceiptPrinterName)
	}
	if cfg.LabelPrinterName != "Xprinter XP-DT426B" {
		t.Errorf("LabelPrinterName = %q, want Xprinter XP-DT426B", cfg.LabelPrinterName)
	}
	if cfg.TSPLDialect != "standard" {
		t.Errorf("TSPLDialect = %q, want standard", cfg.TSPLDialect)
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestLoad_RongtaDialect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rongta.json")
	body := `{"label_printer_name": "Rongta RP-410", "tspl_dialect": "rongta"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TSPLDialect != "rongta" {
		t.Errorf("TSPLDialect = %q, want rongta", cfg.TSPLDialect)
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestValidate_InvalidTSPLDialect(t *testing.T) {
	tests := []struct {
		name    string
		dialect string
	}{
		{"empty string", ""},
		{"typo", "stanard"},
		{"random", "zpl"},
		{"upper case (case-sensitive)", "STANDARD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Defaults()
			c.TSPLDialect = tt.dialect
			err := Validate(c)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tt.dialect)
			}
			if !strings.Contains(err.Error(), "tspl_dialect") {
				t.Errorf("err = %q; want substring tspl_dialect", err.Error())
			}
		})
	}
}

func TestDefaultConfigPath(t *testing.T) {
	got := DefaultConfigPath()
	if runtime.GOOS == "windows" {
		if !strings.Contains(got, "Simsim") || !strings.HasSuffix(got, "config.json") {
			t.Errorf("windows DefaultConfigPath = %q, want containing Simsim and ending config.json", got)
		}
	} else {
		if got != "./config.json" {
			t.Errorf("non-windows DefaultConfigPath = %q, want ./config.json", got)
		}
	}
}
