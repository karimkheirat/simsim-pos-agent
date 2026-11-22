//go:build !windows

package printer

import "errors"

var errUSBWindowsOnly = errors.New("direct usb printing is windows only")

// ListUSB has nothing to list outside Windows.
func ListUSB() ([]Device, error) { return nil, nil }

// USBPrinter is a stub outside Windows.
type USBPrinter struct{ spec string }

// NewUSB always fails outside Windows.
func NewUSB(spec, language string) (*USBPrinter, error) { return nil, errUSBWindowsOnly }

func (u *USBPrinter) Name() string                            { return u.spec }
func (u *USBPrinter) IsReachable() bool                       { return false }
func (u *USBPrinter) Print(jobName string, data []byte) error { return errUSBWindowsOnly }
func (u *USBPrinter) Status() Status                          { return Status{} }
func (u *USBPrinter) StatusNow() Status                       { return Status{} }
func (u *USBPrinter) DeviceID() DeviceID                      { return DeviceID{} }

var _ Printer = (*USBPrinter)(nil)
