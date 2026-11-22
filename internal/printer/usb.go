package printer

import (
	"errors"
	"fmt"
	"strings"
)

// Direct USB printing (ISSUE-115, 11 Nov 2026).
//
// Windows loads its own USB printer connector (usbprint.sys) for every
// device that reports the USB printer class, with no download. The
// connector does not drive the printer; it passes bytes. The agent already
// renders the printer's own language (ESC/POS or TSPL), so it can write
// straight to the device and needs no driver and no print queue. A printer
// that does not report the printer class gets no connector and does not
// appear here; it still needs its maker's driver and a Windows queue.
//
// Each such device also answers with its IEEE 1284 Device ID: who made it,
// its model and the command languages it speaks. That is how the agent
// names the printer and picks receipt (ESC/POS) or label (TSPL) language
// without asking.

// USBSpecPrefix marks a printer spec that names a direct USB device
// (config receipt_printer_name / label_printer_name), e.g.
// "usb:Xprinter XP-370B". Anything after the prefix is the device's display
// name; an empty name means "the only standard USB printer plugged in".
const USBSpecPrefix = "usb:"

// ErrUSBNotFound is returned when no plugged-in USB printer matches a spec.
var ErrUSBNotFound = errors.New("usb printer not found")

// DeviceID is a parsed IEEE 1284 Device ID ("MFG:Xprinter;MDL:XP-370B;
// CMD:TSPL,ESC/POS;CLS:PRINTER;").
type DeviceID struct {
	Manufacturer string
	Model        string
	CommandSet   []string
	Raw          string
}

// ParseDeviceID reads the key:value; pairs of a 1284 Device ID. Keys are
// matched case-insensitively and in both their long and short spellings
// (MANUFACTURER/MFG, MODEL/MDL, COMMAND SET/CMD). Unknown keys are ignored.
func ParseDeviceID(s string) DeviceID {
	d := DeviceID{Raw: s}
	for _, field := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(field, ":")
		if !ok {
			continue
		}
		k = strings.ToUpper(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		switch k {
		case "MFG", "MANUFACTURER":
			d.Manufacturer = v
		case "MDL", "MODEL":
			d.Model = v
		case "CMD", "COMMAND SET":
			for _, c := range strings.Split(v, ",") {
				if c = strings.TrimSpace(c); c != "" {
					d.CommandSet = append(d.CommandSet, c)
				}
			}
		}
	}
	return d
}

// DisplayName is the name the shop sees: maker and model, or the model
// alone when the model already starts with the maker's name.
func (d DeviceID) DisplayName() string {
	mfg, mdl := strings.TrimSpace(d.Manufacturer), strings.TrimSpace(d.Model)
	switch {
	case mdl == "":
		return mfg
	case mfg == "" || strings.HasPrefix(strings.ToLower(mdl), strings.ToLower(mfg)):
		return mdl
	default:
		return mfg + " " + mdl
	}
}

// Language returns the receipt language the agent should render for this
// device: "escpos", "tspl", or "" when the device does not say (or says
// only languages the agent does not render). A device listing both picks
// ESC/POS, the receipt language; the label printer path renders TSPL
// regardless.
func (d DeviceID) Language() string {
	escpos, tspl := false, false
	for _, c := range d.CommandSet {
		u := strings.ToUpper(strings.ReplaceAll(c, " ", ""))
		switch {
		case strings.Contains(u, "ESC/POS"), strings.Contains(u, "ESCPOS"), u == "EPSON", strings.HasPrefix(u, "ESC/P"):
			escpos = true
		case strings.HasPrefix(u, "TSPL"), u == "TSC":
			tspl = true
		}
	}
	switch {
	case escpos:
		return "escpos"
	case tspl:
		return "tspl"
	default:
		return ""
	}
}

// Device is one standard USB printer Windows' connector knows.
type Device struct {
	// Path is the device interface path CreateFile opens. It changes when
	// the printer moves to another USB socket, so it is never stored.
	Path string
	ID   DeviceID
	// PortNumber is the usbprint port (1 → "USB001"), 0 when unknown. A
	// Windows queue on that port is the same physical printer.
	PortNumber int
}

// Port is the Windows port name of the device ("USB001"), "" when unknown.
func (d Device) Port() string {
	if d.PortNumber <= 0 {
		return ""
	}
	return fmt.Sprintf("USB%03d", d.PortNumber)
}

// Spec is the config value that names this device.
func (d Device) Spec() string { return USBSpecPrefix + d.ID.DisplayName() }

// MatchUSB picks the device a spec names: by display name (case-insensitive),
// or, for a bare "usb:", the one device when exactly one is plugged in.
func MatchUSB(spec string, devices []Device) (Device, error) {
	want := strings.TrimSpace(strings.TrimPrefix(spec, USBSpecPrefix))
	// mergeDirect adds " (USB)" when a queue already carries the name.
	want = strings.TrimSpace(strings.TrimSuffix(want, " (USB)"))
	if want == "" {
		if len(devices) == 1 {
			return devices[0], nil
		}
		return Device{}, fmt.Errorf("%w: %d USB printers plugged in, name one", ErrUSBNotFound, len(devices))
	}
	for _, d := range devices {
		if strings.EqualFold(d.ID.DisplayName(), want) {
			return d, nil
		}
	}
	return Device{}, fmt.Errorf("%w: %q", ErrUSBNotFound, want)
}

// Status is what the printer says about itself, read without printing.
// Known is false when the printer did not answer (or answered nonsense);
// the other fields are then meaningless.
type Status struct {
	Known     bool `json:"known"`
	Online    bool `json:"online"`
	PaperOut  bool `json:"paper_out"`
	PaperLow  bool `json:"paper_low"`
	CoverOpen bool `json:"cover_open"`
	// DrawerSignal is the raw level on the drawer connector's sense pin
	// (ESC/POS only). Whether "true" means open depends on the drawer, so
	// it is reported, never interpreted.
	DrawerSignal *bool `json:"drawer_signal,omitempty"`
}

// ESC/POS real-time status requests (DLE EOT n). Sent one at a time: usbprint
// can misalign answers when several are queued in one write.
var (
	escposStatusPrinter = []byte{0x10, 0x04, 0x01}
	escposStatusOffline = []byte{0x10, 0x04, 0x02}
	escposStatusPaper   = []byte{0x10, 0x04, 0x04}
	// tsplStatus is TSPL's immediate status query (<ESC>!?).
	tsplStatus = []byte{0x1B, 0x21, 0x3F}
)

// validESCPOSStatus reports whether b has the fixed bits every DLE EOT
// answer carries (bit 1 and bit 4 set, bit 0 and bit 7 clear). Anything
// else is noise, e.g. the stray first byte usbprint returns after open.
func validESCPOSStatus(b byte) bool { return b&0x93 == 0x12 }

// parseESCPOSStatus builds a Status from the three DLE EOT answers
// (printer, offline cause, paper sensor). Any invalid byte → Known false.
func parseESCPOSStatus(printer, offline, paper byte) Status {
	if !validESCPOSStatus(printer) || !validESCPOSStatus(offline) || !validESCPOSStatus(paper) {
		return Status{}
	}
	drawer := printer&0x04 != 0
	return Status{
		Known:        true,
		Online:       printer&0x08 == 0,
		CoverOpen:    offline&0x04 != 0,
		PaperOut:     paper&0x60 != 0 || offline&0x20 != 0,
		PaperLow:     paper&0x0C != 0,
		DrawerSignal: &drawer,
	}
}

// parseTSPLStatus builds a Status from TSPL's one-byte answer to <ESC>!?:
// bit 0 head open, 1 paper jam, 2 out of paper, 3 out of ribbon, 4 pause,
// 5 printing, 6 cover open, 7 other error.
func parseTSPLStatus(b byte) Status {
	return Status{
		Known:     true,
		Online:    b&(0x01|0x02|0x04|0x10|0x40|0x80) == 0,
		PaperOut:  b&0x04 != 0,
		CoverOpen: b&(0x01|0x40) != 0,
	}
}

// StatusReader is implemented by printers that can be asked their state
// without printing (direct USB today; a Windows queue cannot read answers).
type StatusReader interface {
	Status() Status
}

// Identified is implemented by printers that know what they are.
type Identified interface {
	DeviceID() DeviceID
}
