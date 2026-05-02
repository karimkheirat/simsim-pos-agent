package heartbeat

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/cloud"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

// ----- mocks -----

type mockSecrets struct {
	mu         sync.Mutex
	stored     *config.Secrets
	loadErr    error
	clearCalls int32
}

func (m *mockSecrets) Load() (*config.Secrets, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	if m.stored == nil {
		return nil, config.ErrNoSecrets
	}
	cp := *m.stored
	return &cp, nil
}

func (m *mockSecrets) Save(s *config.Secrets) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *s
	m.stored = &cp
	return nil
}

func (m *mockSecrets) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	atomic.AddInt32(&m.clearCalls, 1)
	m.stored = nil
	return nil
}

func (m *mockSecrets) ClearCalls() int { return int(atomic.LoadInt32(&m.clearCalls)) }

func (m *mockSecrets) SetStored(s *config.Secrets) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s == nil {
		m.stored = nil
		return
	}
	cp := *s
	m.stored = &cp
}

var _ config.SecretStore = (*mockSecrets)(nil)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func pairedSecrets() *config.Secrets {
	return &config.Secrets{
		TerminalID:    "trm_test",
		TerminalToken: "tok_test",
		StoreID:       "store_test",
		PairedAt:      time.Now(),
	}
}

// runLoop spins Run in a goroutine, returns a cancel func + done chan.
func runLoop(loop *Loop) (cancel context.CancelFunc, done chan struct{}) {
	ctx, c := context.WithCancel(context.Background())
	d := make(chan struct{})
	go func() {
		loop.Run(ctx)
		close(d)
	}()
	return c, d
}

// ----- tests -----

// TestLoop_FiresOnInterval — paired loop with fast tick fires multiple
// heartbeats over a measured window.
func TestLoop_FiresOnInterval(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{}})
	}))
	defer server.Close()

	mock := &mockSecrets{stored: pairedSecrets()}
	loop := &Loop{
		Cloud:    cloud.New(server.URL, "test"),
		Secrets:  mock,
		Logger:   discardLogger(),
		Version:  "test",
		Interval: 50 * time.Millisecond,
	}

	cancel, done := runLoop(loop)
	time.Sleep(280 * time.Millisecond) // expect ~5-6 ticks
	cancel()
	<-done

	if h := hits.Load(); h < 4 {
		t.Errorf("hits = %d; expected at least 4 over 280ms with 50ms interval", h)
	}
}

// TestLoop_401ClearsSecrets — cloud returns 401, loop calls Clear and
// subsequent ticks see unpaired state.
func TestLoop_401ClearsSecrets(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": map[string]any{"code": "UNAUTHENTICATED", "message": "Token révoqué"},
		})
	}))
	defer server.Close()

	mock := &mockSecrets{stored: pairedSecrets()}
	loop := &Loop{
		Cloud:                   cloud.New(server.URL, "test"),
		Secrets:                 mock,
		Logger:                  discardLogger(),
		Version:                 "test",
		Interval:                30 * time.Millisecond,
		UnpairedRecheckInterval: 30 * time.Millisecond,
	}

	cancel, done := runLoop(loop)
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	if mock.ClearCalls() < 1 {
		t.Errorf("Secrets.Clear called %d times, want >= 1", mock.ClearCalls())
	}
	// First tick was paired (cloud got 401), subsequent ticks unpaired
	// (no more cloud calls). Total hits should equal the count of
	// paired ticks before Clear took effect, which is 1.
	if h := hits.Load(); h != 1 {
		t.Errorf("/heartbeat hit %d times after 401, want 1 (subsequent ticks unpaired)", h)
	}
}

// TestLoop_NetworkError_SecretsKeptRetried — loop survives network
// errors and keeps trying. Secrets are NOT cleared.
func TestLoop_NetworkError_SecretsKeptRetried(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close() // dial fails → ErrNetwork

	mock := &mockSecrets{stored: pairedSecrets()}
	loop := &Loop{
		Cloud:    cloud.New(server.URL, "test"),
		Secrets:  mock,
		Logger:   discardLogger(),
		Version:  "test",
		Interval: 30 * time.Millisecond,
	}

	cancel, done := runLoop(loop)
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	if mock.ClearCalls() != 0 {
		t.Errorf("Secrets.Clear called %d times despite network error; want 0", mock.ClearCalls())
	}
	if got, _ := mock.Load(); got == nil {
		t.Error("secrets cleared after network error; want kept")
	}
}

// TestLoop_OtherCloudError_NoCrash — internal/rate-limited responses
// don't crash and don't clear secrets.
func TestLoop_OtherCloudError_NoCrash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": map[string]any{"code": "RATE_LIMITED", "message": "Trop de tentatives"},
		})
	}))
	defer server.Close()

	mock := &mockSecrets{stored: pairedSecrets()}
	loop := &Loop{
		Cloud:    cloud.New(server.URL, "test"),
		Secrets:  mock,
		Logger:   discardLogger(),
		Version:  "test",
		Interval: 30 * time.Millisecond,
	}

	cancel, done := runLoop(loop)
	time.Sleep(120 * time.Millisecond)
	cancel()
	<-done

	if mock.ClearCalls() != 0 {
		t.Errorf("Secrets.Clear called %d times on rate-limited response; want 0", mock.ClearCalls())
	}
}

// TestLoop_CtxCancelStopsWithinOneTick — loop exits promptly when ctx
// is canceled.
func TestLoop_CtxCancelStopsWithinOneTick(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{}})
	}))
	defer server.Close()

	mock := &mockSecrets{stored: pairedSecrets()}
	loop := &Loop{
		Cloud:    cloud.New(server.URL, "test"),
		Secrets:  mock,
		Logger:   discardLogger(),
		Version:  "test",
		Interval: 200 * time.Millisecond,
	}

	cancel, done := runLoop(loop)
	time.Sleep(20 * time.Millisecond) // ensure first tick fired
	cancel()

	select {
	case <-done:
		// stopped promptly
	case <-time.After(500 * time.Millisecond):
		t.Fatal("loop did not exit within 500ms of cancel (Interval was 200ms)")
	}
}

// TestLoop_UnpairedThenPaired — start unpaired, no cloud calls; add
// secrets mid-loop, loop picks them up and starts heartbeating.
func TestLoop_UnpairedThenPaired(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{}})
	}))
	defer server.Close()

	mock := &mockSecrets{} // unpaired
	loop := &Loop{
		Cloud:                   cloud.New(server.URL, "test"),
		Secrets:                 mock,
		Logger:                  discardLogger(),
		Version:                 "test",
		Interval:                30 * time.Millisecond,
		UnpairedRecheckInterval: 30 * time.Millisecond,
	}

	cancel, done := runLoop(loop)

	// Phase 1: unpaired for 100ms, no cloud calls.
	time.Sleep(100 * time.Millisecond)
	if h := hits.Load(); h != 0 {
		t.Fatalf("phase 1: cloud hit %d times while unpaired; want 0", h)
	}

	// Phase 2: pair. Loop should pick up next tick.
	mock.SetStored(pairedSecrets())
	time.Sleep(150 * time.Millisecond)
	if h := hits.Load(); h < 1 {
		t.Errorf("phase 2: cloud hit %d times after pairing; want >= 1", h)
	}

	cancel()
	<-done
}
