//go:build windows

package service

import (
	"errors"
	"testing"
)

// TestMutexNameConstant locks in the exact string format. Production
// depends on this being literally `Global\SimsimPOSAgent` — sc.exe and
// SCM both use the same name, and a typo here would cause two service
// instances to coexist silently.
func TestMutexNameConstant(t *testing.T) {
	if MutexName != `Global\SimsimPOSAgent` {
		t.Errorf("MutexName = %q, want exactly %q", MutexName, `Global\SimsimPOSAgent`)
	}
}

// TestSingleInstance_AcquireRelease exercises the contention path:
// first acquire wins, second fails with ErrAlreadyRunning, release
// frees the slot, third acquire succeeds.
//
// Uses a per-test name in the Local\ namespace (per-session, doesn't
// require SeCreateGlobalPrivilege) so the test cannot collide with a
// real running service.
func TestSingleInstance_AcquireRelease(t *testing.T) {
	const testName = `Local\SimsimPOSAgent-test-acquire-release`

	h1, err := acquireMutex(testName)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	if _, err := acquireMutex(testName); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second acquire while held: err = %v, want ErrAlreadyRunning", err)
	}

	if err := h1.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	h2, err := acquireMutex(testName)
	if err != nil {
		t.Fatalf("third acquire after release: %v", err)
	}
	_ = h2.Release()
}
