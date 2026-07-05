package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/printer"
	"github.com/karimkheirat/simsim-pos-agent/internal/scale"
)

// newServerWithScale builds a paired two-printer server with the given
// scale backend (nil = no scale configured).
func newServerWithScale(t *testing.T, sc scale.Scale) (*Server, *httptest.Server) {
	t.Helper()
	cfg := Config{
		ListenAddr:               "127.0.0.1:0",
		AllowedOrigins:           []string{"https://allowed.example"},
		Version:                  "test-1.0.0",
		Logger:                   discardLogger(),
		Secrets:                  pairedSecrets(),
		IdempotencyTTL:           time.Hour,
		IdempotencySweepInterval: time.Hour,
		Scale:                    sc,
	}
	var receipt printer.Printer = &fakePrinter{name: "SP-331", reachable: true}
	srv, err := NewTwo(cfg, receipt, nil)
	if err != nil {
		t.Fatalf("NewTwo: %v", err)
	}
	ts := httptest.NewServer(srv.handler)
	t.Cleanup(ts.Close)
	return srv, ts
}

// syncPLUBody marshals entries in the main repo's ScalePluEntry shape.
func syncPLUBody(t *testing.T, entries []scale.PLU) []byte {
	t.Helper()
	body, err := json.Marshal(syncPLURequest{Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func sampleEntries() []scale.PLU {
	return []scale.PLU{
		{PLU: "12345", Name: "Tomates fraiches", PriceCentimes: 25050, SoldBy: "weight", MeasureUnit: "kg"},
		{PLU: "204", Name: "خبز الدار", PriceCentimes: 5000, SoldBy: "piece", MeasureUnit: "kg"},
	}
}

// ── Happy path ────────────────────────────────────────────────────────

func TestSyncPLU_HappyPath(t *testing.T) {
	mock := &scale.Mock{Reachable: true, MockName: "scale:192.168.1.50:5002"}
	_, ts := newServerWithScale(t, mock)
	token := mintTestJWT(t, nil)

	resp := jwtPostBody(t, ts, "/scale/sync-plu", token, syncPLUBody(t, sampleEntries()))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ok, data, _, _ := decodeEnvelope(t, resp.Body)
	if !ok {
		t.Fatal("envelope ok = false")
	}
	if data["scale_name"] != "scale:192.168.1.50:5002" {
		t.Errorf("scale_name = %v", data["scale_name"])
	}
	if total, _ := data["total"].(float64); total != 2 {
		t.Errorf("total = %v, want 2", data["total"])
	}
	if sent, _ := data["sent"].(float64); sent != 2 {
		t.Errorf("sent = %v, want 2", data["sent"])
	}
	if failed, _ := data["failed"].(float64); failed != 0 {
		t.Errorf("failed = %v, want 0", data["failed"])
	}
	results, _ := data["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}

	// The mock must have received the entries verbatim (JSON round-trip
	// of the ScalePluEntry field names).
	calls := mock.Calls()
	if len(calls) != 1 || len(calls[0]) != 2 {
		t.Fatalf("mock calls = %v", calls)
	}
	if calls[0][0].PLU != "12345" || calls[0][0].PriceCentimes != 25050 ||
		calls[0][0].SoldBy != "weight" || calls[0][0].MeasureUnit != "kg" {
		t.Errorf("entry[0] = %+v, want ScalePluEntry fields decoded", calls[0][0])
	}
	if calls[0][1].Name != "خبز الدار" {
		t.Errorf("entry[1].Name = %q, want Arabic name intact", calls[0][1].Name)
	}
}

func TestSyncPLU_PerPLUFailure_Is200WithFailedCount(t *testing.T) {
	mock := &scale.Mock{
		Reachable: true,
		FailPLUs:  map[string]string{"204": "scale error code 0007"},
	}
	_, ts := newServerWithScale(t, mock)
	token := mintTestJWT(t, nil)

	resp := jwtPostBody(t, ts, "/scale/sync-plu", token, syncPLUBody(t, sampleEntries()))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (per-PLU failures are not a session failure)", resp.StatusCode)
	}
	_, data, _, _ := decodeEnvelope(t, resp.Body)
	if sent, _ := data["sent"].(float64); sent != 1 {
		t.Errorf("sent = %v, want 1", data["sent"])
	}
	if failed, _ := data["failed"].(float64); failed != 1 {
		t.Errorf("failed = %v, want 1", data["failed"])
	}
	results, _ := data["results"].([]any)
	second, _ := results[1].(map[string]any)
	if second["ok"] != false || second["error"] != "scale error code 0007" {
		t.Errorf("results[1] = %v", second)
	}
}

// ── Error paths ───────────────────────────────────────────────────────

func TestSyncPLU_NoScaleConfigured_503(t *testing.T) {
	_, ts := newServerWithScale(t, nil)
	token := mintTestJWT(t, nil)

	resp := jwtPostBody(t, ts, "/scale/sync-plu", token, syncPLUBody(t, sampleEntries()))
	defer resp.Body.Close()
	assertErrorEnvelope(t, resp, http.StatusServiceUnavailable, CodeNoScaleConfigured)
}

func TestSyncPLU_ScaleOffline_503(t *testing.T) {
	_, ts := newServerWithScale(t, &scale.Mock{Reachable: false})
	token := mintTestJWT(t, nil)

	resp := jwtPostBody(t, ts, "/scale/sync-plu", token, syncPLUBody(t, sampleEntries()))
	defer resp.Body.Close()
	assertErrorEnvelope(t, resp, http.StatusServiceUnavailable, CodeScaleOffline)
}

func TestSyncPLU_SessionFailure_502(t *testing.T) {
	mock := &scale.Mock{Reachable: true, Err: errors.New("scale: dial 192.168.1.50:5002: connection refused")}
	_, ts := newServerWithScale(t, mock)
	token := mintTestJWT(t, nil)

	resp := jwtPostBody(t, ts, "/scale/sync-plu", token, syncPLUBody(t, sampleEntries()))
	defer resp.Body.Close()
	assertErrorEnvelope(t, resp, http.StatusBadGateway, CodeScaleSyncFailed)
}

func TestSyncPLU_BadPayloads_400(t *testing.T) {
	_, ts := newServerWithScale(t, &scale.Mock{Reachable: true})
	token := mintTestJWT(t, nil)

	cases := []struct {
		name string
		body []byte
	}{
		{"invalid json", []byte(`{"entries": [`)},
		{"no entries key", []byte(`{}`)},
		{"empty entries", []byte(`{"entries": []}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := jwtPostBody(t, ts, "/scale/sync-plu", token, tc.body)
			defer resp.Body.Close()
			assertErrorEnvelope(t, resp, http.StatusBadRequest, CodeInvalidPluPayload)
		})
	}
}

func TestSyncPLU_OverCapacity_400(t *testing.T) {
	_, ts := newServerWithScale(t, &scale.Mock{Reachable: true})
	token := mintTestJWT(t, nil)

	entries := make([]scale.PLU, maxSyncPLUEntries+1)
	for i := range entries {
		entries[i] = scale.PLU{PLU: "1", Name: "x", PriceCentimes: 1, SoldBy: "weight", MeasureUnit: "kg"}
	}
	resp := jwtPostBody(t, ts, "/scale/sync-plu", token, syncPLUBody(t, entries))
	defer resp.Body.Close()
	assertErrorEnvelope(t, resp, http.StatusBadRequest, CodeInvalidPluPayload)
}

func TestSyncPLU_RequiresAuth(t *testing.T) {
	_, ts := newServerWithScale(t, &scale.Mock{Reachable: true})

	// No Authorization header at all.
	resp, err := http.Post(ts.URL+"/scale/sync-plu", "application/json",
		nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d, want 401", resp.StatusCode)
	}
}

// assertErrorEnvelope checks status + {ok:false, error.code}.
func assertErrorEnvelope(t *testing.T, resp *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Errorf("status = %d, want %d", resp.StatusCode, wantStatus)
	}
	ok, _, code, _ := decodeEnvelope(t, resp.Body)
	if ok {
		t.Error("envelope ok = true, want false")
	}
	if code != wantCode {
		t.Errorf("error code = %q, want %q", code, wantCode)
	}
}
