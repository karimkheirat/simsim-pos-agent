package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		configPath  = fs.String("config", config.DefaultConfigPath(), "Path to config.json")
		secretsPath = fs.String("secrets", config.DefaultSecretsPath(), "Path to secrets file")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	secStore, err := config.NewSecretStore(*secretsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur: %v\n", err)
		return 1
	}

	secrets, err := secStore.Load()
	if errors.Is(err, config.ErrNoSecrets) {
		fmt.Println("Non jumelé.")
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur: %v\n", err)
		return 1
	}

	fmt.Println("✓ Jumelé.")
	fmt.Printf("  ID terminal: %s\n", secrets.TerminalID)
	fmt.Printf("  ID magasin : %s\n", secrets.StoreID)
	fmt.Printf("  Jumelé le  : %s\n", secrets.PairedAt.Local().Format("2006-01-02 15:04:05 MST"))

	// Best-effort poll of the local agent's /health to surface printer
	// state. M3 will add a /status endpoint with richer detail.
	cfg, _ := config.Load(*configPath)
	port := cfg.ListenPort
	if port == 0 {
		port = 47291
	}

	hc := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := hc.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		fmt.Println("\nAgent local non en cours d'exécution.")
		return 0
	}
	defer resp.Body.Close()

	var health struct {
		Version string `json:"version"`
		Printer struct {
			Configured bool   `json:"configured"`
			Reachable  bool   `json:"reachable"`
			Name       string `json:"name"`
		} `json:"printer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		fmt.Println("\nAgent local: réponse illisible.")
		return 0
	}

	fmt.Println("\nAgent local en cours d'exécution.")
	fmt.Printf("  Version    : %s\n", health.Version)
	fmt.Printf("  Imprimante : %s (configurée=%v, joignable=%v)\n",
		health.Printer.Name, health.Printer.Configured, health.Printer.Reachable)
	return 0
}
