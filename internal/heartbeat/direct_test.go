package heartbeat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
)

type directPrinter struct{ st printer.Status }

func (d directPrinter) Name() string               { return "Xprinter XP-370B" }
func (d directPrinter) IsReachable() bool          { return true }
func (d directPrinter) Print(string, []byte) error { return nil }
func (d directPrinter) Status() printer.Status     { return d.st }

type queuePrinter struct{}

func (queuePrinter) Name() string               { return "XP-420B" }
func (queuePrinter) IsReachable() bool          { return true }
func (queuePrinter) Print(string, []byte) error { return nil }

func TestBuildHeartbeat_ADirectPrinterSendsItsState(t *testing.T) {
	l := &Loop{Printer: directPrinter{st: printer.Status{Known: true, PaperOut: true}}}
	raw, _ := json.Marshal(l.buildHeartbeat().Printer)
	for _, want := range []string{`"direct":true`, `"paper_out":true`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s in %s", want, raw)
		}
	}
}

func TestBuildHeartbeat_AnUnansweredOrQueuePrinterSendsNoState(t *testing.T) {
	for _, p := range []printer.Printer{directPrinter{}, queuePrinter{}} {
		raw, _ := json.Marshal((&Loop{Printer: p}).buildHeartbeat().Printer)
		if strings.Contains(string(raw), `"status"`) {
			t.Fatalf("%T sent a state: %s", p, raw)
		}
	}
}
