# POS Agent M2 — Agent Spec (`simsim-pos-agent` Go repo)

**Read first:**
1. `POS_AGENT_M2_CONTRACT.md` at the repo root — authoritative wire contract.
2. `POS_AGENT_SPEC.md` at the repo root — overall architecture, especially §5 (runtime), §8 (pairing flow), §10 (heartbeat).
3. `M1_COMPLETION_REPORT.md` — what's already built.

**Repo:** `karimkheirat/simsim-pos-agent` at `C:\Users\karim\Code\simsim-pos-agent`.

**Estimated effort:** 1.5 working days of Claude Code time.

---

## Operating principles (non-negotiable)

- **No autonomous product decisions.** If the contract or this spec is ambiguous, stop and ask Karim.
- **Investigate before fixing.** M1 set patterns: idempotency store, slog, CORS allowlist, error envelope. Match those patterns; don't reinvent.
- **No scope creep.** Anything not in this spec or the contract is M3+. Specifically: telemetry outbox, auto-update, installer (.exe / Inno Setup) — all M3 or M4.
- **Report findings, then ask.** Stop at every sub-task boundary.
- **Verify, don't trust self-reports.** Cross-compile + run on Windows + curl the endpoints + read the actual logs at every checkpoint.
- **Do not push to GitHub** until M2 is complete and Karim approves.

---

## What you're building

Five additions to the existing agent:
1. A cloud HTTP client that speaks to `/api/pos-agent/*`.
2. A pairing CLI subcommand (`agentctl pair`) and CLI binary.
3. DPAPI-encrypted secrets storage on Windows (terminal token + IDs).
4. Token authentication middleware on the local API.
5. Windows service install/start/stop/uninstall + a heartbeat loop that runs in the background.

### Out of scope

- Telemetry events / SQLite outbox — M3.
- Inno Setup installer (`.exe` artifact) — M4.
- Auto-update / release endpoint — M4.
- Code signing / EV cert — pre-launch ops, separate workstream.
- Any UI work — this is CLI + service only.

---

## Sub-task order

Each sub-task ends with a stop-and-report. Wait for green light before continuing.

### Sub-task A1 — Cloud client (`internal/cloud`)

New package `internal/cloud`. Pure Go, no I/O at construction time.

```go
type Client struct {
    BaseURL    string
    HTTPClient *http.Client       // injectable for tests; default 10s timeout
    UserAgent  string             // "simsim-pos-agent/<version>"
}

func New(baseURL, version string) *Client

// Pairing
func (c *Client) Pair(ctx context.Context, code, agentVersion, machineID string) (*PairResponse, error)

// Authenticated calls — token passed explicitly
func (c *Client) Heartbeat(ctx context.Context, token string, hb HeartbeatRequest) error
func (c *Client) Unpair(ctx context.Context, token string) error

type PairResponse struct {
    TerminalID    string
    TerminalToken string
    StoreID       string
    StoreName     string
    TerminalLabel string
}

type HeartbeatRequest struct {
    AgentVersion   string
    OSVersion      string
    UptimeSeconds  int64
    Printer        PrinterStatus
}
```

Error mapping: define typed errors for the cloud error codes per contract §5.1.

```go
var (
    ErrInvalidRequest    = errors.New("cloud: invalid request")
    ErrInvalidCode       = errors.New("cloud: pairing code invalid or expired")
    ErrUnauthenticated   = errors.New("cloud: terminal token invalid or revoked")
    ErrForbidden         = errors.New("cloud: forbidden")
    ErrNotFound          = errors.New("cloud: not found")
    ErrRateLimited       = errors.New("cloud: rate limited")
    ErrInternal          = errors.New("cloud: server error")
    ErrNetwork           = errors.New("cloud: network error")
)
```

A response envelope decoder maps `error.code` strings to these sentinels.

Tests in `internal/cloud/cloud_test.go`:
- Use `httptest.NewServer` with a mock that returns canned responses for each endpoint.
- Pair happy path → returns PairResponse.
- Pair with `INVALID_CODE` → returns ErrInvalidCode.
- Pair with 429 → returns ErrRateLimited.
- Heartbeat with 401 → returns ErrUnauthenticated.
- Network error (close server before call) → returns ErrNetwork wrapping the underlying error.
- Context cancellation honored.
- `User-Agent` header sent on every request.
- `X-Terminal-Token` header sent on heartbeat and unpair, NOT on pair.

➜ Stop and report.

### Sub-task A2 — Secrets storage (`internal/config/secrets.go` and DPAPI on Windows)

Two storage backends, factory pattern matching the printer package:

```go
// internal/config/secrets.go
type Secrets struct {
    TerminalID    string
    TerminalToken string
    StoreID       string
    PairedAt      time.Time
}

type SecretStore interface {
    Load() (*Secrets, error)   // returns ErrNoSecrets if not paired
    Save(s *Secrets) error
    Clear() error
}

var ErrNoSecrets = errors.New("secrets: no paired secrets present")

func NewSecretStore(path string) (SecretStore, error) // OS-aware factory
```

Implementations:

- **Windows (`secrets_windows.go`, build tag `windows`):** `DPAPISecretStore`. Uses `golang.org/x/sys/windows` to call `CryptProtectData` / `CryptUnprotectData` with `CRYPTPROTECT_LOCAL_MACHINE` flag (so the system service can decrypt). On disk: a single file at `<path>` containing the DPAPI ciphertext blob.
- **Non-Windows (`secrets_stub.go`, build tag `!windows`):** `JSONFileSecretStore`. Plain JSON on disk. Used for dev on the WSL/Linux side and unit tests. Document clearly in the code comment that this is **dev-only and unsafe in production**.

Path: `%ProgramData%\Simsim\POSAgent\secrets.dat` on Windows, `./secrets.json` elsewhere. Already covered by `internal/config.DefaultSecretsPath()` — add it.

Tests in `internal/config/secrets_test.go`:
- Round-trip: save then load returns identical struct.
- Load when missing → `ErrNoSecrets`.
- Clear: file removed, subsequent load → `ErrNoSecrets`.
- DPAPI test only runs on Windows (`//go:build windows`); covers actual DPAPI round-trip.

Important: machine-scope DPAPI is correct here per contract §3.2 reasoning. Anyone with admin on the box can decrypt — that's an accepted trade-off because the alternative is per-user encryption, which doesn't survive the service running under `LocalService`.

➜ Stop and report.

### Sub-task A3 — Pairing CLI (`cmd/agentctl`)

A separate binary from `cmd/agent`. Why separate: the operator runs `agentctl pair` interactively on the cashier's PC; the service binary runs in the background. Different lifecycles, different concerns.

```
agentctl pair --code <6-digit-code>
agentctl unpair
agentctl status
```

`agentctl` reads the same config file as `agent` and writes to the same secrets file.

`pair`:
1. Parse `--code` flag. Validate exactly 6 ASCII digits. Otherwise exit 2 with "Code invalide. Doit être 6 chiffres."
2. Load config.
3. Read or generate a `machine_id` — stable hash of `runtime.GOOS + os.Hostname() + (Windows: machine GUID via reg query, fallback to MAC of first non-loopback NIC)`. Cache it in `<ProgramData>/Simsim/POSAgent/machine_id` so it's stable across runs.
4. Call `cloud.Pair(ctx, code, agentVersion, machineID)`.
5. On success: persist secrets via `SecretStore.Save`. Print confirmation in French:
   ```
   ✓ Appareil jumelé avec succès.
     Magasin    : Hamoud Boualem - Centre Oran
     Caisse     : Caisse 1
     ID terminal: trm_xxx
   ```
6. On error: print human-friendly French message based on the typed error. Exit 1.
7. Trigger a heartbeat to the cloud (one-shot) so the retailer's settings page sees the pair land within seconds. If heartbeat fails, log a warning but don't fail the pair — the pair itself is committed.

`unpair`:
1. Load secrets. If none, print "Pas de jumelage actif." and exit 0.
2. Call `cloud.Unpair(ctx, token)`. On 401, treat as already-revoked (log + continue). On network error, prompt: "Impossible de contacter le serveur. Effacer les secrets locaux quand même ? [o/N]" (default no).
3. Clear local secrets.
4. Print "✓ Jumelage révoqué."

`status`:
- Read secrets. If unpaired, print "Non jumelé."
- If paired, print store name, terminal label, paired_at. Hit local agent's `/health` (or `/status` once we add it in M3) to also show last heartbeat status. Don't fail if the agent isn't running — print "Agent local non en cours d'exécution" and continue.

Tests:
- `cmd/agentctl` itself: only smoke-test the flag parser. Real logic lives in `internal/pairing` (next sub-task).

➜ Stop and report.

### Sub-task A4 — Pairing logic (`internal/pairing`)

Move the orchestration out of the CLI binary into a testable package.

```go
// internal/pairing/pairing.go
type Service struct {
    Cloud      *cloud.Client
    Secrets    config.SecretStore
    MachineID  string
    Version    string
}

func (s *Service) Pair(ctx context.Context, code string) (*cloud.PairResponse, error)
func (s *Service) Unpair(ctx context.Context) error
func (s *Service) Status(ctx context.Context) (*Status, error)

type Status struct {
    Paired        bool
    TerminalID    string
    StoreID       string
    PairedAt      time.Time
}
```

CLI calls into this. Unit tests mock the cloud client and secret store, cover happy + every error path.

Tests in `internal/pairing/pairing_test.go`:
- Pair happy → secrets saved, response returned.
- Pair with cloud error → no secrets saved, error propagated.
- Pair when already paired → succeeds, overwrites old secrets (new pairing supersedes).
- Unpair happy → cloud called, secrets cleared.
- Unpair when cloud returns 401 → secrets cleared, no error returned to caller (already-revoked is benign).
- Unpair when cloud returns network error → secrets NOT cleared; error propagated; CLI handles the "force clear?" prompt itself.
- Status when no secrets → returns `Paired: false`.
- Status when secrets present → returns full Status struct.

➜ Stop and report.

### Sub-task A5 — Token auth middleware on local API

Update `internal/api`:

1. Add a `secretStore config.SecretStore` field on `Server` (set via `Config.Secrets`).
2. Add middleware `requireTerminalToken` that:
   - Loads secrets. If `ErrNoSecrets` → 401 `NOT_PAIRED` with French message "Agent non jumelé. Exécutez 'agentctl pair'."
   - Reads `X-Terminal-Token` header. Constant-time compare against `secrets.TerminalToken`. Mismatch → 401 `UNAUTHENTICATED`.
3. Apply to: `/print`, `/test-print`, `/drawer/open`, `/status` (new — see below). Do NOT apply to `/health`.
4. The existing `// TODO M2: token auth` comments mark exactly where this attaches.

Update `/health` response to include real `paired` state from the secret store:

```json
{
  "ok": true,
  "version": "...",
  "paired": true,
  "store_id": "f0040929-...",
  "terminal_id": "trm_...",
  "printer": { ... }
}
```

`store_id` and `terminal_id` are returned only when paired — this is what lets the POS web app verify the agent is bound to the right terminal.

Add `/status` endpoint (authenticated) that returns the same shape as `/health` plus diagnostic detail (queue depths come in M3; for now just include `last_print_at` from the idempotency store).

Update tests:
- `/print` without token → 401 UNAUTHENTICATED.
- `/print` with wrong token → 401.
- `/print` when unpaired → 401 NOT_PAIRED.
- `/print` when paired with correct token → 200 (existing tests, parameterized to inject a token via the mock secret store).
- `/health` reflects paired state.

➜ Stop and report.

### Sub-task A6 — Windows service lifecycle (`internal/service` and `cmd/agent` subcommands)

Use `github.com/kardianos/service` (already in scope per master spec §3).

Add subcommands to `cmd/agent`:

```
agent service install
agent service uninstall
agent service start
agent service stop
agent service status
agent run                     # foreground (existing — keep working for dev)
```

Service config:
- Name: `SimsimPOSAgent`
- Display name: `Simsim POS Agent`
- Description: `Local printer agent for Simsim POS — handles receipt printing and cash drawer control.`
- Startup type: Automatic (Delayed Start) — `kardianos/service` exposes this on Windows.
- Restart policy: restart on failure (10s, 30s, 60s).
- Account: `LocalService` initially. If the print spooler refuses jobs from `LocalService` on a given machine, document the manual fix (re-install with `--user` flag) — don't auto-detect.

Single-instance enforcement: on startup, attempt to acquire a named mutex `Global\SimsimPOSAgent` (Windows API). If another instance holds it, log "another instance is running" and exit 0. Use `golang.org/x/sys/windows` directly.

Tests:
- Install / uninstall / start / stop work cleanly on a fresh Windows VM. Manual verification only — these are integration tests against the OS.
- The single-instance mutex is unit-testable on Windows builds; skip on non-Windows.

➜ Stop and report.

### Sub-task A7 — Heartbeat loop

Add a heartbeat goroutine that runs while the service is up:

- Interval: 5 minutes (read from config, `heartbeat_seconds`, default 300).
- Lifecycle: started by `Server.Run` after secrets are loaded; stopped on ctx cancel.
- If unpaired (no secrets), the loop sleeps and re-checks every 60s — so a `agentctl pair` while the service is running starts heartbeating without restart.
- Builds the `HeartbeatRequest` from current state: agent version, runtime info, printer status from `printer.IsReachable()`.
- On `cloud.ErrUnauthenticated` (token revoked server-side) → clear secrets, log loudly, the agent is now unpaired and will reject local API calls until re-pair.
- On `cloud.ErrNetwork` → log debug, swallow, retry next tick. Do NOT queue heartbeats offline in M2 — that's the M3 outbox. Missed heartbeats just mean the cloud's `last_seen_at` will lag.

The first heartbeat fires immediately on service start (or right after `agentctl pair` succeeds) so the cloud sees activity within seconds, not minutes.

Tests in `internal/heartbeat/heartbeat_test.go`:
- With a fake cloud client, verify N heartbeats fire over a fast interval (use a 50ms tick for tests).
- On 401, secrets cleared.
- On network error, no panic, retry next tick.
- ctx cancel stops the loop within one tick.

➜ Stop and report.

### Sub-task A8 — End-to-end smoke + completion report

After A1–A7 land:

1. Build both binaries:
   ```
   GOOS=windows GOARCH=amd64 go build -o build/agent.exe ./cmd/agent
   GOOS=windows GOARCH=amd64 go build -o build/agentctl.exe ./cmd/agentctl
   ```
2. Install service: `agent.exe service install`. Start: `agent.exe service start`.
3. Verify service is running: `sc query SimsimPOSAgent` shows `RUNNING`.
4. Verify agent is unpaired: `agentctl status` says "Non jumelé". `curl /health` shows `"paired": false`.
5. Verify endpoints reject unpaired calls: `curl -X POST /test-print` → 401 NOT_PAIRED.
6. **Cross-half integration:** in the `simsim` repo, generate a pairing code via the retailer settings UI (or directly via the cloud API with a session cookie). Get the 6-digit code.
7. Pair: `agentctl pair --code 428193`. Verify success message.
8. `agentctl status` now shows store name, terminal label, paired_at.
9. `curl /health` shows `"paired": true` plus store_id, terminal_id.
10. With token: `curl -H "X-Terminal-Token: <token>" -X POST /test-print` → 200, file written.
11. Wait for first heartbeat. Verify in cloud DB: `SELECT last_seen_at, agent_version FROM terminals WHERE id = ?` — both set, `last_seen_at` is recent.
12. Verify retailer settings page in the `simsim` UI shows the terminal as "🟢 En ligne".
13. Unpair from agent: `agentctl unpair`.
14. Verify cloud DB: `pos_agent_terminal_tokens.revoked_at` is set.
15. `curl -H "X-Terminal-Token: <old_token>" -X POST /test-print` → 401 UNAUTHENTICATED.

Write `M2_AGENT_COMPLETION_REPORT.md` at the repo root with each step's evidence: command run, exit code, key output. Same shape as `M1_COMPLETION_REPORT.md`.

Commit the report. Do not push until Karim approves.

➜ Stop and report. M2 agent half complete.

---

## Coordinator notes (Karim only — Claude Code can ignore)

- A1, A2, A4 are pure Go — no Windows API needed — and can be built/tested on the agent dev box with no service install ceremony.
- A3, A5 need a paired token to integration-test against — do those after the cloud half C1–C5 lands on staging.
- A6 needs Windows admin privileges to install the service (UAC prompt). Manual one-time setup; not a CI step.
- A7 depends on a paired state to fully exercise.
- A8 is the single end-to-end integration. Until A8 passes, M2 isn't done — even if individual sub-tasks all pass.
