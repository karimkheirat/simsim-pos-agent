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

// TestPairOutput_PinsInstallerParserContract pins the literal stdout/
// stderr substrings the M4 installer's runPairStep parser depends on
// (installer/installer.iss, AG5). The Inno Setup wizard's success page
// extracts store_name + terminal_label from agentctl's stdout via:
//
//   if Pos('Magasin', L) > 0 then ... Trim(Copy(L, Pos(':', L)+1, ...))
//   if Pos('Caisse',  L) > 0 then ... same shape
//
// The failure page extracts the cloud-supplied French message via:
//
//   if Pos('Erreur:', L) > 0 then ... same shape
//
// If anyone in M5+ changes agentctl's pair output format ("Boutique"
// instead of "Magasin", different indentation, removed colon, etc.)
// these subtests fail. The fix is to update installer/installer.iss
// runPairStep IN THE SAME COMMIT — otherwise the wizard silently
// degrades to "(empty) (empty) is connected." without any test
// catching it. Brittleness made detected-brittleness.
//
// Future hardening (M5): add `agentctl pair --output-json` for a
// structured contract and retire this test.
func TestPairOutput_PinsInstallerParserContract(t *testing.T) {
	t.Run("success format", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/pos-agent/pair":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok": true,
					"data": map[string]any{
						"terminal_id":    "trm_test",
						"terminal_token": "tok_test",
						"store_id":       "store_test",
						"store_name":     "Hamoud Boualem - Centre Oran",
						"terminal_label": "Caisse 1",
					},
				})
			default:
				// Bootstrap heartbeat after pair — return ok so the
				// pair flow completes cleanly and we observe the full
				// success-format output on stdout.
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"data":{}}`))
			}
		}))
		defer server.Close()

		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		configContent := fmt.Sprintf(`{"cloud_base_url":"%s","listen_port":65000}`, server.URL)
		if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
			t.Fatal(err)
		}
		secretsPath := filepath.Join(dir, "secrets.dat")

		stdout, _, exitCode := captureRunPair(t, []string{
			"--code", "000000",
			"--config", configPath,
			"--secrets", secretsPath,
		})
		if exitCode != 0 {
			t.Fatalf("runPair exit = %d, want 0", exitCode)
		}

		// Pinned literals — match the agentctl pair success block format
		// at M2 freeze. Both lines must appear verbatim. If you change
		// the indentation or padding, also update installer.iss
		// runPairStep IN THE SAME COMMIT.
		pinned := []string{
			"  Magasin    : Hamoud Boualem - Centre Oran",
			"  Caisse     : Caisse 1",
		}
		for _, line := range pinned {
			if !strings.Contains(stdout, line) {
				t.Errorf("stdout missing pinned line %q\n"+
					"\nIf you intentionally changed agentctl's pair output, ALSO update "+
					"installer/installer.iss runPairStep in the same commit.\n"+
					"\nFull stdout:\n%s", line, stdout)
			}
		}
	})

	t.Run("error format", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": map[string]any{"code": "INVALID_CODE", "message": "Code invalide ou expiré"},
			})
		}))
		defer server.Close()

		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		configContent := fmt.Sprintf(`{"cloud_base_url":"%s","listen_port":65000}`, server.URL)
		if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
			t.Fatal(err)
		}
		secretsPath := filepath.Join(dir, "secrets.dat")

		_, stderr, exitCode := captureRunPair(t, []string{
			"--code", "000000",
			"--config", configPath,
			"--secrets", secretsPath,
		})
		if exitCode == 0 {
			t.Fatal("runPair exit = 0; want non-zero on INVALID_CODE")
		}

		// Pinned: "Erreur:" prefix on the error line — installer's
		// Pos('Erreur:', L) keys off this. The colon is part of the
		// substring (not just the value separator).
		if !strings.Contains(stderr, "Erreur:") {
			t.Errorf("stderr missing pinned prefix 'Erreur:'\n"+
				"\nIf you intentionally changed agentctl's error format, ALSO update "+
				"installer/installer.iss runPairStep in the same commit.\n"+
				"\nFull stderr:\n%s", stderr)
		}
		// Bonus pin: the cloud-supplied French message must round-trip
		// through *CloudError.Message() to stderr. Catches a regression
		// if printPairError ever stops preserving the cloud message.
		if !strings.Contains(stderr, "Code invalide ou expiré") {
			t.Errorf("stderr missing cloud-supplied French message; "+
				"*CloudError.Message() preservation regressed?\n\nFull stderr:\n%s", stderr)
		}
	})
}
