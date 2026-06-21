package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

// writeConfigCmd is the public main-package entry. Thin wrapper around
// runWriteConfig that surfaces the exit code via os.Exit. The two-layer
// split lets tests call runWriteConfig directly with captured writers
// instead of subprocessing the binary.
func writeConfigCmd(args []string) {
	if code := runWriteConfig(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

// runWriteConfig parses --config / --printer / --cloud-base-url, loads
// the existing config.json (or starts from Defaults if missing), applies
// non-empty overrides, validates, and atomically writes back. Returns
// 0 on success, 1 on operational failure, 2 on flag-parse failure.
//
// Non-empty semantics: passing `--printer ""` leaves printer_name
// unchanged. This is so the installer can omit fields the operator
// didn't touch in the wizard, without clobbering pre-existing values.
//
// AG5 calls this from the installer's [Run] section to seed config.json
// with the printer + cloud URL chosen in the wizard, before the service
// is installed and started.
func runWriteConfig(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("write-config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		configPath      = fs.String("config", config.DefaultConfigPath(), "Path to config.json")
		printerName     = fs.String("printer", "", "Set printer_name (empty leaves unchanged)")
		cloudBaseURL    = fs.String("cloud-base-url", "", "Set cloud_base_url (empty leaves unchanged)")
		receiptLanguage = fs.String("receipt-language", "", "Set receipt_printer_language (escpos|tspl; empty leaves unchanged)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, loadErr := config.Load(*configPath)
	if loadErr != nil && !errors.Is(loadErr, config.ErrConfigMissing) {
		fmt.Fprintf(stderr, "load config: %v\n", loadErr)
		return 1
	}

	if *printerName != "" {
		// M13 Track B PR 1 — mirror the installer's --printer flag into
		// both the legacy and the new receipt-printer field so a fresh
		// config.json is forward-compatible with the two-printer wiring.
		cfg.PrinterName = *printerName
		cfg.ReceiptPrinterName = *printerName
	}
	if *cloudBaseURL != "" {
		cfg.CloudBaseURL = *cloudBaseURL
	}
	// TSPL receipt path — the setup UI's guided test sets this per-printer
	// once it knows the printer speaks TSPL. Empty leaves the existing
	// value (default "escpos"). config.Validate below rejects bad values.
	if *receiptLanguage != "" {
		cfg.ReceiptPrinterLanguage = *receiptLanguage
	}

	// Always inject the build-time Version so a fresh write doesn't
	// produce a stale "0.1.0" Defaults value in the on-disk JSON.
	cfg.Version = Version

	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(stderr, "validate: %v\n", err)
		return 1
	}

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "marshal: %v\n", err)
		return 1
	}
	if err := config.WriteAtomic(*configPath, raw, 0o644); err != nil {
		fmt.Fprintf(stderr, "write %s: %v\n", *configPath, err)
		return 1
	}

	fmt.Fprintf(stdout, "config written to %s\n", *configPath)
	return 0
}
