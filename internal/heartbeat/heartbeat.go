// Package heartbeat runs the periodic POST /api/pos-agent/heartbeat
// loop while the agent service is up. Lifecycle is bound to the same
// context that drives api.Server.Run — the kardianos service Program
// starts both goroutines on Start and cancels their shared context on
// Stop.
//
// Behavior summary (M2 sub-task A7):
//   - Paired (secrets present): tick every Interval (config-driven,
//     default 5min), send heartbeat, log result.
//   - Unpaired (ErrNoSecrets): tick every UnpairedRecheckInterval
//     (default 60s), don't touch the cloud. Picks up a fresh pair from
//     `agentctl pair` on the next tick — no service restart needed.
//   - 401 UNAUTHENTICATED: cloud has revoked our token; clear local
//     secrets via SecretStore.Clear and log loudly. Subsequent ticks
//     see ErrNoSecrets and switch to recheck cadence.
//   - ErrNetwork: log at debug, swallow, retry next tick. M3's outbox
//     will queue these for replay; M2 just drops them.
//   - Any other cloud error: log at warn, swallow, retry next tick.
package heartbeat

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/cloud"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
)

// defaultUnpairedRecheckInterval is the cadence at which the loop polls
// the secret store while unpaired, looking for a freshly-paired token.
const defaultUnpairedRecheckInterval = 60 * time.Second

// Loop is the periodic heartbeat sender. Construct via the public fields
// then call Run; Run blocks until ctx is canceled.
type Loop struct {
	Cloud   *cloud.Client
	Secrets config.SecretStore
	// Printer may be nil — heartbeats then report configured=false.
	Printer printer.Printer
	Logger  *slog.Logger
	Version string

	// Interval is the paired-state heartbeat cadence. Production: 5min
	// from config.Config.HeartbeatSeconds. Tests typically pass 50ms.
	Interval time.Duration

	// UnpairedRecheckInterval is the polling cadence while unpaired.
	// Zero means defaultUnpairedRecheckInterval (60s). Tests override.
	UnpairedRecheckInterval time.Duration

	// started is the wall-clock time of Run entry; used to compute
	// uptime_seconds in the heartbeat payload.
	started time.Time
}

// Run blocks until ctx is canceled. Fires the first heartbeat
// immediately (or skips if unpaired), then ticks at Interval (paired)
// or UnpairedRecheckInterval (unpaired).
func (l *Loop) Run(ctx context.Context) {
	l.started = time.Now()
	recheck := l.UnpairedRecheckInterval
	if recheck <= 0 {
		recheck = defaultUnpairedRecheckInterval
	}

	for {
		paired := l.tick(ctx)
		if ctx.Err() != nil {
			return
		}
		var wait time.Duration
		if paired {
			wait = l.Interval
		} else {
			wait = recheck
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// tick performs one heartbeat attempt. Returns paired==true if secrets
// were present at the start of this tick (regardless of whether the
// cloud call succeeded). The wait cadence is governed by paired state,
// not by cloud success.
func (l *Loop) tick(ctx context.Context) bool {
	secrets, err := l.Secrets.Load()
	if errors.Is(err, config.ErrNoSecrets) {
		l.Logger.Debug("heartbeat: unpaired; skipping")
		return false
	}
	if err != nil {
		l.Logger.Warn("heartbeat: secret store load failed; skipping",
			"err", err.Error())
		// Treat load errors as "still paired" for cadence purposes —
		// avoid hammering the secret store at the recheck rate when
		// the underlying problem is a transient IO error.
		return true
	}

	hb := l.buildHeartbeat()
	cloudErr := l.Cloud.Heartbeat(ctx, secrets.TerminalToken, hb)

	switch {
	case cloudErr == nil:
		l.Logger.Debug("heartbeat: ok",
			"uptime_seconds", hb.UptimeSeconds,
			"printer_reachable", hb.Printer.Reachable)
	case errors.Is(cloudErr, cloud.ErrUnauthenticated):
		l.Logger.Error("heartbeat: 401 UNAUTHENTICATED — clearing local secrets, agent now unpaired")
		if cerr := l.Secrets.Clear(); cerr != nil {
			l.Logger.Error("heartbeat: secret clear failed after 401", "err", cerr.Error())
		}
	case errors.Is(cloudErr, cloud.ErrNetwork):
		l.Logger.Debug("heartbeat: network error; will retry next tick",
			"err", cloudErr.Error())
	default:
		l.Logger.Warn("heartbeat: cloud error; will retry next tick",
			"err", cloudErr.Error())
	}
	return true
}

// buildHeartbeat assembles the request payload from current state.
// Printer status is best-effort — IsReachable() is allowed to be
// expensive (e.g. a 200ms spooler probe), the cloud only sees the bool.
func (l *Loop) buildHeartbeat() cloud.HeartbeatRequest {
	var ps cloud.PrinterStatus
	if l.Printer != nil && l.Printer.Name() != "" {
		ps = cloud.PrinterStatus{
			Configured: true,
			Reachable:  l.Printer.IsReachable(),
			Name:       l.Printer.Name(),
			LastError:  nil, // M3: wire real error tracking.
		}
	}
	return cloud.HeartbeatRequest{
		AgentVersion:  l.Version,
		OSVersion:     runtime.GOOS, // M3 polish: include "Windows 11 23H2"-style detail.
		UptimeSeconds: int64(time.Since(l.started).Seconds()),
		Printer:       ps,
	}
}
