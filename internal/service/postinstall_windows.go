//go:build windows

package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// postInstall brings the registered service to its required
// configuration. Idempotent — Install runs it on fresh installs AND on
// upgrades over an existing service, and every step converges:
//
//  1. Logon account = ServiceAccount (NT SERVICE\SimsimPOSAgent), with an
//     explicit EMPTY password (virtual accounts have none). Installs made
//     before 2026-09-23 ran as NT AUTHORITY\LocalService; this is what
//     moves them. The change applies at the next service start — the
//     installer stops the service before copying files and starts it
//     after this step.
//  2. Service SID type = unrestricted, so the per-service SID is in the
//     process token (the SID the installer's icacls grants target).
//  3. DelayedAutoStart = true. Avoids competing with boot-critical
//     services during the post-boot rush; the spooler is rarely ready
//     immediately after boot anyway.
//  4. SetRecoveryActions: restart at 10s, then 30s, then 60s, with a
//     60-second reset period. The self-updater relies on this: it exits
//     non-zero after swapping the binary and the SCM restarts it.
func postInstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return err
	}
	defer s.Close()

	cfg, err := s.Config()
	if err != nil {
		return err
	}
	if !strings.EqualFold(cfg.ServiceStartName, ServiceAccount) {
		// mgr.UpdateConfig turns an empty Password into NULL ("leave
		// unchanged"); call ChangeServiceConfig directly so the password
		// is an explicit empty string, as a virtual account requires.
		empty, _ := windows.UTF16PtrFromString("")
		account, _ := windows.UTF16PtrFromString(ServiceAccount)
		if err := windows.ChangeServiceConfig(s.Handle,
			windows.SERVICE_NO_CHANGE, windows.SERVICE_NO_CHANGE, windows.SERVICE_NO_CHANGE,
			nil, nil, nil, nil, account, empty, nil); err != nil {
			return fmt.Errorf("set logon account %s: %w", ServiceAccount, err)
		}
		if cfg, err = s.Config(); err != nil {
			return err
		}
	}
	cfg.SidType = windows.SERVICE_SID_TYPE_UNRESTRICTED
	cfg.DelayedAutoStart = true
	if err := s.UpdateConfig(cfg); err != nil {
		return err
	}

	return s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 60)
}

// statusImpl returns a human-readable service state via the Service
// Control Manager. "not installed" if the service is not registered.
func statusImpl() (string, error) {
	m, err := mgr.Connect()
	if err != nil {
		return "", err
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		// ERROR_SERVICE_DOES_NOT_EXIST = 1060 — the service isn't
		// registered with the SCM at all.
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return "not installed", nil
		}
		return "", err
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return "", err
	}
	return svcStateName(st.State), nil
}

func svcStateName(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "starting"
	case svc.StopPending:
		return "stopping"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continuing"
	case svc.PausePending:
		return "pausing"
	case svc.Paused:
		return "paused"
	default:
		return "unknown"
	}
}
