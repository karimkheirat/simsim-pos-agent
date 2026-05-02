// Package util holds small standalone helpers shared across packages
// that don't justify a package of their own.
package util

import (
	"crypto/rand"
	"fmt"
)

// NewUUIDv4 returns an RFC 4122 v4 UUID in canonical 8-4-4-4-12 form,
// with version (4) and variant (10xx) bits set per the spec. Entropy
// comes from crypto/rand; the only error path is rand.Read failing,
// which is unreachable in practice on healthy systems.
func NewUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0F) | 0x40 // version 4
	b[8] = (b[8] & 0x3F) | 0x80 // variant 10xx
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
