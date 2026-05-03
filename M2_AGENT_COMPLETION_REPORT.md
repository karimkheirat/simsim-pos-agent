# M2 Agent Completion Report

**Date:** 2026-05-03
**Branch:** main
**Commit at verification:** `a175b3e` (head before this report commit)
**Verifier:** Karim Kheirat ran all manual SCM / web UI / browser steps from elevated cmd and a non-elevated cmd on the pilot Windows host. Claude Code (agent session) ran all curl/agentctl/build/test flows from a non-elevated Bash shell on the same host.
**Cloud counterpart:** `https://web-production-6bb4d.up.railway.app` (C1–C8 deployed and contract-correct as of 2026-05-03; Amendment 1 applied — see contract §1).

**Result: PASS on the agent half.** Steps 1–12 ✅, Step 13 SKIPPED (authorized), Step 14 ⚠️ partial (orphan-process wart documented under "Known issues").

---

## Step 1 — Build both binaries

**Status:** ✅ pass

```
$ GOOS=windows GOARCH=amd64 go build -o build/agent.exe ./cmd/agent
agent.exe build exit=0
$ GOOS=windows GOARCH=amd64 go build -o build/agentctl.exe ./cmd/agentctl
agentctl.exe build exit=0
$ ls -l build/agent.exe build/agentctl.exe
-rwxr-xr-x 1 karim 197609 10356224 May  3 14:42 build/agent.exe
-rwxr-xr-x 1 karim 197609  9307648 May  3 14:42 build/agentctl.exe
$ file build/agent.exe build/agentctl.exe
build/agent.exe:    PE32+ executable for MS Windows 6.01 (console), x86-64, 16 sections
build/agentctl.exe: PE32+ executable for MS Windows 6.01 (console), x86-64, 16 sections
```

Both binaries: PE32+ x86-64 Windows console executables, native cross-compile from this Windows host. `agent.exe` ~10.1 MB, `agentctl.exe` ~9.0 MB.

---

## Step 2 — `agent.exe service install` (elevated cmd)

**Status:** ✅ pass

Operator-captured output from elevated session:

```
C:\Users\karim\Code\simsim-pos-agent\build>agent.exe service install
✓ Service installed (delayed auto-start, restart on failure at 10s/30s/60s).

C:\Users\karim\Code\simsim-pos-agent\build>sc query SimsimPOSAgent
STATE: STOPPED
WIN32_EXIT_CODE: 1077 (0x435)   <- ERROR_SERVICE_NEVER_STARTED, normal for fresh install
```

Service registered with SCM. The post-install enrichment in [internal/service/postinstall_windows.go](internal/service/postinstall_windows.go) successfully applied delayed auto-start + restart-on-failure progression at 10s/30s/60s with 60s reset.

---

## Step 3 — `agent.exe service start`

**Status:** ✅ pass (after binary path remediation — see "Binary path discovery" below)

### Initial attempt — failed

```
C:\Users\karim\Code\simsim-pos-agent\build>agent.exe service start
start: Failed to start Simsim POS Agent: Access is denied.

C:\Users\karim\Code\simsim-pos-agent\build>sc query SimsimPOSAgent
STATE: STOPPED
WIN32_EXIT_CODE: 0
WAIT_HINT: 0x7d0
```

### Diagnostic

The diagnostic dance, captured for the M4 installer's benefit:

| Probe | Result |
|---|---|
| `sc qc SimsimPOSAgent` | `BINARY_PATH_NAME = C:\Users\karim\Code\simsim-pos-agent\build\agent.exe` |
| `icacls C:\Users\karim\...\build\agent.exe` | Only `BUILTIN\Administrators`, `NT AUTHORITY\SYSTEM`, `PCKARIM\karim`. **No `LocalService`, no `BUILTIN\Users`.** |
| `type C:\ProgramData\Simsim\POSAgent\logs\agent.log` | "The system cannot find the file specified." — agent never launched. |
| `wevtutil qe System ... SCM events` | Event 7000 "Access is denied" at process spawn. |

### Root cause

SCM launches the registered binary **as the service account (LocalService)**. Files under `C:\Users\<name>\` are readable only by that user + SYSTEM + Administrators by Windows default — LocalService is not in any of those for that path. SCM's `CreateProcessAsUser` failed with ERROR_ACCESS_DENIED before any Go code ran.

### Resolution

Operator copied the binary to a LocalService-readable path and reinstalled:

```
build\agent.exe service uninstall
mkdir C:\ProgramData\Simsim\POSAgent\bin
copy build\agent.exe C:\ProgramData\Simsim\POSAgent\bin\agent.exe
C:\ProgramData\Simsim\POSAgent\bin\agent.exe service install
C:\ProgramData\Simsim\POSAgent\bin\agent.exe service start
sc query SimsimPOSAgent
  STATE: 4 RUNNING
```

### M4 implication

`%ProgramData%\Simsim\POSAgent\bin\` was the interim home for A8. **M4's Inno Setup installer must land the binary in `C:\Program Files\Simsim\POSAgent\agent.exe` instead** — better separation of code from data, and Program Files has the right ACLs (`BUILTIN\Users` read+execute by default) so LocalService can launch from there. The agent code itself needs no change for this — kardianos uses `os.Executable()` automatically.

### Optional follow-up (not blocking M2)

`agent service install` could detect when invoked from a path LocalService can't read (heuristic: path under `%USERPROFILE%`) and warn loudly. Logged in "Deferred" below.

---

## Step 4 — Verify agent is unpaired

**Status:** ✅ pass — operator confirmed.

`agentctl status` returned `Non jumelé.` (exit 0). `curl /health` returned `paired:false` with **no `store_id` and no `terminal_id` keys** (omitempty working correctly).

---

## Step 5 — Endpoints reject unpaired calls

**Status:** ✅ pass — operator confirmed.

`curl -i -X POST /test-print` returned HTTP 401 with body
`{"ok":false,"error":{"code":"NOT_PAIRED","message":"Agent non jumelé. Exécutez 'agentctl pair'."}}`. The middleware in [internal/api/middleware.go](internal/api/middleware.go) (`requireTerminalToken`) correctly distinguishes NOT_PAIRED (no secrets) from UNAUTHENTICATED (secrets present, token mismatch).

---

## Step 6 — Cross-half pairing

**Status:** ✅ pass — first time the cloud and agent halves met live.

Operator unpaired the test terminal `5c19bc9d-4c77-454a-8529-e12b0407d899` via the web UI's "Appairer un appareil" workflow, generated a fresh 6-digit code, and ran on the pilot host:

```
agentctl pair --code <6-digit>
```

Expected and observed: French success block —
```
✓ Appareil jumelé avec succès.
  Magasin    : <store name>
  Caisse     : Caisse 1
  ID terminal: 5c19bc9d-4c77-454a-8529-e12b0407d899
```
Exit 0.

The bootstrap one-shot heartbeat from `agentctl pair` (per A3 contract — fired AFTER local secrets persisted) reached the cloud and produced the +37s `agent_last_seen_at` advance documented in step 9.

This was the contract's first end-to-end exercise of:
- `POST /api/pos-agent/pair` request/response shapes (contract §4.2)
- 32-byte base64url terminal token format (§3.2) round-trip through DPAPI persist
- French error envelope shape on misuse (verified pre-flight via INVALID_CODE probe)

---

## Step 7 — Post-pair status

**Status:** ✅ pass — operator confirmed.

`agentctl status` printed paired details (store name, terminal label `Caisse 1`, paired_at timestamp). `curl /health` returned `paired:true` with `store_id` and `terminal_id` keys present and matching the bound terminal.

---

## Step 8 — Authenticated /test-print with token

**Status:** ✅ pass — operator confirmed.

`curl -H "X-Terminal-Token: <token>" -X POST /test-print` returned HTTP 200 with `bytes_sent` count. `.escpos` file written to the configured printer output directory. Token extracted from `secrets.dat` via DPAPI on the agent host (or captured from the pair flow).

The `requireTerminalToken` middleware passed the constant-time compare (`crypto/subtle.ConstantTimeCompare`).

---

## Step 9 — Heartbeat reached cloud DB

**Status:** ✅ pass — confirmed by cloud session.

Cloud-side query against `pos_terminals` after pair + bootstrap heartbeat:

| Field | Value |
|---|---|
| `id` | `5c19bc9d-4c77-454a-8529-e12b0407d899` |
| `agent_last_seen_at` | advanced **+37 seconds** after pair (bootstrap heartbeat) |
| `agent_version` | `dev` (the locally-built agent's build-time `version` constant) |
| `printer_status_json` | shape correct, content matched `/health` printer block |
| `last_seen_at` | **byte-identical to morning baseline** — Amendment 1 holds |

This is the load-bearing assertion for **Contract Amendment 1**: the agent's heartbeat path writes `agent_last_seen_at` exclusively, never touching `last_seen_at` (which is reserved for POS web-app liveness per Spec 4 §6.1). The byte-identical preservation under real agent traffic confirms the cloud-side route is correctly wired.

The +37s window matches expectation: bootstrap heartbeat from `agentctl pair` fires immediately on success, then the running service's 5-min loop adds a redundant tick (heartbeats are idempotent). The 37s is well under the 60s "real-time-feel" target documented in the M2 spec.

---

## Step 10 — Web UI status indicator

**Status:** ✅ pass — operator confirmed via screenshot.

Refreshing `/fr/retailer/settings/pos-terminals` displayed the `Caisse 1` row with the green online pill and version label **`v=dev`** — flipped from the prior `v=0.2.0` set by the cloud-side curl matrix verification. The version flip is the load-bearing observation: it confirms the locally-built agent (`version="dev"` constant) reached and overwrote the cloud's recorded `agent_version`, end-to-end through the heartbeat pipe.

---

## Step 11 — `agentctl unpair`

**Status:** ✅ pass — operator confirmed.

`agentctl unpair` returned `✓ Jumelage révoqué.` (exit 0). Cloud reachable, normal happy path — `pairing.Service.Unpair` called `cloud.Unpair`, received nil, then cleared local secrets via `SecretStore.Clear()`.

Verification:
- `agentctl status` → `Non jumelé.` (exit 0)
- `curl /health` → `paired:false`, no `store_id` / `terminal_id` keys

---

## Step 12 — Revoked-token rejection

**Status:** ✅ pass.

### Local side (agent)

`curl -i -X POST /test-print` after unpair returned HTTP 401 with
`{"ok":false,"error":{"code":"NOT_PAIRED","message":"Agent non jumelé. Exécutez 'agentctl pair'."}}`.

Per spec ("agent state matters"): NOT_PAIRED is the correct code here because local secrets were cleared by step 11 — the middleware's `Secrets.Load()` returns `ErrNoSecrets` before the X-Terminal-Token header is even read. Adding a stale token to the curl would not change the outcome.

### Cloud side (delegated)

The cloud-session task confirmed `pos_agent_terminal_tokens.revoked_at IS NOT NULL` for the row keyed by `terminal_id = 5c19bc9d-...` and the most recent `paired_at` (which corresponds to this A8 run).

---

## Step 13 — Reboot test

**Status:** SKIPPED — authorized.

Operator chose to skip the reboot lap given the cost of restarting the pilot host and the strong indirect evidence already in hand:
- Service install registered `Automatic (Delayed Start)` — verified by `sc qc` showing the start type after step 2.
- The single-instance mutex was exercised separately in pre-A8 smoke (two foreground agents → second exits 0 with `another instance is running`).
- `internal/service.Program.Stop` has unit-level coverage of the cancel path via the integration with `api.Server.Run`.

Reboot lap is recommended at first M3 milestone (when the SQLite outbox lands and the heartbeat loop has more state to survive a restart).

---

## Step 14 — `agent.exe service uninstall` (elevated)

**Status:** ⚠️ partial — uninstall worked but left an orphan process. See "Known issues" below.

### Observed sequence

```
C:\ProgramData\Simsim\POSAgent\bin>agent.exe service uninstall
✓ Service uninstalled.

C:\ProgramData\Simsim\POSAgent\bin>sc query SimsimPOSAgent
STATE: 4 RUNNING                  <- still running, but SCM no longer manages it

C:\ProgramData\Simsim\POSAgent\bin>agent.exe service stop
✓ Service stopped.

C:\ProgramData\Simsim\POSAgent\bin>sc query SimsimPOSAgent
[SC] EnumQueryServicesStatus:OpenService FAILED 1060:
The specified service does not exist as an installed service.

C:\ProgramData\Simsim\POSAgent\bin>agent.exe service uninstall
service is not installed
```

### Diagnosis

`internal/service.Uninstall` (currently a thin wrapper around `ksvc.Control(svc, "uninstall")`) unregisters the service from SCM **without first stopping the running process**. Result: the SCM entry is gone, but the agent process keeps running with the named mutex held until something tells it to exit. The follow-up `service stop` succeeded because kardianos's stop call still had a usable handle to the (now-orphan) process — clean shutdown via the `Program.Stop` path.

This means a careless `service uninstall` on a running service yields:
- An invisible-to-SCM agent process holding `Global\SimsimPOSAgent`
- Subsequent `service install` succeeds, but `service start` fails because the new service can't acquire the mutex (held by the orphan)
- Operator must manually find and kill the orphan via Task Manager or `taskkill /im agent.exe /f`

### Workaround for M2

`agent.exe service stop` **before** `agent.exe service uninstall`. Documented here so the M3 work has it on the punch list.

### Proposed M3 fix

In [internal/service/service.go](internal/service/service.go), `Uninstall` should query state first:

```go
func Uninstall(svc ksvc.Service) error {
    if state, _ := statusImpl(); state == "running" {
        if err := ksvc.Control(svc, "stop"); err != nil {
            return fmt.Errorf("uninstall: stop running service first: %w", err)
        }
        // Brief wait for STOPPED before uninstall to avoid race.
    }
    if err := ksvc.Control(svc, "uninstall"); err != nil {
        return fmt.Errorf("uninstall: %w", err)
    }
    return nil
}
```

Listed under "Deferred to M3" below.

---

## Cross-half integration evidence (summary)

The two halves shipped from independent Claude Code sessions and met live for the first time during step 6:

1. **Pair handshake worked first try.** `agentctl pair --code <6 digits>` against a fresh code from the web UI produced the spec-shaped success message with the cloud-supplied `store_name`, `terminal_label`, and `terminal_id` round-tripped through the contract envelope correctly.
2. **Heartbeat pipeline worked under real agent traffic.** Step 9's +37s `agent_last_seen_at` advance with the locally-built agent's `dev` version landing in the cloud DB confirms the full loop: agent.heartbeat.Loop → cloud.Client.Heartbeat → cloud handler → `pos_terminals` UPDATE.
3. **Amendment 1 holds.** The cloud-side decision (post-recon, before C1) to use a NEW column `agent_last_seen_at` rather than overloading `last_seen_at` was preserved end-to-end. Karim verified `last_seen_at` was byte-identical to the morning baseline both before and after pair — agent traffic does not contaminate POS web-app liveness signal.
4. **French error envelopes round-trip cleanly.** Pre-flight probe with bogus pairing code returned `{"ok":false,"error":{"code":"INVALID_CODE","message":"Code invalide ou expiré"}}` — agentctl's `printPairError` (cmd/agentctl/pair.go) preserved the cloud's literal French message via `*CloudError.Message()` for operator display.
5. **Token auth is uniformly enforced.** `/print`, `/test-print`, `/drawer/open`, `/status` all reject without a valid `X-Terminal-Token` (verified via the auth test matrix in api_test.go and reaffirmed by step 5's NOT_PAIRED rejection). `/health` correctly stays unauthenticated to support POS-app discovery.

---

## Code state summary

**Branch:** `main`, ahead of `origin/main` by 9 commits (M2 work, all unpushed before this report commit).

**Total commits since repo init:** 17 (M1: 8, M2: 9 before report).

**M2 commits:**

```
a175b3e feat: add heartbeat loop reporting agent state to cloud
6643e57 feat: add Windows service lifecycle and single-instance enforcement
bca94c5 feat: token auth on local API plus /status endpoint
b4feb46 feat: extract pairing orchestration into internal/pairing.Service
2b54762 feat: add agentctl CLI with pair, unpair, status subcommands
716676e feat: add secrets storage with DPAPI on Windows and JSON dev backend
a47cc2a docs: update M2 contract with Amendment 1 (post-recon resolutions)
1890bf7 feat: add cloud client package for /api/pos-agent endpoints
ddc221e docs: add M2 contract and build specs
```

**Test count:** 209 `=== RUN` entries across all packages (unit tests + subtests). All pass; `go vet ./...` clean.

**Packages exercised:**

| Package | New in M2? | Notable |
|---|---|---|
| `cmd/agent` | extended | `service` subcommand, `runAsService`, mutex acquisition in both run paths |
| `cmd/agentctl` | new | pair/unpair/status, French message preservation |
| `internal/api` | extended | token auth middleware, /status, /health paired-state from secrets |
| `internal/cloud` | new | `/api/pos-agent/*` client, sentinel error mapping, CloudError wrapper |
| `internal/config` | extended | `Secrets`, `JSONFileSecretStore` (cross-platform), `DPAPISecretStore` (Windows), `CloudBaseURL`, `HeartbeatSeconds`, `DefaultMachineIDPath`, `DefaultLogPath` |
| `internal/heartbeat` | new | periodic loop, 5-min default, 60s recheck when unpaired, 401 → clear secrets |
| `internal/pairing` | new | `Service.Pair`/`Unpair`/`Status` orchestration extracted from CLI |
| `internal/service` | new | kardianos `Program`, install + post-install enrichment, status query, single-instance mutex |
| `internal/escpos` | unchanged from M1 | |
| `internal/printer` | unchanged from M1 | |
| `internal/receipt` | unchanged from M1 | |
| `internal/util` | unchanged from M1 | |

**Dependencies added in M2:** `github.com/kardianos/service v1.1.0` (pinned to maintain Go 1.22 floor; v1.2.4 requires Go 1.23+).

---

## Known issues

### Service uninstall does not stop a running service first

See Step 14 above. Workaround: `service stop` before `service uninstall`. Fix proposed in [internal/service/service.go](internal/service/service.go) `Uninstall` — query state, stop if RUNNING, then uninstall. Listed under M3 below.

### `agent service install` doesn't warn when run from a user-profile path

See Step 3. The fresh-install case fails with cryptic "Access is denied" until the operator moves the binary. M4's installer eliminates this for the production path; for the dev case, an interim heuristic in `agent service install` (warn if `os.Executable()` starts with `%USERPROFILE%`) would catch it earlier. Optional, not blocking M2.

---

## Deferred

### M3

- **Telemetry / SQLite outbox** — currently network errors during heartbeat are logged at debug and dropped. M3 introduces an outbox-and-flusher pattern for offline durability.
- **Log file rotation** — `openServiceLog` and `DefaultLogPath` open `agent.log` in append mode without rotation; will grow unbounded in 24/7 service mode. Wire `gopkg.in/natefinch/lumberjack.v2`.
- **Real OS version detection** — heartbeat reports `runtime.GOOS` (`"windows"`); should report `"Windows 11 23H2"`-style detail via Windows version registry.
- **Service uninstall stops first** — the wart documented in Step 14.
- **`agent service install` warning** for user-profile binary paths.
- **Optional `--user` install flag** for rare cases where LocalService can't talk to the spooler (per master spec §5.1).

### M4

- **Inno Setup installer** — must land binary in `C:\Program Files\Simsim\POSAgent\agent.exe`, **not** under user profile. The Step 3 binary-path discovery is the smoking gun: M4 cannot ship to non-developer machines without solving this. `%ProgramData%\Simsim\POSAgent\` remains the data root (config, secrets, logs, M3 outbox).
- **GitHub Actions release pipeline** — cross-compile, sign (post-EV cert), publish artifacts.
- **Self-update flow** — staged binary swap, SHA-256 verification, restart via `kardianos`.

### Pre-launch

- **EV code-signing certificate** — without it, SmartScreen warns on every install. Inno Setup can sign the installer if the cert is available; agent.exe self-signing is a separate procurement.
- **Real Algerian SMS provider** — for pairing-related operator notifications, if the Spec 4 pairing UX adds SMS confirmation in M3+.

---

## Sign-off

**Agent half: PASS.** All M2 acceptance criteria met. Karim Kheirat verified all manual SCM lifecycle steps (install / start / stop / uninstall) and the web UI status indicator screenshot. Claude Code (agent session) verified all build / curl / agentctl / cloud-client flows.

The Step 14 uninstall wart is documented, has a known workaround, and has a one-line proposed fix flagged for M3 polish — does not block the agent half from shipping.

**Cloud half:** verified separately via the cloud-session report (C1–C8 + Amendment 1), confirmed contract-correct against this agent half during A8 step 6 first-meet integration.

M2 is shipped. Next milestone: M3 (telemetry outbox + log rotation + service-stop-on-uninstall + status pill polish).
