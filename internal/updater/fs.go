package updater

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// FS is the narrow set of filesystem operations the updater performs.
// Production uses OSFS; tests wrap it to record the order of renames or
// to inject failures at a chosen step.
type FS interface {
	Rename(oldpath, newpath string) error
	Remove(name string) error
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Stat(name string) (fs.FileInfo, error)
	Create(name string) (io.WriteCloser, error)
	Open(name string) (io.ReadCloser, error)
}

// OSFS is the real filesystem.
type OSFS struct{}

func (OSFS) Rename(o, n string) error                  { return os.Rename(o, n) }
func (OSFS) Remove(name string) error                  { return os.Remove(name) }
func (OSFS) ReadFile(name string) ([]byte, error)      { return os.ReadFile(name) }
func (OSFS) MkdirAll(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }
func (OSFS) Stat(name string) (fs.FileInfo, error)     { return os.Stat(name) }
func (OSFS) Open(name string) (io.ReadCloser, error)   { return os.Open(name) }
func (OSFS) Create(name string) (io.WriteCloser, error) {
	return os.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
}

// WriteFile writes via a temp file + rename so a marker is never left
// half-written by a power cut.
func (OSFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

// Paths names every file the updater touches. Exe is the running
// binary (os.Executable() in production: {app}\bin\agent.exe);
// UpdateDir is %ProgramData%\Simsim\POSAgent\update.
type Paths struct {
	Exe       string
	UpdateDir string
}

// Download is where the verified download lands: update\new.exe.
func (p Paths) Download() string { return filepath.Join(p.UpdateDir, "new.exe") }

// Staged is the copy beside the running exe (agent.exe.new). The swap
// renames within ONE directory so it never depends on ProgramData and
// Program Files sharing a volume.
func (p Paths) Staged() string { return p.Exe + ".new" }

// Old is the previous binary kept for rollback (agent.exe.old).
func (p Paths) Old() string { return p.Exe + ".old" }

// Bad is where a rolled-back new binary is parked (agent.exe.bad).
func (p Paths) Bad() string { return p.Exe + ".bad" }

// Marker is update\pending.json — present from the swap until the new
// binary has been healthy for 60 s.
func (p Paths) Marker() string { return filepath.Join(p.UpdateDir, "pending.json") }

// RollbackNote is update\last-rollback.json — the record of the last
// automatic rollback, surfaced on /status. Never deleted by cleanup.
func (p Paths) RollbackNote() string { return filepath.Join(p.UpdateDir, "last-rollback.json") }

func exists(f FS, name string) bool {
	_, err := f.Stat(name)
	return err == nil
}

// removeIfExists removes name, treating "does not exist" as success.
func removeIfExists(f FS, name string) error {
	if err := f.Remove(name); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
