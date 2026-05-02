//go:build windows

package main

import "golang.org/x/sys/windows/registry"

// windowsMachineGUID reads HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid,
// the standard Windows-installation-stable identifier. Returns "" on any
// failure so the caller can fall back to MAC.
func windowsMachineGUID() string {
	k, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Cryptography`,
		registry.QUERY_VALUE|registry.WOW64_64KEY,
	)
	if err != nil {
		return ""
	}
	defer k.Close()

	guid, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return ""
	}
	return guid
}
