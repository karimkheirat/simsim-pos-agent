package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
)

// directFake is a printer the agent drives itself (ISSUE-115): it can say
// what state it is in.
type directFake struct {
	fakePrinter
	status printer.Status
}

func (d *directFake) Status() printer.Status { return d.status }

func TestHealth_ADirectPrinterReportsItsPaperAndCover(t *testing.T) {
	p := &directFake{fakePrinter: fakePrinter{name: "Xprinter XP-370B", reachable: true},
		status: printer.Status{Known: true, Online: false, PaperOut: true}}
	_, ts := newTestServer(t, p)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	for _, want := range []string{`"direct":true`, `"paper_out":true`, `"online":false`, `"name":"Xprinter XP-370B"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
}

func TestHealth_AnUnansweredStatusIsLeftOut(t *testing.T) {
	p := &directFake{fakePrinter: fakePrinter{name: "Xprinter XP-370B", reachable: true}}
	_, ts := newTestServer(t, p)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"direct":true`) || strings.Contains(string(b), `"status"`) {
		t.Fatalf("body %s", b)
	}
}

func TestHealth_AWindowsQueueSaysNothingAboutItsState(t *testing.T) {
	_, ts := newTestServer(t, &fakePrinter{name: "XP-420B", reachable: true})
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), `"direct"`) || strings.Contains(string(b), `"status"`) {
		t.Fatalf("body %s", b)
	}
}
