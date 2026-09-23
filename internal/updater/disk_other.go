//go:build !windows

package updater

import "errors"

// DiskFree is not implemented off Windows — the updater only runs as a
// Windows service. Returning an error makes the free-space condition
// fail closed.
func DiskFree(string) (uint64, error) {
	return 0, errors.New("updater: disk free not supported on this platform")
}
