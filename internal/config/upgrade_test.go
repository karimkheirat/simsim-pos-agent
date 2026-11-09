package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultsSpeakMolshi(t *testing.T) {
	d := Defaults()
	if d.CloudBaseURL != "https://app.molshi.com" {
		t.Errorf("cloud_base_url = %q", d.CloudBaseURL)
	}
	if d.AllowedOrigins[0] != "https://app.molshi.com" {
		t.Errorf("first origin = %q", d.AllowedOrigins[0])
	}
	if len(Upgrade(&d)) != 0 {
		t.Error("defaults are not already up to date")
	}
}

// Rachid's config on 9 Nov: the two old origins plus app.molshi.com added
// by hand, cloud_base_url already moved by hand.
func TestUpgradeKeepsHandEditsAndAddsWhatIsMissing(t *testing.T) {
	c := Defaults()
	c.AllowedOrigins = []string{"https://web-production-6bb4d.up.railway.app", "https://opensimsim.co", "https://app.molshi.com", "http://localhost:3000"}
	c.CloudBaseURL = "https://app.molshi.com"
	changes := Upgrade(&c)
	want := []string{"https://web-production-6bb4d.up.railway.app", "https://opensimsim.co", "https://app.molshi.com", "http://localhost:3000", "https://staging.molshi.com"}
	if !reflect.DeepEqual(c.AllowedOrigins, want) {
		t.Errorf("origins = %v, want %v", c.AllowedOrigins, want)
	}
	if len(changes) != 1 {
		t.Errorf("changes = %v, want one", changes)
	}
}

func TestUpgradeMovesOnlyRetiredCloudAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"https://opensimsim.co":                       "https://app.molshi.com",
		"https://web-production-6bb4d.up.railway.app": "https://app.molshi.com",
		"https://staging.molshi.com":                  "https://staging.molshi.com",
		"http://192.168.1.10:3000":                    "http://192.168.1.10:3000",
	} {
		c := Defaults()
		c.CloudBaseURL = in
		Upgrade(&c)
		if c.CloudBaseURL != want {
			t.Errorf("%s -> %s, want %s", in, c.CloudBaseURL, want)
		}
	}
}

func TestUpgradeFileRewritesAnOldConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	old := `{"version":"0.3.11","cloud_base_url":"https://opensimsim.co","allowed_origins":["https://opensimsim.co"],"printer_name":"Xprinter XP-420B","receipt_printer_language":"tspl"}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, err := UpgradeFile(path, "0.3.13")
	if err != nil || len(changes) == 0 {
		t.Fatalf("changes=%v err=%v", changes, err)
	}
	raw, _ := os.ReadFile(path)
	var got Config
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.CloudBaseURL != "https://app.molshi.com" || got.Version != "0.3.13" || got.PrinterName != "Xprinter XP-420B" || got.ReceiptPrinterLanguage != "tspl" {
		t.Errorf("rewritten config lost or missed something: %+v", got)
	}
	if again, _ := UpgradeFile(path, "0.3.13"); len(again) != 0 {
		t.Errorf("second upgrade changed %v", again)
	}
}

func TestUpgradeFileLeavesAMissingFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if changes, err := UpgradeFile(path, "0.3.13"); err != nil || changes != nil {
		t.Fatalf("changes=%v err=%v", changes, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a missing config was created")
	}
}
