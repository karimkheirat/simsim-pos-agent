package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

// runVerifyPrint reports the operator's test-print confirmation to
// the cloud via the local agent's loopback /report-verified endpoint.
//
// Used by the installer's print-verification step after the operator
// has answered Oui/Non to "Did the test print correctly?".
//
//	agentctl verify-print --ok                        → verified=true
//	agentctl verify-print --fail OPERATOR_REJECTED    → verified=false + error_class
//
// Exactly one of --ok / --fail is required. --fail takes a free-form
// error class string the cloud logs; conventional values:
//
//	OPERATOR_REJECTED        operator said Non
//	MAX_RETRIES_EXCEEDED     installer hit the retry cap
//	TEST_PRINT_FAILED        the agent's /test-print itself returned an error
//	AGENT_UNREACHABLE        the loopback handshake or print call failed
func runVerifyPrint(args []string) int {
	fs := flag.NewFlagSet("verify-print", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		configPath  = fs.String("config", config.DefaultConfigPath(), "Path to config.json")
		secretsPath = fs.String("secrets", config.DefaultSecretsPath(), "Path to secrets file")
		ok          = fs.Bool("ok", false, "Report verified=true (operator confirmed)")
		fail        = fs.String("fail", "", "Report verified=false with the given error class (operator rejected or pipeline failed)")
		timeout     = fs.Duration("timeout", 15*time.Second, "Overall timeout")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Exactly one of --ok / --fail must be supplied. Both / neither
	// are operator-script errors and surface fast.
	if (*ok && *fail != "") || (!*ok && *fail == "") {
		fmt.Fprintln(os.Stderr, "Erreur: USAGE: exactement un de --ok ou --fail <error_class> requis")
		return 2
	}

	verified := *ok
	errorClass := *fail

	cfg, loadErr := config.Load(*configPath)
	if loadErr != nil && !errors.Is(loadErr, config.ErrConfigMissing) {
		fmt.Fprintln(os.Stderr, "Erreur: AGENT_CONFIG: "+loadErr.Error())
		return 1
	}

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

	if code, msg, err := loopbackReportVerified(ctx, cfg.ListenPort, jwt, verified, errorClass); err != nil {
		fmt.Fprintln(os.Stderr, "Erreur: "+code+": "+msg)
		return 1
	}

	if verified {
		fmt.Println("✓ Vérification d'impression enregistrée (verified=true).")
	} else {
		fmt.Printf("✓ Échec d'impression enregistré (verified=false, error_class=%s).\n", errorClass)
	}
	return 0
}

// loopbackReportVerified POSTs /report-verified on the local agent
// with the given verified / error_class. Returns ("", "", nil) on 200.
func loopbackReportVerified(
	ctx context.Context, port int, jwt string, verified bool, errorClass string,
) (string, string, error) {
	body, _ := json.Marshal(struct {
		Verified   bool   `json:"verified"`
		ErrorClass string `json:"error_class,omitempty"`
	}{
		Verified:   verified,
		ErrorClass: errorClass,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		agentBaseURL(port)+"/report-verified", bytes.NewReader(body))
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
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK {
		return "", "", nil
	}
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.Error != nil {
		return env.Error.Code, env.Error.Message, fmt.Errorf("report-verified: %s", env.Error.Code)
	}
	return "REPORT_VERIFIED_FAILED",
		fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(raw)),
		fmt.Errorf("report-verified HTTP %d", resp.StatusCode)
}
