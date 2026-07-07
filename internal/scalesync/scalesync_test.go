package scalesync

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/cloud"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

// ── Test doubles ──────────────────────────────────────────────────────

type fakeSecrets struct {
	secrets *config.Secrets
}

func (f *fakeSecrets) Load() (*config.Secrets, error) {
	if f.secrets == nil {
		return nil, config.ErrNoSecrets
	}
	return f.secrets, nil
}
func (f *fakeSecrets) Save(s *config.Secrets) error { f.secrets = s; return nil }
func (f *fakeSecrets) Clear() error                 { f.secrets = nil; return nil }

func pairedSecrets() *fakeSecrets {
	return &fakeSecrets{secrets: &config.Secrets{
		TerminalID:    "trm_test",
		TerminalToken: "tok_test",
	}}
}

// pluServer serves a scale-plu-file envelope built from resp (mutable
// between ticks via the pointer) and counts requests.
type pluServer struct {
	ts    *httptest.Server
	calls atomic.Int64
	// respond builds the HTTP response for each request.
	respond func(w http.ResponseWriter)
}

func newPLUServer(t *testing.T, respond func(w http.ResponseWriter)) *pluServer {
	t.Helper()
	s := &pluServer{respond: respond}
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pos-agent/scale-plu-file" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Terminal-Token"); got != "tok_test" {
			t.Errorf("X-Terminal-Token = %q, want tok_test", got)
		}
		s.calls.Add(1)
		s.respond(w)
	}))
	t.Cleanup(s.ts.Close)
	return s
}

func okEnvelope(w http.ResponseWriter, data map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}

// pluData builds a well-formed v2 response data map for content. The
// sha256 is computed over the ENCODED bytes (BOM + UTF-16 LE), exactly
// like the route does via encodeLink69PluFile.
func pluData(content string, extra map[string]any) map[string]any {
	d := map[string]any{
		"format":      cloud.ScalePLUFileFormat,
		"encoding":    cloud.ScalePLUFileEncoding,
		"path_hint":   WindowsPLUFilePath,
		"content":     content,
		"sha256":      hashHex(encodeUTF16LEBOM(content)),
		"entry_count": 2,
		"generated":   []any{},
		"skipped":     []any{},
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// newLoop builds a Loop against the given server with a temp-dir dest
// and a log buffer for assertion.
func newLoop(t *testing.T, s *pluServer) (*Loop, string, *bytes.Buffer) {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "balance", "PLU.txt")
	var logBuf bytes.Buffer
	l := &Loop{
		Cloud:    cloud.New(s.ts.URL, "test"),
		Secrets:  pairedSecrets(),
		Logger:   slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		DestPath: dest,
	}
	return l, dest, &logBuf
}

// v2 content shape: header row + data rows, tab-separated, CRLF, every
// line ending with a tab before its CRLF (abridged columns — the
// worker never parses fields, only counts rows).
const headerOnlyContent = "ID\tName1\tPrice\t\r\n"
const sampleContent = headerOnlyContent +
	"7\tTomates fraiches\t250,50\t\r\n" +
	"204\tخبز الدار\t50\t\r\n"

// ── Mirror behavior ───────────────────────────────────────────────────

func TestTick_WritesFetchedContent(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(sampleContent, nil))
	})
	l, dest, _ := newLoop(t, s)

	if paired := l.tick(context.Background()); !paired {
		t.Fatal("tick reported unpaired")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	want := encodeUTF16LEBOM(sampleContent)
	if !bytes.Equal(got, want) {
		t.Errorf("file bytes = %x..., want encoded UTF-16LE+BOM (%d bytes, got %d)",
			got[:min(8, len(got))], len(want), len(got))
	}
	if l.lastSHA256 != hashHex(want) {
		t.Errorf("lastSHA256 = %q not updated to encoded-bytes hash", l.lastSHA256)
	}
}

func TestTick_UnchangedContent_SkipsRewrite(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(sampleContent, nil))
	})
	l, dest, logBuf := newLoop(t, s)

	l.tick(context.Background())
	fi1, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // ensure a rewrite would move mtime

	l.tick(context.Background())
	fi2, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Error("file was rewritten despite unchanged sha256")
	}
	if !strings.Contains(logBuf.String(), "content unchanged") {
		t.Error("expected 'content unchanged' debug log")
	}
}

func TestTick_UnchangedButFileDeleted_Rewrites(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(sampleContent, nil))
	})
	l, dest, _ := newLoop(t, s)

	l.tick(context.Background())
	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	l.tick(context.Background())
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("file not restored after deletion: %v", err)
	}
}

func TestSeedFromDisk_SkipsFirstWriteWhenAlreadyCurrent(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(sampleContent, nil))
	})
	l, dest, logBuf := newLoop(t, s)

	// Pre-existing file with identical ENCODED content (e.g. from
	// before an agent restart) — the seed hashes raw disk bytes, which
	// are the encoded bytes.
	if err := config.WriteAtomic(dest, encodeUTF16LEBOM(sampleContent), 0o644); err != nil {
		t.Fatal(err)
	}
	l.seedFromDisk()
	fi1, _ := os.Stat(dest)
	time.Sleep(20 * time.Millisecond)

	l.tick(context.Background())
	fi2, _ := os.Stat(dest)
	if !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Error("file rewritten on first tick despite matching seed hash")
	}
	if !strings.Contains(logBuf.String(), "content unchanged") {
		t.Error("expected 'content unchanged' debug log")
	}
}

// ── Safety guards ─────────────────────────────────────────────────────

func TestTick_HeaderOnlyContent_NeverClobbersFileWithDataRows(t *testing.T) {
	content := sampleContent
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(content, nil))
	})
	l, dest, logBuf := newLoop(t, s)

	l.tick(context.Background()) // writes the good file (2 data rows)
	content = headerOnlyContent  // cloud starts returning zero data rows
	l.tick(context.Background())

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, encodeUTF16LEBOM(sampleContent)) {
		t.Errorf("last good file was clobbered (%d bytes)", len(got))
	}
	if !strings.Contains(logBuf.String(), "refusing to overwrite PLU file containing data rows") {
		t.Error("expected header-only warning log")
	}
	// The guard must not poison the dedupe hash — a later good render
	// with the ORIGINAL content must still dedupe correctly.
	if l.lastSHA256 != hashHex(encodeUTF16LEBOM(sampleContent)) {
		t.Errorf("lastSHA256 = %q, want hash of last good encoded content", l.lastSHA256)
	}
}

func TestTick_TrulyEmptyContent_AlsoTripsGuard(t *testing.T) {
	// A v2 cloud always sends a header row, but a completely empty
	// string (0 lines) must be treated as "zero data rows" too.
	content := sampleContent
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(content, nil))
	})
	l, dest, logBuf := newLoop(t, s)

	l.tick(context.Background())
	content = ""
	l.tick(context.Background())

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, encodeUTF16LEBOM(sampleContent)) {
		t.Errorf("last good file was clobbered (%d bytes)", len(got))
	}
	if !strings.Contains(logBuf.String(), "refusing to overwrite PLU file containing data rows") {
		t.Error("expected guard warning log")
	}
}

func TestTick_HeaderOnlyContent_NoExistingFile_Writes(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(headerOnlyContent, nil))
	})
	l, dest, _ := newLoop(t, s)

	l.tick(context.Background())
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("header-only file not written when no prior file existed: %v", err)
	}
	if !bytes.Equal(got, encodeUTF16LEBOM(headerOnlyContent)) {
		t.Errorf("file bytes wrong for header-only write (%d bytes)", len(got))
	}
}

func TestTick_HeaderOnlyOverHeaderOnly_Writes(t *testing.T) {
	// An existing header-only file has no data rows to protect — a new
	// header-only render (e.g. changed columns) may replace it.
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(headerOnlyContent, nil))
	})
	l, dest, _ := newLoop(t, s)
	if err := config.WriteAtomic(dest, encodeUTF16LEBOM("Old\tHeader\t\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	l.tick(context.Background())
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, encodeUTF16LEBOM(headerOnlyContent)) {
		t.Errorf("header-only file not replaced by new header-only render")
	}
}

func TestTick_SHA256Mismatch_SkipsWrite(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(sampleContent, map[string]any{"sha256": "deadbeef"}))
	})
	l, dest, logBuf := newLoop(t, s)

	l.tick(context.Background())
	if _, err := os.Stat(dest); err == nil {
		t.Error("file written despite sha256 mismatch")
	}
	if !strings.Contains(logBuf.String(), "sha256 mismatch") {
		t.Error("expected sha256-mismatch warning log")
	}
}

func TestTick_UnknownFormat_SkipsWrite(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(sampleContent, map[string]any{"format": "link69_plu_v1"}))
	})
	l, dest, logBuf := newLoop(t, s)

	l.tick(context.Background())
	if _, err := os.Stat(dest); err == nil {
		t.Error("file written despite unknown format")
	}
	if !strings.Contains(logBuf.String(), "unknown PLU file format") {
		t.Error("expected unknown-format warning log")
	}
}

func TestTick_UnknownEncoding_SkipsWrite(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(sampleContent, map[string]any{"encoding": "utf-8"}))
	})
	l, dest, logBuf := newLoop(t, s)

	l.tick(context.Background())
	if _, err := os.Stat(dest); err == nil {
		t.Error("file written despite unknown encoding")
	}
	if !strings.Contains(logBuf.String(), "unknown PLU file encoding") {
		t.Error("expected unknown-encoding warning log")
	}
}

// ── Encoding helpers ──────────────────────────────────────────────────

func TestEncodeUTF16LEBOM_ExactBytes(t *testing.T) {
	// "A\r\n" → FF FE (BOM) 41 00 0D 00 0A 00.
	got := encodeUTF16LEBOM("A\r\n")
	want := []byte{0xFF, 0xFE, 0x41, 0x00, 0x0D, 0x00, 0x0A, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("encoded = % x, want % x", got, want)
	}
	// Arabic BMP char 'م' (U+0645) → 45 06 little-endian.
	got = encodeUTF16LEBOM("م")
	want = []byte{0xFF, 0xFE, 0x45, 0x06}
	if !bytes.Equal(got, want) {
		t.Errorf("encoded Arabic = % x, want % x", got, want)
	}
}

func TestDataRowCounting(t *testing.T) {
	if n := dataRowsText(""); n != 0 {
		t.Errorf("dataRowsText(empty) = %d, want 0", n)
	}
	if n := dataRowsText(headerOnlyContent); n != 0 {
		t.Errorf("dataRowsText(header-only) = %d, want 0", n)
	}
	if n := dataRowsText(sampleContent); n != 2 {
		t.Errorf("dataRowsText(sample) = %d, want 2", n)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "PLU.txt")
	if fileHasDataRows(path) {
		t.Error("fileHasDataRows(missing) = true, want false")
	}
	_ = os.WriteFile(path, encodeUTF16LEBOM(headerOnlyContent), 0o644)
	if fileHasDataRows(path) {
		t.Error("fileHasDataRows(header-only) = true, want false")
	}
	_ = os.WriteFile(path, encodeUTF16LEBOM(sampleContent), 0o644)
	if !fileHasDataRows(path) {
		t.Error("fileHasDataRows(sample) = false, want true")
	}
}

// ── Logging of cloud-side diagnostics ─────────────────────────────────

func TestTick_LogsSkippedEntries(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(sampleContent, map[string]any{
			"skipped": []map[string]any{
				{"product_id": "prod_1", "reason": "missing_price"},
				{"product_id": "prod_2", "reason": "missing_plu"},
				// v2 additions from partitionLink69Entries.
				{"product_id": "prod_3", "reason": "plu_out_of_range"},
				{"product_id": "prod_4", "reason": "unit_unverified"},
			},
		}))
	})
	l, _, logBuf := newLoop(t, s)

	l.tick(context.Background())
	logs := logBuf.String()
	for _, want := range []string{
		"prod_1", "missing_price",
		"prod_2", "missing_plu",
		"prod_3", "plu_out_of_range",
		"prod_4", "unit_unverified",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("skipped-entry log missing %q", want)
		}
	}
}

func TestTick_PathHintDrift_Warns(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(sampleContent, map[string]any{
			"path_hint": `D:\somewhere\else\PLU.txt`,
		}))
	})
	l, dest, logBuf := newLoop(t, s)

	l.tick(context.Background())
	if !strings.Contains(logBuf.String(), "path_hint differs") {
		t.Error("expected path_hint drift warning")
	}
	// Still writes to the agent's fixed destination.
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("file not written despite hint drift: %v", err)
	}
}

// ── Pairing / cloud-error behavior ────────────────────────────────────

func TestTick_Unpaired_NeverCallsCloud(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(sampleContent, nil))
	})
	l, _, _ := newLoop(t, s)
	l.Secrets = &fakeSecrets{} // unpaired

	if paired := l.tick(context.Background()); paired {
		t.Error("tick reported paired while unpaired")
	}
	if n := s.calls.Load(); n != 0 {
		t.Errorf("cloud called %d times while unpaired, want 0", n)
	}
}

func TestTick_NotFound_QuietSkip(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": map[string]any{"code": "NOT_FOUND", "message": "Pas de balance."},
		})
	})
	l, dest, logBuf := newLoop(t, s)

	l.tick(context.Background())
	if _, err := os.Stat(dest); err == nil {
		t.Error("file written despite NOT_FOUND")
	}
	logs := logBuf.String()
	if strings.Contains(logs, "level=WARN") || strings.Contains(logs, "level=ERROR") {
		t.Errorf("NOT_FOUND should be quiet (debug), got:\n%s", logs)
	}
}

func TestTick_Unauthenticated_DoesNotClearSecrets(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": map[string]any{"code": "UNAUTHENTICATED", "message": "Jeton invalide."},
		})
	})
	l, _, _ := newLoop(t, s)
	fs := l.Secrets.(*fakeSecrets)

	l.tick(context.Background())
	// The heartbeat loop owns 401 → clear-secrets; scalesync must not
	// race it by clearing too.
	if fs.secrets == nil {
		t.Error("scalesync cleared secrets on 401 — that's heartbeat's job")
	}
}

// ── Run loop lifecycle ────────────────────────────────────────────────

func TestRun_StopsOnContextCancel(t *testing.T) {
	s := newPLUServer(t, func(w http.ResponseWriter) {
		okEnvelope(w, pluData(sampleContent, nil))
	})
	l, dest, _ := newLoop(t, s)
	l.Interval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		l.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancel")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("Run never wrote the file: %v", err)
	}
	if s.calls.Load() < 2 {
		t.Errorf("Run polled %d times in 150ms at 10ms interval, want >= 2", s.calls.Load())
	}
}
