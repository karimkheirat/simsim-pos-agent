package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

// runTestPrint fires the local agent's POST /test-print endpoint.
// Used by the installer's print-verification step.
//
// Flow:
//  1. Load config (for ListenPort) + secrets (for the terminal token
//     used in the loopback handshake).
//  2. GET /handshake → JWT.
//  3. POST /test-print with Bearer JWT → prints the canned fixture.
//  4. Exit 0 on agent 200. Exit non-zero with the agent's error
//     envelope printed to stderr on any other outcome.
//
// Output is structured for the installer's Pascal-side parsing:
// stdout = success line; stderr = "Erreur: <code>: <message>".
func runTestPrint(args []string) int {
	fs := flag.NewFlagSet("test-print", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		configPath  = fs.String("config", config.DefaultConfigPath(), "Path to config.json")
		secretsPath = fs.String("secrets", config.DefaultSecretsPath(), "Path to secrets file")
		timeout     = fs.Duration("timeout", 20*time.Second, "Overall timeout")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, loadErr := config.Load(*configPath)
	if loadErr != nil && !errors.Is(loadErr, config.ErrConfigMissing) {
		fmt.Fprintln(os.Stderr, "Erreur: AGENT_CONFIG: "+loadErr.Error())
		return 1
	}

	// Loading secrets up front fails fast on an unpaired agent. The
	// installer fires test-print only after pair succeeded, so an
	// unpaired error here means the operator skipped pairing or it
	// failed silently — surface both cases the same.
	secStore, err := config.NewSecretStore(*secretsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Erreur: SECRETS_STORE: "+err.Error())
		return 1
	}
	if _, err := secStore.Load(); err != nil {
		fmt.Fprintln(os.Stderr, "Erreur: NOT_PAIRED: l'agent n'est pas jumelé.")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	jwt, err := loopbackHandshake(ctx, cfg.ListenPort)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Erreur: HANDSHAKE_FAILED: "+err.Error())
		return 1
	}

	if code, msg, err := loopbackTestPrint(ctx, cfg.ListenPort, jwt); err != nil {
		fmt.Fprintln(os.Stderr, "Erreur: "+code+": "+msg)
		return 1
	}

	fmt.Println("✓ Impression de test envoyée à l'imprimante.")
	return 0
}

// agentBaseURL returns the loopback URL for the running agent's API.
// Constants instead of config.New because agentctl is a one-shot CLI.
func agentBaseURL(port int) string {
	return "http://127.0.0.1:" + strconv.Itoa(port)
}

// loopbackHandshake calls GET /handshake on the local agent. Returns
// the JWT string on success. On any non-200 the body is folded into
// the error message for the installer to surface.
func loopbackHandshake(ctx context.Context, port int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, agentBaseURL(port)+"/handshake", nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("agent unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		JWT string `json:"jwt"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode handshake: %w", err)
	}
	if parsed.JWT == "" {
		return "", errors.New("handshake response missing jwt")
	}
	return parsed.JWT, nil
}

// loopbackTestPrint POSTs /test-print with the given JWT. Returns
// (errorCode, message, err) — on 200 returns ("", "", nil).
func loopbackTestPrint(ctx context.Context, port int, jwt string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		agentBaseURL(port)+"/test-print", nil)
	if err != nil {
		return "INTERNAL", err.Error(), err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "AGENT_UNREACHABLE", err.Error(), err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK {
		return "", "", nil
	}
	// Decode the agent's envelope for a cleaner CLI message.
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error != nil {
		return env.Error.Code, env.Error.Message, fmt.Errorf("test-print: %s", env.Error.Code)
	}
	return "TEST_PRINT_FAILED",
		fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		fmt.Errorf("test-print HTTP %d", resp.StatusCode)
}
