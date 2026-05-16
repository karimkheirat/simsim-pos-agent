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
	if c.CloudBaseURL != "https://web-production-6bb4d.up.railway.app" {
		t.Errorf("CloudBaseURL = %q, want railway prod URL", c.CloudBaseURL)
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
