// Package printer abstracts print transports. Production uses the
// Windows print spooler in RAW mode; tests and dev environments use a
// FilePrinter that writes the ESC/POS byte stream to disk for inspection.
package printer

import "errors"

// Printer is the transport interface satisfied by every backend.
type Printer interface {
	// Print sends data to the printer as a single RAW job. jobName is
	// the human-readable spool job title (and, for FilePrinter, the
	// filename stem). Returns nil on success.
	Print(jobName string, data []byte) error

	// IsReachable reports whether the underlying device is currently
	// available. Implementations should not block longer than ~200ms.
	IsReachable() bool

	// Name returns the printer identifier (Windows printer name, or
	// "file:<dir>" for a FilePrinter).
	Name() string
}

// ErrInvalidSpec is returned by New when the spec string is empty or
// otherwise malformed. Use errors.Is to detect.
var ErrInvalidSpec = errors.New("invalid printer spec")
