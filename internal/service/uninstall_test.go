package service

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	ksvc "github.com/kardianos/service"
)

// fakeService satisfies ksvc.Service. It records the order of Stop /
// Uninstall calls and supports configurable errors + a Stop delay so
// the timeout path can be exercised without sleeping for real seconds.
type fakeService struct {
	mu           sync.Mutex
	callOrder    []string
	stopDelay    time.Duration
	stopErr      error
	uninstallErr error
}

func (f *fakeService) Run() error                   { return nil }
func (f *fakeService) Start() error                 { return nil }
func (f *fakeService) Restart() error               { return nil }
func (f *fakeService) Install() error               { return nil }
func (f *fakeService) String() string               { return "fake" }
func (f *fakeService) Platform() string             { return "fake-platform" }
func (f *fakeService) Status() (ksvc.Status, error) { return ksvc.StatusUnknown, nil }
func (f *fakeService) Logger(_ chan<- error) (ksvc.Logger, error) {
	return nil, nil
}
func (f *fakeService) SystemLogger(_ chan<- error) (ksvc.Logger, error) {
	return nil, nil
}

func (f *fakeService) Stop() error {
	if f.stopDelay > 0 {
		time.Sleep(f.stopDelay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callOrder = append(f.callOrder, "stop")
	return f.stopErr
}

func (f *fakeService) Uninstall() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callOrder = append(f.callOrder, "uninstall")
	return f.uninstallErr
}

func (f *fakeService) Order() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.callOrder))
	copy(out, f.callOrder)
	return out
}

// Compile-time assertion that fakeService satisfies the kardianos
// Service interface. If a future kardianos release adds methods, this
// line catches the gap at compile time.
var _ ksvc.Service = (*fakeService)(nil)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func stoppedStatus() (string, error) { return "stopped", nil }
func runningStatus() (string, error) { return "running", nil }
func errStatus(err error) func() (string, error) {
	return func() (string, error) { return "", err }
}

// TestUninstall_Stopped — when SCM reports the service stopped, we go
// straight to unregister without sending a Stop signal.
func TestUninstall_Stopped(t *testing.T) {
	f := &fakeService{}
	if err := uninstallWithDeps(f, stoppedStatus, 50*time.Millisecond, discardLogger()); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got := f.Order()
	if len(got) != 1 || got[0] != "uninstall" {
		t.Errorf("call order = %v, want [uninstall]", got)
	}
}

// TestUninstall_Running_StopsFirst — when the service is running, Stop
// is sent before Uninstall, in that order. This is the M2 wart fix:
// previously Uninstall went directly to SCM and left an orphan process.
func TestUninstall_Running_StopsFirst(t *testing.T) {
	f := &fakeService{}
	if err := uninstallWithDeps(f, runningStatus, 50*time.Millisecond, discardLogger()); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got := f.Order()
	if len(got) != 2 || got[0] != "stop" || got[1] != "uninstall" {
		t.Errorf("call order = %v, want [stop, uninstall]", got)
	}
}

// TestUninstall_StopFails_StillUninstalls — Stop returning an error
// must not abort uninstall. The orphan-process risk is the same whether
// stop fails fast or hangs forever; in either case unregistering the
// SCM entry is still the right thing to do.
func TestUninstall_StopFails_StillUninstalls(t *testing.T) {
	f := &fakeService{stopErr: errors.New("stop failed for reasons")}
	if err := uninstallWithDeps(f, runningStatus, 50*time.Millisecond, discardLogger()); err != nil {
		t.Fatalf("uninstall returned error despite stop-failure-should-warn: %v", err)
	}
	got := f.Order()
	if len(got) != 2 || got[0] != "stop" || got[1] != "uninstall" {
		t.Errorf("call order = %v, want [stop, uninstall]", got)
	}
}

// TestUninstall_StopTimesOut_StillUninstalls — Stop blocked past the
// timeout. uninstallWithDeps must return after the timeout (not wait
// for Stop to complete) and still call Uninstall.
//
// The leaked Stop goroutine completes asynchronously after stopDelay;
// we sleep at the end to let it finish before the test exits, so it
// doesn't affect the next test.
func TestUninstall_StopTimesOut_StillUninstalls(t *testing.T) {
	f := &fakeService{stopDelay: 200 * time.Millisecond}
	start := time.Now()
	if err := uninstallWithDeps(f, runningStatus, 50*time.Millisecond, discardLogger()); err != nil {
		t.Fatalf("uninstall returned error despite timeout-should-warn: %v", err)
	}
	if d := time.Since(start); d > 150*time.Millisecond {
		t.Errorf("uninstall took %v; should have returned within ~50ms timeout", d)
	}
	// Wait for the leaked Stop goroutine to complete so it doesn't bleed
	// into other tests' fakeService instances.
	time.Sleep(250 * time.Millisecond)

	// Order is non-deterministic here (uninstall ran before stop completed),
	// but uninstall must have been called at least once.
	got := f.Order()
	foundUninstall := false
	for _, c := range got {
		if c == "uninstall" {
			foundUninstall = true
			break
		}
	}
	if !foundUninstall {
		t.Errorf("uninstall not called: order = %v", got)
	}
}

// TestUninstall_StatusError_ProceedsToUninstall — if the SCM status
// query itself fails, we still try to uninstall. Per the implementation
// comment, the SCM may still be reachable for the unregister even if the
// query path tripped.
func TestUninstall_StatusError_ProceedsToUninstall(t *testing.T) {
	f := &fakeService{}
	err := uninstallWithDeps(f, errStatus(errors.New("status query died")), 50*time.Millisecond, discardLogger())
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got := f.Order()
	if len(got) != 1 || got[0] != "uninstall" {
		t.Errorf("call order = %v, want [uninstall]", got)
	}
}

// TestUninstall_PropagatesUninstallError — if SCM unregister itself
// fails (e.g. ERROR_SERVICE_DOES_NOT_EXIST when called twice), the
// error is wrapped and returned. Stops here are not modified.
func TestUninstall_PropagatesUninstallError(t *testing.T) {
	f := &fakeService{uninstallErr: errors.New("service is not installed")}
	err := uninstallWithDeps(f, stoppedStatus, 50*time.Millisecond, discardLogger())
	if err == nil {
		t.Fatal("expected error from uninstall, got nil")
	}
}
