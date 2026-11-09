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

// Rachid's PC on 9 Nov: a hand-built 0.3.12 and a config.json edited by
// hand (app.molshi.com added to the two old origins, cloud address moved,
// the Xprinter as receipt printer in TSPL). Saved with and without a BOM.
func TestRachidsHandEditedConfigUpgradesCleanly(t *testing.T) {
	const handEdited = `{
  "version": "0.3.12",
  "listen_port": 47291,
  "cloud_base_url": "https://app.molshi.com",
  "heartbeat_seconds": 300,
  "printer_name": "Xprinter XP-420B",
  "receipt_printer_name": "Xprinter XP-420B",
  "label_printer_name": "",
  "tspl_dialect": "standard",
  "log_level": "info",
  "allowed_origins": [
    "https://web-production-6bb4d.up.railway.app",
    "https://opensimsim.co",
    "https://app.molshi.com"
  ],
  "paper_width_mm": 80,
  "receipt_printer_language": "tspl",
  "receipt_width_dots": 0,
  "dpi": 203,
  "scale_ip": "",
  "scale_port": 0,
  "scale_sync_seconds": 60,
  "release_check_seconds": 86400,
  "auto_update": true
}`
	for name, body := range map[string]string{"plain": handEdited, "bom": "\ufeff" + handEdited} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err != nil {
				t.Fatalf("0.3.13 cannot read the hand-edited config: %v", err)
			}
			changes, err := UpgradeFile(path, "0.3.13")
			if err != nil {
				t.Fatal(err)
			}
			if len(changes) != 1 || changes[0] != "allowed_origins += https://staging.molshi.com" {
				t.Errorf("changes = %v", changes)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(got); err != nil {
				t.Fatal(err)
			}
			wantOrigins := []string{"https://web-production-6bb4d.up.railway.app", "https://opensimsim.co", "https://app.molshi.com", "https://staging.molshi.com"}
			if !reflect.DeepEqual(got.AllowedOrigins, wantOrigins) || got.CloudBaseURL != "https://app.molshi.com" ||
				got.ReceiptPrinterName != "Xprinter XP-420B" || got.ReceiptPrinterLanguage != "tspl" || got.Version != "0.3.13" || !got.AutoUpdate {
				t.Errorf("after upgrade: %+v", got)
			}
		})
	}
}
