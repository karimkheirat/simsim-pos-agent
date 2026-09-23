package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// A drawer kick and a print both stamp the hardware-activity clock the
// self-updater reads; /health (not hardware) does not.
func TestLastHardwareActivity_StampedByHardwareRoutes(t *testing.T) {
	fp := &fakePrinter{name: "SP-331", reachable: true}
	srv, ts := newTestServer(t, fp)

	if last, busy := srv.LastHardwareActivity(); !last.IsZero() || busy {
		t.Fatalf("fresh server: last=%v busy=%v, want zero/false", last, busy)
	}

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if last, _ := srv.LastHardwareActivity(); !last.IsZero() {
		t.Fatalf("/health must not count as hardware activity (last=%v)", last)
	}

	before := time.Now()
	r := authPost(t, ts, "/drawer/open", nil)
	r.Body.Close()
	last, busy := srv.LastHardwareActivity()
	if last.Before(before) {
		t.Errorf("after /drawer/open last=%v, want >= %v", last, before)
	}
	if busy {
		t.Errorf("no request in flight, busy should be false")
	}

	before = time.Now()
	r = authPost(t, ts, "/print", bytes.NewReader(validPrintBody("job-activity-1", false)))
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if last, _ := srv.LastHardwareActivity(); last.Before(before) {
		t.Errorf("after /print last=%v, want >= %v", last, before)
	}
}

func TestReady_ClosedOnceListening(t *testing.T) {
	srv, err := New(Config{ListenAddr: "127.0.0.1:0", Version: "test", Logger: discardLogger()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-srv.Ready():
		t.Fatal("Ready closed before Run")
	default:
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	select {
	case <-srv.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("Ready not closed after Run bound its listener")
	}
	cancel()
	<-done
}

func TestStatus_IncludesUpdateWhenWired(t *testing.T) {
	cfg := Config{
		ListenAddr: "127.0.0.1:0", Version: "test", Logger: discardLogger(),
		Secrets:        pairedSecrets(),
		IdempotencyTTL: time.Hour, IdempotencySweepInterval: time.Hour,
		UpdateStatus: func() any { return map[string]any{"auto_update": true, "pending_version": "0.3.11"} },
	}
	_, ts := newTestServerWith(t, &fakePrinter{name: "SP-331", reachable: true}, cfg)
	resp := authGet(t, ts, "/status")
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	upd, ok := body["update"].(map[string]any)
	if !ok || upd["pending_version"] != "0.3.11" {
		t.Fatalf("status.update = %v", body["update"])
	}
}
