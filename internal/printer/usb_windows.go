//go:build windows

package printer

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Windows plumbing for direct USB printing (see usb.go for why). Only
// syscall and lazy DLL procedures: no new dependency.
//
// References: Microsoft, "Programming considerations for USBPRINT" (device
// interface GUID_DEVINTERFACE_USBPRINT, IOCTL_USBPRINT_GET_1284_ID) and
// "USB printing" (usbprint.sys is a conduit, not a printer driver).

var (
	cfgmgr32                          = syscall.NewLazyDLL("cfgmgr32.dll")
	procCMGetDeviceInterfaceListSizeW = cfgmgr32.NewProc("CM_Get_Device_Interface_List_SizeW")
	procCMGetDeviceInterfaceListW     = cfgmgr32.NewProc("CM_Get_Device_Interface_ListW")
	procCMOpenDeviceInterfaceKeyW     = cfgmgr32.NewProc("CM_Open_Device_Interface_KeyW")

	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procCreateEventW        = kernel32.NewProc("CreateEventW")
	procGetOverlappedResult = kernel32.NewProc("GetOverlappedResult")
)

// guidUSBPrint is GUID_DEVINTERFACE_USBPRINT {28d78fad-5a12-11d1-ae5b-0000f803a8c2}.
var guidUSBPrint = syscall.GUID{
	Data1: 0x28d78fad, Data2: 0x5a12, Data3: 0x11d1,
	Data4: [8]byte{0xae, 0x5b, 0x00, 0x00, 0xf8, 0x03, 0xa8, 0xc2},
}

const (
	ioctlUSBPrintGet1284ID     = 0x220034 // CTL_CODE(FILE_DEVICE_UNKNOWN, 13, METHOD_BUFFERED, FILE_ANY_ACCESS)
	cmListPresent              = 0        // CM_GET_DEVICE_INTERFACE_LIST_PRESENT
	crSuccess                  = 0
	crBufferSmall              = 0x1A
	regDispositionOpenExisting = 1
	errorIOPending             = syscall.Errno(997)
	waitTimeout                = 0x102
)

// interfacePaths lists the device interface paths of the USB printers
// present now.
func interfacePaths() ([]string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		var size uint32
		r, _, _ := procCMGetDeviceInterfaceListSizeW.Call(
			uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&guidUSBPrint)), 0, cmListPresent)
		if r != crSuccess {
			return nil, fmt.Errorf("CM_Get_Device_Interface_List_Size: CONFIGRET 0x%x", r)
		}
		if size <= 1 {
			return nil, nil
		}
		buf := make([]uint16, size)
		r, _, _ = procCMGetDeviceInterfaceListW.Call(
			uintptr(unsafe.Pointer(&guidUSBPrint)), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(size), cmListPresent)
		if r == crBufferSmall {
			continue // a printer arrived between the two calls
		}
		if r != crSuccess {
			return nil, fmt.Errorf("CM_Get_Device_Interface_List: CONFIGRET 0x%x", r)
		}
		var out []string
		start := 0
		for i, c := range buf {
			if c != 0 {
				continue
			}
			if i > start {
				out = append(out, syscall.UTF16ToString(buf[start:i]))
			}
			start = i + 1
		}
		return out, nil
	}
	return nil, errors.New("CM_Get_Device_Interface_List: list kept changing")
}

// ListUSB returns the standard USB printers plugged in now, each with its
// 1284 identity. A device that cannot be opened (another program holds it)
// is still listed, unnamed, so it is not silently invisible.
func ListUSB() ([]Device, error) {
	paths, err := interfacePaths()
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(paths))
	for _, p := range paths {
		d := Device{Path: p, PortNumber: portNumber(p)}
		if id, err := read1284(p); err == nil {
			d.ID = ParseDeviceID(id)
		}
		out = append(out, d)
	}
	return out, nil
}

func openDevice(path string, overlapped bool) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	var flags uint32 = syscall.FILE_ATTRIBUTE_NORMAL
	if overlapped {
		flags |= syscall.FILE_FLAG_OVERLAPPED
	}
	return syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, flags, 0)
}

// read1284 asks the device for its IEEE 1284 Device ID: two big-endian
// length bytes (which count themselves) then the ID text.
func read1284(path string) (string, error) {
	h, err := openDevice(path, false)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	buf := make([]byte, 1024)
	var n uint32
	if err := syscall.DeviceIoControl(h, ioctlUSBPrintGet1284ID, nil, 0, &buf[0], uint32(len(buf)), &n, nil); err != nil {
		return "", err
	}
	if n < 2 {
		return "", errors.New("1284 id: empty")
	}
	l := int(buf[0])<<8 | int(buf[1])
	if l < 2 || l > int(n) {
		l = int(n)
	}
	return string(buf[2:l]), nil
}

// portNumber reads the usbprint port number from the device interface's
// "Device Parameters" key (1 → USB001), 0 when unknown.
func portNumber(path string) int {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	var key syscall.Handle
	r, _, _ := procCMOpenDeviceInterfaceKeyW.Call(uintptr(unsafe.Pointer(p)), syscall.KEY_READ,
		regDispositionOpenExisting, uintptr(unsafe.Pointer(&key)), 0)
	if r != crSuccess {
		return 0
	}
	defer syscall.RegCloseKey(key)
	name, _ := syscall.UTF16PtrFromString("Port Number")
	var v, typ uint32
	size := uint32(4)
	if err := syscall.RegQueryValueEx(key, name, nil, &typ, (*byte)(unsafe.Pointer(&v)), &size); err != nil || typ != syscall.REG_DWORD {
		return 0
	}
	return int(v)
}

// overlappedIO runs one ReadFile or WriteFile with a deadline. On timeout
// the operation is cancelled and waited out, so buf is never touched after
// return.
func overlappedIO(h syscall.Handle, write bool, buf []byte, timeout time.Duration) (int, error) {
	ev, _, e := procCreateEventW.Call(0, 1, 0, 0)
	if ev == 0 {
		return 0, fmt.Errorf("CreateEvent: %w", e)
	}
	defer syscall.CloseHandle(syscall.Handle(ev))
	ov := syscall.Overlapped{HEvent: syscall.Handle(ev)}
	var n uint32
	var err error
	if write {
		err = syscall.WriteFile(h, buf, &n, &ov)
	} else {
		err = syscall.ReadFile(h, buf, &n, &ov)
	}
	if err != nil && err != errorIOPending {
		return 0, err
	}
	if err == errorIOPending {
		w, werr := syscall.WaitForSingleObject(syscall.Handle(ev), uint32(timeout/time.Millisecond))
		if werr != nil {
			return 0, werr
		}
		if w == waitTimeout {
			_ = syscall.CancelIoEx(h, &ov)
			procGetOverlappedResult.Call(uintptr(h), uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&n)), 1)
			return int(n), errTimeout
		}
	}
	r, _, e := procGetOverlappedResult.Call(uintptr(h), uintptr(unsafe.Pointer(&ov)), uintptr(unsafe.Pointer(&n)), 0)
	if r == 0 {
		return int(n), e
	}
	return int(n), nil
}

var errTimeout = errors.New("printer did not answer in time")

// USBPrinter writes straight to a USB printer through Windows' own
// connector. It satisfies Printer, StatusReader and Identified.
//
// Two locks: io serialises everything that talks to the device (a print,
// a status query); mu guards the fields. /health only ever takes mu, so it
// never waits behind a long print.
type USBPrinter struct {
	spec string
	// language picks the status query: "tspl" or anything else (ESC/POS).
	language string

	io sync.Mutex

	mu         sync.Mutex
	dev        Device
	status     Status
	statusAt   time.Time
	refreshing bool
}

// NewUSB binds a USBPrinter to a "usb:" spec. The device need not be
// plugged in yet; it is looked up again whenever it has moved or vanished.
// language is the receipt language configured for it ("" → ESC/POS).
func NewUSB(spec, language string) (*USBPrinter, error) {
	u := &USBPrinter{spec: spec, language: language}
	if devs, err := ListUSB(); err == nil {
		if d, err := MatchUSB(spec, devs); err == nil {
			u.dev = d
		}
	}
	return u, nil
}

// Name is the display name the web app's picker shows ("Xprinter XP-370B").
func (u *USBPrinter) Name() string {
	if n := u.spec[len(USBSpecPrefix):]; n != "" {
		return n
	}
	// A bare "usb:" spec: the printer's own name once seen.
	u.mu.Lock()
	defer u.mu.Unlock()
	if n := u.dev.ID.DisplayName(); n != "" {
		return n
	}
	return "USB"
}

// DeviceID returns the printer's 1284 identity (zero when never seen).
func (u *USBPrinter) DeviceID() DeviceID {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.dev.ID
}

// locate returns the device, looking it up again when its path is gone
// (unplugged, moved to another socket).
func (u *USBPrinter) locate() (Device, error) {
	paths, err := interfacePaths()
	if err != nil {
		return Device{}, err
	}
	u.mu.Lock()
	known := u.dev
	u.mu.Unlock()
	if known.Path != "" {
		for _, p := range paths {
			if p == known.Path {
				return known, nil
			}
		}
	}
	devs, err := ListUSB()
	if err != nil {
		return Device{}, err
	}
	d, err := MatchUSB(u.spec, devs)
	if err != nil {
		return Device{}, err
	}
	u.mu.Lock()
	u.dev = d
	u.mu.Unlock()
	return d, nil
}

// IsReachable is true when the device is plugged in. It reads the device
// list only (no open), well inside the 200 ms /health budget.
func (u *USBPrinter) IsReachable() bool {
	paths, err := interfacePaths()
	if err != nil {
		return false
	}
	u.mu.Lock()
	known := u.dev.Path
	u.mu.Unlock()
	for _, p := range paths {
		if p == known {
			return true
		}
	}
	// Moved or first seen: the full lookup opens devices, still fast.
	_, err = u.locate()
	return err == nil
}

// Print writes the whole job to the device. If the device cannot be opened
// at all (another program holds it), and a Windows queue sits on the same
// USB port, the job goes through that queue instead. A job that started
// writing is never retried, so nothing prints twice.
func (u *USBPrinter) Print(jobName string, data []byte) error {
	u.io.Lock()
	defer u.io.Unlock()
	d, err := u.locate()
	if err != nil {
		return fmt.Errorf("usb printer %q: %w", u.spec, err)
	}
	h, err := openDevice(d.Path, true)
	if err != nil {
		openErr := fmt.Errorf("open %s: %w", d.ID.DisplayName(), err)
		q := queueOnPort(d.Port())
		if q == "" {
			return openErr
		}
		sp, serr := NewWindowsSpooler(q)
		if serr != nil {
			return errors.Join(openErr, serr)
		}
		if perr := sp.Print(jobName, data); perr != nil {
			return errors.Join(openErr, fmt.Errorf("queue %q: %w", q, perr))
		}
		return nil
	}
	defer syscall.CloseHandle(h)
	// Generous: a long receipt with pictures on a slow printer.
	timeout := 10*time.Second + time.Duration(len(data)/1024)*time.Second
	for off := 0; off < len(data); {
		n, err := overlappedIO(h, true, data[off:], timeout)
		off += n
		if err != nil {
			return fmt.Errorf("write %s (%d of %d bytes sent): %w", d.ID.DisplayName(), off, len(data), err)
		}
		if n == 0 {
			return fmt.Errorf("write %s: device took no bytes", d.ID.DisplayName())
		}
	}
	u.mu.Lock()
	u.statusAt = time.Time{} // the next status read is fresh
	u.mu.Unlock()
	return nil
}

// Status returns what the printer last said about itself, at once. When
// that answer is older than two seconds a fresh one is asked for in the
// background, so /health never waits on the device. Until a printer has
// answered, Status{Known: false}.
func (u *USBPrinter) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	if time.Since(u.statusAt) >= 2*time.Second && !u.refreshing {
		u.refreshing = true
		go func() {
			u.io.Lock()
			s := u.readStatus()
			u.io.Unlock()
			u.mu.Lock()
			u.status, u.statusAt, u.refreshing = s, time.Now(), false
			u.mu.Unlock()
		}()
	}
	return u.status
}

// StatusNow asks the printer and waits for the answer (about a second at
// most). For the test print, not for /health.
func (u *USBPrinter) StatusNow() Status {
	u.io.Lock()
	s := u.readStatus()
	u.io.Unlock()
	u.mu.Lock()
	u.status, u.statusAt = s, time.Now()
	u.mu.Unlock()
	return s
}

func (u *USBPrinter) readStatus() Status {
	d, err := u.locate()
	if err != nil {
		return Status{}
	}
	h, err := openDevice(d.Path, true)
	if err != nil {
		return Status{}
	}
	defer syscall.CloseHandle(h)
	ask := func(q []byte, valid func(byte) bool) (byte, bool) {
		if _, err := overlappedIO(h, true, q, 300*time.Millisecond); err != nil {
			return 0, false
		}
		buf := make([]byte, 8)
		// usbprint may hand back a stray byte first; read until a valid one.
		for tries := 0; tries < 2; tries++ {
			n, err := overlappedIO(h, false, buf, 250*time.Millisecond)
			for i := n - 1; i >= 0; i-- {
				if valid(buf[i]) {
					return buf[i], true
				}
			}
			if err != nil {
				return 0, false
			}
		}
		return 0, false
	}
	if u.language == "tspl" {
		b, ok := ask(tsplStatus, func(byte) bool { return true })
		if !ok {
			return Status{}
		}
		return parseTSPLStatus(b)
	}
	p, ok1 := ask(escposStatusPrinter, validESCPOSStatus)
	o, ok2 := ask(escposStatusOffline, validESCPOSStatus)
	pa, ok3 := ask(escposStatusPaper, validESCPOSStatus)
	if !ok1 || !ok2 || !ok3 {
		return Status{}
	}
	return parseESCPOSStatus(p, o, pa)
}

// queueOnPort names an installed Windows queue printing to port, "" if none.
func queueOnPort(port string) string {
	if port == "" {
		return ""
	}
	queues, err := listQueues()
	if err != nil {
		return ""
	}
	for _, q := range queues {
		if q.Port == port {
			return q.Name
		}
	}
	return ""
}

var (
	_ Printer      = (*USBPrinter)(nil)
	_ StatusReader = (*USBPrinter)(nil)
	_ Identified   = (*USBPrinter)(nil)
)
