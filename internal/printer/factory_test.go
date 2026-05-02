package printer

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNew_FileSpec(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbox")
	p, err := New("file:" + dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := p.(*FilePrinter); !ok {
		t.Errorf("New(file:...) = %T, want *FilePrinter", p)
	}
	if p.Name() != "file:"+dir {
		t.Errorf("Name() = %q, want %q", p.Name(), "file:"+dir)
	}
}

func TestNew_WindowsSpec(t *testing.T) {
	p, err := New("SP-331")
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("New on windows: %v", err)
		}
		if _, ok := p.(*WindowsSpooler); !ok {
			t.Errorf("New(\"SP-331\") on windows = %T, want *WindowsSpooler", p)
		}
		return
	}
	// Non-Windows: stub returns an error.
	if err == nil {
		t.Errorf("New on non-windows: expected error, got %v", p)
	}
}

func TestNew_EmptySpec(t *testing.T) {
	p, err := New("")
	if err == nil {
		t.Fatalf("expected error for empty spec, got %v", p)
	}
	if !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("err = %v; want errors.Is(err, ErrInvalidSpec) == true", err)
	}
}

func TestNew_FilePrefix_EmptyPath(t *testing.T) {
	// "file:" alone (no path) → FilePrinter rejects empty dir.
	_, err := New("file:")
	if err == nil {
		t.Error("expected error for spec \"file:\" with empty path")
	}
}
