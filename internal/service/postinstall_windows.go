//go:build windows

package service

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// postInstall enriches the kardianos-installed service with two
// Windows-specific configurations the kardianos v1.1.0 Config doesn't
// expose:
//
//  1. DelayedAutoStart = true. Avoids competing with boot-critical
//     services during the post-boot rush; the spooler is rarely ready
//     immediately after boot anyway.
//  2. SetRecoveryActions: restart at 10s, then 30s, then 60s, with a
//     60-second reset period (any 60s of healthy uptime resets the
//     failure counter).
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
