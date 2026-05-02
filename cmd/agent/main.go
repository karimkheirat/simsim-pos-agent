// Command agent is the Simsim POS Agent — a local Windows service that
// receives print jobs from the POS web app and drives a thermal printer
// + cash drawer. M2 added the `service` subcommand for SCM lifecycle
// management; `run` remains the dev/foreground entry point.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	ksvc "github.com/kardianos/service"

	"github.com/karimkheirat/simsim-pos-agent/internal/api"
	"github.com/karimkheirat/simsim-pos-agent/internal/cloud"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
	"github.com/karimkheirat/simsim-pos-agent/internal/heartbeat"
	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
	svcpkg "github.com/karimkheirat/simsim-pos-agent/internal/service"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

const usageTemplate = `Simsim POS Agent %s

Usage:
  agent run [flags]
  agent service install
  agent service uninstall
  agent service start
  agent service stop
  agent service status

Flags (run):
  --config string             Path to config.json
  --printer string            Override printer spec (e.g. "SP-331" or "file:./out")
  --port int                  Override listen port (default 47291)
  --log-level string          debug | info | warn | error
  --heartbeat-seconds int     Override heartbeat cadence (default 300)
`

func main() {
	// When SCM starts the service, ksvc.Interactive() returns false. In
	// that branch we don't process os.Args at all — the SCM controls
	// lifecycle via Program.Start / Stop.
	if !ksvc.Interactive() {
		runAsService()
		return
	}

	if len(os.Args) < 2 {
		printUsage()
		return
	}
	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "service":
		serviceCmd(os.Args[2:])
	default:
		// Unknown subcommands fall through to friendly usage.
		printUsage()
	}
}

func printUsage() {
	fmt.Printf(usageTemplate, version)
}

// runCmd is the foreground / dev entry point. Same as M1 but now also:
//   - Acquires the single-instance Windows mutex (Global\SimsimPOSAgent).
//   - Wires the SecretStore into api.Config so requireTerminalToken can
//     authenticate /print et al.
func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var (
		configPath       = fs.String("config", config.DefaultConfigPath(), "Path to config.json")
		printerSpec      = fs.String("printer", "", `Override printer spec ("SP-331" or "file:./out")`)
		port             = fs.Int("port", 0, "Override listen port (0 = use config)")
		logLevel         = fs.String("log-level", "", "Override log_level (debug|info|warn|error)")
		heartbeatSeconds = fs.Int("heartbeat-seconds", 0, "Override heartbeat cadence (0 = use config)")
	)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	cfg, loadErr := loadAndOverride(*configPath, *printerSpec, *port, *logLevel, *heartbeatSeconds)
	if loadErr != nil && !errors.Is(loadErr, config.ErrConfigMissing) {
		fmt.Fprintf(os.Stderr, "%v\n", loadErr)
		os.Exit(2)
	}
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
		"mode", "foreground",
	)

	mutex, err := svcpkg.AcquireSingleInstance()
	if errors.Is(err, svcpkg.ErrAlreadyRunning) {
		logger.Warn("another instance is running; exiting", "mutex", svcpkg.MutexName)
		os.Exit(0)
	}
	if err != nil {
		logger.Error("single-instance mutex acquire failed", "err", err.Error())
		os.Exit(1)
	}
	defer mutex.Release()

	rt, err := buildRuntime(cfg, logger)
	if err != nil {
		logger.Error("runtime init failed", "err", err.Error())
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

	// Start the heartbeat loop alongside the server, sharing ctx.
	heartbeatDone := make(chan struct{})
	if rt.Heartbeat != nil {
		go func() {
			rt.Heartbeat.Run(ctx)
			close(heartbeatDone)
		}()
	} else {
		close(heartbeatDone)
	}

	if err := rt.Server.Run(ctx); err != nil {
		logger.Error("server stopped with error", "err", err.Error())
		os.Exit(1)
	}
	<-heartbeatDone
	logger.Info("server stopped")
}

// runAsService is the entry point when SCM started us. Logs to a file
// (SCM doesn't capture stdout), wires secrets, hands lifecycle to
// kardianos via svc.Run() which blocks until SCM stops the service.
func runAsService() {
	cfg, loadErr := loadAndOverride(config.DefaultConfigPath(), "", 0, "", 0)
	if loadErr != nil && !errors.Is(loadErr, config.ErrConfigMissing) {
		// No place to log this except the system event log via kardianos
		// later; for now exit non-zero so SCM marks the start as failed.
		os.Exit(1)
	}
	if err := config.Validate(cfg); err != nil {
		os.Exit(1)
	}

	logFile := openServiceLog(config.DefaultLogPath())
	logger := slog.New(slog.NewJSONHandler(logFile, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)
	if errors.Is(loadErr, config.ErrConfigMissing) {
		logger.Warn("config file missing — using defaults", "path", config.DefaultConfigPath())
	}
	logger.Info("simsim-pos-agent starting",
		"version", cfg.Version,
		"listen_port", cfg.ListenPort,
		"printer", cfg.PrinterName,
		"log_level", cfg.LogLevel,
		"mode", "service",
	)

	mutex, err := svcpkg.AcquireSingleInstance()
	if errors.Is(err, svcpkg.ErrAlreadyRunning) {
		logger.Warn("another instance is running; exiting", "mutex", svcpkg.MutexName)
		os.Exit(0)
	}
	if err != nil {
		logger.Error("single-instance mutex acquire failed", "err", err.Error())
		os.Exit(1)
	}
	defer mutex.Release()

	rt, err := buildRuntime(cfg, logger)
	if err != nil {
		logger.Error("runtime init failed", "err", err.Error())
		os.Exit(1)
	}

	prg := &svcpkg.Program{
		Server:    rt.Server,
		Logger:    logger,
		Heartbeat: rt.Heartbeat,
	}
	svc, err := ksvc.New(prg, svcpkg.BuildConfig())
	if err != nil {
		logger.Error("service.New failed", "err", err.Error())
		os.Exit(1)
	}

	if err := svc.Run(); err != nil {
		logger.Error("service.Run failed", "err", err.Error())
		os.Exit(1)
	}
	logger.Info("service stopped")
}

// serviceCmd dispatches `agent service <action>` to install / uninstall /
// start / stop / status.
func serviceCmd(args []string) {
	if len(args) < 1 {
		printUsage()
		return
	}

	// For control operations we don't actually run the Program — the
	// SCM-side install/start/stop/uninstall just needs the kardianos
	// Service binding. A bare Program is fine here.
	prg := &svcpkg.Program{Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	svc, err := ksvc.New(prg, svcpkg.BuildConfig())
	if err != nil {
		fmt.Fprintf(os.Stderr, "service.New: %v\n", err)
		os.Exit(1)
	}

	action := args[0]
	switch action {
	case "install":
		if err := svcpkg.Install(svc); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Service installed (delayed auto-start, restart on failure at 10s/30s/60s).")
	case "uninstall":
		if err := svcpkg.Uninstall(svc); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Service uninstalled.")
	case "start":
		if err := ksvc.Control(svc, "start"); err != nil {
			fmt.Fprintf(os.Stderr, "start: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Service started.")
	case "stop":
		if err := ksvc.Control(svc, "stop"); err != nil {
			fmt.Fprintf(os.Stderr, "stop: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Service stopped.")
	case "status":
		state, err := svcpkg.Status()
		if err != nil {
			fmt.Fprintf(os.Stderr, "status: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(state)
	default:
		printUsage()
	}
}

// loadAndOverride loads config.json from configPath and applies CLI
// overrides. Always sets Version from the build-time variable. Returns
// the loaded Config plus the original Load error so the caller can
// distinguish missing-file from validation problems.
func loadAndOverride(configPath, printerSpec string, port int, logLevel string, heartbeatSeconds int) (config.Config, error) {
	cfg, loadErr := config.Load(configPath)
	if printerSpec != "" {
		cfg.PrinterName = printerSpec
	}
	if port != 0 {
		cfg.ListenPort = port
	}
	if logLevel != "" {
		cfg.LogLevel = logLevel
	}
	if heartbeatSeconds != 0 {
		cfg.HeartbeatSeconds = heartbeatSeconds
	}
	cfg.Version = version
	return cfg, loadErr
}

// agentRuntime bundles the long-lived runtime objects shared by the
// foreground and service run paths.
type agentRuntime struct {
	Server    *api.Server
	Heartbeat *heartbeat.Loop // nil if cloud_base_url is empty
}

// buildRuntime constructs the api.Server (with printer + secrets wired
// in) and the heartbeat loop. Used by both the foreground and service
// run paths so they're guaranteed identical in capabilities.
//
// Secrets non-nil invariant: config.NewSecretStore returns a non-nil
// store on success. The api.Config.Secrets field receives that store —
// no nil-secrets path through this function.
func buildRuntime(cfg config.Config, logger *slog.Logger) (*agentRuntime, error) {
	var p printer.Printer
	if cfg.PrinterName != "" {
		var err error
		p, err = printer.New(cfg.PrinterName)
		if err != nil {
			return nil, fmt.Errorf("printer %q: %w", cfg.PrinterName, err)
		}
	} else {
		logger.Warn("no printer configured; /print will return PRINTER_NOT_CONFIGURED")
	}

	secStore, err := config.NewSecretStore(config.DefaultSecretsPath())
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}

	srv, err := api.New(api.Config{
		ListenAddr:     "127.0.0.1:" + strconv.Itoa(cfg.ListenPort),
		AllowedOrigins: cfg.AllowedOrigins,
		Version:        cfg.Version,
		Logger:         logger,
		Secrets:        secStore,
	}, p)
	if err != nil {
		return nil, err
	}

	rt := &agentRuntime{Server: srv}

	// Heartbeat loop — skip if no cloud configured (e.g. dev/CI agent
	// with cloud_base_url cleared in config.json).
	if cfg.CloudBaseURL != "" {
		rt.Heartbeat = &heartbeat.Loop{
			Cloud:    cloud.New(cfg.CloudBaseURL, cfg.Version),
			Secrets:  secStore,
			Printer:  p,
			Logger:   logger,
			Version:  cfg.Version,
			Interval: time.Duration(cfg.HeartbeatSeconds) * time.Second,
		}
	} else {
		logger.Warn("cloud_base_url empty; heartbeat loop disabled")
	}

	return rt, nil
}

// openServiceLog opens (creating dirs as needed) the service-mode log
// file. On any failure, falls back to os.Stderr so we never lose logs
// silently.
//
// TODO M3: wrap with gopkg.in/natefinch/lumberjack.v2 — currently
// appends without rotation, will grow unbounded in 24/7 service mode.
// Acceptable for pilot launch where M3 ships shortly after M2 stabilizes.
func openServiceLog(path string) io.Writer {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return os.Stderr
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return os.Stderr
	}
	return f
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
