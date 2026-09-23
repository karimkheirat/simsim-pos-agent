// Package updater is the agent's self-update (POS_AGENT_SPEC.md §9).
//
// Lifecycle, in service mode only:
//
//  1. Startup (startup.go) runs before anything else in the service
//     process: it counts trial starts of a freshly-swapped binary and
//     rolls back to agent.exe.old on the 4th start.
//  2. Updater.Run waits for the HTTP server to be listening, then 60 s
//     more, and calls Finalize — the update (if any) is accepted.
//  3. ~5 min after start, and every release_check_seconds after that,
//     it asks the cloud for the latest release. A strictly newer version
//     with a bare agent.exe asset + SHA-256 becomes PENDING.
//  4. A pending update installs only inside 03:00–05:00 local time, with
//     no print / label / drawer job in the last 60 s and at least 200 MB
//     free on the install volume. Outside those conditions it re-checks
//     every 10 min.
//  5. Install = download over HTTPS to update\new.exe (size-capped,
//     SHA-256 verified), copy beside the exe as agent.exe.new, write
//     update\pending.json, rename agent.exe → agent.exe.old, rename
//     agent.exe.new → agent.exe, stop the HTTP server gracefully and
//     exit(1). The SCM recovery action (restart after 10 s) brings the
//     service back on the new binary.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/cloud"
)

// Defaults for the knobs on Updater. Zero-valued fields take these.
const (
	DefaultFirstCheckDelay  = 5 * time.Minute
	DefaultPendingRecheck   = 10 * time.Minute
	DefaultHealthyAfter     = 60 * time.Second
	DefaultQuietPeriod      = 60 * time.Second
	DefaultMinFreeBytes     = 200 << 20 // 200 MB
	DefaultMaxDownloadBytes = 100 << 20 // 100 MB
	DefaultDownloadTimeout  = 10 * time.Minute
	WindowStartHour         = 3
	WindowEndHour           = 5 // exclusive: 03:00:00 <= t < 05:00:00
)

// maxConsecutiveFailures bounds download/verify/swap retries inside one
// night's window before backing off to the daily cadence.
const maxConsecutiveFailures = 3

// ReleaseSource is the cloud call the updater makes. *cloud.Client
// satisfies it.
type ReleaseSource interface {
	LatestRelease(ctx context.Context) (*cloud.ReleaseInfo, error)
}

// Clock abstracts time for tests.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Updater is the self-update loop. Construct with the public fields and
// call Run. Required: Version, Paths, Releases, Logger, Exit.
type Updater struct {
	// Version is the running build's version (main.Version).
	Version string
	// Enabled mirrors config auto_update. false → Run only finalizes.
	Enabled bool

	Paths    Paths
	Releases ReleaseSource
	Logger   *slog.Logger

	// Ready is closed once the HTTP server is listening. nil → treated
	// as already ready.
	Ready <-chan struct{}
	// Activity reports the last hardware job and whether one is in
	// flight (api.Server.LastHardwareActivity). nil → never busy.
	Activity func() (time.Time, bool)
	// DiskFree reports free bytes on the volume holding a path.
	// nil → the package DiskFree.
	DiskFree func(path string) (uint64, error)
	// Shutdown stops the HTTP server gracefully before the exit.
	Shutdown func()
	// Exit terminates the process (os.Exit in production).
	Exit func(code int)

	FS         FS           // nil → OSFS
	Clock      Clock        // nil → real clock
	HTTPClient *http.Client // download client; nil → HTTPS-only default

	CheckInterval    time.Duration // release_check_seconds; 0 → 24 h
	FirstCheckDelay  time.Duration
	PendingRecheck   time.Duration
	HealthyAfter     time.Duration
	QuietPeriod      time.Duration
	MinFreeBytes     uint64
	MaxDownloadBytes int64
	DownloadTimeout  time.Duration

	mu          sync.Mutex
	pending     *cloud.ReleaseInfo
	lastCheckAt time.Time
	lastError   string
	failures    int
	exited      bool
}

// Status is the updater's state as surfaced on the local GET /status.
type Status struct {
	AutoUpdate     bool          `json:"auto_update"`
	CurrentVersion string        `json:"current_version"`
	PendingVersion string        `json:"pending_version,omitempty"`
	LastCheckAt    *time.Time    `json:"last_check_at,omitempty"`
	LastError      string        `json:"last_error,omitempty"`
	LastRollback   *RollbackNote `json:"last_rollback,omitempty"`
}

// Status returns a snapshot for /status. Safe for concurrent use.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := Status{
		AutoUpdate:     u.Enabled && u.Version != "dev",
		CurrentVersion: u.Version,
		LastError:      u.lastError,
		LastRollback:   ReadRollbackNote(u.fs(), u.Paths),
	}
	if u.pending != nil {
		s.PendingVersion = u.pending.Version
	}
	if !u.lastCheckAt.IsZero() {
		t := u.lastCheckAt
		s.LastCheckAt = &t
	}
	return s
}

// Run blocks until ctx is canceled (or the process exits for an update).
func (u *Updater) Run(ctx context.Context) {
	finalized := make(chan struct{})
	go func() {
		defer close(finalized)
		u.finalizeWhenHealthy(ctx)
	}()

	if !u.Enabled || u.Version == "dev" {
		u.Logger.Info("updater: auto-update off", "auto_update", u.Enabled, "version", u.Version)
		<-finalized
		return
	}

	u.Logger.Info("updater: auto-update on",
		"version", u.Version,
		"first_check_in", u.firstCheckDelay().String(),
		"check_interval", u.checkInterval().String())
	wait := u.firstCheckDelay()
	for {
		select {
		case <-ctx.Done():
			<-finalized
			return
		case <-u.clock().After(wait):
		}
		wait = u.cycle(ctx)
		if u.hasExited() {
			return
		}
	}
}

func (u *Updater) finalizeWhenHealthy(ctx context.Context) {
	if u.Ready != nil {
		select {
		case <-ctx.Done():
			return
		case <-u.Ready:
		}
	}
	select {
	case <-ctx.Done():
		return
	case <-u.clock().After(u.healthyAfter()):
	}
	Finalize(u.fs(), u.Paths, u.Logger)
}

// cycle runs one check/install pass and returns how long to wait before
// the next one.
func (u *Updater) cycle(ctx context.Context) time.Duration {
	rel := u.getPending()
	if rel == nil {
		var err error
		rel, err = u.check(ctx)
		if err != nil {
			u.setError(err)
			u.Logger.Warn("updater: release check failed", "err", err.Error())
			return u.checkInterval()
		}
		if rel == nil {
			return u.checkInterval()
		}
		u.setPending(rel)
		u.Logger.Info("updater: update pending", "from", u.Version, "to", rel.Version)
	}

	if reason := u.blockedReason(); reason != "" {
		u.Logger.Info("updater: update pending but not installing now", "to", rel.Version, "reason", reason)
		return u.pendingRecheck()
	}

	if err := u.install(ctx, rel); err != nil {
		u.setError(err)
		u.clearPending() // re-fetch the manifest next time
		u.mu.Lock()
		u.failures++
		failures := u.failures
		if failures >= maxConsecutiveFailures {
			u.failures = 0
		}
		u.mu.Unlock()
		u.Logger.Error("updater: install failed", "to", rel.Version, "err", err.Error(), "consecutive_failures", failures)
		if failures >= maxConsecutiveFailures {
			return u.checkInterval()
		}
		return u.pendingRecheck()
	}
	return 0
}

// check asks the cloud for the latest release and returns it if the
// updater should act on it, or (nil, nil) when there is nothing to do.
func (u *Updater) check(ctx context.Context) (*cloud.ReleaseInfo, error) {
	rel, err := u.Releases.LatestRelease(ctx)
	u.mu.Lock()
	u.lastCheckAt = u.clock().Now()
	u.mu.Unlock()
	if err != nil {
		return nil, err
	}
	act, why := u.shouldAct(rel)
	if !act {
		u.Logger.Info("updater: no update", "latest", rel.Version, "running", u.Version, "reason", why)
		u.setError(nil)
		return nil, nil
	}
	return rel, nil
}

// shouldAct decides whether a release manifest is an update to take.
func (u *Updater) shouldAct(rel *cloud.ReleaseInfo) (bool, string) {
	if rel == nil {
		return false, "empty manifest"
	}
	cmp, err := CompareVersions(rel.Version, u.Version)
	if err != nil {
		return false, err.Error()
	}
	if cmp <= 0 {
		return false, "not newer"
	}
	if rel.AgentDownloadURL == nil || *rel.AgentDownloadURL == "" || rel.AgentSHA256 == nil || *rel.AgentSHA256 == "" {
		return false, "release has no bare agent.exe asset"
	}
	if note := ReadRollbackNote(u.fs(), u.Paths); note != nil && note.From == rel.Version {
		return false, "this version was rolled back on this machine"
	}
	return true, ""
}

// blockedReason returns why a pending update may not install right now,
// or "" when every §9.3 condition holds.
func (u *Updater) blockedReason() string {
	now := u.clock().Now()
	if !InWindow(now) {
		return fmt.Sprintf("outside %02d:00-%02d:00 local (now %s)", WindowStartHour, WindowEndHour, now.Format("15:04"))
	}
	if u.Activity != nil {
		last, busy := u.Activity()
		if busy {
			return "a print or drawer job is in progress"
		}
		if !last.IsZero() && now.Sub(last) < u.quietPeriod() {
			return "a print or drawer job ran in the last 60 s"
		}
	}
	diskFree := u.DiskFree
	if diskFree == nil {
		diskFree = DiskFree
	}
	free, err := diskFree(filepath.Dir(u.Paths.Exe))
	if err != nil {
		return "cannot read free disk space: " + err.Error()
	}
	if free < u.minFreeBytes() {
		return fmt.Sprintf("only %d MB free on the install volume (need %d MB)", free>>20, u.minFreeBytes()>>20)
	}
	return ""
}

// InWindow reports whether t (in its own location — local time in
// production) falls in [03:00, 05:00).
func InWindow(t time.Time) bool {
	h := t.Hour()
	return h >= WindowStartHour && h < WindowEndHour
}

// install downloads, verifies, swaps, and exits the process.
func (u *Updater) install(ctx context.Context, rel *cloud.ReleaseInfo) error {
	u.Logger.Info("updater: installing", "from", u.Version, "to", rel.Version)
	if err := u.download(ctx, *rel.AgentDownloadURL, *rel.AgentSHA256); err != nil {
		return err
	}
	if err := u.swap(rel.Version); err != nil {
		return err
	}
	u.setError(nil)
	u.Logger.Info("updater: binary swapped; stopping the server and exiting so the service manager restarts on the new version",
		"to", rel.Version)
	if u.Shutdown != nil {
		u.Shutdown()
	}
	u.mu.Lock()
	u.exited = true
	u.mu.Unlock()
	u.Exit(1)
	return nil
}

// ErrChecksumMismatch is returned when the download's SHA-256 does not
// match the manifest. The file is deleted.
var ErrChecksumMismatch = errors.New("updater: sha256 mismatch")

// download fetches url to Paths.Download() and verifies its SHA-256.
func (u *Updater) download(ctx context.Context, url, wantSHA string) error {
	if !strings.HasPrefix(strings.ToLower(url), "https://") {
		return fmt.Errorf("updater: refusing non-https download url %q", url)
	}
	want := strings.ToLower(strings.TrimSpace(wantSHA))
	if len(want) != 64 {
		return fmt.Errorf("updater: agent_sha256 is not 64 hex characters (%d)", len(want))
	}
	if _, err := hex.DecodeString(want); err != nil {
		return fmt.Errorf("updater: agent_sha256 is not hex: %w", err)
	}

	f := u.fs()
	if err := f.MkdirAll(u.Paths.UpdateDir, 0o755); err != nil {
		return fmt.Errorf("updater: create update dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, u.downloadTimeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("updater: build download request: %w", err)
	}
	req.Header.Set("User-Agent", "simsim-pos-agent/"+u.Version)
	resp, err := u.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("updater: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("updater: download: HTTP %d", resp.StatusCode)
	}
	limit := u.maxDownloadBytes()
	if resp.ContentLength > limit {
		return fmt.Errorf("updater: download is %d bytes, over the %d-byte cap", resp.ContentLength, limit)
	}

	dst := u.Paths.Download()
	out, err := f.Create(dst)
	if err != nil {
		return fmt.Errorf("updater: create %s: %w", dst, err)
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, limit+1))
	closeErr := out.Close()
	switch {
	case copyErr != nil:
		_ = removeIfExists(f, dst)
		return fmt.Errorf("updater: download: %w", copyErr)
	case closeErr != nil:
		_ = removeIfExists(f, dst)
		return fmt.Errorf("updater: write %s: %w", dst, closeErr)
	case n > limit:
		_ = removeIfExists(f, dst)
		return fmt.Errorf("updater: download exceeded the %d-byte cap", limit)
	case n == 0:
		_ = removeIfExists(f, dst)
		return errors.New("updater: download was empty")
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		_ = removeIfExists(f, dst)
		u.Logger.Error("updater: sha256 mismatch; download deleted", "want", want, "got", got, "bytes", n)
		return ErrChecksumMismatch
	}
	u.Logger.Info("updater: download verified", "path", dst, "bytes", n, "sha256", got)
	return nil
}

// swap installs the verified download as the service binary. Order:
//
//  1. copy update\new.exe → agent.exe.new (beside the exe, same volume)
//  2. write update\pending.json {from, to, attempts:0, at}
//  3. rename agent.exe → agent.exe.old
//  4. rename agent.exe.new → agent.exe
//
// A failure at 3 or 4 undoes what was done and deletes the marker, so
// the running binary is left exactly as it was.
func (u *Updater) swap(toVersion string) error {
	f := u.fs()
	p := u.Paths

	if err := copyFile(f, p.Download(), p.Staged()); err != nil {
		_ = removeIfExists(f, p.Staged())
		return fmt.Errorf("updater: stage beside exe: %w", err)
	}
	u.Logger.Info("updater: staged", "path", p.Staged())

	m := Marker{From: u.Version, To: toVersion, Attempts: 0, At: u.clock().Now().UTC()}
	if err := writeJSON(f, p.Marker(), m); err != nil {
		_ = removeIfExists(f, p.Staged())
		return fmt.Errorf("updater: write marker: %w", err)
	}
	u.Logger.Info("updater: marker written", "path", p.Marker())

	if err := removeIfExists(f, p.Old()); err != nil {
		_ = removeIfExists(f, p.Marker())
		_ = removeIfExists(f, p.Staged())
		return fmt.Errorf("updater: remove stale %s: %w", p.Old(), err)
	}
	if err := f.Rename(p.Exe, p.Old()); err != nil {
		_ = removeIfExists(f, p.Marker())
		_ = removeIfExists(f, p.Staged())
		return fmt.Errorf("updater: rename running exe aside: %w", err)
	}
	u.Logger.Info("updater: running binary renamed", "to", p.Old())

	if err := f.Rename(p.Staged(), p.Exe); err != nil {
		if rerr := f.Rename(p.Old(), p.Exe); rerr != nil {
			// Worst case: no agent.exe on disk. The running process is
			// still fine; the next service start would fail and the
			// installer is the recovery path. Shout.
			u.Logger.Error("updater: CRITICAL - could not put the old binary back", "err", rerr.Error())
		}
		_ = removeIfExists(f, p.Marker())
		_ = removeIfExists(f, p.Staged())
		return fmt.Errorf("updater: move new binary into place: %w", err)
	}
	u.Logger.Info("updater: new binary in place", "path", p.Exe)
	_ = removeIfExists(f, p.Download())
	return nil
}

func copyFile(f FS, src, dst string) error {
	in, err := f.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := f.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// --- small accessors / defaults ---

func (u *Updater) fs() FS {
	if u.FS == nil {
		return OSFS{}
	}
	return u.FS
}

func (u *Updater) clock() Clock {
	if u.Clock == nil {
		return realClock{}
	}
	return u.Clock
}

// httpClient refuses to follow a redirect off HTTPS (GitHub release
// assets redirect to a CDN; that hop must stay encrypted too).
func (u *Updater) httpClient() *http.Client {
	if u.HTTPClient != nil {
		return u.HTTPClient
	}
	return &http.Client{CheckRedirect: HTTPSOnlyRedirects}
}

// HTTPSOnlyRedirects is an http.Client.CheckRedirect that refuses any
// hop to a non-https URL and caps the chain at 10.
func HTTPSOnlyRedirects(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("updater: refusing redirect to non-https %s", req.URL.Redacted())
	}
	if len(via) >= 10 {
		return errors.New("updater: too many redirects")
	}
	return nil
}

func durOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func (u *Updater) checkInterval() time.Duration { return durOr(u.CheckInterval, 24*time.Hour) }
func (u *Updater) firstCheckDelay() time.Duration {
	return durOr(u.FirstCheckDelay, DefaultFirstCheckDelay)
}
func (u *Updater) pendingRecheck() time.Duration {
	return durOr(u.PendingRecheck, DefaultPendingRecheck)
}
func (u *Updater) healthyAfter() time.Duration { return durOr(u.HealthyAfter, DefaultHealthyAfter) }
func (u *Updater) quietPeriod() time.Duration  { return durOr(u.QuietPeriod, DefaultQuietPeriod) }
func (u *Updater) downloadTimeout() time.Duration {
	return durOr(u.DownloadTimeout, DefaultDownloadTimeout)
}

func (u *Updater) minFreeBytes() uint64 {
	if u.MinFreeBytes > 0 {
		return u.MinFreeBytes
	}
	return DefaultMinFreeBytes
}

func (u *Updater) maxDownloadBytes() int64 {
	if u.MaxDownloadBytes > 0 {
		return u.MaxDownloadBytes
	}
	return DefaultMaxDownloadBytes
}

func (u *Updater) getPending() *cloud.ReleaseInfo {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.pending
}

func (u *Updater) setPending(r *cloud.ReleaseInfo) {
	u.mu.Lock()
	u.pending = r
	u.mu.Unlock()
}

func (u *Updater) clearPending() { u.setPending(nil) }

func (u *Updater) setError(err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if err == nil {
		u.lastError = ""
		return
	}
	u.lastError = err.Error()
}

func (u *Updater) hasExited() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.exited
}
