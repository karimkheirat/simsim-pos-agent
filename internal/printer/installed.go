package printer

import (
	"encoding/json"
	"strings"
)

// Installed is a printer Windows knows, as the first-setup picker shows it
// (simsim États G 14, 10 Nov 2026): its name, its driver and its port.
type Installed struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Port   string `json:"port"`
}

// IsVirtual reports printers that are not a device on the counter:
// remote-desktop and PDF/XPS writers, OneNote, fax. The same list the
// installer's picker hides (installer/printer-picker.iss, found at the first
// pilot shop on 9 Nov 2026). Case-insensitive.
func IsVirtual(name string) bool {
	l := strings.ToLower(name)
	for _, s := range []string{"anydesk", "teamviewer", "onenote", "fax", "microsoft print to pdf", "microsoft xps", "xps document writer", "pdf"} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// parseGetPrinterJSON reads `Get-Printer | Select-Object Name,DriverName,PortName
// | ConvertTo-Json`, which is an object for one printer and an array for
// several, and drops the virtual ones. Pure, so it is tested on every OS.
func parseGetPrinterJSON(raw []byte) ([]Installed, error) {
	type row struct {
		Name       string `json:"Name"`
		DriverName string `json:"DriverName"`
		PortName   string `json:"PortName"`
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return []Installed{}, nil
	}
	var rows []row
	if strings.HasPrefix(trimmed, "{") {
		var one row
		if err := json.Unmarshal([]byte(trimmed), &one); err != nil {
			return nil, err
		}
		rows = []row{one}
	} else if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
		return nil, err
	}
	out := make([]Installed, 0, len(rows))
	for _, r := range rows {
		if strings.TrimSpace(r.Name) == "" || IsVirtual(r.Name) {
			continue
		}
		out = append(out, Installed{Name: r.Name, Driver: r.DriverName, Port: r.PortName})
	}
	return out, nil
}
