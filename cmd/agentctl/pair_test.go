package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRunPair_HeartbeatFailureDoesNotFailPair verifies the bootstrap
// heartbeat is fully best-effort: a 500 from /heartbeat must not
// propagate as a non-zero exit from `agentctl pair`. The pair itself
// succeeded — the heartbeat is a nice-to-have nudge to the cloud.
func TestRunPair_HeartbeatFailureDoesNotFailPair(t *testing.T) {
	var (
		pairCalls atomic.Int32
		hbCalls   atomic.Int32
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pos-agent/pair":
			pairCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"terminal_id":    "trm_test",
					"terminal_token": "tok_test_43chars",
					"store_id":       "store_test",
					"store_name":     "Hamoud Boualem - Centre Oran",
					"terminal_label": "Caisse 1",
				},
			})
		case "/api/pos-agent/heartbeat":
			hbCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": map[string]any{"code": "INTERNAL", "message": "Erreur interne du serveur"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	// listen_port = 65000 — a port we expect nothing to listen on, so
	// fetchPrinterStatusFromLocalAgent gets connection refused fast and
	// returns the zero PrinterStatus.
	configContent := fmt.Sprintf(`{"cloud_base_url":"%s","listen_port":65000}`, server.URL)
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}
	secretsPath := filepath.Join(dir, "secrets.dat")

	stdout, stderr, exitCode := captureRunPair(t, []string{
		"--code", "000000",
		"--config", configPath,
		"--secrets", secretsPath,
	})

	if exitCode != 0 {
		t.Errorf("exit = %d, want 0\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "✓ Appareil jumelé") {
		t.Errorf("stdout missing success message:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Caisse 1") {
		t.Errorf("stdout missing terminal label:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Avertissement") {
		t.Errorf("stderr missing heartbeat warning:\n%s", stderr)
	}
	if _, err := os.Stat(secretsPath); err != nil {
		t.Errorf("secrets file not written: %v", err)
	}
	if got := pairCalls.Load(); got != 1 {
		t.Errorf("/pair called %d times, want 1", got)
	}
	if got := hbCalls.Load(); got != 1 {
		t.Errorf("/heartbeat called %d times, want 1", got)
	}
}

// captureRunPair invokes runPair with the given args, redirecting
// stdout/stderr to in-memory pipes so the test can assert on them.
func captureRunPair(t *testing.T, args []string) (stdout, stderr string, exitCode int) {
	t.Helper()
	oldStdout, oldStderr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wOut
	os.Stderr = wErr

	doneOut := make(chan string, 1)
	doneErr := make(chan string, 1)
	go func() { b, _ := io.ReadAll(rOut); doneOut <- string(b) }()
	go func() { b, _ := io.ReadAll(rErr); doneErr <- string(b) }()

	exitCode = runPair(args)

	wOut.Close()
	wErr.Close()
	stdout = <-doneOut
	stderr = <-doneErr
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	return
}
