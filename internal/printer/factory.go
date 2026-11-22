package printer

import (
	"fmt"
	"strings"
)

// New returns a Printer for the given spec.
//
//   - "file:<dir>"    → FilePrinter writing to <dir>
//   - "usb:<name>"    → USBPrinter writing straight to that USB printer,
//     no Windows driver or queue (ISSUE-115)
//   - any other value → WindowsSpooler bound to that printer name
//   - empty string    → ErrInvalidSpec
//
// On non-Windows builds, any non-"file:" spec returns an error.
func New(spec string) (Printer, error) {
	return NewWithLanguage(spec, "")
}

// NewWithLanguage is New, telling a direct USB printer which language it is
// driven in ("tspl" or ESC/POS) so it asks for its status in that language.
func NewWithLanguage(spec, language string) (Printer, error) {
	if spec == "" {
		return nil, fmt.Errorf("%w: spec is empty", ErrInvalidSpec)
	}
	if strings.HasPrefix(spec, "file:") {
		return NewFilePrinter(strings.TrimPrefix(spec, "file:"))
	}
	if strings.HasPrefix(spec, USBSpecPrefix) {
		u, err := NewUSB(spec, language)
		if err != nil {
			return nil, err
		}
		return u, nil
	}
	return NewWindowsSpooler(spec)
}
