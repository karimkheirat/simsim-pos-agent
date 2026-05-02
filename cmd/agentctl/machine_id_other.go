//go:build !windows

package main

// windowsMachineGUID returns "" on non-Windows builds; the caller falls
// back to a MAC-based identifier.
func windowsMachineGUID() string { return "" }
