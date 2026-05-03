package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/cloud"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
	"github.com/karimkheirat/simsim-pos-agent/internal/pairing"
)

func runUnpair(args []string) int {
	fs := flag.NewFlagSet("unpair", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		configPath  = fs.String("config", config.DefaultConfigPath(), "Path to config.json")
		secretsPath = fs.String("secrets", config.DefaultSecretsPath(), "Path to secrets file")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, loadErr := config.Load(*configPath)
	if loadErr != nil && !errors.Is(loadErr, config.ErrConfigMissing) {
		fmt.Fprintln(os.Stderr, loadErr.Error())
		return 2
	}

	secStore, err := config.NewSecretStore(*secretsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur: %v\n", err)
		return 1
	}

	svc := &pairing.Service{
		Cloud:   cloud.New(cfg.CloudBaseURL, Version),
		Secrets: secStore,
		Version: Version,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = svc.Unpair(ctx)
	switch {
	case err == nil:
		fmt.Println("✓ Jumelage révoqué.")
		return 0
	case errors.Is(err, config.ErrNoSecrets):
		fmt.Println("Pas de jumelage actif.")
		return 0
	case errors.Is(err, cloud.ErrNetwork):
		// Force-clear prompt is CLI-only per A4 contract.
		fmt.Print("Impossible de contacter le serveur. Effacer les secrets locaux quand même ? [o/N] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "o" && answer != "oui" {
			fmt.Println("Annulation. Les secrets locaux n'ont pas été effacés.")
			return 1
		}
		if err := secStore.Clear(); err != nil {
			fmt.Fprintf(os.Stderr, "Erreur: impossible d'effacer les secrets: %v\n", err)
			return 1
		}
		fmt.Println("✓ Jumelage révoqué (local seulement — le serveur n'a pas été contacté).")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "Erreur: %v\n", err)
		return 1
	}
}
