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

	secStore, err := config.NewSecretStore(*secretsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur: %v\n", err)
		return 1
	}

	secrets, err := secStore.Load()
	if errors.Is(err, config.ErrNoSecrets) {
		fmt.Println("Pas de jumelage actif.")
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur: impossible de charger les secrets: %v\n", err)
		return 1
	}

	cfg, loadErr := config.Load(*configPath)
	if loadErr != nil && !errors.Is(loadErr, config.ErrConfigMissing) {
		fmt.Fprintln(os.Stderr, loadErr.Error())
		return 2
	}

	client := cloud.New(cfg.CloudBaseURL, version)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = client.Unpair(ctx, secrets.TerminalToken)
	switch {
	case err == nil:
		// Cloud revoked successfully — clear local secrets next.
	case errors.Is(err, cloud.ErrUnauthenticated):
		// Already revoked server-side; treat as benign and clear locally.
		fmt.Fprintln(os.Stderr, "Avertissement: le token était déjà révoqué côté serveur. Nettoyage local.")
	case errors.Is(err, cloud.ErrNetwork):
		fmt.Print("Impossible de contacter le serveur. Effacer les secrets locaux quand même ? [o/N] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "o" && answer != "oui" {
			fmt.Println("Annulation. Les secrets locaux n'ont pas été effacés.")
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "Erreur: %v\n", err)
		return 1
	}

	if err := secStore.Clear(); err != nil {
		fmt.Fprintf(os.Stderr, "Erreur: impossible d'effacer les secrets: %v\n", err)
		return 1
	}

	fmt.Println("✓ Jumelage révoqué.")
	return 0
}
