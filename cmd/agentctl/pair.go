package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/cloud"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
	"github.com/karimkheirat/simsim-pos-agent/internal/pairing"
)

// errMsgInvalidCode is the French error string surfaced when --code fails
// the format check. Matches the M2 agent spec verbatim.
const errMsgInvalidCode = "Code invalide. Doit être 6 chiffres."

// validatePairingCode returns an error if code is not exactly 6 ASCII digits.
func validatePairingCode(code string) error {
	if len(code) != 6 {
		return errors.New(errMsgInvalidCode)
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return errors.New(errMsgInvalidCode)
		}
	}
	return nil
}

func runPair(args []string) int {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		code        = fs.String("code", "", "6-digit pairing code (required)")
		configPath  = fs.String("config", config.DefaultConfigPath(), "Path to config.json")
		secretsPath = fs.String("secrets", config.DefaultSecretsPath(), "Path to secrets file")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := validatePairingCode(*code); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}

	cfg, loadErr := config.Load(*configPath)
	if loadErr != nil && !errors.Is(loadErr, config.ErrConfigMissing) {
		fmt.Fprintln(os.Stderr, loadErr.Error())
		return 2
	}

	machineID, err := getMachineID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur: impossible de générer l'identifiant machine: %v\n", err)
		return 1
	}

	secStore, err := config.NewSecretStore(*secretsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur: impossible de créer le store de secrets: %v\n", err)
		return 1
	}

	client := cloud.New(cfg.CloudBaseURL, version)
	svc := &pairing.Service{
		Cloud:     client,
		Secrets:   secStore,
		MachineID: machineID,
		Version:   version,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := svc.Pair(ctx, *code)
	if err != nil {
		printPairError(err)
		return 1
	}

	fmt.Println("✓ Appareil jumelé avec succès.")
	fmt.Printf("  Magasin    : %s\n", resp.StoreName)
	fmt.Printf("  Caisse     : %s\n", resp.TerminalLabel)
	fmt.Printf("  ID terminal: %s\n", resp.TerminalID)

	// Bootstrap one-shot heartbeat — best effort, intentionally CLI-layer
	// (not part of pairing.Service.Pair) per the A4 contract. Lets the
	// cloud's last-seen flip green within seconds instead of waiting for
	// the running service's 5min cycle.
	hbCtx, hbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer hbCancel()
	if hbErr := client.Heartbeat(hbCtx, resp.TerminalToken, buildHeartbeat(cfg)); hbErr != nil {
		fmt.Fprintf(os.Stderr, "Avertissement: heartbeat initial échoué (%v) — l'agent enverra le prochain dans 5 min.\n", hbErr)
	}

	return 0
}

// printPairError translates a cloud-client error to a French CLI message.
// Prefers the cloud-supplied message when available, falls back to a
// generic French string per error type.
func printPairError(err error) {
	var ce *cloud.CloudError
	cloudMsg := ""
	if errors.As(err, &ce) {
		cloudMsg = ce.Message()
	}
	switch {
	case errors.Is(err, cloud.ErrInvalidCode):
		if cloudMsg == "" {
			cloudMsg = "Code invalide ou expiré."
		}
		fmt.Fprintln(os.Stderr, "Erreur: "+cloudMsg)
	case errors.Is(err, cloud.ErrRateLimited):
		if cloudMsg == "" {
			cloudMsg = "Trop de tentatives. Réessayez dans une heure."
		}
		fmt.Fprintln(os.Stderr, "Erreur: "+cloudMsg)
	case errors.Is(err, cloud.ErrInvalidRequest):
		if cloudMsg == "" {
			cloudMsg = "Requête invalide."
		}
		fmt.Fprintln(os.Stderr, "Erreur: "+cloudMsg)
	case errors.Is(err, cloud.ErrNetwork):
		fmt.Fprintln(os.Stderr, "Erreur: impossible de contacter le serveur Simsim. Vérifiez votre connexion réseau.")
	case errors.Is(err, cloud.ErrInternal):
		if cloudMsg == "" {
			cloudMsg = "Erreur serveur. Réessayez plus tard."
		}
		fmt.Fprintln(os.Stderr, "Erreur: "+cloudMsg)
	default:
		fmt.Fprintf(os.Stderr, "Erreur: %v\n", err)
	}
}

// buildHeartbeat constructs the one-shot pair-confirmation heartbeat.
// agentctl is short-lived so uptime is 0; printer status is fetched best-
// effort from the local agent's /health endpoint.
func buildHeartbeat(cfg config.Config) cloud.HeartbeatRequest {
	return cloud.HeartbeatRequest{
		AgentVersion:  version,
		OSVersion:     runtime.GOOS,
		UptimeSeconds: 0,
		Printer:       fetchPrinterStatusFromLocalAgent(cfg.ListenPort),
	}
}

// fetchPrinterStatusFromLocalAgent queries the local agent's /health for
// printer status. Best effort; on any failure returns the zero
// PrinterStatus (configured=false). The running agent's own heartbeat
// loop will overwrite this with real status on its next cycle.
//
// A configured=false bootstrap heartbeat from a paired-but-service-not-
// running agent is intentional and correct: the cloud is honestly
// reporting "agent paired but printer-driving service isn't up yet."
// The next regular heartbeat from the running service (every 5 min by
// default) overwrites with real status. Operators see the right state
// at every moment.
func fetchPrinterStatusFromLocalAgent(port int) cloud.PrinterStatus {
	if port == 0 {
		port = 47291
	}
	hc := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := hc.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		return cloud.PrinterStatus{}
	}
	defer resp.Body.Close()
	var health struct {
		Printer struct {
			Configured bool   `json:"configured"`
			Reachable  bool   `json:"reachable"`
			Name       string `json:"name"`
		} `json:"printer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return cloud.PrinterStatus{}
	}
	return cloud.PrinterStatus{
		Configured: health.Printer.Configured,
		Reachable:  health.Printer.Reachable,
		Name:       health.Printer.Name,
	}
}
