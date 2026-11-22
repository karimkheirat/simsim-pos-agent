package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
)

func printerTestConfig(list func() ([]printer.Installed, error), choose func(string, string) error) Config {
	return Config{
		ListenAddr:               "127.0.0.1:0",
		AllowedOrigins:           []string{"https://allowed.example"},
		Version:                  "test-1.0.0",
		Logger:                   discardLogger(),
		Secrets:                  pairedSecrets(),
		IdempotencyTTL:           time.Hour,
		IdempotencySweepInterval: time.Hour,
		ListPrinters:             list,
		ChooseReceiptPrinter:     choose,
	}
}

var twoPrinters = func() ([]printer.Installed, error) {
	return []printer.Installed{{Name: "XP-420B", Driver: "XP-420B", Port: "USB001"}, {Name: "EPSON TM-T20III", Port: "ESDPRT001"}}, nil
}

func TestPrinters_ListsWhatWindowsKnowsAndTheCurrentOne(t *testing.T) {
	_, ts := newTestServerWith(t, &fakePrinter{name: "XP-420B", reachable: true}, printerTestConfig(twoPrinters, nil))
	resp := authGet(t, ts, "/printers")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"current":"XP-420B"`) || !strings.Contains(string(b), "EPSON TM-T20III") {
		t.Fatalf("body %s", b)
	}
}

func TestPrinters_UnavailableWithoutAListing(t *testing.T) {
	_, ts := newTestServerWith(t, &fakePrinter{name: "x", reachable: true}, printerTestConfig(nil, nil))
	resp := authGet(t, ts, "/printers")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestChoosePrinter_TakesOnlyAListedPrinter(t *testing.T) {
	chosen := ""
	choose := func(n, _ string) error { chosen = n; return nil }
	_, ts := newTestServerWith(t, &fakePrinter{name: "x", reachable: true}, printerTestConfig(twoPrinters, choose))

	resp := authPost(t, ts, "/printer", strings.NewReader(`{"name":"AnyDesk Printer"}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || chosen != "" {
		t.Fatalf("an unlisted printer must be refused: %d %q", resp.StatusCode, chosen)
	}

	resp = authPost(t, ts, "/printer", strings.NewReader(`{"name":"EPSON TM-T20III"}`))
	defer resp.Body.Close()
	var env struct {
		Data struct {
			Chosen     string `json:"chosen"`
			Restarting bool   `json:"restarting"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if resp.StatusCode != 200 || chosen != "EPSON TM-T20III" || !env.Data.Restarting {
		t.Fatalf("status %d chosen %q env %+v", resp.StatusCode, chosen, env)
	}
}

func TestChoosePrinter_SaysWhenItCannotWrite(t *testing.T) {
	choose := func(string, string) error { return errors.New("disk full") }
	_, ts := newTestServerWith(t, &fakePrinter{name: "x", reachable: true}, printerTestConfig(twoPrinters, choose))
	resp := authPost(t, ts, "/printer", strings.NewReader(`{"name":"XP-420B"}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestPrinters_RequireAuth(t *testing.T) {
	_, ts := newTestServerWith(t, &fakePrinter{name: "x", reachable: true}, printerTestConfig(twoPrinters, nil))
	resp, err := http.Get(ts.URL + "/printers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestChoosePrinter_ADirectPrinterIsStoredByItsSpecWithItsLanguage(t *testing.T) {
	list := func() ([]printer.Installed, error) {
		return []printer.Installed{{Name: "Xprinter XP-370B", Port: "USB001", Direct: true, Language: "tspl", Spec: "usb:Xprinter XP-370B"}}, nil
	}
	var spec, lang string
	choose := func(s, l string) error { spec, lang = s, l; return nil }
	_, ts := newTestServerWith(t, &fakePrinter{name: "x", reachable: true}, printerTestConfig(list, choose))
	resp := authPost(t, ts, "/printer", strings.NewReader(`{"name":"Xprinter XP-370B"}`))
	defer resp.Body.Close()
	if resp.StatusCode != 200 || spec != "usb:Xprinter XP-370B" || lang != "tspl" {
		t.Fatalf("status %d spec %q lang %q", resp.StatusCode, spec, lang)
	}
}

func TestPrinters_ADirectPrinterSaysSo(t *testing.T) {
	list := func() ([]printer.Installed, error) {
		return []printer.Installed{{Name: "Xprinter XP-370B", Port: "USB001", Direct: true, Language: "escpos", Spec: "usb:Xprinter XP-370B"}}, nil
	}
	_, ts := newTestServerWith(t, &fakePrinter{name: "x", reachable: true}, printerTestConfig(list, nil))
	resp := authGet(t, ts, "/printers")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"direct":true`) || !strings.Contains(string(b), `"language":"escpos"`) || strings.Contains(string(b), "usb:") {
		t.Fatalf("body %s", b)
	}
}
