//go:build windows

package printer

import (
	"os/exec"
	"syscall"
)

// ListInstalled asks Windows for its printers (PowerShell's Get-Printer, the
// installer's own enumeration) and returns the real ones.
func ListInstalled() ([]Installed, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Get-Printer | Select-Object Name,DriverName,PortName | ConvertTo-Json -Compress")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseGetPrinterJSON(out)
}
