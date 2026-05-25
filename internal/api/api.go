// Package api implements the local HTTP server exposed on 127.0.0.1 to
// the Simsim POS web app. See POS_AGENT_SPEC.md §5.3 and §5.4.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/config"
	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
)

// Default Config values, exposed only in the New constructor's defaulting.
const (
	defaultIdempotencyTTL            = 24 * time.Hour
	defaultIdempotencySweepInterval  = 5 * time.Minute
	defaultShutdownTimeout           = 5 * time.Second
)

// Config carries the runtime parameters New needs to assemble a Server.
type Config struct {
	// ListenAddr is the bind address, always "127.0.0.1:<port>".
	ListenAddr string

	// AllowedOrigins is the CORS allowlist; requests with no Origin header
	// (curl, agentctl) bypass the check entirely.
	AllowedOrigins []string

	// Version surfaces in the /health response.
	Version string

	// Logger is the slog handle used by middleware and handlers. Defaults
	// to slog.Default() when nil.
	Logger *slog.Logger

	// Secrets is the agent's pairing secret store, consulted by the
	// requireTerminalToken middleware. nil → all auth-protected endpoints
	// reject with NOT_PAIRED. Added in M2 (sub-task A5).
	Secrets config.SecretStore

	// IdempotencyTTL controls how long a /print response is replayable for
	// a repeat job_id. Defaults to 24h. Exposed for tests.
	IdempotencyTTL time.Duration

	// IdempotencySweepInterval controls how often the janitor goroutine
	// removes expired entries. Defaults to 5min. Exposed for tests.
	IdempotencySweepInterval time.Duration

	// PaperWidthMM is the thermal paper width (58 or 80) the renderer
	// formats receipts for, and the value reported via GET /capabilities.
	// Added in M13 A.5a. Defaults to 80 when zero (back-compat with
	// pre-A.5a constructions of api.Config — e.g. older tests).
	PaperWidthMM int

	// TSPLDialect selects the EAN-13 command variant for the label
	// printer's TSPL builder ("standard" or "rongta"). Added in M13
	// Track B PR 1. Empty / unset → treated as "standard" (the
	// Xprinter/TSC/Aclas baseline). Surfaced to /capabilities's
	// label sibling so the web client can mirror the choice in any
	// preview UI.
	TSPLDialect string

	// CloudReporter is the agent's outbound channel to the cloud's
	// /api/pos-agent/* endpoints. Used by the M13 print-verification
	// loopback handler (POST /report-verified) to forward operator-
	// confirmed test-print outcomes upstream with the agent's stored
	// terminal token. Nil → /report-verified returns 503; production
	// main wires this from a cloud.Client shim.
	CloudReporter CloudReporter
}

// CloudReporter is the narrow interface the M13 /report-verified
// handler depends on. Defined in this package (not internal/cloud)
// so api stays decoupled from the cloud client; production wiring
// passes a thin adapter, tests pass a fake.
type CloudReporter interface {
	ReportPrintVerified(
		ctx context.Context, token string, verified bool, errorClass string,
	) error
}

// Server holds the assembled HTTP handler chain and its dependencies.
//
// M13 Track B PR 1 — the single printer reference split into two:
// receiptPrinter drives ESC/POS /print + /test-print + /drawer/open,
// labelPrinter drives the TSPL /print-label endpoint (added in PR 2).
// One or both may be nil; per-endpoint guards (or printerForIntent)
// surface PRINTER_NOT_CONFIGURED / NO_LABEL_PRINTER_CONFIGURED.
type Server struct {
	cfg            Config
	logger         *slog.Logger
	receiptPrinter printer.Printer
	labelPrinter   printer.Printer
	secrets        config.SecretStore
	idem           *IdempotencyStore
	cloud          CloudReporter
	handler        http.Handler
}

// New builds a Server with all middleware wired and routes registered.
// The returned *Server is ready to serve via Run or to mount on an
// httptest.Server (via the unexported handler field; tests live in this
// package).
//
// Back-compat shape: callers pass the receipt printer as p (the
// pre-M13-Track-B signature). For a two-printer deployment, call
// NewTwo and pass both printers explicitly.
func New(cfg Config, p printer.Printer) (*Server, error) {
	return NewTwo(cfg, p, nil)
}

// NewTwo is the two-printer constructor introduced in M13 Track B PR 1.
// receiptPrinter drives ESC/POS endpoints (/print, /test-print,
// /drawer/open); labelPrinter drives the TSPL /print-label endpoint
// (added in PR 2 and reached via the intent='label' query param on
// /test-print).
//
// Either may be nil: handlers and printerForIntent surface
// PRINTER_NOT_CONFIGURED / NO_LABEL_PRINTER_CONFIGURED accordingly.
func NewTwo(cfg Config, receiptPrinter, labelPrinter printer.Printer) (*Server, error) {
	if cfg.ListenAddr == "" {
		return nil, errors.New("api: ListenAddr is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.IdempotencyTTL == 0 {
		cfg.IdempotencyTTL = defaultIdempotencyTTL
	}
	if cfg.IdempotencySweepInterval == 0 {
		cfg.IdempotencySweepInterval = defaultIdempotencySweepInterval
	}
	// M13 A.5a — back-compat default. Tests constructing api.Config
	// directly without PaperWidthMM (pre-A.5a era) get 80mm; the
	// production main wires this from the validated config.Config
	// loader, which rejects invalid values.
	if cfg.PaperWidthMM == 0 {
		cfg.PaperWidthMM = 80
	}
	// M13 Track B PR 1 — default TSPL dialect for callers that
	// construct api.Config directly. Production main wires this from
	// the validated config.Config loader (which has the same default).
	if cfg.TSPLDialect == "" {
		cfg.TSPLDialect = "standard"
	}

	s := &Server{
		cfg:            cfg,
		logger:         cfg.Logger,
		receiptPrinter: receiptPrinter,
		labelPrinter:   labelPrinter,
		secrets:        cfg.Secrets,
		idem:           NewIdempotencyStore(cfg.IdempotencyTTL),
		cloud:          cfg.CloudReporter,
	}

	mux := http.NewServeMux()
	// /health is intentionally unauthenticated — POS web app uses it to
	// discover the local agent and verify the bound terminal_id matches
	// the cashier's current session before sending any /print request.
	mux.HandleFunc("GET /health", s.handleHealth)
	// M13 A.1 — /handshake is also unauthenticated at the handler level:
	// reaching the loopback listener IS the trust boundary for the
	// bootstrap. It mints the JWT that /print + /test-print then verify.
	mux.HandleFunc("GET /handshake", s.handleHandshake)
	// M13 A.1 — /print + /test-print move from requireTerminalToken to
	// requireAuth, which accepts EITHER the new JWT (Authorization:
	// Bearer) OR the legacy X-Terminal-Token. The legacy header keeps
	// working until A.3 removes it (once the web client has cut over).
	mux.HandleFunc("POST /print", s.requireAuth(s.handlePrint))
	mux.HandleFunc("POST /test-print", s.requireAuth(s.handleTestPrint))
	// M13 Track B PR 2 — /print-label is the TSPL counterpart to /print.
	// Same JWT gate (requireAuth); routes to s.labelPrinter; surfaces
	// 503 NO_LABEL_PRINTER_CONFIGURED when no label printer is wired.
	mux.HandleFunc("POST /print-label", s.requireAuth(s.handlePrintLabel))
	// M13 print-verification — /report-verified is the loopback bridge
	// for operator-confirmed test-print outcomes. JWT-authed. Loads the
	// agent's stored terminal token from secrets and forwards to the
	// cloud's /api/pos-agent/print-verified.
	mux.HandleFunc("POST /report-verified", s.requireAuth(s.handleReportVerified))
	// M13 A.5a — /capabilities surfaces the printer-feature matrix
	// (paper width, cut, drawer, barcode types) the web client uses to
	// gate UI affordances. JWT-authed via requireAuth (same gate as
	// /print). Not on the legacy X-Terminal-Token path — /capabilities
	// is post-handshake, the web client always has a JWT before
	// touching it.
	mux.HandleFunc("GET /capabilities", s.requireAuth(s.handleCapabilities))
	// /drawer/open + /status are NOT print operations and are out of
	// the A.1 scope — they stay on the legacy X-Terminal-Token gate.
	mux.HandleFunc("POST /drawer/open", s.requireTerminalToken(s.handleDrawerOpen))
	mux.HandleFunc("GET /status", s.requireTerminalToken(s.handleStatus))

	// Outer → inner: recover, requestLog, checkLoopback, cors, mux.
	s.handler = chain(mux,
		s.recoverMiddleware,
		s.requestLogMiddleware,
		s.checkLoopbackMiddleware,
		s.corsMiddleware,
	)

	return s, nil
}

// Run binds a TCP4 loopback listener, serves until ctx is canceled, then
// initiates a graceful shutdown bounded by defaultShutdownTimeout.
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp4", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("api: listen %q: %w", s.cfg.ListenAddr, err)
	}

	httpsrv := &http.Server{Handler: s.handler}

	// Idempotency janitor — bound to the same ctx so it exits with Run.
	go s.idem.RunJanitor(ctx, s.cfg.IdempotencySweepInterval)

	serveErr := make(chan error, 1)
	go func() {
		err := httpsrv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			serveErr <- nil
		} else {
			serveErr <- err
		}
	}()

	s.logger.Info("api: listening", "addr", listener.Addr().String())

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		if err := httpsrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("api: shutdown: %w", err)
		}
		<-serveErr // drain the Serve goroutine
		return nil
	case err := <-serveErr:
		return err
	}
}

// chain composes middlewares so that mws[0] is the outermost wrapper.
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
