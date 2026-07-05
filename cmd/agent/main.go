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
	"github.com/karimkheirat/simsim-pos-agent/internal/scale"
	svcpkg "github.com/karimkheirat/simsim-pos-agent/internal/service"
)

// Version is the build-injected version string. The "dev" default
// applies to local `go build` invocations; release builds set it via
//
//   go build -ldflags "-X main.Version=0.3.0" ...
//
// It is the single source of truth for agent version reporting:
// /health and /status responses, heartbeat payloads, log fields, and
// the --version CLI flag all surface this value.
var Version = "dev"

const usageTemplate = `Simsim POS Agent %s

Usage:
  agent run [flags]
  agent service install
  agent service uninstall
  agent service start
  agent service stop
  agent service status
  agent write-config [flags]
  agent --version

Flags (run):
  --config string             Path to config.json
  --printer string            Override printer spec (e.g. "SP-331" or "file:./out")
  --port int                  Override listen port (default 47291)
  --log-level string          debug | info | warn | error
  --heartbeat-seconds int     Override heartbeat cadence (default 300)

Flags (write-config):
  --config string             Path to config.json
  --printer string            Set printer_name (skip arg leaves unchanged)
  --cloud-base-url string     Set cloud_base_url (skip arg leaves unchanged)
  --scale-ip string           Set scale_ip (skip arg leaves unchanged)
  --scale-port int            Set scale_port (0 leaves unchanged)
`

func main() {
	// When SCM starts the service, ksvc.Interactive() returns false. In
	// that branch we don't process os.Args at all — the SCM controls
	// lifecycle via Program.Start / Stop.
	if !ksvc.Interactive() {
		runAsService()
		return
	}

	// Top-level --version flag, parsed via the standard flag package
	// against a private FlagSet so it does not interfere with subcommand
	// dispatch below. Subcommand-specific flags are parsed inside their
	// handlers.
	if handleVersionFlag(os.Args[1:]) {
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
	case "write-config":
		writeConfigCmd(os.Args[2:])
	default:
		// Unknown subcommands fall through to friendly usage.
		printUsage()
	}
}

func printUsage() {
	fmt.Printf(usageTemplate, Version)
}

// handleVersionFlag inspects args for a top-level --version (or -version)
// flag; if present, prints Version to stdout and returns true to signal
// the caller to exit cleanly. Returns false otherwise.
//
// Uses a private FlagSet with ContinueOnError + io.Discard output so
// unknown flags (which subcommands handle) don't generate noise here.
// Parse stops at the first non-flag arg, so subcommands like
// "agent service install --foo" reach the dispatch below unaffected.
func handleVersionFlag(args []string) bool {
	fs := flag.NewFlagSet("agent-top", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	showVersion := fs.Bool("version", false, "print version and exit")
	_ = fs.Parse(args)
	if *showVersion {
		fmt.Println(Version)
		return true
	}
	return false
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
		"receipt_printer", cfg.ReceiptPrinterName,
		"label_printer", cfg.LabelPrinterName,
		"tspl_dialect", cfg.TSPLDialect,
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
		"receipt_printer", cfg.ReceiptPrinterName,
		"label_printer", cfg.LabelPrinterName,
		"tspl_dialect", cfg.TSPLDialect,
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
		// --printer is the legacy CLI flag (sets receipt printer for
		// back-compat with pre-M13-B operators / scripts). Mirror into
		// ReceiptPrinterName so the two-printer wiring picks it up.
		cfg.PrinterName = printerSpec
		cfg.ReceiptPrinterName = printerSpec
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
	cfg.Version = Version
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
	// M13 Track B PR 1 — two-printer wiring. ReceiptPrinterName
	// drives the ESC/POS receipt path; LabelPrinterName drives the
	// TSPL label path (added in PR 2). Either may be empty.
	//
	// Back-compat: config.Load mirrors a legacy `printer_name` into
	// ReceiptPrinterName, so deployed pre-Track-B agents keep working
	// without touching their config.json.
	receiptP, err := newPrinterOrNil(cfg.ReceiptPrinterName, "receipt", logger)
	if err != nil {
		return nil, err
	}
	labelP, err := newPrinterOrNil(cfg.LabelPrinterName, "label", logger)
	if err != nil {
		return nil, err
	}

	// Scale sync — the LAN label scale beside the two printers.
	// nil when scale_ip/scale_port are unset (config.Validate already
	// guaranteed they're either both set or both empty).
	scaleDev := newScaleOrNil(cfg.ScaleIP, cfg.ScalePort, logger)

	secStore, err := config.NewSecretStore(config.DefaultSecretsPath())
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}

	// M13 print-verification — share one cloud.Client between the
	// heartbeat loop (existing) and the api Server's CloudReporter
	// (new, for /report-verified). When CloudBaseURL is empty (dev/CI
	// builds), both surfaces gracefully no-op via their nil checks.
	var cloudClient *cloud.Client
	if cfg.CloudBaseURL != "" {
		cloudClient = cloud.New(cfg.CloudBaseURL, cfg.Version)
	}

	srv, err := api.NewTwo(api.Config{
		ListenAddr:     "127.0.0.1:" + strconv.Itoa(cfg.ListenPort),
		AllowedOrigins: cfg.AllowedOrigins,
		Version:        cfg.Version,
		Logger:         logger,
		Secrets:        secStore,
		// M13 A.5a — paper width from validated agent config (58 or 80).
		PaperWidthMM: cfg.PaperWidthMM,
		// M13 Track B PR 1 — TSPL dialect ("standard" or "rongta").
		TSPLDialect: cfg.TSPLDialect,
		// TSPL receipt path — language selector + sizing knobs. WidthDots
		// resolved here (config 0 = derive from PaperWidthMM).
		ReceiptPrinterLanguage: cfg.ReceiptPrinterLanguage,
		ReceiptWidthDots:       cfg.EffectiveReceiptWidthDots(),
		DPI:                    cfg.DPI,
		// M13 print-verification — forward operator-confirmed test-print
		// outcomes to the cloud's /api/pos-agent/print-verified. nil
		// when no cloud is configured; api's defensive 503 surfaces.
		CloudReporter: cloudReporterAdapter(cloudClient),
		// Scale sync — nil when no scale is configured; the
		// /scale/sync-plu handler surfaces NO_SCALE_CONFIGURED.
		Scale: scaleDev,
	}, receiptP, labelP)
	if err != nil {
		return nil, err
	}

	rt := &agentRuntime{Server: srv}

	// Heartbeat loop — skip if no cloud configured (e.g. dev/CI agent
	// with cloud_base_url cleared in config.json). v1 heartbeat reports
	// only the receipt printer's status; label-printer status surfaces
	// only via /health + /capabilities. Future M13 Track B PR (heartbeat
	// extension) can split this into two cloud fields.
	if cloudClient != nil {
		rt.Heartbeat = &heartbeat.Loop{
			Cloud:    cloudClient,
			Secrets:  secStore,
			Printer:  receiptP,
			Logger:   logger,
			Version:  cfg.Version,
			Interval: time.Duration(cfg.HeartbeatSeconds) * time.Second,
		}
	} else {
		logger.Warn("cloud_base_url empty; heartbeat loop disabled")
	}

	return rt, nil
}

// cloudReporterAdapter wraps a *cloud.Client into the narrow
// api.CloudReporter interface. Internal to cmd/agent so internal/api
// stays free of any dependency on internal/cloud (the interface is
// declared in api/api.go for test injectability).
//
// Returns nil when c is nil — api's defensive 503 covers the
// "cloud-disabled" deploy mode for /report-verified.
func cloudReporterAdapter(c *cloud.Client) api.CloudReporter {
	if c == nil {
		return nil
	}
	return &cloudReporterShim{client: c}
}

// cloudReporterShim implements api.CloudReporter by forwarding to a
// concrete *cloud.Client. The shim collapses api's narrow
// (verified, errorClass) signature back into the cloud client's
// PrintVerifiedRequest struct so internal/api stays decoupled.
type cloudReporterShim struct {
	client *cloud.Client
}

func (s *cloudReporterShim) ReportPrintVerified(
	ctx context.Context, token string, verified bool, errorClass string,
) error {
	return s.client.ReportPrintVerified(ctx, token, cloud.PrintVerifiedRequest{
		Verified:   verified,
		ErrorClass: errorClass,
	})
}

// newPrinterOrNil constructs a printer.Printer from the given spec or
// returns (nil, nil) when spec is empty (operator has not configured
// that role). Logs a kind-tagged warning on empty so the operator
// pulling agent.log sees which role is unconfigured.
func newPrinterOrNil(spec, kind string, logger *slog.Logger) (printer.Printer, error) {
	if spec == "" {
		logger.Warn("no "+kind+" printer configured", "role", kind)
		return nil, nil
	}
	p, err := printer.New(spec)
	if err != nil {
		return nil, fmt.Errorf("%s printer %q: %w", kind, spec, err)
	}
	return p, nil
}

// newScaleOrNil constructs the TCP scale from scale_ip/scale_port or
// returns nil when the operator has not configured one (both fields
// empty — config.Validate rejects half-configured pairs). The typed
// *scale.TCP is only wrapped into the scale.Scale interface when
// non-nil, so api's `s.scale == nil` check stays truthful.
func newScaleOrNil(ip string, port int, logger *slog.Logger) scale.Scale {
	if ip == "" {
		logger.Warn("no scale configured", "role", "scale")
		return nil
	}
	return scale.NewTCP(ip, port)
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
