package printer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/karimkheirat/simsim-pos-agent/internal/util"
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
		uuid, err := util.NewUUIDv4()
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

// Compile-time assertion that *FilePrinter satisfies Printer.
var _ Printer = (*FilePrinter)(nil)
