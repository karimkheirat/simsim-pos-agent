package updater

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// MaxStartAttempts is how many times a freshly-swapped binary may start
// without reaching 60 s of health before the next start rolls it back.
// The 4th start (attempts > 3) restores agent.exe.old.
const MaxStartAttempts = 3

// Marker is update\pending.json, written just before the swap renames.
type Marker struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	Attempts int       `json:"attempts"`
	At       time.Time `json:"at"`
}

// RollbackNote is update\last-rollback.json. From is the version that
// failed (and was parked as agent.exe.bad); To is the version restored.
type RollbackNote struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	Attempts int       `json:"attempts"`
	At       time.Time `json:"at"`
	Reason   string    `json:"reason"`
}

// StartupResult tells the caller what Startup did.
type StartupResult struct {
	// RolledBack is true when the previous binary was restored. The
	// caller MUST exit non-zero immediately so the SCM recovery action
	// restarts the service with the restored agent.exe.
	RolledBack bool
	// Pending is true when this start is a trial run of a new binary
	// (the marker exists and names this version). Informational.
	Pending bool
}

// Startup is the FIRST thing the service does — before config, logging
// setup beyond a bare file logger, printers or the network — so that a
// new build which dies anywhere later in startup is still rolled back.
//
//   - No marker: nothing to do.
//   - Marker unreadable, or naming a different version than the one now
//     running (the swap never took, or an installer replaced the binary
//     since): the marker is stale — delete it and carry on.
//   - Marker names this version: count the attempt. On the 4th start
//     (attempts > MaxStartAttempts) with agent.exe.old present, park
//     this binary as agent.exe.bad, restore agent.exe.old, write
//     last-rollback.json, delete the marker, and report RolledBack.
func Startup(f FS, p Paths, version string, now func() time.Time, log *slog.Logger) StartupResult {
	raw, err := f.ReadFile(p.Marker())
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn("updater: cannot read pending marker; ignoring", "path", p.Marker(), "err", err.Error())
		}
		return StartupResult{}
	}
	var m Marker
	if err := json.Unmarshal(raw, &m); err != nil {
		log.Warn("updater: pending marker is corrupt; deleting", "err", err.Error())
		_ = removeIfExists(f, p.Marker())
		return StartupResult{}
	}
	if m.To != version {
		log.Warn("updater: pending marker names another version; deleting as stale",
			"marker_to", m.To, "marker_from", m.From, "running", version)
		_ = removeIfExists(f, p.Marker())
		return StartupResult{}
	}

	m.Attempts++
	log.Info("updater: trial start of updated binary", "from", m.From, "to", m.To, "attempt", m.Attempts)

	if m.Attempts <= MaxStartAttempts {
		if err := writeJSON(f, p.Marker(), m); err != nil {
			log.Error("updater: cannot persist start attempt", "err", err.Error())
		}
		return StartupResult{Pending: true}
	}

	if !exists(f, p.Old()) {
		// Nothing to go back to. Stop counting so we don't re-evaluate
		// forever; the binary stays as-is and SCM keeps restarting it.
		log.Error("updater: new binary failed repeatedly but agent.exe.old is missing; cannot roll back",
			"to", m.To, "attempts", m.Attempts)
		_ = removeIfExists(f, p.Marker())
		return StartupResult{}
	}

	log.Error("updater: new binary failed to stay up; rolling back",
		"bad_version", m.To, "restore_version", m.From, "attempts", m.Attempts)
	if err := rollback(f, p); err != nil {
		log.Error("updater: rollback failed", "err", err.Error())
		// Leave the marker so the next start tries again.
		_ = writeJSON(f, p.Marker(), m)
		return StartupResult{}
	}
	note := RollbackNote{
		From:     m.To,
		To:       m.From,
		Attempts: m.Attempts,
		At:       now().UTC(),
		Reason:   fmt.Sprintf("version %s did not stay up for 60 s in %d starts", m.To, MaxStartAttempts),
	}
	if err := writeJSON(f, p.RollbackNote(), note); err != nil {
		log.Error("updater: cannot write rollback note", "err", err.Error())
	}
	_ = removeIfExists(f, p.Marker())
	log.Info("updater: rollback complete; exiting so the service manager restarts the restored binary",
		"restored", m.From)
	return StartupResult{RolledBack: true}
}

// rollback: agent.exe → agent.exe.bad, agent.exe.old → agent.exe. The
// running image can be renamed on Windows (it is mapped with delete
// sharing); it cannot be deleted, which is why it is parked, not removed.
func rollback(f FS, p Paths) error {
	if err := removeIfExists(f, p.Bad()); err != nil {
		return fmt.Errorf("remove stale %s: %w", p.Bad(), err)
	}
	if err := f.Rename(p.Exe, p.Bad()); err != nil {
		return fmt.Errorf("park new binary: %w", err)
	}
	if err := f.Rename(p.Old(), p.Exe); err != nil {
		// Put the new binary back so the service can at least start.
		if rerr := f.Rename(p.Bad(), p.Exe); rerr != nil {
			return fmt.Errorf("restore old binary: %v; and un-park failed: %w", err, rerr)
		}
		return fmt.Errorf("restore old binary: %w", err)
	}
	return nil
}

// Finalize runs once the service has been healthy (listening) for 60 s:
// the update — if any — is accepted. Deletes agent.exe.old,
// agent.exe.bad, the marker and any leftover staging files. The rollback
// note is kept on purpose: it is the only record a rollback happened.
func Finalize(f FS, p Paths, log *slog.Logger) {
	hadMarker := exists(f, p.Marker())
	for _, name := range []string{p.Old(), p.Bad(), p.Marker(), p.Staged(), p.Download()} {
		if err := removeIfExists(f, name); err != nil {
			log.Warn("updater: cleanup could not remove file", "path", name, "err", err.Error())
		}
	}
	if hadMarker {
		log.Info("updater: update accepted after 60 s healthy; rollback copy removed")
	}
}

// ReadRollbackNote returns the last rollback note, or nil if none.
func ReadRollbackNote(f FS, p Paths) *RollbackNote {
	raw, err := f.ReadFile(p.RollbackNote())
	if err != nil {
		return nil
	}
	var n RollbackNote
	if json.Unmarshal(raw, &n) != nil {
		return nil
	}
	return &n
}

func writeJSON(f FS, name string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return f.WriteFile(name, raw, 0o644)
}
