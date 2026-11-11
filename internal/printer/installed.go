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
	// Direct is true for a USB printer the agent writes to itself, with no
	// Windows driver or queue (ISSUE-115). Driver is then empty.
	Direct bool `json:"direct,omitempty"`
	// Language is the receipt language the printer says it speaks
	// ("escpos", "tspl"), "" when it does not say or is a Windows queue.
	Language string `json:"language,omitempty"`
	// Spec is what config.json stores to find this printer again: the
	// queue name, or "usb:<name>" for a direct printer. Not sent; the web
	// app picks by Name.
	Spec string `json:"-"`
}

// SpecOrName is the config value for this printer.
func (i Installed) SpecOrName() string {
	if i.Spec != "" {
		return i.Spec
	}
	return i.Name
}

// mergeDirect puts the direct USB printers first and drops a Windows queue
// that prints to the same USB port (it is the same physical printer, and the
// direct path needs no driver). A device that could not tell its name is
// skipped: there is nothing to show or to find it by.
func mergeDirect(queues []Installed, devices []Device) []Installed {
	out := make([]Installed, 0, len(queues)+len(devices))
	ports := map[string]bool{}
	names := map[string]bool{}
	for _, q := range queues {
		names[strings.ToLower(q.Name)] = true
	}
	for _, d := range devices {
		name := d.ID.DisplayName()
		if name == "" {
			continue
		}
		if names[strings.ToLower(name)] {
			name += " (USB)"
		}
		names[strings.ToLower(name)] = true
		if p := d.Port(); p != "" {
			ports[p] = true
		}
		out = append(out, Installed{
			Name: name, Port: d.Port(), Direct: true, Language: d.ID.Language(),
			Spec: USBSpecPrefix + name,
		})
	}
	for _, q := range queues {
		if q.Port != "" && ports[q.Port] {
			continue
		}
		out = append(out, q)
	}
	return out
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
