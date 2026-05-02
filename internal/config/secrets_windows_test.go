//go:build windows

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestDPAPI_RoundTrip exercises a real DPAPI Save/Load on the Windows
// host. NewSecretStore on Windows returns *DPAPISecretStore; this test
// verifies the on-disk blob is genuine ciphertext (not the plaintext
// JSON the JSON store would write).
func TestDPAPI_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dpapi-secrets.dat")
	raw, err := NewSecretStore(path)
	if err != nil {
		t.Fatalf("NewSecretStore: %v", err)
	}
	store, ok := raw.(*DPAPISecretStore)
	if !ok {
		t.Fatalf("NewSecretStore on Windows = %T, want *DPAPISecretStore", raw)
	}

	want := fixtureSecrets(t)
	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify on-disk content is NOT the plaintext token. DPAPI ciphertext
	// has a Win32 header + IV + ciphertext + MAC; the plaintext token
	// string must not appear verbatim.
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	if bytes.Contains(disk, []byte(want.TerminalToken)) {
		t.Errorf("on-disk DPAPI blob contains plaintext token — encryption did not happen")
	}
	if len(disk) < 32 {
		t.Errorf("on-disk DPAPI blob suspiciously small (%d bytes); expected ciphertext + header", len(disk))
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertSecretsEqual(t, got, want)
}

// TestDPAPI_UnprotectGarbage — feeding non-DPAPI bytes to dpapiUnprotect
// must return an error, not crash or panic.
func TestDPAPI_UnprotectGarbage(t *testing.T) {
	if _, err := dpapiUnprotect([]byte("this is not a DPAPI blob")); err == nil {
		t.Error("dpapiUnprotect(garbage) returned nil error")
	}
}

// TestDPAPI_LoadCorruptedFile — Load should fail gracefully if the
// on-disk file is corrupted (e.g. partial overwrite).
func TestDPAPI_LoadCorruptedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupted.dat")
	if err := os.WriteFile(path, []byte("\x00\x01\x02 garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := NewSecretStore(path)
	if err != nil {
		t.Fatalf("NewSecretStore: %v", err)
	}
	_, err = store.Load()
	if err == nil {
		t.Error("Load on corrupted file returned nil error")
	}
}
