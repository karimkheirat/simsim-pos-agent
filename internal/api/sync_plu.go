package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/scale"
)

// maxSyncPLUEntries caps one sync payload at the LS2 series' documented
// PLU capacity (12,000). Anything larger is a caller bug, not a bigger
// scale.
const maxSyncPLUEntries = 12000

// syncPLURequest is the body of POST /scale/sync-plu. Entries carry the
// main repo's ScalePluEntry shape verbatim (plu, name, priceCentimes,
// soldBy, measureUnit) — the JSON tags live on scale.PLU.
type syncPLURequest struct {
	Entries []scale.PLU `json:"entries"`
}

// syncPLUResponseData is the shape returned inside the {ok,data}
// envelope. Failed counts per-PLU rejections AND encode failures; a
// 200 with failed > 0 means "the session ran, these PLUs didn't take".
type syncPLUResponseData struct {
	ScaleName  string         `json:"scale_name"`
	Total      int            `json:"total"`
	Sent       int            `json:"sent"`
	Failed     int            `json:"failed"`
	Results    []scale.Result `json:"results"`
	DurationMs int64          `json:"duration_ms"`
}

// handleSyncPLU downloads a PLU payload to the configured label scale.
// JWT-authed via requireAuth. Scale sync day 2 (transport +
// protocol + loopback endpoint; the cloud-side sync job calls this in
// a later PR).
//
// Pipeline (mirrors handlePrintLabel's shape):
//  1. Parse + validate body
//  2. Resolve scale (503 NO_SCALE_CONFIGURED when nil)
//  3. Reachability gate (503 SCALE_OFFLINE)
//  4. SendPLUs — session failure → 502 SCALE_SYNC_FAILED;
//     per-PLU failures → 200 with per-PLU results
//  5. Structured log + response
//
// No idempotency store: a PLU download is a full overwrite of the
// affected PLUs, so replaying the same payload is naturally idempotent.
func (s *Server) handleSyncPLU(w http.ResponseWriter, r *http.Request) {
	var req syncPLURequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidPluPayload, "invalid JSON: "+err.Error())
		return
	}
	if len(req.Entries) == 0 {
		writeError(w, http.StatusBadRequest, CodeInvalidPluPayload, "entries required")
		return
	}
	if len(req.Entries) > maxSyncPLUEntries {
		writeError(w, http.StatusBadRequest, CodeInvalidPluPayload,
			"too many entries (max 12000, the LS2 PLU capacity)")
		return
	}

	if s.scale == nil {
		writeError(w, http.StatusServiceUnavailable, CodeNoScaleConfigured,
			"no scale configured (set scale_ip + scale_port)")
		return
	}
	if !s.scale.IsReachable() {
		writeError(w, http.StatusServiceUnavailable, CodeScaleOffline, "scale not reachable")
		return
	}

	start := time.Now()
	results, err := s.scale.SendPLUs(r.Context(), req.Entries)
	duration := time.Since(start)
	if err != nil {
		s.logger.Error("scale sync session failed",
			"event", "scale_sync",
			"scale_name", s.scale.Name(),
			"total", len(req.Entries),
			"duration_ms", duration.Milliseconds(),
			"err", err.Error(),
			"success", false,
		)
		writeError(w, http.StatusBadGateway, CodeScaleSyncFailed, err.Error())
		return
	}

	sent := 0
	for _, res := range results {
		if res.OK {
			sent++
		}
	}
	writeOK(w, syncPLUResponseData{
		ScaleName:  s.scale.Name(),
		Total:      len(req.Entries),
		Sent:       sent,
		Failed:     len(req.Entries) - sent,
		Results:    results,
		DurationMs: duration.Milliseconds(),
	})

	s.logger.Info("scale sync complete",
		"event", "scale_sync",
		"scale_name", s.scale.Name(),
		"total", len(req.Entries),
		"sent", sent,
		"failed", len(req.Entries)-sent,
		"duration_ms", duration.Milliseconds(),
		"success", true,
	)
}
