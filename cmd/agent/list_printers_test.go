package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
)

func withPrinters(t *testing.T, list []printer.Installed) {
	t.Helper()
	old := listInstalled
	listInstalled = func() ([]printer.Installed, error) { return list, nil }
	t.Cleanup(func() { listInstalled = old })
}

var directAndQueue = []printer.Installed{
	{Name: "Xprinter XP-370B", Port: "USB001", Direct: true, Language: "tspl", Spec: "usb:Xprinter XP-370B"},
	{Name: "EPSON TM-T20III", Driver: "EPSON", Port: "ESDPRT001"},
}

func TestListPrinters_OneNamePerLine(t *testing.T) {
	withPrinters(t, directAndQueue)
	var out, errb bytes.Buffer
	if code := runListPrinters(&out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	if out.String() != "Xprinter XP-370B\nEPSON TM-T20III\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestWriteConfig_ADirectPrinterNameIsStoredAsItsSpecWithItsLanguage(t *testing.T) {
	withPrinters(t, directAndQueue)
	path := filepath.Join(t.TempDir(), "config.json")
	var out, errb bytes.Buffer
	if code := runWriteConfig([]string{"-config", path, "--printer", "Xprinter XP-370B"}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"receipt_printer_name": "usb:Xprinter XP-370B"`) || !strings.Contains(string(raw), `"receipt_printer_language": "tspl"`) {
		t.Fatalf("config %s", raw)
	}
}

func TestWriteConfig_AnExplicitLanguageWins(t *testing.T) {
	withPrinters(t, directAndQueue)
	path := filepath.Join(t.TempDir(), "config.json")
	var out, errb bytes.Buffer
	if code := runWriteConfig([]string{"-config", path, "--printer", "Xprinter XP-370B", "--receipt-language", "escpos"}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"receipt_printer_language": "escpos"`) {
		t.Fatalf("config %s", raw)
	}
}

func TestWriteConfig_AQueueNameIsKeptAsIs(t *testing.T) {
	withPrinters(t, directAndQueue)
	path := filepath.Join(t.TempDir(), "config.json")
	var out, errb bytes.Buffer
	if code := runWriteConfig([]string{"-config", path, "--printer", "EPSON TM-T20III"}, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"receipt_printer_name": "EPSON TM-T20III"`) || !strings.Contains(string(raw), `"receipt_printer_language": "escpos"`) {
		t.Fatalf("config %s", raw)
	}
}
