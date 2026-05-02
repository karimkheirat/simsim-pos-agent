// Command agent is the Simsim POS Agent — a local Windows service that
// receives print jobs from the POS web app and drives a thermal printer
// + cash drawer. M1 ships the `run` subcommand; service install/start
// land in M2.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/karimkheirat/simsim-pos-agent/internal/api"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

const usageTemplate = `Simsim POS Agent %s

Usage:
  agent run [flags]

Flags:
  --config string      Path to config.json
  --printer string     Override printer spec (e.g. "SP-331" or "file:./out")
  --port int           Override listen port (default 47291)
  --log-level string   debug | info | warn | error
`

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}
	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	default:
		// Unknown / future subcommands (service install, etc.) — friendly fallback.
		printUsage()
	}
}

func printUsage() {
	fmt.Printf(usageTemplate, version)
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var (
		configPath  = fs.String("config", config.DefaultConfigPath(), "Path to config.json")
		printerSpec = fs.String("printer", "", `Override printer spec ("SP-331" or "file:./out")`)
		port        = fs.Int("port", 0, "Override listen port (0 = use config)")
		logLevel    = fs.String("log-level", "", "Override log_level (debug|info|warn|error)")
	)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	cfg, loadErr := config.Load(*configPath)
	if loadErr != nil && !errors.Is(loadErr, config.ErrConfigMissing) {
		fmt.Fprintf(os.Stderr, "%v\n", loadErr)
		os.Exit(2)
	}

	// Apply CLI overrides on top of file/defaults.
	if *printerSpec != "" {
		cfg.PrinterName = *printerSpec
	}
	if *port != 0 {
		cfg.ListenPort = *port
	}
	if *logLevel != "" {
		cfg.LogLevel = *logLevel
	}
	// Always inject the build-time version; config-file Version is informational.
	cfg.Version = version

	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	if errors.Is(loadErr, config.ErrConfigMissing) {
		logger.Warn("config file missing — using defaults", "path", *configPath)
	}
	logger.Info("simsim-pos-agent starting",
		"version", cfg.Version,
		"listen_port", cfg.ListenPort,
		"printer", cfg.PrinterName,
		"log_level", cfg.LogLevel,
	)

	// Build printer transport (nil if unconfigured — api surfaces it as PRINTER_NOT_CONFIGURED).
	var p printer.Printer
	if cfg.PrinterName != "" {
		var err error
		p, err = printer.New(cfg.PrinterName)
		if err != nil {
			logger.Error("printer init failed", "spec", cfg.PrinterName, "err", err.Error())
			os.Exit(1)
		}
	} else {
		logger.Warn("no printer configured; /print will return PRINTER_NOT_CONFIGURED")
	}

	srv, err := api.New(api.Config{
		ListenAddr:     "127.0.0.1:" + strconv.Itoa(cfg.ListenPort),
		AllowedOrigins: cfg.AllowedOrigins,
		Version:        cfg.Version,
		Logger:         logger,
	}, p)
	if err != nil {
		logger.Error("server init failed", "err", err.Error())
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("shutdown signal received", "signal", sig.String())
		cancel()
	}()

	if err := srv.Run(ctx); err != nil {
		logger.Error("server stopped with error", "err", err.Error())
		os.Exit(1)
	}
	logger.Info("server stopped")
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
