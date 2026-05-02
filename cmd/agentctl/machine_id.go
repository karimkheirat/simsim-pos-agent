package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

// getMachineID returns a stable per-machine identifier. Cached on disk so
// the same value is returned across runs even if a NIC enumeration or
// hostname change would otherwise perturb the hash.
//
// Generation: SHA-256 of GOOS + hostname + (Windows machine GUID via
// registry, falling back to MAC of the first non-loopback NIC).
func getMachineID() (string, error) {
	cachePath := config.DefaultMachineIDPath()

	if data, err := os.ReadFile(cachePath); err == nil {
		if cached := strings.TrimSpace(string(data)); cached != "" {
			return cached, nil
		}
	}

	id, err := generateMachineID()
	if err != nil {
		return "", err
	}

	// Best-effort cache. Failure to persist is fine — next run regenerates
	// the same value (inputs are stable).
	if mkErr := os.MkdirAll(filepath.Dir(cachePath), 0o755); mkErr == nil {
		_ = os.WriteFile(cachePath, []byte(id), 0o600)
	}
	return id, nil
}

func generateMachineID() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}

	// Prefer the Windows machine GUID; fall back to MAC.
	extra := windowsMachineGUID()
	if extra == "" {
		extra = firstNonLoopbackMAC()
	}

	h := sha256.New()
	h.Write([]byte(runtime.GOOS))
	h.Write([]byte("|"))
	h.Write([]byte(hostname))
	h.Write([]byte("|"))
	h.Write([]byte(extra))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// firstNonLoopbackMAC returns the MAC of the first non-loopback interface
// with a hardware address, or "" if none.
func firstNonLoopbackMAC() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifa := range ifaces {
		if ifa.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(ifa.HardwareAddr) == 0 {
			continue
		}
		return ifa.HardwareAddr.String()
	}
	return ""
}
