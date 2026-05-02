package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fixtureSecrets returns a Secrets value with all fields populated for
// round-trip tests.
func fixtureSecrets(t *testing.T) *Secrets {
	t.Helper()
	pairedAt, err := time.Parse(time.RFC3339, "2026-05-02T14:30:00Z")
	if err != nil {
		t.Fatalf("parse fixture time: %v", err)
	}
	return &Secrets{
		TerminalID:    "trm_abc123",
		TerminalToken: "tok_43chars_base64url_xxxxxxxxxxxxxxxxxxxxx",
		StoreID:       "f0040929-0001",
		PairedAt:      pairedAt,
	}
}

// assertSecretsEqual compares two *Secrets, treating time.Time via Equal.
func assertSecretsEqual(t *testing.T, got, want *Secrets) {
	t.Helper()
	if got.TerminalID != want.TerminalID {
		t.Errorf("TerminalID = %q, want %q", got.TerminalID, want.TerminalID)
	}
	if got.TerminalToken != want.TerminalToken {
		t.Errorf("TerminalToken = %q, want %q", got.TerminalToken, want.TerminalToken)
	}
	if got.StoreID != want.StoreID {
		t.Errorf("StoreID = %q, want %q", got.StoreID, want.StoreID)
	}
	if !got.PairedAt.Equal(want.PairedAt) {
		t.Errorf("PairedAt = %v, want %v", got.PairedAt, want.PairedAt)
	}
}

// TestSecretStore_RoundTrip exercises Save → Load via NewSecretStore. On
// Windows this hits DPAPI; elsewhere JSON. Same contract either way.
func TestSecretStore_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subdir", "secrets")
	store, err := NewSecretStore(path)
	if err != nil {
		t.Fatalf("NewSecretStore: %v", err)
	}

	want := fixtureSecrets(t)
	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertSecretsEqual(t, got, want)
}

// TestSecretStore_LoadMissing — loading from a path with no file returns
// ErrNoSecrets, not a generic file-not-found error.
func TestSecretStore_LoadMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never-written")
	store, err := NewSecretStore(path)
	if err != nil {
		t.Fatalf("NewSecretStore: %v", err)
	}

	_, err = store.Load()
	if !errors.Is(err, ErrNoSecrets) {
		t.Fatalf("err = %v; want errors.Is(err, ErrNoSecrets)", err)
	}
}

// TestSecretStore_Clear — Clear removes the file; subsequent Load returns
// ErrNoSecrets. Clearing again is idempotent (no error).
func TestSecretStore_Clear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets")
	store, err := NewSecretStore(path)
	if err != nil {
		t.Fatalf("NewSecretStore: %v", err)
	}
	if err := store.Save(fixtureSecrets(t)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat after Save: %v", err)
	}

	if err := store.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still exists after Clear: %v", err)
	}

	if _, err := store.Load(); !errors.Is(err, ErrNoSecrets) {
		t.Errorf("Load after Clear: err = %v; want ErrNoSecrets", err)
	}

	// Idempotent — clearing nothing is fine.
	if err := store.Clear(); err != nil {
		t.Errorf("Clear (idempotent): %v", err)
	}
}

// TestJSONFileSecretStore_RoundTrip is the cross-platform JSON-stub round
// trip: works on every OS (the JSON store has no platform-specific deps).
func TestJSONFileSecretStore_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "json-secrets.json")
	store := NewJSONFileSecretStore(path)

	want := fixtureSecrets(t)
	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify on-disk format is human-readable JSON containing the token.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	if !strings.Contains(string(raw), want.TerminalToken) {
		t.Errorf("on-disk JSON does not contain terminal token; raw = %q", raw)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertSecretsEqual(t, got, want)
}

func TestJSONFileSecretStore_LoadMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-file")
	store := NewJSONFileSecretStore(path)

	_, err := store.Load()
	if !errors.Is(err, ErrNoSecrets) {
		t.Errorf("err = %v; want errors.Is(err, ErrNoSecrets)", err)
	}
}

// TestSave_AtomicWrite — after Save, the temp file is gone (rename
// completed) and the final file holds the data. Mid-write atomicity is
// inherent to the rename pattern; this test confirms cleanup at least.
func TestSave_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets")
	store, err := NewSecretStore(path)
	if err != nil {
		t.Fatalf("NewSecretStore: %v", err)
	}
	if err := store.Save(fixtureSecrets(t)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp file remains after Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("final file missing after Save: %v", err)
	}
}

func TestDefaultSecretsPath(t *testing.T) {
	got := DefaultSecretsPath()
	if runtime.GOOS == "windows" {
		if !strings.Contains(got, "Simsim") || !strings.HasSuffix(got, "secrets.dat") {
			t.Errorf("windows DefaultSecretsPath = %q, want containing Simsim and ending secrets.dat", got)
		}
	} else {
		if got != "./secrets.json" {
			t.Errorf("non-windows DefaultSecretsPath = %q, want ./secrets.json", got)
		}
	}
}
