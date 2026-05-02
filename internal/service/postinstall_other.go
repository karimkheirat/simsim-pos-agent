//go:build !windows

package service

// postInstall is a no-op on non-Windows. Production deploys to Windows;
// this stub keeps the package buildable on dev hosts (Linux/macOS).
func postInstall() error { return nil }

// statusImpl is unsupported off Windows — kardianos handles platform
// service managers, but mgr-based query is Windows-only.
func statusImpl() (string, error) { return "unsupported on this platform", nil }
