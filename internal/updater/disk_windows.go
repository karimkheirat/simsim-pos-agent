//go:build windows

package updater

import "golang.org/x/sys/windows"

// DiskFree returns the bytes available to the caller on the volume that
// holds path (GetDiskFreeSpaceExW honours per-user quotas).
func DiskFree(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return 0, err
	}
	return avail, nil
}
