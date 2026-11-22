package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
)

// listInstalled is printer.ListInstalled, a variable for tests.
var listInstalled = printer.ListInstalled

// runListPrinters prints the printers the shop can pick, one name per line:
// the USB printers the agent drives directly (no driver needed, ISSUE-115)
// first, then the real Windows queues. The installer's printer page reads
// this instead of asking Windows, which knows only printers with a driver.
func runListPrinters(stdout, stderr io.Writer) int {
	list, err := listInstalled()
	if err != nil {
		fmt.Fprintf(stderr, "list printers: %v\n", err)
		return 1
	}
	for _, p := range list {
		fmt.Fprintln(stdout, p.Name)
	}
	return 0
}

// resolvePrinterChoice turns a printer name picked from that list into what
// config.json stores, with the receipt language the printer said it speaks
// ("" when it did not say). A spec ("usb:…", "file:…") or a name the list
// does not hold is kept as given.
func resolvePrinterChoice(name string) (spec, language string) {
	if strings.HasPrefix(name, printer.USBSpecPrefix) || strings.HasPrefix(name, "file:") {
		return name, ""
	}
	list, err := listInstalled()
	if err != nil {
		return name, ""
	}
	for _, p := range list {
		if p.Name == name {
			return p.SpecOrName(), p.Language
		}
	}
	return name, ""
}
