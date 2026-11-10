package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

// activeConfigPath is the config.json this process loaded: the -config flag
// in foreground mode, config.DefaultConfigPath() as a service. Set before
// buildRuntime, read by chooseReceiptPrinter.
var activeConfigPath = config.DefaultConfigPath()

// restartForPrinter restarts the agent after a new receipt printer is
// written: as a service, the self-updater's own path (graceful shutdown,
// non-zero exit, the SCM's recovery restarts it in 10 s). Nil in foreground
// mode, where the operator restarts it.
var restartForPrinter func()

// chooseReceiptPrinter writes the printer the shop picked in the web app's
// first setup (simsim États G 14) into config.json — both the receipt field
// and the legacy one, as `write-config --printer` does — then restarts so the
// new printer is the one used.
func chooseReceiptPrinter(name string) error {
	cfg, err := config.Load(activeConfigPath)
	if err != nil && !errors.Is(err, config.ErrConfigMissing) {
		return fmt.Errorf("load config: %w", err)
	}
	config.Upgrade(&cfg)
	cfg.PrinterName = name
	cfg.ReceiptPrinterName = name
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := config.WriteAtomic(activeConfigPath, raw, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if restartForPrinter != nil {
		go func() {
			// Let the HTTP answer reach the web app first.
			time.Sleep(500 * time.Millisecond)
			restartForPrinter()
		}()
	}
	return nil
}

// exitForRestart is os.Exit, kept as a variable for tests.
var exitForRestart = os.Exit
