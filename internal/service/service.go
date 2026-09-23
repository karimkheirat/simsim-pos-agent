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
	"github.com/karimkheirat/simsim-pos-agent/internal/scalesync"
)

// ServiceName is the SCM identifier — referenced by sc.exe and by the
// Status query. Spec §5.1.
const ServiceName = "SimsimPOSAgent"

// ServiceAccount is the Windows virtual service account the service runs
// as: a per-service identity (SID S-1-5-80-<hash of the name>) that the
// SCM manages itself — no password, no rights shared with any other
// service, and not SYSTEM. It replaced NT AUTHORITY\LocalService on
// 2026-09-23 so the self-updater can be granted write access to its own
// bin directory without extending that right to every LocalService
// process on the PC. The installer grants this name Modify on {app}in
// and %ProgramData%\Simsim (icacls "NT SERVICE\SimsimPOSAgent"), which
// only resolves once the service exists.
const ServiceAccount = `NT SERVICE\` + ServiceName

// BuildConfig returns the kardianos/service Config. Stable for the
// lifetime of this binary; used by both `service install` and the
// service-runtime path so install + run agree on identity.
func BuildConfig() *ksvc.Config {
	return &ksvc.Config{
		Name:        ServiceName,
		DisplayName: "Simsim POS Agent",
		Description: "Local printer agent for Simsim POS — handles receipt printing and cash drawer control.",
		// Virtual service account (see ServiceAccount). Like
		// LocalService it is a low-privilege local identity, so raw
		// spooler jobs to a local printer work the same way. Virtual
		// accounts take no password — kardianos passes Option
		// "Password" (unset → "") to CreateService. Existing installs
		// created under LocalService are moved by postInstall.
		UserName: ServiceAccount,
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
	// ScaleSync is optional. nil → no scale PLU-file mirroring (same
	// no-cloud condition as Heartbeat).
	ScaleSync *scalesync.Loop
	// Updater is optional. nil → no self-update loop and no post-update
	// cleanup (internal/updater; POS_AGENT_SPEC.md §9).
	Updater Runner

	cancel        context.CancelFunc
	serverDone    chan error
	heartbeatDone chan struct{}
	scaleSyncDone chan struct{}
	updaterDone   chan struct{}
}

// Runner is a background loop bound to the service context. The
// self-updater satisfies it; declared here so service does not import
// internal/updater.
type Runner interface {
	Run(ctx context.Context)
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

	if p.ScaleSync != nil {
		p.scaleSyncDone = make(chan struct{})
		go func() {
			p.ScaleSync.Run(ctx)
			close(p.scaleSyncDone)
		}()
	}

	if p.Updater != nil {
		p.updaterDone = make(chan struct{})
		go func() {
			p.Updater.Run(ctx)
			close(p.updaterDone)
		}()
	}

	p.Logger.Info("service started",
		"service_name", ServiceName,
		"heartbeat_enabled", p.Heartbeat != nil,
		"scale_sync_enabled", p.ScaleSync != nil,
		"updater_enabled", p.Updater != nil)
	return nil
}

// ShutdownForRestart stops the HTTP server gracefully (in-flight
// requests finish, bounded by api's 5 s shutdown timeout) and returns,
// so the self-updater can exit the process non-zero right after the
// binary swap. It deliberately does NOT wait for the updater loop — it
// is called FROM that loop. The SCM sees the process die without a
// SERVICE_STOPPED report and applies the recovery action (restart after
// 10 s), which starts the new binary.
func (p *Program) ShutdownForRestart() {
	p.Logger.Info("service: shutting down for self-update restart")
	if p.cancel == nil {
		return
	}
	p.cancel()
	select {
	case err := <-p.serverDone:
		if err != nil {
			p.Logger.Error("service: server returned error on update shutdown", "err", err.Error())
		}
	case <-time.After(10 * time.Second):
		p.Logger.Warn("service: server did not stop within 10s; exiting anyway")
	}
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
	if p.scaleSyncDone != nil {
		select {
		case <-p.scaleSyncDone:
		case <-time.After(2 * time.Second):
			return errors.New("service: scale-sync loop did not exit within 2s of stop")
		}
	}
	if p.updaterDone != nil {
		select {
		case <-p.updaterDone:
		case <-time.After(2 * time.Second):
			return errors.New("service: updater loop did not exit within 2s of stop")
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
//
// Idempotent (2026-09-23): when the service is ALREADY registered — an
// upgrade over an existing install — the create step is skipped and
// postInstall still runs. postInstall is what moves an old install's
// logon account from LocalService to the virtual account, so the
// installer's `service install` step converges every install on the same
// configuration whether it is fresh or an upgrade.
func Install(svc ksvc.Service) error {
	return installWithDeps(svc, statusImpl, postInstall)
}

// installWithDeps is the testable variant of Install.
func installWithDeps(svc ksvc.Service, status func() (string, error), post func() error) error {
	state, err := status()
	if err != nil {
		return fmt.Errorf("install: query existing service: %w", err)
	}
	if state == "not installed" || !isWindowsState(state) {
		if err := ksvc.Control(svc, "install"); err != nil {
			return fmt.Errorf("install: %w", err)
		}
	}
	if err := post(); err != nil {
		return fmt.Errorf("post-install (service registered; account/recovery settings not applied): %w", err)
	}
	return nil
}

// isWindowsState reports whether state is one statusImpl returns for a
// service that EXISTS in the SCM. Off Windows statusImpl returns
// "unsupported on this platform", which must still go through the
// kardianos create path.
func isWindowsState(state string) bool {
	switch state {
	case "stopped", "starting", "stopping", "running", "continuing", "pausing", "paused", "unknown":
		return true
	}
	return false
}

// Uninstall removes the service from the OS service manager. If the
// service is currently RUNNING, it is stopped first (10s timeout) so we
// don't leave an orphan process holding the single-instance mutex
// (Global\SimsimPOSAgent) — which would silently prevent the next
// install from starting.
//
// Stop failures or timeouts are logged at warn level but do not block
// uninstall: a stuck service should not be allowed to refuse SCM
// unregistration. Operator can taskkill the orphan and retry install.
//
// Fixes the M2 wart documented in M2_AGENT_COMPLETION_REPORT.md §14.
func Uninstall(svc ksvc.Service) error {
	return uninstallWithDeps(svc, statusImpl, 10*time.Second, slog.Default())
}

// uninstallWithDeps is the testable variant. Production Uninstall pins
// statusImpl + 10s timeout + slog.Default(); tests inject canned status
// values, fast timeouts (~50ms), and a discard logger.
func uninstallWithDeps(svc ksvc.Service, status func() (string, error), stopTimeout time.Duration, logger *slog.Logger) error {
	state, err := status()
	if err != nil {
		// Status query failure shouldn't block uninstall — the SCM may
		// still be reachable for the unregister itself.
		logger.Warn("service: status query failed before uninstall; proceeding anyway",
			"err", err.Error())
	}
	if state == "running" {
		logger.Info("service: stopping before uninstall",
			"stop_timeout", stopTimeout.String())
		if err := stopWithTimeout(svc, stopTimeout); err != nil {
			logger.Warn("service: stop before uninstall failed; proceeding to unregister anyway",
				"err", err.Error())
		}
	}
	if err := ksvc.Control(svc, "uninstall"); err != nil {
		return fmt.Errorf("uninstall: %w", err)
	}
	return nil
}

// stopWithTimeout calls ksvc.Control(svc, "stop") in a goroutine and
// returns either its result or a timeout error. If the timeout fires,
// the goroutine keeps running in the background — uninstall is a
// short-lived operation so the leak is bounded by process lifetime.
func stopWithTimeout(svc ksvc.Service, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- ksvc.Control(svc, "stop")
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("stop did not return within %v", timeout)
	}
}
