//go:build !windows

package printer

import "errors"

// errWindowsOnly is returned by every WindowsSpooler operation on
// non-Windows platforms. The real implementation lives in windows.go.
var errWindowsOnly = errors.New("windows spooler unavailable on this platform")

// WindowsSpooler is a stub on non-Windows builds. It exists only so the
// factory in factory.go can name the type unconditionally.
type WindowsSpooler struct {
	name string
}

// NewWindowsSpooler always returns errWindowsOnly on non-Windows builds.
func NewWindowsSpooler(name string) (*WindowsSpooler, error) {
	return nil, errWindowsOnly
}

func (w *WindowsSpooler) Name() string                              { return w.name }
func (w *WindowsSpooler) IsReachable() bool                         { return false }
func (w *WindowsSpooler) Print(jobName string, data []byte) error  { return errWindowsOnly }

// Compile-time assertion that *WindowsSpooler satisfies Printer.
var _ Printer = (*WindowsSpooler)(nil)
