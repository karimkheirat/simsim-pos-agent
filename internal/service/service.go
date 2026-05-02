// Package service wraps github.com/kardianos/service to host the agent
// as a Windows service (and dev-mode dispatch on other platforms).
//
// Public surface:
//   - Program: kardianos service.Interface implementation that runs the
//     api.Server in the background and shuts it down on Stop.
//   - BuildConfig / ServiceName: stable identifiers for the SCM entry.
//   - Install / Uninstall: kardianos Control + Windows-specific
//     post-install enrichment (delayed auto-start + restart-on-failure
//     progression at 10s/30s/60s).
//   - Status: human-readable service state via x/sys/windows/svc/mgr on
//     Windows; "unsupported" elsewhere.
//   - AcquireSingleInstance: named mutex Global\SimsimPOSAgent so we
//     can refuse to start a second copy.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	// kardianos/service pinned to v1.1.0 to maintain the Go 1.22 floor;
	// v1.2.4 requires Go 1.23+. Service status is implemented directly
	// via x/sys/windows/svc/mgr (which we already pin) rather than
	// v1.2.4's Service.Status() method — also gives us cleaner SCM
	// error mapping (ERROR_SERVICE_DOES_NOT_EXIST → "not installed").
	ksvc "github.com/kardianos/service"

	"github.com/karimkheirat/simsim-pos-agent/internal/api"
	"github.com/karimkheirat/simsim-pos-agent/internal/heartbeat"
)

// ServiceName is the SCM identifier — referenced by sc.exe and by the
// Status query. Spec §5.1.
const ServiceName = "SimsimPOSAgent"

// BuildConfig returns the kardianos/service Config. Stable for the
// lifetime of this binary; used by both `service install` and the
// service-runtime path so install + run agree on identity.
func BuildConfig() *ksvc.Config {
	return &ksvc.Config{
		Name:        ServiceName,
		DisplayName: "Simsim POS Agent",
		Description: "Local printer agent for Simsim POS — handles receipt printing and cash drawer control.",
		// Account choice per spec §5.1: LocalService is sufficient for
		// raw spooler print jobs to a shared local printer. If an
		// install hits a printer that refuses LocalService, the manual
		// fix is to re-install under a user account (sc.exe config /
		// kardianos --user flag in M3 polish).
		UserName: `NT AUTHORITY\LocalService`,
	}
}

// Program implements ksvc.Interface. Hands the api.Server + heartbeat
// loop lifecycle to the SCM: Start kicks off both in goroutines sharing
// one context, Stop cancels and waits up to 10s for graceful shutdown.
type Program struct {
	Server *api.Server
	Logger *slog.Logger
	// Heartbeat is optional. nil → no cloud heartbeats (e.g. when the
	// agent is misconfigured with no CloudBaseURL). The api server still
	// runs.
	Heartbeat *heartbeat.Loop

	cancel        context.CancelFunc
	serverDone    chan error
	heartbeatDone chan struct{}
}

// Start is invoked by the SCM (or by service.Run in foreground service
// dispatch). MUST NOT block — both the server and the heartbeat loop
// run in goroutines.
func (p *Program) Start(_ ksvc.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.serverDone = make(chan error, 1)
	go func() {
		err := p.Server.Run(ctx)
		p.serverDone <- err
	}()

	if p.Heartbeat != nil {
		p.heartbeatDone = make(chan struct{})
		go func() {
			p.Heartbeat.Run(ctx)
			close(p.heartbeatDone)
		}()
	}

	p.Logger.Info("service started",
		"service_name", ServiceName,
		"heartbeat_enabled", p.Heartbeat != nil)
	return nil
}

// Stop is invoked by the SCM. Cancels the shared context and blocks up
// to 10s waiting for graceful shutdown of both goroutines.
func (p *Program) Stop(_ ksvc.Service) error {
	p.Logger.Info("service stopping", "service_name", ServiceName)
	if p.cancel == nil {
		return nil
	}
	p.cancel()
	select {
	case err := <-p.serverDone:
		if err != nil {
			p.Logger.Error("service: server returned error on shutdown", "err", err.Error())
		}
	case <-time.After(10 * time.Second):
		return errors.New("service: server did not exit within 10s of stop")
	}
	if p.heartbeatDone != nil {
		select {
		case <-p.heartbeatDone:
		case <-time.After(2 * time.Second):
			return errors.New("service: heartbeat loop did not exit within 2s of stop")
		}
	}
	return nil
}

// Install registers the service with the OS service manager and applies
// platform-specific post-install enrichment. On Windows: delayed auto-
// start + restart-on-failure progression at 10s/30s/60s with a 60s reset
// period. On other platforms: kardianos defaults only.
//
// Returns an error if install or post-install fails. Post-install errors
// leave the service installed (kardianos already created the SCM entry)
// but missing the failure-recovery polish — the operator can re-run
// install or configure via sc.exe.
func Install(svc ksvc.Service) error {
	if err := ksvc.Control(svc, "install"); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	if err := postInstall(); err != nil {
		return fmt.Errorf("post-install (service installed; recovery actions unset): %w", err)
	}
	return nil
}

// Uninstall removes the service from the OS service manager.
func Uninstall(svc ksvc.Service) error {
	if err := ksvc.Control(svc, "uninstall"); err != nil {
		return fmt.Errorf("uninstall: %w", err)
	}
	return nil
}
