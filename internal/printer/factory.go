package printer

import (
	"fmt"
	"strings"
)

// New returns a Printer for the given spec.
//
//   - "file:<dir>"    → FilePrinter writing to <dir>
//   - any other value → WindowsSpooler bound to that printer name
//   - empty string    → ErrInvalidSpec
//
// On non-Windows builds, any non-"file:" spec returns an error from the
// WindowsSpooler stub.
func New(spec string) (Printer, error) {
	if spec == "" {
		return nil, fmt.Errorf("%w: spec is empty", ErrInvalidSpec)
	}
	if strings.HasPrefix(spec, "file:") {
		return NewFilePrinter(strings.TrimPrefix(spec, "file:"))
	}
	return NewWindowsSpooler(spec)
}
