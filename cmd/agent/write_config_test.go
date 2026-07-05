package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

// TestRunWriteConfig_FreshFile — config.json doesn't exist; runWriteConfig
// creates it from Defaults() with the supplied --printer + --cloud-base-url
// applied. Other fields (port, log level, allowed origins) come from
// Defaults — proves the merge starts from a sensible base.
func TestRunWriteConfig_FreshFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	var stdout, stderr bytes.Buffer
	code := runWriteConfig([]string{
		"--config", path,
		"--printer", "SP-331",
		"--cloud-base-url", "https://test.example",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}

	cfg := loadConfig(t, path)
	if cfg.PrinterName != "SP-331" {
		t.Errorf("PrinterName = %q, want SP-331", cfg.PrinterName)
	}
	if cfg.CloudBaseURL != "https://test.example" {
		t.Errorf("CloudBaseURL = %q, want https://test.example", cfg.CloudBaseURL)
	}
	if cfg.ListenPort != 47291 {
		t.Errorf("ListenPort = %d, want default 47291", cfg.ListenPort)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want default info", cfg.LogLevel)
	}
}

// TestRunWriteConfig_PreservesUnrelatedFields — pre-populated config.json
// with non-default port + log level. runWriteConfig sets only --printer,
// must NOT clobber port/log-level (the merge-don't-replace semantic).
func TestRunWriteConfig_PreservesUnrelatedFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	initial := []byte(`{"listen_port": 9999, "log_level": "debug", "cloud_base_url": "https://existing.example"}`)
	if err := os.WriteFile(path, initial, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWriteConfig([]string{
		"--config", path,
		"--printer", "TM-T20",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}

	cfg := loadConfig(t, path)
	if cfg.PrinterName != "TM-T20" {
		t.Errorf("PrinterName = %q, want TM-T20", cfg.PrinterName)
	}
	if cfg.ListenPort != 9999 {
		t.Errorf("ListenPort = %d, want preserved 9999", cfg.ListenPort)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want preserved debug", cfg.LogLevel)
	}
	if cfg.CloudBaseURL != "https://existing.example" {
		t.Errorf("CloudBaseURL = %q, want preserved (--cloud-base-url not passed)", cfg.CloudBaseURL)
	}
}

// TestRunWriteConfig_EmptyOverridesPreserve — empty --printer "" must
// NOT clobber an existing value. Important for the installer case
// where the operator might re-run write-config without re-entering all
// fields.
func TestRunWriteConfig_EmptyOverridesPreserve(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	initial := []byte(`{"printer_name": "SP-331-existing"}`)
	if err := os.WriteFile(path, initial, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWriteConfig([]string{
		"--config", path,
		"--printer", "", // explicit empty — should NOT clobber
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}

	cfg := loadConfig(t, path)
	if cfg.PrinterName != "SP-331-existing" {
		t.Errorf("PrinterName = %q, want preserved 'SP-331-existing'", cfg.PrinterName)
	}
}

// TestRunWriteConfig_AtomicWrite — after a successful write, the .tmp
// sidecar must be gone (rename completed) and the final file is in
// place. Mid-write atomicity is inherent to the rename pattern; this
// test catches any leak in the cleanup path.
func TestRunWriteConfig_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	var stdout, stderr bytes.Buffer
	code := runWriteConfig([]string{
		"--config", path,
		"--printer", "SP-331",
		"--cloud-base-url", "https://test.example",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}

	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp file remains after write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("final file missing: %v", err)
	}
}

// TestRunWriteConfig_CreatesParentDir — config dir doesn't exist yet
// (fresh installer scenario). runWriteConfig must MkdirAll the parents.
func TestRunWriteConfig_CreatesParentDir(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "Simsim", "POSAgent", "config.json")

	var stdout, stderr bytes.Buffer
	code := runWriteConfig([]string{
		"--config", deep,
		"--printer", "SP-331",
		"--cloud-base-url", "https://test.example",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	if _, err := os.Stat(deep); err != nil {
		t.Errorf("file not created at %s: %v", deep, err)
	}
}

// TestRunWriteConfig_ValidationError — bogus cloud URL doesn't trip
// validation (Validate doesn't check URL format), but a fake test that
// produces an invalid log_level via a pre-loaded config does. Catches
// the validate-before-write path.
func TestRunWriteConfig_ValidationError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// Pre-load with invalid log_level — Load fails on unknown JSON
	// fields but accepts unknown values for known fields. Validate
	// catches it.
	initial := []byte(`{"log_level": "not-a-level"}`)
	if err := os.WriteFile(path, initial, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWriteConfig([]string{
		"--config", path,
		"--printer", "SP-331",
	}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit = %d, want 1\nstderr: %s", code, stderr.String())
	}
}

// TestRunWriteConfig_ScaleFlags — --scale-ip/--scale-port land in the
// new fields; both must be passed together (config.Validate enforces
// the pairing, exit 1 on a lone flag).
func TestRunWriteConfig_ScaleFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	var stdout, stderr bytes.Buffer
	code := runWriteConfig([]string{
		"--config", path,
		"--scale-ip", "192.168.1.50",
		"--scale-port", "5002",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	cfg := loadConfig(t, path)
	if cfg.ScaleIP != "192.168.1.50" || cfg.ScalePort != 5002 {
		t.Errorf("scale = %q:%d, want 192.168.1.50:5002", cfg.ScaleIP, cfg.ScalePort)
	}

	// Re-running without the scale flags must preserve both values.
	stdout.Reset()
	stderr.Reset()
	if code := runWriteConfig([]string{"--config", path, "--printer", "SP-331"}, &stdout, &stderr); code != 0 {
		t.Fatalf("re-run exit = %d\nstderr: %s", code, stderr.String())
	}
	cfg = loadConfig(t, path)
	if cfg.ScaleIP != "192.168.1.50" || cfg.ScalePort != 5002 {
		t.Errorf("scale after re-run = %q:%d, want preserved", cfg.ScaleIP, cfg.ScalePort)
	}
}

func TestRunWriteConfig_LoneScaleFlagRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	var stdout, stderr bytes.Buffer
	code := runWriteConfig([]string{
		"--config", path,
		"--scale-ip", "192.168.1.50",
	}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit = %d, want 1 (scale_ip without scale_port)\nstderr: %s", code, stderr.String())
	}
}

// loadConfig is a test helper that decodes the on-disk config.json into
// a config.Config without going through config.Load (which has its
// DisallowUnknownFields strictness — we want a permissive read here).
func loadConfig(t *testing.T, path string) config.Config {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return cfg
}
