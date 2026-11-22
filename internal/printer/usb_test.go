package printer

import (
	"errors"
	"testing"
)

func TestParseDeviceID(t *testing.T) {
	d := ParseDeviceID("MFG:Xprinter;MDL:XP-370B;CMD:TSPL,ESC/POS;CLS:PRINTER;")
	if d.Manufacturer != "Xprinter" || d.Model != "XP-370B" {
		t.Fatalf("got %+v", d)
	}
	if len(d.CommandSet) != 2 || d.CommandSet[0] != "TSPL" || d.CommandSet[1] != "ESC/POS" {
		t.Fatalf("command set %v", d.CommandSet)
	}
	if got := d.DisplayName(); got != "Xprinter XP-370B" {
		t.Fatalf("display %q", got)
	}
}

func TestParseDeviceIDLongKeys(t *testing.T) {
	d := ParseDeviceID("MANUFACTURER:Gprinter; MODEL:Gprinter GP-3150TN; COMMAND SET:TSPL2;")
	if d.DisplayName() != "Gprinter GP-3150TN" {
		t.Fatalf("display %q (model already carries the maker)", d.DisplayName())
	}
	if d.Language() != "tspl" {
		t.Fatalf("language %q", d.Language())
	}
}

func TestLanguage(t *testing.T) {
	cases := map[string]string{
		"CMD:ESC/POS;":          "escpos",
		"CMD:ESCPOS;":           "escpos",
		"CMD:EPSON;":            "escpos",
		"CMD:TSPL;":             "tspl",
		"CMD:TSPL2,ZPL;":        "tspl",
		"CMD:TSPL,ESC/POS;":     "escpos",
		"CMD:ZPL;":              "",
		"MFG:Unknown;MDL:Thing": "",
	}
	for in, want := range cases {
		if got := ParseDeviceID(in).Language(); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestMatchUSB(t *testing.T) {
	a := Device{ID: ParseDeviceID("MFG:Xprinter;MDL:XP-370B;")}
	b := Device{ID: ParseDeviceID("MFG:Gprinter;MDL:GP-3150TN;")}
	if d, err := MatchUSB("usb:xprinter xp-370b", []Device{a, b}); err != nil || d.ID.Model != "XP-370B" {
		t.Fatalf("by name: %v %v", d, err)
	}
	if d, err := MatchUSB("usb:", []Device{b}); err != nil || d.ID.Model != "GP-3150TN" {
		t.Fatalf("the only one: %v %v", d, err)
	}
	if _, err := MatchUSB("usb:", []Device{a, b}); !errors.Is(err, ErrUSBNotFound) {
		t.Fatalf("two plugged in must ask for a name, got %v", err)
	}
	if d, err := MatchUSB("usb:Xprinter XP-370B (USB)", []Device{a, b}); err != nil || d.ID.Model != "XP-370B" {
		t.Fatalf("suffixed name: %v %v", d, err)
	}
	if _, err := MatchUSB("usb:Epson TM-T20", []Device{a}); !errors.Is(err, ErrUSBNotFound) {
		t.Fatalf("absent: %v", err)
	}
}

func TestDevicePortAndSpec(t *testing.T) {
	d := Device{ID: ParseDeviceID("MFG:Xprinter;MDL:XP-370B;"), PortNumber: 1}
	if d.Port() != "USB001" || d.Spec() != "usb:Xprinter XP-370B" {
		t.Fatalf("port %q spec %q", d.Port(), d.Spec())
	}
	if (Device{}).Port() != "" {
		t.Fatal("unknown port must be empty")
	}
}

func TestParseESCPOSStatus(t *testing.T) {
	// Idle, online, paper present, cover closed: every answer is 0x12.
	s := parseESCPOSStatus(0x12, 0x12, 0x12)
	if !s.Known || !s.Online || s.PaperOut || s.PaperLow || s.CoverOpen {
		t.Fatalf("idle: %+v", s)
	}
	// Offline (bit 3), cover open (bit 2 of n=2), paper end (bits 5,6 of n=4).
	s = parseESCPOSStatus(0x1A, 0x16, 0x72)
	if !s.Known || s.Online || !s.CoverOpen || !s.PaperOut {
		t.Fatalf("faults: %+v", s)
	}
	// Paper near end only.
	if s = parseESCPOSStatus(0x12, 0x12, 0x1E); !s.PaperLow || s.PaperOut {
		t.Fatalf("near end: %+v", s)
	}
	// The stray byte usbprint returns after open is not a status.
	if s = parseESCPOSStatus(0x2B, 0x12, 0x12); s.Known {
		t.Fatalf("noise accepted: %+v", s)
	}
}

func TestParseTSPLStatus(t *testing.T) {
	if s := parseTSPLStatus(0x00); !s.Online || s.PaperOut {
		t.Fatalf("ready: %+v", s)
	}
	if s := parseTSPLStatus(0x04); s.Online || !s.PaperOut {
		t.Fatalf("out of paper: %+v", s)
	}
	if s := parseTSPLStatus(0x40); s.Online || !s.CoverOpen {
		t.Fatalf("cover: %+v", s)
	}
	if s := parseTSPLStatus(0x20); !s.Online {
		t.Fatalf("printing is not a fault: %+v", s)
	}
}
