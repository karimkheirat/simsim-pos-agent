package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/cloud"
)

// ---------- helpers ----------

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// fakeClock returns a fixed Now; After fires immediately.
type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }
func (c fakeClock) After(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

// recFS wraps OSFS, records renames/removes in order, and can fail a
// chosen rename.
type recFS struct {
	OSFS
	mu       sync.Mutex
	ops      []string
	failFrom string // Rename(failFrom, *) returns an error
}

func (r *recFS) Rename(o, n string) error {
	r.mu.Lock()
	r.ops = append(r.ops, "rename "+filepath.Base(o)+" -> "+filepath.Base(n))
	fail := r.failFrom != "" && o == r.failFrom
	r.mu.Unlock()
	if fail {
		return errors.New("injected rename failure")
	}
	return r.OSFS.Rename(o, n)
}

func (r *recFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	r.mu.Lock()
	r.ops = append(r.ops, "write "+filepath.Base(name))
	r.mu.Unlock()
	return r.OSFS.WriteFile(name, data, perm)
}

func (r *recFS) Ops() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ops...)
}

// layout makes {tmp}/bin/agent.exe (content "OLD") and {tmp}/update.
func layout(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	p := Paths{Exe: filepath.Join(bin, "agent.exe"), UpdateDir: filepath.Join(dir, "update")}
	mustWrite(t, p.Exe, "OLD")
	return p
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func strp(s string) *string { return &s }

type fakeReleases struct {
	rel   *cloud.ReleaseInfo
	err   error
	calls int
}

func (f *fakeReleases) LatestRelease(context.Context) (*cloud.ReleaseInfo, error) {
	f.calls++
	return f.rel, f.err
}

// at returns hour:min local time on a fixed date.
func at(hour, min int) time.Time {
	return time.Date(2026, 9, 23, hour, min, 0, 0, time.Local)
}

// newTestUpdater wires an updater against a TLS test server serving body.
func newTestUpdater(t *testing.T, p Paths, body string, rel *cloud.ReleaseInfo) (*Updater, *int, *bool) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	if rel != nil && rel.AgentDownloadURL != nil && *rel.AgentDownloadURL == "SERVER" {
		rel.AgentDownloadURL = strp(srv.URL + "/agent.exe")
	}
	exitCode := -1
	shutdown := false
	u := &Updater{
		Version:    "0.3.10",
		Enabled:    true,
		Paths:      p,
		Releases:   &fakeReleases{rel: rel},
		Logger:     discard(),
		FS:         &recFS{},
		Clock:      fakeClock{now: at(3, 30)},
		HTTPClient: srv.Client(),
		DiskFree:   func(string) (uint64, error) { return 10 << 30, nil },
		Shutdown:   func() { shutdown = true },
		Exit:       func(code int) { exitCode = code },
	}
	return u, &exitCode, &shutdown
}

// ---------- semver ----------

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.3.10", "0.3.9", 1},
		{"0.3.9", "0.3.10", -1},
		{"0.3.10", "0.3.10", 0},
		{"v1.0.0", "0.99.99", 1},
		{"1.2", "1.2.0", 0},
		{"1.2.0.1", "1.2", 1},
		{"0.10.0", "0.9.99", 1},
		{"2.0.0", "10.0.0", -1},
	}
	for _, c := range cases {
		got, err := CompareVersions(c.a, c.b)
		if err != nil {
			t.Errorf("Compare(%q,%q) err: %v", c.a, c.b, err)
			continue
		}
		if got != c.want {
			t.Errorf("Compare(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, bad := range []string{"dev", "", "0.0.0-manual", "1..2", "1.2.x", "1.-2.3"} {
		if _, err := CompareVersions(bad, "0.1.0"); err == nil {
			t.Errorf("Compare(%q, ...) should error", bad)
		}
	}
}

// ---------- window + conditions ----------

func TestInWindow(t *testing.T) {
	cases := map[time.Time]bool{
		at(2, 59): false,
		at(3, 0):  true,
		at(4, 59): true,
		at(5, 0):  false,
		at(14, 0): false,
		time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC): true,
	}
	for tm, want := range cases {
		if got := InWindow(tm); got != want {
			t.Errorf("InWindow(%s) = %v, want %v", tm.Format("15:04"), got, want)
		}
	}
}

func TestBlockedReason(t *testing.T) {
	p := layout(t)
	base := func() *Updater {
		u, _, _ := newTestUpdater(t, p, "", nil)
		return u
	}

	if r := base().blockedReason(); r != "" {
		t.Errorf("all conditions met: got %q", r)
	}

	u := base()
	u.Clock = fakeClock{now: at(14, 0)}
	if r := u.blockedReason(); !strings.Contains(r, "outside") {
		t.Errorf("afternoon: got %q", r)
	}

	u = base()
	u.Activity = func() (time.Time, bool) { return at(3, 30).Add(-30 * time.Second), false }
	if r := u.blockedReason(); !strings.Contains(r, "last 60 s") {
		t.Errorf("job 30 s ago: got %q", r)
	}

	u = base()
	u.Activity = func() (time.Time, bool) { return at(3, 30).Add(-61 * time.Second), false }
	if r := u.blockedReason(); r != "" {
		t.Errorf("job 61 s ago should not block: got %q", r)
	}

	u = base()
	u.Activity = func() (time.Time, bool) { return at(3, 30).Add(-10 * time.Minute), true }
	if r := u.blockedReason(); !strings.Contains(r, "in progress") {
		t.Errorf("in-flight job: got %q", r)
	}

	u = base()
	u.DiskFree = func(string) (uint64, error) { return 199 << 20, nil }
	if r := u.blockedReason(); !strings.Contains(r, "free") {
		t.Errorf("199 MB free: got %q", r)
	}

	u = base()
	u.DiskFree = func(string) (uint64, error) { return 0, errors.New("boom") }
	if r := u.blockedReason(); !strings.Contains(r, "disk") {
		t.Errorf("disk error must fail closed: got %q", r)
	}
}

// ---------- shouldAct ----------

func TestShouldAct(t *testing.T) {
	p := layout(t)
	u, _, _ := newTestUpdater(t, p, "", nil)
	full := func(v string) *cloud.ReleaseInfo {
		return &cloud.ReleaseInfo{Version: v, AgentDownloadURL: strp("https://x/a.exe"), AgentSHA256: strp(sha("x"))}
	}
	if ok, _ := u.shouldAct(full("0.3.11")); !ok {
		t.Error("newer with assets should act")
	}
	if ok, _ := u.shouldAct(full("0.3.10")); ok {
		t.Error("same version must not act")
	}
	if ok, _ := u.shouldAct(full("0.3.9")); ok {
		t.Error("older must not act")
	}
	if ok, _ := u.shouldAct(&cloud.ReleaseInfo{Version: "0.4.0", AgentSHA256: strp(sha("x"))}); ok {
		t.Error("null agent_download_url must not act")
	}
	if ok, _ := u.shouldAct(&cloud.ReleaseInfo{Version: "0.4.0", AgentDownloadURL: strp("https://x")}); ok {
		t.Error("null agent_sha256 must not act")
	}
	if ok, _ := u.shouldAct(full("0.0.0-manual")); ok {
		t.Error("unparseable version must not act")
	}
	// A version this machine already rolled back is skipped.
	mustWrite(t, p.RollbackNote(), `{"from":"0.3.11","to":"0.3.10"}`)
	if ok, why := u.shouldAct(full("0.3.11")); ok || !strings.Contains(why, "rolled back") {
		t.Errorf("rolled-back version: ok=%v why=%q", ok, why)
	}
	if ok, _ := u.shouldAct(full("0.3.12")); !ok {
		t.Error("a newer version than the rolled-back one should act")
	}
}

// ---------- download ----------

func TestDownload_RefusesNonHTTPS(t *testing.T) {
	p := layout(t)
	u, _, _ := newTestUpdater(t, p, "NEW", nil)
	err := u.download(context.Background(), "http://example.test/agent.exe", sha("NEW"))
	if err == nil || !strings.Contains(err.Error(), "non-https") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownload_SHAMismatchDeletes(t *testing.T) {
	p := layout(t)
	u, _, _ := newTestUpdater(t, p, "TAMPERED", &cloud.ReleaseInfo{Version: "0.3.11", AgentDownloadURL: strp("SERVER")})
	rel, _ := u.Releases.LatestRelease(context.Background())
	err := u.download(context.Background(), *rel.AgentDownloadURL, sha("NEW"))
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v, want ErrChecksumMismatch", err)
	}
	if fileExists(p.Download()) {
		t.Error("mismatched download must be deleted")
	}
}

func TestDownload_SHAIsCaseInsensitive(t *testing.T) {
	p := layout(t)
	u, _, _ := newTestUpdater(t, p, "NEW", &cloud.ReleaseInfo{Version: "0.3.11", AgentDownloadURL: strp("SERVER")})
	rel, _ := u.Releases.LatestRelease(context.Background())
	if err := u.download(context.Background(), *rel.AgentDownloadURL, strings.ToUpper(sha("NEW"))); err != nil {
		t.Fatalf("download: %v", err)
	}
	if read(t, p.Download()) != "NEW" {
		t.Error("download content wrong")
	}
}

func TestDownload_SizeCap(t *testing.T) {
	p := layout(t)
	big := strings.Repeat("A", 2048)
	u, _, _ := newTestUpdater(t, p, big, &cloud.ReleaseInfo{Version: "0.3.11", AgentDownloadURL: strp("SERVER")})
	u.MaxDownloadBytes = 1024
	rel, _ := u.Releases.LatestRelease(context.Background())
	err := u.download(context.Background(), *rel.AgentDownloadURL, sha(big))
	if err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("err = %v, want cap error", err)
	}
	if fileExists(p.Download()) {
		t.Error("over-cap download must be deleted")
	}
}

func TestHTTPSOnlyRedirects(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://cdn.example/agent.exe", nil)
	if err := HTTPSOnlyRedirects(req, nil); err == nil {
		t.Error("redirect to http must be refused")
	}
	req, _ = http.NewRequest(http.MethodGet, "https://cdn.example/agent.exe", nil)
	if err := HTTPSOnlyRedirects(req, nil); err != nil {
		t.Errorf("https redirect refused: %v", err)
	}
}

// ---------- full cycle: swap order + exit ----------

func TestCycle_InstallsSwapsInOrderAndExits(t *testing.T) {
	p := layout(t)
	rel := &cloud.ReleaseInfo{Version: "0.3.11", AgentDownloadURL: strp("SERVER"), AgentSHA256: strp(sha("NEW"))}
	u, exitCode, shutdown := newTestUpdater(t, p, "NEW", rel)

	u.cycle(context.Background())

	if *exitCode != 1 {
		t.Fatalf("exit code = %d, want 1 (SCM restarts on failure exit)", *exitCode)
	}
	if !*shutdown {
		t.Error("server must be shut down before exit")
	}
	if got := read(t, p.Exe); got != "NEW" {
		t.Errorf("agent.exe = %q, want NEW", got)
	}
	if got := read(t, p.Old()); got != "OLD" {
		t.Errorf("agent.exe.old = %q, want OLD", got)
	}
	if fileExists(p.Staged()) || fileExists(p.Download()) {
		t.Error("staging files should be gone after the swap")
	}
	var m Marker
	if err := json.Unmarshal([]byte(read(t, p.Marker())), &m); err != nil {
		t.Fatal(err)
	}
	if m.From != "0.3.10" || m.To != "0.3.11" || m.Attempts != 0 {
		t.Errorf("marker = %+v", m)
	}

	ops := u.FS.(*recFS).Ops()
	want := []string{"write pending.json", "rename agent.exe -> agent.exe.old", "rename agent.exe.new -> agent.exe"}
	if strings.Join(ops, " | ") != strings.Join(want, " | ") {
		t.Errorf("swap ops =\n  %v\nwant\n  %v", ops, want)
	}
}

func TestCycle_OutsideWindowKeepsPending(t *testing.T) {
	p := layout(t)
	rel := &cloud.ReleaseInfo{Version: "0.3.11", AgentDownloadURL: strp("SERVER"), AgentSHA256: strp(sha("NEW"))}
	u, exitCode, _ := newTestUpdater(t, p, "NEW", rel)
	u.Clock = fakeClock{now: at(14, 0)}

	wait := u.cycle(context.Background())
	if wait != DefaultPendingRecheck {
		t.Errorf("wait = %v, want %v", wait, DefaultPendingRecheck)
	}
	if *exitCode != -1 || read(t, p.Exe) != "OLD" {
		t.Error("nothing should install outside the window")
	}
	if u.Status().PendingVersion != "0.3.11" {
		t.Error("update should stay pending")
	}
	// Next cycle inside the window installs without re-asking the cloud.
	u.Clock = fakeClock{now: at(3, 5)}
	u.cycle(context.Background())
	if *exitCode != 1 || read(t, p.Exe) != "NEW" {
		t.Error("pending update should install once inside the window")
	}
	if calls := u.Releases.(*fakeReleases).calls; calls != 1 {
		t.Errorf("cloud calls = %d, want 1", calls)
	}
}

func TestCycle_NoUpdateWaitsCheckInterval(t *testing.T) {
	p := layout(t)
	rel := &cloud.ReleaseInfo{Version: "0.3.10", AgentDownloadURL: strp("SERVER"), AgentSHA256: strp(sha("NEW"))}
	u, exitCode, _ := newTestUpdater(t, p, "NEW", rel)
	u.CheckInterval = 7 * time.Hour
	if wait := u.cycle(context.Background()); wait != 7*time.Hour {
		t.Errorf("wait = %v", wait)
	}
	if *exitCode != -1 {
		t.Error("same version must not install")
	}
}

func TestCycle_SHAMismatchLeavesBinaryAlone(t *testing.T) {
	p := layout(t)
	rel := &cloud.ReleaseInfo{Version: "0.3.11", AgentDownloadURL: strp("SERVER"), AgentSHA256: strp(sha("NEW"))}
	u, exitCode, _ := newTestUpdater(t, p, "EVIL", rel)
	wait := u.cycle(context.Background())
	if *exitCode != -1 {
		t.Fatal("must not exit on sha mismatch")
	}
	if read(t, p.Exe) != "OLD" || fileExists(p.Old()) || fileExists(p.Marker()) {
		t.Error("running binary must be untouched")
	}
	if wait != DefaultPendingRecheck {
		t.Errorf("wait = %v, want retry at %v", wait, DefaultPendingRecheck)
	}
	if !strings.Contains(u.Status().LastError, "sha256") {
		t.Errorf("last error = %q", u.Status().LastError)
	}
	// After three consecutive failures it backs off to the daily cadence.
	u.cycle(context.Background())
	if wait := u.cycle(context.Background()); wait != 24*time.Hour {
		t.Errorf("third failure wait = %v, want 24h", wait)
	}
}

func TestSwap_SecondRenameFailsRestoresOld(t *testing.T) {
	p := layout(t)
	rel := &cloud.ReleaseInfo{Version: "0.3.11", AgentDownloadURL: strp("SERVER"), AgentSHA256: strp(sha("NEW"))}
	u, exitCode, _ := newTestUpdater(t, p, "NEW", rel)
	u.FS = &recFS{failFrom: p.Staged()}

	u.cycle(context.Background())

	if *exitCode != -1 {
		t.Fatal("must not exit when the swap failed")
	}
	if read(t, p.Exe) != "OLD" {
		t.Error("old binary must be put back")
	}
	if fileExists(p.Marker()) || fileExists(p.Staged()) || fileExists(p.Old()) {
		t.Error("marker/staged/old must be cleaned after a failed swap")
	}
}

// ---------- startup: rollback + stale marker ----------

func writeMarker(t *testing.T, p Paths, m Marker) {
	t.Helper()
	b, _ := json.Marshal(m)
	mustWrite(t, p.Marker(), string(b))
}

func TestStartup_NoMarker(t *testing.T) {
	p := layout(t)
	r := Startup(OSFS{}, p, "0.3.11", time.Now, discard())
	if r.RolledBack || r.Pending {
		t.Errorf("result = %+v", r)
	}
}

func TestStartup_CountsAttemptsThenRollsBack(t *testing.T) {
	p := layout(t)
	// State right after a swap: agent.exe is the new build, .old the previous.
	mustWrite(t, p.Exe, "NEW")
	mustWrite(t, p.Old(), "OLD")
	writeMarker(t, p, Marker{From: "0.3.10", To: "0.3.11"})

	for i := 1; i <= MaxStartAttempts; i++ {
		r := Startup(OSFS{}, p, "0.3.11", time.Now, discard())
		if r.RolledBack || !r.Pending {
			t.Fatalf("start %d: result = %+v", i, r)
		}
		var m Marker
		_ = json.Unmarshal([]byte(read(t, p.Marker())), &m)
		if m.Attempts != i {
			t.Fatalf("start %d: attempts = %d", i, m.Attempts)
		}
	}

	rec := &recFS{}
	r := Startup(rec, p, "0.3.11", func() time.Time { return at(3, 40) }, discard())
	if !r.RolledBack {
		t.Fatalf("4th start should roll back: %+v", r)
	}
	if read(t, p.Exe) != "OLD" || read(t, p.Bad()) != "NEW" {
		t.Error("expected agent.exe=OLD, agent.exe.bad=NEW")
	}
	if fileExists(p.Old()) || fileExists(p.Marker()) {
		t.Error("agent.exe.old and marker should be gone after rollback")
	}
	ops := rec.Ops()
	want := []string{"rename agent.exe -> agent.exe.bad", "rename agent.exe.old -> agent.exe", "write last-rollback.json"}
	if strings.Join(ops, " | ") != strings.Join(want, " | ") {
		t.Errorf("rollback ops =\n  %v\nwant\n  %v", ops, want)
	}
	note := ReadRollbackNote(OSFS{}, p)
	if note == nil || note.From != "0.3.11" || note.To != "0.3.10" || note.Attempts != 4 {
		t.Errorf("rollback note = %+v", note)
	}
}

func TestStartup_NoOldBinaryCannotRollBack(t *testing.T) {
	p := layout(t)
	writeMarker(t, p, Marker{From: "0.3.10", To: "0.3.11", Attempts: 3})
	r := Startup(OSFS{}, p, "0.3.11", time.Now, discard())
	if r.RolledBack {
		t.Error("cannot roll back without agent.exe.old")
	}
	if fileExists(p.Marker()) {
		t.Error("marker should be dropped so the count stops")
	}
}

func TestStartup_StaleMarkerForOtherVersion(t *testing.T) {
	p := layout(t)
	writeMarker(t, p, Marker{From: "0.3.10", To: "0.3.11", Attempts: 3})
	r := Startup(OSFS{}, p, "0.3.10", time.Now, discard())
	if r.RolledBack || r.Pending {
		t.Errorf("result = %+v", r)
	}
	if fileExists(p.Marker()) {
		t.Error("stale marker should be deleted")
	}
}

func TestStartup_CorruptMarker(t *testing.T) {
	p := layout(t)
	mustWrite(t, p.Marker(), "{not json")
	r := Startup(OSFS{}, p, "0.3.11", time.Now, discard())
	if r.RolledBack || fileExists(p.Marker()) {
		t.Errorf("corrupt marker: result=%+v exists=%v", r, fileExists(p.Marker()))
	}
}

// ---------- finalize after healthy ----------

func TestRun_FinalizesAfterHealthy(t *testing.T) {
	p := layout(t)
	mustWrite(t, p.Old(), "OLD")
	mustWrite(t, p.Bad(), "BAD")
	mustWrite(t, p.Download(), "X")
	writeMarker(t, p, Marker{From: "0.3.10", To: "0.3.11", Attempts: 1})
	mustWrite(t, p.RollbackNote(), `{"from":"0.3.9"}`)

	ready := make(chan struct{})
	u := &Updater{
		Version:      "dev", // loop off; finalize still runs
		Enabled:      true,
		Paths:        p,
		Logger:       discard(),
		Ready:        ready,
		HealthyAfter: 20 * time.Millisecond,
		Exit:         func(int) { t.Fatal("must not exit") },
	}
	done := make(chan struct{})
	go func() { u.Run(context.Background()); close(done) }()

	time.Sleep(50 * time.Millisecond)
	if !fileExists(p.Old()) {
		t.Fatal("must not finalize before the server is ready")
	}
	close(ready)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after finalize with the loop off")
	}
	for _, f := range []string{p.Old(), p.Bad(), p.Marker(), p.Download()} {
		if fileExists(f) {
			t.Errorf("%s should be removed", filepath.Base(f))
		}
	}
	if !fileExists(p.RollbackNote()) {
		t.Error("rollback note must survive cleanup")
	}
}

func TestRun_CanceledBeforeHealthyKeepsRollbackCopy(t *testing.T) {
	p := layout(t)
	mustWrite(t, p.Old(), "OLD")
	writeMarker(t, p, Marker{From: "0.3.10", To: "0.3.11", Attempts: 1})
	ready := make(chan struct{})
	close(ready)
	u := &Updater{Version: "0.3.11", Enabled: false, Paths: p, Logger: discard(), Ready: ready, HealthyAfter: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { u.Run(ctx); close(done) }()
	cancel()
	<-done
	if !fileExists(p.Old()) || !fileExists(p.Marker()) {
		t.Error("stopping before 60 s healthy must keep agent.exe.old and the marker")
	}
}

func TestStatus_ReportsRollback(t *testing.T) {
	p := layout(t)
	mustWrite(t, p.RollbackNote(), fmt.Sprintf(`{"from":"0.3.11","to":"0.3.10","attempts":4,"reason":%q}`, "x"))
	u := &Updater{Version: "0.3.10", Enabled: true, Paths: p, Logger: discard()}
	s := u.Status()
	if !s.AutoUpdate || s.LastRollback == nil || s.LastRollback.From != "0.3.11" {
		t.Errorf("status = %+v", s)
	}
	u.Version = "dev"
	if u.Status().AutoUpdate {
		t.Error("dev builds never auto-update")
	}
}
