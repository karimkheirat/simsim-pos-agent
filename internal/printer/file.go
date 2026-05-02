package printer

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
)

// FilePrinter writes each print job to a file in dir, suffixed .escpos.
// It is the primary backend for tests and dev environments where no
// physical printer is attached.
type FilePrinter struct {
	dir string
}

// NewFilePrinter constructs a FilePrinter for dir. The directory is
// created (with any missing parents) if it does not already exist.
func NewFilePrinter(dir string) (*FilePrinter, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: file printer requires a directory", ErrInvalidSpec)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create printer dir %q: %w", dir, err)
	}
	return &FilePrinter{dir: dir}, nil
}

// Name returns "file:<dir>".
func (f *FilePrinter) Name() string {
	return "file:" + f.dir
}

// IsReachable returns true if the directory exists and is writable.
func (f *FilePrinter) IsReachable() bool {
	info, err := os.Stat(f.dir)
	if err != nil || !info.IsDir() {
		return false
	}
	// Writability probe: create + remove a temp file.
	probe, err := os.CreateTemp(f.dir, ".reachable-*")
	if err != nil {
		return false
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	return true
}

// Print writes data to <dir>/<jobName>.escpos. If jobName is empty, a
// fresh UUIDv4 is generated for the filename.
func (f *FilePrinter) Print(jobName string, data []byte) error {
	if jobName == "" {
		uuid, err := newUUIDv4()
		if err != nil {
			return fmt.Errorf("generate job id: %w", err)
		}
		jobName = uuid
	}
	path := filepath.Join(f.dir, jobName+".escpos")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write print job %q: %w", path, err)
	}
	return nil
}

// newUUIDv4 returns an RFC 4122 v4 UUID in canonical 8-4-4-4-12 form,
// using crypto/rand for the entropy.
func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0F) | 0x40 // version 4
	b[8] = (b[8] & 0x3F) | 0x80 // variant 10xx
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// Compile-time assertion that *FilePrinter satisfies Printer.
var _ Printer = (*FilePrinter)(nil)
