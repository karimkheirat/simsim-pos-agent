package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChooseReceiptPrinter_WritesBothFieldsAndAsksForARestart(t *testing.T) {
	dir := t.TempDir()
	activeConfigPath = filepath.Join(dir, "config.json")
	t.Cleanup(func() { activeConfigPath = ""; restartForPrinter = nil })
	restarted := make(chan struct{}, 1)
	restartForPrinter = func() { restarted <- struct{}{} }

	if err := chooseReceiptPrinter("XP-420B", ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(activeConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"receipt_printer_name": "XP-420B"`) || !strings.Contains(string(raw), `"printer_name": "XP-420B"`) {
		t.Fatalf("config %s", raw)
	}
	<-restarted
}

func TestChooseReceiptPrinter_ADirectPrinterSetsItsLanguage(t *testing.T) {
	dir := t.TempDir()
	activeConfigPath = filepath.Join(dir, "config.json")
	t.Cleanup(func() { activeConfigPath = ""; restartForPrinter = nil })
	restartForPrinter = nil

	if err := chooseReceiptPrinter("usb:Xprinter XP-370B", "tspl"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(activeConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"receipt_printer_name": "usb:Xprinter XP-370B"`) || !strings.Contains(string(raw), `"receipt_printer_language": "tspl"`) {
		t.Fatalf("config %s", raw)
	}
}
