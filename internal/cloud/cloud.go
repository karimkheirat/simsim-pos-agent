// Package cloud is the agent's HTTP client for the Simsim cloud's
// /api/pos-agent/* endpoints (M2). The wire contract is in
// POS_AGENT_M2_CONTRACT.md at the repo root and is treated as immutable
// during the build. Pure Go; no I/O at construction time.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultTimeout = 10 * time.Second

// Endpoint paths, kept centralized so contract drift is one-line obvious.
const (
	pathPair            = "/api/pos-agent/pair"
	pathHeartbeat       = "/api/pos-agent/heartbeat"
	pathUnpair          = "/api/pos-agent/unpair"
	pathPrintVerified   = "/api/pos-agent/print-verified"
)

// Client speaks to the cloud's /api/pos-agent/* endpoints. The zero value
// is not usable — call New.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	UserAgent  string
}

// New returns a Client targeting baseURL with a default 10s HTTP timeout
// and a User-Agent of "simsim-pos-agent/<version>". Override HTTPClient
// post-construction for tests that need a custom transport.
func New(baseURL, version string) *Client {
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		UserAgent:  "simsim-pos-agent/" + version,
	}
}

// Pair exchanges a 6-digit pairing code for a long-lived terminal token.
// Contract §4.2. No X-Terminal-Token header — the code is the credential.
func (c *Client) Pair(ctx context.Context, code, agentVersion, machineID string) (*PairResponse, error) {
	body := pairRequest{
		Code:         code,
		AgentVersion: agentVersion,
		MachineID:    machineID,
	}
	var data PairResponse
	if err := c.do(ctx, http.MethodPost, pathPair, "", body, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// Heartbeat updates the cloud's last-seen timestamp and agent state.
// Contract §4.3. Authenticated with X-Terminal-Token.
func (c *Client) Heartbeat(ctx context.Context, token string, hb HeartbeatRequest) error {
	return c.do(ctx, http.MethodPost, pathHeartbeat, token, hb, nil)
}

// Unpair revokes the terminal's token cloud-side. Contract §4.4.
// Authenticated with X-Terminal-Token; the body is empty.
func (c *Client) Unpair(ctx context.Context, token string) error {
	return c.do(ctx, http.MethodPost, pathUnpair, token, struct{}{}, nil)
}

// ReportPrintVerified records the operator's test-print confirmation
// on the cloud. Authenticated with X-Terminal-Token.
//
//   - body.Verified=true  → cloud stamps last_print_verified_at = NOW().
//     A subsequent successful re-verification re-stamps (no set-once
//     constraint, unlike firstAgentPrintAt).
//   - body.Verified=false → cloud CLEARS last_print_verified_at to NULL.
//     A confirmed bad test downgrades a previously-verified terminal
//     to "not verified" — see the cloud route's docstring for rationale.
//
// Returns nil on cloud 200; a typed cloud.Err* sentinel on auth /
// network failures.
func (c *Client) ReportPrintVerified(
	ctx context.Context, token string, body PrintVerifiedRequest,
) error {
	return c.do(ctx, http.MethodPost, pathPrintVerified, token, body, nil)
}

// do performs an HTTP request and decodes the response envelope.
// token == "" omits the X-Terminal-Token header. out (if non-nil) is
// populated from the envelope's `data` field on success.
func (c *Client) do(ctx context.Context, method, path, token string, body, out any) error {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("cloud: marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("cloud: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	if token != "" {
		req.Header.Set("X-Terminal-Token", token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNetwork, err)
	}
	defer resp.Body.Close()

	return decodeResponse(resp, out)
}

// decodeResponse parses the response envelope and dispatches success vs
// error. Non-JSON 4xx/5xx (LB / proxy responses) maps to ErrInternal so
// callers always get a typed sentinel.
func decodeResponse(resp *http.Response, out any) error {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read body: %v", ErrNetwork, err)
	}

	var env struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error *errorPayload   `json:"error"`
	}
	if jsonErr := json.Unmarshal(raw, &env); jsonErr != nil {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return fmt.Errorf("cloud: invalid JSON response (HTTP %d): %v", resp.StatusCode, jsonErr)
		}
		return fmt.Errorf("%w: HTTP %d (no JSON envelope)", ErrInternal, resp.StatusCode)
	}

	if !env.OK {
		if env.Error == nil {
			return fmt.Errorf("%w: HTTP %d (envelope ok=false but no error field)", ErrInternal, resp.StatusCode)
		}
		return mapError(env.Error)
	}

	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("cloud: decode data: %w", err)
		}
	}
	return nil
}
