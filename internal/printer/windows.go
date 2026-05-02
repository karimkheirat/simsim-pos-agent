//go:build windows

package printer

import (
	"errors"
	"fmt"
	"time"

	winp "github.com/alexbrainman/printer"
)

// WindowsSpooler sends RAW print jobs to a named Windows print queue
// via the alexbrainman/printer wrapper around the Win32 spooler API.
type WindowsSpooler struct {
	name string
}

// NewWindowsSpooler returns a WindowsSpooler bound to the given Windows
// printer name. The constructor does not attempt to open the printer;
// reachability is observed lazily via IsReachable / Print.
func NewWindowsSpooler(name string) (*WindowsSpooler, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: windows printer name required", ErrInvalidSpec)
	}
	return &WindowsSpooler{name: name}, nil
}

// Name returns the bound Windows printer name.
func (w *WindowsSpooler) Name() string {
	return w.name
}

// IsReachable returns true if the printer name appears in the spooler's
// list of installed printers. Capped at 200ms; on timeout or error the
// result is false.
func (w *WindowsSpooler) IsReachable() bool {
	type result struct {
		names []string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		names, err := winp.ReadNames()
		ch <- result{names, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return false
		}
		for _, n := range r.names {
			if n == w.name {
				return true
			}
		}
		return false
	case <-time.After(200 * time.Millisecond):
		return false
	}
}

// Print opens the Windows printer, submits data as a single RAW
// document with title jobName, and closes. On any spooler error the
// underlying message is preserved via %w wrapping.
func (w *WindowsSpooler) Print(jobName string, data []byte) error {
	p, err := winp.Open(w.name)
	if err != nil {
		return fmt.Errorf("open printer %q: %w", w.name, err)
	}
	defer p.Close()

	if err := p.StartRawDocument(jobName); err != nil {
		return fmt.Errorf("start raw document %q: %w", jobName, err)
	}
	if err := p.StartPage(); err != nil {
		// Best-effort cleanup — surface the original error.
		return errors.Join(
			fmt.Errorf("start page: %w", err),
			p.EndDocument(),
		)
	}
	if _, err := p.Write(data); err != nil {
		return errors.Join(
			fmt.Errorf("write data: %w", err),
			p.EndPage(),
			p.EndDocument(),
		)
	}
	if err := p.EndPage(); err != nil {
		return errors.Join(
			fmt.Errorf("end page: %w", err),
			p.EndDocument(),
		)
	}
	if err := p.EndDocument(); err != nil {
		return fmt.Errorf("end document: %w", err)
	}
	return nil
}

// Compile-time assertion that *WindowsSpooler satisfies Printer.
var _ Printer = (*WindowsSpooler)(nil)
