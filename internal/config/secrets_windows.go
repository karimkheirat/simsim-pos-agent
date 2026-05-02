//go:build windows

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// DPAPISecretStore persists secrets encrypted via the Windows Data
// Protection API (DPAPI) with CRYPTPROTECT_LOCAL_MACHINE so the agent's
// LocalService account can decrypt without per-user keys. Anyone with
// administrator privileges on the machine can decrypt — accepted trade-off
// per POS_AGENT_SPEC.md §5.2 and the M2 contract reasoning.
type DPAPISecretStore struct {
	path string
}

// NewSecretStore returns a DPAPI-backed SecretStore on Windows builds.
func NewSecretStore(path string) (SecretStore, error) {
	return &DPAPISecretStore{path: path}, nil
}

// Load reads the ciphertext blob from disk, decrypts via DPAPI, and
// unmarshals JSON. Returns ErrNoSecrets if the file does not exist.
func (s *DPAPISecretStore) Load() (*Secrets, error) {
	blob, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoSecrets
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: read %s: %w", s.path, err)
	}
	plain, err := dpapiUnprotect(blob)
	if err != nil {
		return nil, fmt.Errorf("secrets: dpapi decrypt %s: %w", s.path, err)
	}
	var sec Secrets
	if err := json.Unmarshal(plain, &sec); err != nil {
		return nil, fmt.Errorf("secrets: decode %s: %w", s.path, err)
	}
	return &sec, nil
}

// Save marshals to JSON, encrypts via DPAPI, and writes atomically.
func (s *DPAPISecretStore) Save(sec *Secrets) error {
	plain, err := json.Marshal(sec)
	if err != nil {
		return fmt.Errorf("secrets: marshal: %w", err)
	}
	blob, err := dpapiProtect(plain)
	if err != nil {
		return fmt.Errorf("secrets: dpapi encrypt: %w", err)
	}
	return writeAtomic(s.path, blob, 0o600)
}

// Clear removes the ciphertext file. Idempotent.
func (s *DPAPISecretStore) Clear() error {
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// --- DPAPI plumbing --------------------------------------------------------
//
// We bind crypt32!CryptProtectData / CryptUnprotectData via syscall.LazyDLL
// rather than golang.org/x/sys/windows because the version of x/sys pinned
// by the project (v0.0.0-20200909) does not expose these. Lifting x/sys
// would also bump the go.mod toolchain floor above 1.22.

const cryptProtectLocalMachine = 0x4

var (
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")

	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree = kernel32.NewProc("LocalFree")
)

// dataBlob mirrors the Win32 DATA_BLOB struct: cbData (DWORD) + pbData (BYTE*).
type dataBlob struct {
	cbData uint32
	pbData *byte
}

func dpapiProtect(plaintext []byte) ([]byte, error) {
	in := dataBlob{cbData: uint32(len(plaintext))}
	if len(plaintext) > 0 {
		in.pbData = &plaintext[0]
	}
	var out dataBlob

	r1, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // szDataDescr (NULL)
		0, // pOptionalEntropy (NULL)
		0, // pvReserved (NULL)
		0, // pPromptStruct (NULL)
		uintptr(cryptProtectLocalMachine),
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))

	return blobToBytes(out), nil
}

func dpapiUnprotect(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, errors.New("secrets: empty ciphertext")
	}
	in := dataBlob{
		cbData: uint32(len(ciphertext)),
		pbData: &ciphertext[0],
	}
	var out dataBlob

	r1, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // ppszDataDescr
		0, // pOptionalEntropy
		0, // pvReserved
		0, // pPromptStruct
		uintptr(cryptProtectLocalMachine),
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("CryptUnprotectData: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))

	return blobToBytes(out), nil
}

// blobToBytes copies the Win32-allocated buffer into a Go-managed slice
// before LocalFree releases it.
func blobToBytes(b dataBlob) []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	src := unsafe.Slice(b.pbData, b.cbData)
	out := make([]byte, b.cbData)
	copy(out, src)
	return out
}
