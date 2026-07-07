// Package scalesync runs the periodic scale-PLU-file mirror loop: it
// polls GET /api/pos-agent/scale-plu-file (X-Terminal-Token auth, same
// gate as /heartbeat) and writes the returned file content to the local
// balance directory that the vendor's scale PC software reads.
//
// Lifecycle mirrors internal/heartbeat: the loop is bound to the same
// context that drives api.Server.Run, ticks at Interval while paired,
// and rechecks at a faster cadence while unpaired.
//
// Write discipline (v2, main repo commit 59778f0):
//   - encoding — `content` travels as a normal JSON/UTF-8 string; the
//     worker transcodes to UTF-16 LE and prepends the FF FE BOM (the
//     web repo's encodeLink69PluFile is the reference implementation).
//     The cloud's sha256 is defined over these ENCODED bytes — i.e.
//     the exact bytes on disk — so dedupe, transfer verification, and
//     startup seeding all hash encoded bytes.
//   - sha256 dedupe — content whose encoded hash matches the last
//     written file is not rewritten (unless the file has gone missing
//     on disk).
//   - transfer verification — the cloud-supplied sha256 must match the
//     hash of the locally encoded bytes, else the write is skipped.
//   - header-only guard — v2 content always carries a header row, so
//     "empty" means "zero data rows". A PLU file containing data rows
//     is NEVER overwritten with header-only content; the last good
//     file is kept and a warning is logged.
//   - atomic writes via config.WriteAtomic (tmp + fsync + rename), so
//     the scale software never observes a half-written file.
package scalesync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/karimkheirat/simsim-pos-agent/internal/cloud"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

const (
	// defaultInterval is the paired-state poll cadence when Interval is
	// unset (zero). Production wires Interval from the validated
	// config's scale_sync_seconds; this fallback covers direct
	// construction (tests, future callers).
	defaultInterval = 5 * time.Minute
	// defaultUnpairedRecheckInterval matches the heartbeat loop's
	// unpaired polling cadence.
	defaultUnpairedRecheckInterval = 60 * time.Second
)

// WindowsPLUFilePath is the EXACT destination the cloud's web UI shows
// retailers ("path_hint" in the scale-plu-file response). It is a
// product-facing contract — DO NOT derive it from %ProgramData% or
// otherwise change it without a coordinated cloud/web release.
const WindowsPLUFilePath = `C:\ProgramData\Simsim\balance\PLU.txt`

// DefaultPLUFilePath returns the platform destination for the mirrored
// PLU file: the literal retailer-facing path on Windows, a repo-local
// path elsewhere (dev/CI only — production is Windows).
func DefaultPLUFilePath() string {
	if runtime.GOOS == "windows" {
		return WindowsPLUFilePath
	}
	return "./balance/PLU.txt"
}

// Loop is the periodic scale-PLU-file mirror. Construct via the public
// fields then call Run; Run blocks until ctx is canceled.
type Loop struct {
	Cloud   *cloud.Client
	Secrets config.SecretStore
	Logger  *slog.Logger

	// DestPath is the file the PLU content is mirrored to. Production
	// wiring passes DefaultPLUFilePath(); tests point into a temp dir.
	DestPath string

	// Interval is the paired-state poll cadence. Zero means
	// defaultInterval (5min). Tests typically pass 50ms.
	Interval time.Duration

	// UnpairedRecheckInterval is the polling cadence while unpaired.
	// Zero means defaultUnpairedRecheckInterval (60s).
	UnpairedRecheckInterval time.Duration

	// lastSHA256 is the hex digest of the last content written (or
	// found on disk at startup). Guards against redundant rewrites.
	lastSHA256 string
}

// Run blocks until ctx is canceled. Seeds the dedupe hash from any
// existing file, fires the first poll immediately, then ticks at
// Interval (paired) or UnpairedRecheckInterval (unpaired).
func (l *Loop) Run(ctx context.Context) {
	interval := l.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	recheck := l.UnpairedRecheckInterval
	if recheck <= 0 {
		recheck = defaultUnpairedRecheckInterval
	}

	l.seedFromDisk()

	for {
		paired := l.tick(ctx)
		if ctx.Err() != nil {
			return
		}
		var wait time.Duration
		if paired {
			wait = interval
		} else {
			wait = recheck
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// seedFromDisk initializes lastSHA256 from the existing PLU file so an
// agent restart doesn't rewrite an unchanged file (and doesn't trip
// retailer-side file watchers). A missing/unreadable file just leaves
// the hash empty — the first fetched content will be written.
func (l *Loop) seedFromDisk() {
	raw, err := os.ReadFile(l.DestPath)
	if err != nil {
		return
	}
	l.lastSHA256 = hashHex(raw)
	l.Logger.Debug("scalesync: seeded dedupe hash from existing file",
		"path", l.DestPath, "bytes", len(raw), "sha256", l.lastSHA256)
}

// tick performs one poll+mirror attempt. Returns paired==true if
// secrets were present at the start of this tick (the wait cadence is
// governed by paired state, not by cloud success — same contract as
// heartbeat.Loop.tick).
func (l *Loop) tick(ctx context.Context) bool {
	secrets, err := l.Secrets.Load()
	if errors.Is(err, config.ErrNoSecrets) {
		l.Logger.Debug("scalesync: unpaired; skipping")
		return false
	}
	if err != nil {
		l.Logger.Warn("scalesync: secret store load failed; skipping", "err", err.Error())
		return true
	}

	resp, cloudErr := l.Cloud.FetchScalePLUFile(ctx, secrets.TerminalToken)
	switch {
	case cloudErr == nil:
		l.process(resp)
	case errors.Is(cloudErr, cloud.ErrUnauthenticated):
		// Token revoked. The heartbeat loop owns the clear-secrets
		// reaction to a 401; this loop just backs off until the secret
		// store reflects the new state.
		l.Logger.Warn("scalesync: 401 UNAUTHENTICATED; heartbeat loop handles unpairing")
	case errors.Is(cloudErr, cloud.ErrNotFound):
		// No scale/PLU file provisioned for this store (or the cloud
		// route isn't deployed yet). Normal for scale-less stores —
		// keep quiet at debug.
		l.Logger.Debug("scalesync: no PLU file for this terminal", "err", cloudErr.Error())
	case errors.Is(cloudErr, cloud.ErrNetwork):
		l.Logger.Debug("scalesync: network error; will retry next tick", "err", cloudErr.Error())
	default:
		l.Logger.Warn("scalesync: cloud error; will retry next tick", "err", cloudErr.Error())
	}
	return true
}

// process validates one fetched response and mirrors its content to
// DestPath under the package's write discipline.
func (l *Loop) process(resp *cloud.ScalePLUFileResponse) {
	if resp.Format != cloud.ScalePLUFileFormat {
		l.Logger.Warn("scalesync: unknown PLU file format; skipping write (agent update needed?)",
			"format", resp.Format, "want", cloud.ScalePLUFileFormat)
		return
	}
	if resp.Encoding != cloud.ScalePLUFileEncoding {
		l.Logger.Warn("scalesync: unknown PLU file encoding; skipping write (agent update needed?)",
			"encoding", resp.Encoding, "want", cloud.ScalePLUFileEncoding)
		return
	}
	if resp.PathHint != "" && resp.PathHint != WindowsPLUFilePath {
		// The web UI tells retailers where the file lives; if the cloud
		// ever changes the hint, agent and UI must move in lockstep.
		l.Logger.Warn("scalesync: cloud path_hint differs from agent destination",
			"path_hint", resp.PathHint, "dest", WindowsPLUFilePath)
	}

	// Transcode to the exact bytes LINK69 reads from disk. The cloud's
	// sha256 is defined over THESE bytes, so all hashing below (and the
	// startup seed, which hashes the raw file) agrees byte-for-byte.
	encoded := encodeUTF16LEBOM(resp.Content)
	gotSHA := hashHex(encoded)
	if resp.SHA256 != "" && resp.SHA256 != gotSHA {
		l.Logger.Warn("scalesync: encoded-bytes sha256 mismatch; skipping write",
			"claimed", resp.SHA256, "computed", gotSHA)
		return
	}

	// Dedupe: identical content already on disk → nothing to do. The
	// file-exists check heals the case where someone deleted PLU.txt
	// between ticks — the hash matches but the file must come back.
	if gotSHA == l.lastSHA256 && fileExists(l.DestPath) {
		l.Logger.Debug("scalesync: content unchanged; skipping write",
			"sha256", gotSHA, "entry_count", resp.EntryCount)
		return
	}

	// Safety guard: v2 content always includes the header row, so an
	// "empty" render is header-only (zero data rows). A header-only
	// render (catalog glitch, cloud bug) must not wipe the scale's
	// last good product list.
	if dataRowsText(resp.Content) == 0 && fileHasDataRows(l.DestPath) {
		l.Logger.Warn("scalesync: refusing to overwrite PLU file containing data rows with header-only content; keeping last good file",
			"path", l.DestPath, "entry_count", resp.EntryCount)
		return
	}

	if err := config.WriteAtomic(l.DestPath, encoded, 0o644); err != nil {
		l.Logger.Error("scalesync: write failed", "path", l.DestPath, "err", err.Error())
		return
	}
	l.lastSHA256 = gotSHA

	l.Logger.Info("scalesync: PLU file updated",
		"path", l.DestPath,
		"bytes", len(encoded),
		"entry_count", resp.EntryCount,
		"sha256", gotSHA,
		"generated_count", len(resp.Generated),
		"skipped_count", len(resp.Skipped),
	)
	// Per-entry skip reasons are data-quality signals the retailer can
	// act on (e.g. a weighed product missing a price). Logged only when
	// content actually changed, so a stable catalog doesn't repeat the
	// same warnings every tick.
	for _, s := range resp.Skipped {
		l.Logger.Warn("scalesync: product skipped in PLU file",
			"product_id", s.ProductID, "reason", s.Reason)
	}
}

// encodeUTF16LEBOM transcodes text to the exact bytes LINK69 expects
// on disk: FF FE BOM + UTF-16 LE code units. Mirrors the web repo's
// encodeLink69PluFile (src/lib/scale/link69-file.ts) — Go's
// utf16.Encode produces the same code units (surrogate pairs included)
// as Node's Buffer.from(text, 'utf16le').
func encodeUTF16LEBOM(text string) []byte {
	units := utf16.Encode([]rune(text))
	buf := make([]byte, 2+2*len(units))
	buf[0], buf[1] = 0xFF, 0xFE
	for i, u := range units {
		buf[2+2*i] = byte(u)
		buf[3+2*i] = byte(u >> 8)
	}
	return buf
}

// dataRowsText counts data rows (CRLF-terminated lines beyond the
// header row) in the response's content string. Zero for header-only
// content — and for completely empty content, which a v2 cloud never
// sends but which must also trip the guard.
func dataRowsText(content string) int {
	lines := strings.Count(content, "\r\n")
	if lines <= 1 {
		return 0
	}
	return lines - 1
}

// fileHasDataRows reports whether the on-disk PLU file (UTF-16 LE +
// BOM) contains at least one data row beyond the header. Counts CRLF
// code units directly without a full decode; a missing or unreadable
// file counts as "no data rows" (nothing to protect).
func fileHasDataRows(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	crlf := 0
	for i := 0; i+3 < len(b); i += 2 {
		if b[i] == 0x0D && b[i+1] == 0x00 && b[i+2] == 0x0A && b[i+3] == 0x00 {
			crlf++
		}
	}
	return crlf > 1
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
