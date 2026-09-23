package service

import (
	"errors"
	"testing"
)

func TestBuildConfig_RunsAsVirtualServiceAccount(t *testing.T) {
	if got := BuildConfig().UserName; got != `NT SERVICE\SimsimPOSAgent` {
		t.Fatalf("UserName = %q, want the per-service virtual account", got)
	}
	if ServiceAccount != `NT SERVICE\`+ServiceName {
		t.Fatalf("ServiceAccount = %q", ServiceAccount)
	}
}

func notInstalledStatus() (string, error) { return "not installed", nil }

// Fresh install: create, then post-install.
func TestInstall_Fresh_CreatesThenConfigures(t *testing.T) {
	f := &fakeService{}
	posted := false
	if err := installWithDeps(f, notInstalledStatus, func() error { posted = true; return nil }); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := f.Order(); len(got) != 1 || got[0] != "install" {
		t.Errorf("call order = %v, want [install]", got)
	}
	if !posted {
		t.Error("postInstall must run on a fresh install")
	}
}

// Upgrade over an existing service (any SCM state): skip the create —
// kardianos would fail "already exists" — but still run postInstall,
// which is what moves an old LocalService install to the virtual account.
func TestInstall_Existing_SkipsCreateStillConfigures(t *testing.T) {
	for _, st := range []string{"running", "stopped"} {
		f := &fakeService{}
		posted := false
		status := func() (string, error) { return st, nil }
		if err := installWithDeps(f, status, func() error { posted = true; return nil }); err != nil {
			t.Fatalf("%s: install: %v", st, err)
		}
		if got := f.Order(); len(got) != 0 {
			t.Errorf("%s: call order = %v, want no create", st, got)
		}
		if !posted {
			t.Errorf("%s: postInstall must run on upgrade", st)
		}
	}
}

func TestInstall_PostInstallErrorSurfaces(t *testing.T) {
	f := &fakeService{}
	err := installWithDeps(f, runningStatus, func() error { return errors.New("access denied") })
	if err == nil {
		t.Fatal("post-install failure must be returned")
	}
}

func TestInstall_StatusErrorSurfaces(t *testing.T) {
	f := &fakeService{}
	if err := installWithDeps(f, errStatus(errors.New("scm down")), func() error { return nil }); err == nil {
		t.Fatal("status failure must be returned")
	}
	if got := f.Order(); len(got) != 0 {
		t.Errorf("no create after a status failure; got %v", got)
	}
}

// Off Windows statusImpl returns a non-SCM string; that must still take
// the kardianos create path.
func TestInstall_NonWindowsStatusStillCreates(t *testing.T) {
	f := &fakeService{}
	status := func() (string, error) { return "unsupported on this platform", nil }
	if err := installWithDeps(f, status, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := f.Order(); len(got) != 1 || got[0] != "install" {
		t.Errorf("call order = %v, want [install]", got)
	}
}
