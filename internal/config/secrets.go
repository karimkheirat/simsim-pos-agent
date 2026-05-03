package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Secrets is the persisted-on-disk pairing state of the agent.
type Secrets struct {
	TerminalID    string    `json:"terminal_id"`
	TerminalToken string    `json:"terminal_token"`
	StoreID       string    `json:"store_id"`
	PairedAt      time.Time `json:"paired_at"`
}

// SecretStore abstracts where pairing secrets are persisted. On Windows,
// production uses DPAPISecretStore (machine-scope DPAPI). Elsewhere and
// for tests, JSONFileSecretStore is used.
type SecretStore interface {
	// Load returns the persisted Secrets. ErrNoSecrets when no secrets
	// have been saved (file does not exist) — distinct from "file present
	// but unreadable" which returns a wrapped I/O error.
	Load() (*Secrets, error)

	// Save persists the given secrets atomically.
	Save(s *Secrets) error

	// Clear removes the persisted secrets. Idempotent — clearing when
	// nothing is stored returns nil.
	Clear() error
}

// ErrNoSecrets indicates the store has no persisted secrets (the agent is
// unpaired). Callers detect via errors.Is.
var ErrNoSecrets = errors.New("secrets: no paired secrets present")

// JSONFileSecretStore stores secrets as plaintext JSON on disk.
//
// **DEV-ONLY. UNSAFE IN PRODUCTION.** This backend exists for non-Windows
// development environments, CI, and unit tests. The on-disk file is
// plaintext JSON; anyone with shell access can read the terminal token.
//
// Production deployments run on Windows and must use DPAPISecretStore
// (defined in secrets_windows.go), which encrypts the blob via
// CryptProtectData with CRYPTPROTECT_LOCAL_MACHINE so only this machine
// can decrypt. The factory NewSecretStore picks the right backend per OS.
type JSONFileSecretStore struct {
	path string
}

// NewJSONFileSecretStore returns a JSON-backed SecretStore at path. Used
// by the non-Windows factory and by cross-platform tests.
func NewJSONFileSecretStore(path string) *JSONFileSecretStore {
	return &JSONFileSecretStore{path: path}
}

// Load reads and unmarshals the JSON file. Returns ErrNoSecrets if the
// file does not exist.
func (s *JSONFileSecretStore) Load() (*Secrets, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoSecrets
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: read %s: %w", s.path, err)
	}
	var sec Secrets
	if err := json.Unmarshal(raw, &sec); err != nil {
		return nil, fmt.Errorf("secrets: decode %s: %w", s.path, err)
	}
	return &sec, nil
}

// Save marshals to JSON and writes atomically. Mode 0o600 on POSIX
// (no-op on Windows; ProgramData ACL governs there).
func (s *JSONFileSecretStore) Save(sec *Secrets) error {
	raw, err := json.MarshalIndent(sec, "", "  ")
	if err != nil {
		return fmt.Errorf("secrets: marshal: %w", err)
	}
	return WriteAtomic(s.path, raw, 0o600)
}

// Clear removes the file. Returns nil if it doesn't exist (idempotent).
func (s *JSONFileSecretStore) Clear() error {
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// WriteAtomic writes data to path via a temp file + fsync + rename. Avoids
// leaving a half-written file on a crash mid-write. Creates parent
// directories if missing.
//
// Exported in M4 (sub-task AG5) so cmd/agent's `write-config` subcommand
// can reuse the same write semantics for config.json. Originally lived
// here as the secrets-store save primitive — same shape, same guarantees.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("secrets: mkdir parent: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("secrets: create tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("secrets: write tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("secrets: fsync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("secrets: close tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("secrets: rename tmp -> final: %w", err)
	}
	return nil
}
