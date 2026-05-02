//go:build windows

package service

import (
	"fmt"
	"syscall"
	"unsafe"
)

// errorAlreadyExists is the Win32 ERROR_ALREADY_EXISTS code returned by
// CreateMutexW (via GetLastError) when the mutex name already exists in
// the target namespace. The handle returned in that case is a
// reference to the existing mutex, NOT a fresh one.
const errorAlreadyExists = syscall.Errno(183)

var (
	mutexKernel32     = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW  = mutexKernel32.NewProc("CreateMutexW")
)

// Handle wraps a Win32 mutex HANDLE. Release closes it.
type Handle struct {
	h syscall.Handle
}

// Release closes the mutex handle. Idempotent; safe to call on a
// zero-value Handle (no-op).
func (h Handle) Release() error {
	if h.h == 0 {
		return nil
	}
	return syscall.CloseHandle(h.h)
}

// AcquireSingleInstance creates the production mutex defined by
// MutexName. Returns ErrAlreadyRunning if another process holds it.
func AcquireSingleInstance() (Handle, error) {
	return acquireMutex(MutexName)
}

// acquireMutex is the parameterized form, used by tests with a per-test
// mutex name to avoid clashing with a real running service.
func acquireMutex(name string) (Handle, error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return Handle{}, fmt.Errorf("mutex name %q: %w", name, err)
	}

	// CreateMutexW(NULL, FALSE, name) — un-owned at creation, name is
	// the kernel object name. Returns the handle (or 0 on failure);
	// GetLastError == ERROR_ALREADY_EXISTS when the name was held.
	h, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if h == 0 {
		return Handle{}, fmt.Errorf("CreateMutexW(%q): %v", name, lastErr)
	}
	handle := syscall.Handle(h)
	if errno, ok := lastErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		_ = syscall.CloseHandle(handle)
		return Handle{}, ErrAlreadyRunning
	}
	return Handle{h: handle}, nil
}
