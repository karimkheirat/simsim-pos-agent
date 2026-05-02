//go:build !windows

package service

// Handle is a no-op handle on non-Windows platforms.
type Handle struct{}

// Release is a no-op.
func (Handle) Release() error { return nil }

// AcquireSingleInstance is a no-op stub on non-Windows builds. Production
// deploys to Windows; this exists so cmd/agent compiles on dev hosts.
// Returns a zero Handle and nil error — never ErrAlreadyRunning.
func AcquireSingleInstance() (Handle, error) {
	return Handle{}, nil
}
