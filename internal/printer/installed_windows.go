//go:build windows

package printer

import (
	"os/exec"
	"syscall"
)

// ListInstalled returns the printers the shop can pick: the standard USB
// printers the agent drives directly (no driver needed), then the real
// Windows queues not already covered by one of them.
func ListInstalled() ([]Installed, error) {
	queues, qerr := listQueues()
	devices, derr := ListUSB()
	if qerr != nil && (derr != nil || len(devices) == 0) {
		return nil, qerr
	}
	return mergeDirect(queues, devices), nil
}

// listQueues asks Windows for its print queues (PowerShell's Get-Printer, the
// installer's own enumeration) and returns the real ones.
func listQueues() ([]Installed, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Get-Printer | Select-Object Name,DriverName,PortName | ConvertTo-Json -Compress")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseGetPrinterJSON(out)
}
