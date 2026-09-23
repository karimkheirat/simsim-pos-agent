package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLatestRelease_WithAgentAsset(t *testing.T) {
	var seenPath, seenMethod, seenToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath, seenMethod, seenToken = r.URL.Path, r.Method, r.Header.Get("X-Terminal-Token")
		writeOKEnvelope(w, http.StatusOK, map[string]any{
			"version":            "0.3.11",
			"download_url":       "https://example.test/setup.exe",
			"published_at":       "2026-09-23T00:00:00Z",
			"agent_download_url": "https://example.test/agent.exe",
			"agent_sha256":       "ab12",
		})
	}))
	defer srv.Close()

	rel, err := New(srv.URL, "0.3.10").LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if seenPath != "/api/pos-agent/release/latest" || seenMethod != http.MethodGet {
		t.Errorf("request = %s %s", seenMethod, seenPath)
	}
	if seenToken != "" {
		t.Errorf("release/latest must be unauthenticated; got token %q", seenToken)
	}
	if rel.Version != "0.3.11" || rel.AgentDownloadURL == nil || *rel.AgentDownloadURL != "https://example.test/agent.exe" ||
		rel.AgentSHA256 == nil || *rel.AgentSHA256 != "ab12" {
		t.Errorf("decoded = %+v", rel)
	}
}

func TestLatestRelease_NullAgentFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeOKEnvelope(w, http.StatusOK, map[string]any{
			"version": "0.3.11", "download_url": "https://x/setup.exe",
			"agent_download_url": nil, "agent_sha256": nil,
		})
	}))
	defer srv.Close()
	rel, err := New(srv.URL, "dev").LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if rel.AgentDownloadURL != nil || rel.AgentSHA256 != nil {
		t.Errorf("null fields should decode to nil pointers: %+v", rel)
	}
}

func TestLatestRelease_ErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErrorEnvelope(w, http.StatusNotFound, "NOT_FOUND", "Aucune version publiée.")
	}))
	defer srv.Close()
	_, err := New(srv.URL, "dev").LatestRelease(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
