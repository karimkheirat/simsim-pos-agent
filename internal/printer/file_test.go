package printer

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestFilePrinter_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	fp, err := NewFilePrinter(dir)
	if err != nil {
		t.Fatalf("NewFilePrinter: %v", err)
	}

	want := []byte("\x1b@hello, printer\n")
	if err := fp.Print("test-job", want); err != nil {
		t.Fatalf("Print: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "test-job.escpos"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("file contents = %q, want %q", got, want)
	}
}

func TestFilePrinter_EmptyJobName_GeneratesUUID(t *testing.T) {
	dir := t.TempDir()
	fp, err := NewFilePrinter(dir)
	if err != nil {
		t.Fatalf("NewFilePrinter: %v", err)
	}

	if err := fp.Print("", []byte("data")); err != nil {
		t.Fatalf("Print with empty jobName: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir entries = %d, want 1", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasSuffix(name, ".escpos") {
		t.Errorf("filename %q lacks .escpos suffix", name)
	}
	stem := strings.TrimSuffix(name, ".escpos")
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRe.MatchString(stem) {
		t.Errorf("filename stem %q is not a v4 UUID", stem)
	}
}

func TestNewFilePrinter_CreatesMissingDir(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "nonexistent", "nested", "outbox")

	if _, err := os.Stat(deep); !os.IsNotExist(err) {
		t.Fatalf("precondition: expected %q not to exist, got err=%v", deep, err)
	}
	fp, err := NewFilePrinter(deep)
	if err != nil {
		t.Fatalf("NewFilePrinter: %v", err)
	}
	info, err := os.Stat(deep)
	if err != nil {
		t.Fatalf("stat after NewFilePrinter: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%q exists but is not a directory", deep)
	}
	// Sanity: a print into the new dir succeeds.
	if err := fp.Print("first", []byte("x")); err != nil {
		t.Errorf("Print into freshly-created dir: %v", err)
	}
}

func TestFilePrinter_PrintErrorOnInvalidJobName(t *testing.T) {
	dir := t.TempDir()
	fp, err := NewFilePrinter(dir)
	if err != nil {
		t.Fatalf("NewFilePrinter: %v", err)
	}
	// NUL bytes in filenames are rejected by every mainstream filesystem.
	err = fp.Print("bad\x00name", []byte("data"))
	if err == nil {
		t.Error("expected error for jobName containing NUL byte, got nil")
	}
}

func TestFilePrinter_NameAndIsReachable(t *testing.T) {
	dir := t.TempDir()
	fp, err := NewFilePrinter(dir)
	if err != nil {
		t.Fatalf("NewFilePrinter: %v", err)
	}
	if got, want := fp.Name(), "file:"+dir; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if !fp.IsReachable() {
		t.Errorf("IsReachable() = false on fresh temp dir, want true")
	}
}

func TestNewFilePrinter_EmptyDirRejected(t *testing.T) {
	_, err := NewFilePrinter("")
	if err == nil {
		t.Error("expected error for empty dir, got nil")
	}
}
