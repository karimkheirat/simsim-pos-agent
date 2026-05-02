package pairing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/cloud"
	"github.com/karimkheirat/simsim-pos-agent/internal/config"
)

// ----- mock SecretStore -----

type mockSecrets struct {
	mu          sync.Mutex
	stored      *config.Secrets
	loadErr     error
	saveErr     error
	clearErr    error
	saveCalls   int32
	clearCalls  int32
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
	if m.saveErr != nil {
		return m.saveErr
	}
	atomic.AddInt32(&m.saveCalls, 1)
	cp := *s
	m.stored = &cp
	return nil
}

func (m *mockSecrets) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clearErr != nil {
		return m.clearErr
	}
	atomic.AddInt32(&m.clearCalls, 1)
	m.stored = nil
	return nil
}

func (m *mockSecrets) SaveCalls() int  { return int(atomic.LoadInt32(&m.saveCalls)) }
func (m *mockSecrets) ClearCalls() int { return int(atomic.LoadInt32(&m.clearCalls)) }

// Compile-time assertion that mockSecrets satisfies SecretStore.
var _ config.SecretStore = (*mockSecrets)(nil)

// ----- helpers -----

// canned response shapes
type pairResp struct {
	TerminalID    string `json:"terminal_id"`
	TerminalToken string `json:"terminal_token"`
	StoreID       string `json:"store_id"`
	StoreName     string `json:"store_name"`
	TerminalLabel string `json:"terminal_label"`
}

func writeOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":    false,
		"error": map[string]any{"code": code, "message": msg},
	})
}

// newService spins up an httptest server with the given handler and
// returns a Service wired to it plus the mock secret store.
func newService(t *testing.T, handler http.HandlerFunc) (*Service, *mockSecrets, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	mock := &mockSecrets{}
	svc := &Service{
		Cloud:     cloud.New(server.URL, "test-1.2.3"),
		Secrets:   mock,
		MachineID: "machine-test",
		Version:   "test-1.2.3",
	}
	return svc, mock, server.Close
}

// ----- Pair -----

func TestPair_Happy(t *testing.T) {
	var pairCalls atomic.Int32
	svc, mock, cleanup := newService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pos-agent/pair" {
			pairCalls.Add(1)
			writeOK(w, pairResp{
				TerminalID:    "trm_x",
				TerminalToken: "tok_y",
				StoreID:       "store_z",
				StoreName:     "Hamoud Boualem",
				TerminalLabel: "Caisse 1",
			})
			return
		}
		w.WriteHeader(404)
	})
	defer cleanup()

	resp, err := svc.Pair(context.Background(), "428193")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if resp.TerminalID != "trm_x" || resp.TerminalToken != "tok_y" {
		t.Errorf("resp = %+v", resp)
	}
	if pairCalls.Load() != 1 {
		t.Errorf("/pair called %d times, want 1", pairCalls.Load())
	}

	// Secrets persisted with the cloud-supplied identifiers.
	saved, err := mock.Load()
	if err != nil {
		t.Fatalf("Load after Pair: %v", err)
	}
	if saved.TerminalID != "trm_x" || saved.TerminalToken != "tok_y" || saved.StoreID != "store_z" {
		t.Errorf("saved = %+v", saved)
	}
	if saved.PairedAt.IsZero() {
		t.Error("PairedAt is zero; want set to wall-clock time")
	}
	if mock.SaveCalls() != 1 {
		t.Errorf("Save called %d times, want 1", mock.SaveCalls())
	}
}

func TestPair_CloudError_NoSecretsSaved(t *testing.T) {
	svc, mock, cleanup := newService(t, func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusUnauthorized, "INVALID_CODE", "Code invalide ou expiré")
	})
	defer cleanup()

	_, err := svc.Pair(context.Background(), "000000")
	if !errors.Is(err, cloud.ErrInvalidCode) {
		t.Fatalf("err = %v, want errors.Is(err, ErrInvalidCode)", err)
	}
	if mock.SaveCalls() != 0 {
		t.Errorf("secrets saved despite cloud error (Save called %d times)", mock.SaveCalls())
	}
	if _, err := mock.Load(); !errors.Is(err, config.ErrNoSecrets) {
		t.Errorf("Load = %v, want ErrNoSecrets (no secrets should have been saved)", err)
	}
}

func TestPair_AlreadyPaired_OverwritesSecrets(t *testing.T) {
	// Pre-populate with old secrets.
	mock := &mockSecrets{
		stored: &config.Secrets{
			TerminalID:    "trm_old",
			TerminalToken: "tok_old",
			StoreID:       "store_old",
			PairedAt:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, pairResp{
			TerminalID:    "trm_new",
			TerminalToken: "tok_new",
			StoreID:       "store_new",
			StoreName:     "Nouveau",
			TerminalLabel: "Caisse 2",
		})
	}))
	defer server.Close()

	svc := &Service{
		Cloud: cloud.New(server.URL, "v"), Secrets: mock,
		MachineID: "m", Version: "v",
	}
	resp, err := svc.Pair(context.Background(), "111111")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if resp.TerminalID != "trm_new" {
		t.Errorf("resp.TerminalID = %q, want trm_new", resp.TerminalID)
	}

	saved, _ := mock.Load()
	if saved.TerminalID != "trm_new" || saved.TerminalToken != "tok_new" || saved.StoreID != "store_new" {
		t.Errorf("saved = %+v; expected new pair to overwrite old", saved)
	}
	if !saved.PairedAt.After(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("PairedAt = %v; expected newer than the old timestamp", saved.PairedAt)
	}
}

// ----- Unpair -----

func TestUnpair_Happy(t *testing.T) {
	var unpairCalls atomic.Int32
	var seenToken string
	svc, mock, cleanup := newService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pos-agent/unpair" {
			unpairCalls.Add(1)
			seenToken = r.Header.Get("X-Terminal-Token")
			writeOK(w, map[string]any{})
			return
		}
		w.WriteHeader(404)
	})
	defer cleanup()

	mock.stored = &config.Secrets{
		TerminalID:    "trm_x",
		TerminalToken: "tok_y",
		StoreID:       "store_z",
		PairedAt:      time.Now(),
	}

	if err := svc.Unpair(context.Background()); err != nil {
		t.Fatalf("Unpair: %v", err)
	}
	if unpairCalls.Load() != 1 {
		t.Errorf("/unpair called %d times, want 1", unpairCalls.Load())
	}
	if seenToken != "tok_y" {
		t.Errorf("X-Terminal-Token = %q, want tok_y", seenToken)
	}
	if mock.ClearCalls() != 1 {
		t.Errorf("Secrets.Clear called %d times, want 1", mock.ClearCalls())
	}
	if _, err := mock.Load(); !errors.Is(err, config.ErrNoSecrets) {
		t.Errorf("after Unpair, Load = %v; want ErrNoSecrets", err)
	}
}

func TestUnpair_AlreadyRevoked401_ClearsSecrets(t *testing.T) {
	svc, mock, cleanup := newService(t, func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Token invalide ou révoqué")
	})
	defer cleanup()

	mock.stored = &config.Secrets{TerminalID: "trm_x", TerminalToken: "tok_y"}

	if err := svc.Unpair(context.Background()); err != nil {
		t.Fatalf("Unpair: %v; want nil (already-revoked is benign)", err)
	}
	if mock.ClearCalls() != 1 {
		t.Errorf("Clear called %d times, want 1 (secrets must be cleared on 401)", mock.ClearCalls())
	}
}

func TestUnpair_NetworkError_DoesNotClear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close() // dial will fail → ErrNetwork
	mock := &mockSecrets{
		stored: &config.Secrets{TerminalID: "trm_x", TerminalToken: "tok_y"},
	}
	svc := &Service{
		Cloud: cloud.New(server.URL, "v"), Secrets: mock,
		MachineID: "m", Version: "v",
	}

	err := svc.Unpair(context.Background())
	if !errors.Is(err, cloud.ErrNetwork) {
		t.Fatalf("err = %v, want errors.Is(err, ErrNetwork)", err)
	}
	if mock.ClearCalls() != 0 {
		t.Errorf("Clear called %d times despite network error; want 0 (CLI handles force-clear)", mock.ClearCalls())
	}
	if _, err := mock.Load(); err != nil {
		t.Errorf("Load after Unpair network error = %v; secrets should remain intact", err)
	}
}

func TestUnpair_NoSecrets(t *testing.T) {
	svc, _, cleanup := newService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("cloud should not have been called when no secrets present")
	})
	defer cleanup()

	err := svc.Unpair(context.Background())
	if !errors.Is(err, config.ErrNoSecrets) {
		t.Errorf("err = %v, want ErrNoSecrets", err)
	}
}

func TestUnpair_OtherCloudError_DoesNotClear(t *testing.T) {
	svc, mock, cleanup := newService(t, func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "Erreur serveur")
	})
	defer cleanup()

	mock.stored = &config.Secrets{TerminalID: "trm_x", TerminalToken: "tok_y"}

	err := svc.Unpair(context.Background())
	if !errors.Is(err, cloud.ErrInternal) {
		t.Fatalf("err = %v, want ErrInternal", err)
	}
	if mock.ClearCalls() != 0 {
		t.Errorf("Clear called %d times on 500; want 0 (only 401 / nil clear)", mock.ClearCalls())
	}
}

// ----- Status -----

func TestStatus_NoSecrets(t *testing.T) {
	svc := &Service{Secrets: &mockSecrets{}}
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Paired {
		t.Error("Paired = true, want false")
	}
	if st.TerminalID != "" || st.StoreID != "" || !st.PairedAt.IsZero() {
		t.Errorf("non-zero fields on unpaired Status: %+v", st)
	}
}

func TestStatus_Paired(t *testing.T) {
	pairedAt := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
	svc := &Service{
		Secrets: &mockSecrets{
			stored: &config.Secrets{
				TerminalID:    "trm_x",
				TerminalToken: "tok_y",
				StoreID:       "store_z",
				PairedAt:      pairedAt,
			},
		},
	}
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Paired {
		t.Error("Paired = false, want true")
	}
	if st.TerminalID != "trm_x" {
		t.Errorf("TerminalID = %q", st.TerminalID)
	}
	if st.StoreID != "store_z" {
		t.Errorf("StoreID = %q", st.StoreID)
	}
	if !st.PairedAt.Equal(pairedAt) {
		t.Errorf("PairedAt = %v, want %v", st.PairedAt, pairedAt)
	}
}

func TestStatus_LoadError_Propagated(t *testing.T) {
	svc := &Service{Secrets: &mockSecrets{loadErr: errors.New("disk on fire")}}
	_, err := svc.Status(context.Background())
	if err == nil || err.Error() != "disk on fire" {
		t.Errorf("err = %v, want propagation of underlying load error", err)
	}
}
