# POS Agent — Build Spec

**Component:** Local desktop agent for POS peripherals (printer + cash drawer).
**Repo:** New repo — `karimkheirat/simsim-pos-agent`. Does **not** live inside `simsim` web repo.
**Target OS:** Windows 10 / 11 (64-bit).
**Language/runtime:** Go 1.22+. Single static binary.
**Pilot:** Hamoud Boualem store, Oran. SP-331 thermal printer, SP-8500 scanner (HID, no integration), TBD cash drawer cabled to printer DK port.
**Estimated effort:** 7–10 working days, phased into 5 milestones.

---

## 1. Goals & non-goals

### Goals
1. Print POS receipts from the Simsim web POS (`/pos`) to a USB-connected ESC/POS thermal printer, **offline-first** (no internet required at print time).
2. Open a cash drawer wired to the printer's DK port via ESC/POS pulse.
3. Bind cleanly to a single Simsim store/terminal at install time, using the existing Spec 4 terminal binding model.
4. Run as a Windows service: starts on boot, restarts on crash, no cashier intervention.
5. Self-update from a Simsim-hosted release endpoint.
6. Report status, errors, and heartbeat back to Simsim when online; queue offline.
7. Provide a clean, signed (later) installer for non-technical operators.

### Non-goals (v1)
- Price checker screens.
- Scales.
- Network or Serial-only printers (USB only for pilot; transport abstraction allows later).
- macOS / Linux support.
- Arabic text rendering on receipts (see §16, decision pending).
- Code-signing for the installer (deferred until EV cert acquired).

---

## 2. Architecture overview

```
┌──────────────────────────┐         ┌─────────────────────────┐
│  Simsim POS web app      │         │  Simsim cloud (Next.js) │
│  (https://…/pos)         │         │                         │
│  Browser, offline-first  │  HTTPS  │  Pairing API            │
│  Dexie/IndexedDB         │ ◀────── │  Telemetry API          │
└────────────┬─────────────┘         │  Release/update API     │
             │ HTTP                  └────────────▲────────────┘
             │ 127.0.0.1:47291                    │
             ▼                                    │ HTTPS (when online)
┌──────────────────────────┐                      │
│  POS Agent (Go service)  │ ─────────────────────┘
│  - Local HTTP API        │
│  - Print job renderer    │
│  - Outbox queue (SQLite) │
│  - Updater               │
└────────────┬─────────────┘
             │ Windows Print Spooler (raw passthrough)
             ▼
┌──────────────────────────┐
│  SP-331 ESC/POS printer  │
│  ↳ DK port → cash drawer │
└──────────────────────────┘
```

### Why Windows Print Spooler (raw passthrough), not direct USB

The agent does **not** open the USB device directly. It writes raw ESC/POS bytes to a named Windows printer queue using the Windows Print Spooler API. This means:
- USB, Serial, and Ethernet variants of the SP-331 all work with one code path. The transport is Windows' problem.
- No libusb / WinUSB driver replacement. The user installs the printer through Windows the normal way.
- Manufacturer driver, "Generic / Text Only", or any "raw passthrough" driver works.

Library: `github.com/alexbrainman/printer` (Windows-only, MIT). Mature, minimal, used in production by ESC/POS projects.

---

## 3. Tech stack (locked)

| Concern | Choice | Why |
|---|---|---|
| Language | Go 1.22+ | Single binary, native Windows service, fast cross-compile from WSL |
| HTTP server | stdlib `net/http` | No deps |
| Windows service | `github.com/kardianos/service` | Cross-platform service installer; production-tested |
| Printer transport | `github.com/alexbrainman/printer` | Raw passthrough via Windows spooler |
| Local storage | `modernc.org/sqlite` (pure-Go SQLite) | No CGO; clean cross-compile |
| Config encryption | Windows DPAPI via `golang.org/x/sys/windows` | Per-machine encrypted secrets |
| Logging | `log/slog` (stdlib) + rotating file via `gopkg.in/natefinch/lumberjack.v2` | Stdlib + tiny rotator |
| Installer | Inno Setup 6 (free, MIT-equivalent) | De-facto Windows installer for small ISVs |
| CI / build | GitHub Actions, build on `windows-latest` runner | Same as iOS/Android workflow |

No CGO. No external runtime dependencies. The installer ships one `.exe` plus support files.

---

## 4. Repository structure

```
simsim-pos-agent/
├── cmd/
│   ├── agent/              # main service entrypoint
│   └── agentctl/           # CLI for pair/unpair/status (used by installer & support)
├── internal/
│   ├── api/                # local HTTP server (handlers, middleware, auth)
│   ├── cloud/              # Simsim cloud client (heartbeat, telemetry, release check)
│   ├── config/             # config load/save, DPAPI encryption
│   ├── escpos/             # ESC/POS command builder
│   ├── outbox/             # SQLite-backed offline queue
│   ├── pairing/            # pairing flow state machine
│   ├── printer/            # printer abstraction + Windows spooler driver
│   ├── receipt/            # receipt model → ESC/POS rendering
│   ├── service/            # Windows service lifecycle (kardianos/service)
│   ├── telemetry/          # heartbeat + error reporting
│   └── updater/            # self-update (download, verify, swap, restart)
├── installer/
│   └── simsim-pos-agent.iss  # Inno Setup script
├── scripts/
│   ├── build.ps1
│   └── release.ps1
├── .github/workflows/
│   └── release.yml
├── README.md
├── CHANGELOG.md
├── LICENSE
├── go.mod
└── go.sum
```

---

## 5. Agent runtime

### 5.1 Process model
- Runs as a Windows service named `SimsimPOSAgent`.
- Display name: `Simsim POS Agent`.
- Startup: Automatic (Delayed Start) — avoids competing with boot-critical services.
- Recovery: restart on first, second, and subsequent failures (10s, 30s, 60s).
- Service account: `LocalService`. The print spooler does not require user-level credentials for raw print jobs to a shared local printer. If access fails on a given machine, fall back to running under the cashier's user account — document in installer.
- Single instance only (named mutex `Global\SimsimPOSAgent`).

### 5.2 Config & state

Two stores:

**`config.json`** (plaintext, non-secret) — `%ProgramData%\Simsim\POSAgent\config.json`:
```json
{
  "version": "1.0.0",
  "listen_port": 47291,
  "cloud_base_url": "https://web-production-6bb4d.up.railway.app",
  "printer_name": "SP-331",
  "log_level": "info",
  "heartbeat_seconds": 300,
  "release_check_seconds": 86400
}
```

**`secrets.dat`** (DPAPI-encrypted, machine scope) — `%ProgramData%\Simsim\POSAgent\secrets.dat`:
```json
{
  "terminal_id": "trm_…",
  "terminal_token": "…",
  "store_id": "…",
  "paired_at": "2026-04-28T14:00:00Z"
}
```

DPAPI encryption uses `CryptProtectData` with `CRYPTPROTECT_LOCAL_MACHINE` so the system service can decrypt it. Anyone with admin on the machine can decrypt it — this is acceptable; the alternative requires a per-user key and we run as a service.

### 5.3 Local HTTP API

Listens on `127.0.0.1:47291` only — never on `0.0.0.0`. Refuses non-loopback connections at the socket level.

| Method | Path | Purpose | Auth |
|---|---|---|---|
| `GET` | `/health` | Liveness probe; returns version, paired state, printer connectivity | None |
| `GET` | `/status` | Detailed status; same as health plus last-print result, queue depth | Token |
| `POST` | `/pair` | Submit a pairing code; agent exchanges with cloud, stores token | None (one-time) |
| `POST` | `/unpair` | Clear secrets; reset to unpaired state | Token |
| `POST` | `/print` | Submit a print job (receipt) | Token |
| `POST` | `/drawer/open` | Pulse the cash drawer kick | Token |
| `POST` | `/test-print` | Print a built-in self-test receipt | Token |

#### CORS

Allow only the Simsim production origin(s). Configurable via `config.json`:

```json
"allowed_origins": [
  "https://web-production-6bb4d.up.railway.app",
  "https://opensimsim.co"
]
```

Preflight: `Access-Control-Allow-Methods: GET, POST, OPTIONS`; `Access-Control-Allow-Headers: Content-Type, X-Terminal-Token`. No credentials, no wildcard.

#### Auth

Every authenticated endpoint requires `X-Terminal-Token: <terminal_token>`. Agent compares against its stored token (constant-time compare). Mismatch → `401`. No token → `401`.

This prevents random websites the cashier visits from sending print jobs. Mixed-content rules already prevent HTTPS pages other than allowed-origin from reaching `http://127.0.0.1` cleanly, but we belt-and-brace with the token.

#### `/health` response (unauthenticated, for POS discovery)

```json
{
  "ok": true,
  "version": "1.0.0",
  "paired": true,
  "store_id": "f0040929-…",
  "terminal_id": "trm_…",
  "printer": { "configured": true, "reachable": true, "name": "SP-331" }
}
```

`store_id` and `terminal_id` are returned in `/health` so the POS web app can verify the agent is bound to the same store/terminal the cashier is logged into. Mismatch → POS shows "wrong terminal" error and refuses to print.

#### `/print` request

```json
{
  "job_id": "uuid-v4-from-pos",
  "idempotency_key": "same-as-job-id",
  "receipt": { ... see §7 ... },
  "open_drawer_after": true
}
```

`/print` is idempotent on `job_id`. If the agent has already successfully printed `job_id`, return `200` with the prior result and do not re-print. Idempotency window: 24 hours.

#### Response envelope (all endpoints)

```json
{ "ok": true,  "data": { ... } }
{ "ok": false, "error": { "code": "PRINTER_OFFLINE", "message": "…" } }
```

Error codes (initial set): `UNAUTHENTICATED`, `NOT_PAIRED`, `PRINTER_NOT_CONFIGURED`, `PRINTER_OFFLINE`, `PRINT_FAILED`, `DRAWER_FAILED`, `INVALID_RECEIPT`, `RATE_LIMITED`, `INTERNAL`.

### 5.4 Rate limiting
Token-bucket per endpoint. Defaults: `/print` 10 rps burst 20; `/drawer/open` 2 rps burst 5; `/health` unrestricted. Prevents a runaway POS bug from queueing thousands of jobs.

---

## 6. Printer driver (ESC/POS via Windows spooler)

### 6.1 Discovery
At service start, list installed Windows printers (`printer.ReadNames()`). If `config.printer_name` matches one, use it. If not, log a warning and `printer.reachable = false` until configured.

### 6.2 Print path
1. POS sends `/print` with a structured receipt.
2. `internal/receipt` renders it to a byte stream of ESC/POS commands.
3. `internal/printer` opens the named Windows printer, sends a single RAW datatype print job containing those bytes, closes.
4. On success: enqueue a telemetry event, return `200`.
5. On failure: classify (offline / spooler error / unknown), enqueue error event, return appropriate error code.

### 6.3 ESC/POS command set used (v1)

Reset and basic formatting only. Codepage default CP437 / CP858 (Latin) — sufficient for French text without diacritic loss when we use compatible characters. See §16 for Arabic.

| Command | Bytes | Use |
|---|---|---|
| Initialize | `ESC @` | `1B 40` | Reset before each job |
| Select codepage | `ESC t n` | `1B 74 n` | n=19 for CP858 |
| Bold on/off | `ESC E n` | `1B 45 n` |
| Double height/width | `GS ! n` | `1D 21 n` |
| Align L/C/R | `ESC a n` | `1B 61 n` (0/1/2) |
| Line feed | `LF` | `0A` |
| Cut paper (full) | `GS V 0` | `1D 56 00` |
| Cut paper (partial) | `GS V 1` | `1D 56 01` |
| Drawer kick | `ESC p m t1 t2` | `1B 70 00 32 FA` (DK1, ~50ms pulse) |
| Status request | `DLE EOT n` | `10 04 n` (n=1 for status) |

Cut paper at the end of every job (full cut). Drawer kick after cut if `open_drawer_after: true`.

### 6.4 Cash drawer
Same printer connection. The drawer is a passive solenoid wired to the printer's RJ11 DK port. The printer fires the pulse when it receives `ESC p`. No separate driver, no separate config. If `/drawer/open` is called without a print job, the agent sends a single `ESC p` to the printer.

### 6.5 Printer status
ESC/POS `DLE EOT n` returns a status byte. Use it best-effort during `/health` (timeout 200ms — never block the health endpoint). If the printer doesn't respond cleanly via spooler (some don't), report `reachable: unknown` rather than `false`.

---

## 7. Receipt model

The POS web app sends a structured receipt; the agent renders it. The contract — defined here — must match what the POS produces.

```jsonc
{
  "store": {
    "name": "Hamoud Boualem - Centre Oran",
    "address_line_1": "12 Rue Larbi Ben M'hidi",
    "address_line_2": "Oran 31000",
    "phone": "+213 41 …",
    "tax_id": "NIF/RC line if applicable"
  },
  "terminal": { "id": "trm_…", "label": "Caisse 1" },
  "cashier": { "name": "Amine Benali" },
  "receipt_number": "2026-0428-0001",
  "issued_at": "2026-04-28T14:32:11+01:00",
  "currency": "DZD",
  "lines": [
    {
      "sku": "HB-COLA-33",
      "name": "Hamoud Cola 33cl",
      "qty": 6,
      "unit_price": 45,
      "line_total": 270,
      "discount_label": null
    }
  ],
  "discounts": [
    { "label": "Remise -5%", "amount": -13.50 }
  ],
  "totals": {
    "subtotal": 270,
    "discount_total": -13.50,
    "tax_total": 0,
    "grand_total": 256.50
  },
  "payment": {
    "method": "cash",
    "tendered": 300,
    "change": 43.50
  },
  "footer_lines": [
    "Merci de votre visite",
    "Conservez ce ticket"
  ]
}
```

Rendering rules:
- 80mm paper → ~42 characters per line at standard font.
- Header: store name (double-height, centered), address (centered, normal), phone, tax ID.
- Receipt number, date, cashier, terminal label.
- Items: name on left (truncate at 24 chars), `qty x unit` middle, `line_total` right-aligned.
- Discounts: separate section.
- Totals: separator line, `Sous-total`, `Remise`, `Total` (double-height).
- Payment: method, tendered, change (cash only for v1).
- Footer lines: centered, normal weight.
- Final: 4 line feeds, full cut, optional drawer kick.

All formatting decisions are encapsulated in `internal/receipt/render.go`. Spec for visual layout follows Karim's design conventions — final visual sign-off after first hardware test. **No autonomous changes to receipt layout without Karim's approval.**

---

## 8. Pairing flow

Pairing binds one running agent to one Simsim terminal record. Reuses Spec 4 terminal binding.

### 8.1 Operator flow (cashier or owner)
1. In Simsim web app, navigate to `/retailer/settings/pos-terminals`.
2. Click "Pair new device" on the terminal row.
3. Cloud generates a 6-digit numeric pairing code, single-use, 15-minute TTL. Displays it on screen.
4. On the POS PC, double-click the desktop shortcut "Pair Simsim POS Agent". This opens `agentctl pair --code 123456` in a small window.
5. Agent calls cloud `POST /api/pos-agent/pair` with the code. Cloud validates and returns `{ terminal_id, terminal_token, store_id }`.
6. Agent encrypts and stores secrets via DPAPI. Restarts the local HTTP server with paired state. Prints a confirmation receipt automatically (great signal that everything works).

### 8.2 Cloud-side endpoints (must be added to Simsim Next.js)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/api/pos-agent/pairing-codes` | Retailer session (existing Spec 1 token) | Generate pairing code for a `terminal_id` owned by the user's store |
| `POST` | `/api/pos-agent/pair` | None (the code is the auth) | Exchange pairing code → terminal token |
| `POST` | `/api/pos-agent/heartbeat` | Terminal token | Update last-seen, agent version, printer status |
| `POST` | `/api/pos-agent/telemetry` | Terminal token | Append error/event records |
| `GET` | `/api/pos-agent/release/latest?channel=stable` | Terminal token | Latest version metadata + signed download URL |
| `POST` | `/api/pos-agent/unpair` | Terminal token | Revoke this terminal token |

#### Pairing code generation
- 6-digit numeric, generated with `crypto/rand`. Padded.
- Stored hashed (SHA-256) — never plaintext. Don't display the same code twice on regenerate.
- TTL: 15 minutes, enforced server-side on consume.
- Single-use — marked consumed on first successful exchange.
- Bound to one `terminal_id` and one `store_id`. Mismatched terminal → fail.
- Rate limit: 5 codes per terminal per hour; 20 attempts per IP per hour for `/pair`.

#### Terminal token
- Opaque, 32 bytes from `crypto/rand`, base64url encoded.
- Stored hashed in DB; only revealable once on `/pair` response.
- Bound to `(terminal_id, store_id)`.
- Revocable via `/unpair` or admin action. Revoked tokens fail `401` on all cloud calls.

### 8.3 Re-pairing
If the agent is reset (`agentctl unpair`), or if the cloud revokes the token, the agent enters unpaired state and stops accepting `/print` requests. Operator must repeat the pairing flow.

---

## 9. Auto-update (self-update)

### 9.1 Release artifact
Each release produces:
- `simsim-pos-agent_<version>_windows_amd64.exe` — the agent binary.
- `simsim-pos-agent_<version>_windows_amd64.exe.sha256` — SHA-256 of the binary.
- `simsim-pos-agent-installer_<version>.exe` — Inno Setup installer (for first install / manual reinstall).

Hosted under `https://<cloud>/api/pos-agent/release/<version>/<artifact>`. Signed download URLs (HMAC, 5-minute TTL) returned via `/release/latest`.

### 9.2 Update flow (binary swap)
1. Once per day (`release_check_seconds`), the agent calls `/api/pos-agent/release/latest`. Cloud returns `{ version, sha256, download_url, mandatory: bool }`.
2. If newer than current and conditions allow (see 9.3), download to `%ProgramData%\Simsim\POSAgent\update\new.exe`.
3. Verify SHA-256.
4. Atomic swap:
   - Rename current `agent.exe` → `agent.exe.old`.
   - Rename `new.exe` → `agent.exe`.
   - Tell `kardianos/service` to restart the service.
5. On next start, if `agent.exe.old` exists and current version matches the expected new version, delete `agent.exe.old`. Otherwise, roll back.

### 9.3 Update conditions
- No print job in the last 60 seconds.
- No queued telemetry that's currently being flushed.
- Time of day between 03:00 and 05:00 local — avoids surprise restarts during a sale. Mandatory updates ignore this window.
- Skip updates if disk free < 200MB.

### 9.4 No code signing for pilot
First releases will be unsigned. Inno Setup installer will trigger Windows SmartScreen warning ("Unrecognized app"). Operator clicks "More info → Run anyway". Document this in the install instructions. Acquire EV cert before broader rollout (separate workstream — flagged in pre-launch ops).

---

## 10. Telemetry, heartbeat, error reporting

### 10.1 Outbox pattern
All events written to a local SQLite table `outbox`:

```sql
CREATE TABLE outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,            -- 'heartbeat' | 'telemetry'
  payload BLOB NOT NULL,         -- JSON
  created_at INTEGER NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  last_attempt_at INTEGER,
  last_error TEXT
);
CREATE INDEX outbox_kind_created ON outbox(kind, created_at);
```

A flusher goroutine pulls oldest-first, posts to cloud, deletes on success. Exponential backoff per event up to 1h cap. Heartbeats older than 24h are dropped (stale). Telemetry never expires.

### 10.2 Heartbeat
Every `heartbeat_seconds` (default 300):

```json
{
  "agent_version": "1.0.0",
  "os_version": "Windows 11 23H2",
  "machine_id": "stable-hash",
  "uptime_seconds": 12345,
  "printer": { "configured": true, "reachable": true, "last_error": null },
  "queue_depth": { "outbox": 0 },
  "last_print_at": "2026-04-28T14:32:11Z"
}
```

Cloud stores `last_seen_at` per terminal, used by the brand admin / retailer dashboards to show "POS online / offline".

### 10.3 Telemetry events
Structured event with `code`, `severity`, `context`. Examples:
- `PRINT_OK` (info) — successful print.
- `PRINT_FAILED` (error) — printer offline / spooler returned error.
- `DRAWER_OPENED` (info).
- `PAIRED` / `UNPAIRED` (info).
- `UPDATE_APPLIED` (info, with from/to versions).
- `UPDATE_FAILED` (warn).
- `PANIC` (critical) — Go panic recovered in handler.

No PII. No receipt contents. Just metadata.

### 10.4 Local logging
Rotating file logs at `%ProgramData%\Simsim\POSAgent\logs\agent.log`. 10MB per file, keep 5 files. Default level `info`; `agentctl set-log-level debug` for support.

---

## 11. Installer (Inno Setup)

`installer/simsim-pos-agent.iss` produces `simsim-pos-agent-installer_<version>.exe`.

Steps the installer performs:
1. Install binary to `%ProgramFiles%\Simsim\POSAgent\agent.exe`.
2. Create `%ProgramData%\Simsim\POSAgent\` (config, secrets, logs, outbox.db).
3. Install `agentctl.exe` and add to system PATH.
4. Register the Windows service via `agent.exe service install`.
5. Start the service.
6. Create desktop shortcut "Pair Simsim POS Agent" → runs `agentctl pair` interactively.
7. Create Start Menu folder with: Pair, Unpair, Status, Open Logs, Uninstall.
8. Pre-flight check: confirm at least one printer is installed in Windows; warn if none. (Do not auto-install printer drivers — out of scope.)

Uninstaller: stops service, unregisters service, removes binaries. **Leaves** `%ProgramData%\Simsim\POSAgent\` (logs, outbox) by default; offers "remove all data" checkbox for full wipe.

---

## 12. POS web app integration

These changes happen in the `karimkheirat/simsim` Next.js repo, alongside the cloud API endpoints.

### 12.1 Discovery & status indicator
- New module `lib/pos-agent.ts` — typed client for the local agent.
- On `/pos` mount, poll `http://127.0.0.1:47291/health` every 5s.
- Show a small status pill: green "POS connecté", amber "POS hors ligne", red "Mauvais terminal" (mismatch between agent's `terminal_id` and the cashier's bound terminal).
- Cache the last-known state in Dexie so we don't spin a red pill during transient network blips.

### 12.2 Print on payment
- Existing payment success handler queues a print job.
- Generate `job_id = uuid()`, persist to Dexie before calling the agent.
- On agent error, mark the job as "needs reprint" and surface a "Reimprimer le ticket" button on the order detail screen.
- On agent success, mark job as printed; the agent's idempotency window protects against double-print.

### 12.3 Pairing UI
- `/retailer/settings/pos-terminals` — add "Appairer un appareil" button per terminal row.
- Modal shows the 6-digit code with a 15-minute countdown.
- Below the code: copy-to-clipboard, link to install instructions PDF, and a live "Appareil détecté ✓" indicator that turns green once the cloud sees the heartbeat from the newly paired agent.

### 12.4 UI design authority
All UI for §12.1 and §12.3 (status pill, pairing modal, error states) goes through Replit per Karim's design authority rule before implementation. Spec files: `REPLIT_POS_AGENT_STATUS_V1.md`, `REPLIT_POS_AGENT_PAIRING_V1.md`. Implementation does not start on these screens until Replit specs land. **The Go agent build does not block on this** — it can ship with `agentctl pair --code XXXXXX` from CLI.

---

## 13. Build & release pipeline

### 13.1 Local dev
```powershell
# from WSL or Windows
go build -o build/agent.exe ./cmd/agent
go build -o build/agentctl.exe ./cmd/agentctl
```

### 13.2 GitHub Actions (`.github/workflows/release.yml`)
Triggers on tag `v*.*.*`:
1. Checkout, set up Go.
2. Run `go vet ./...` and `go test ./...`.
3. Cross-compile for `windows/amd64` with `-trimpath` and `-ldflags "-s -w -X main.version=$TAG"`.
4. Compute SHA-256.
5. Run Inno Setup compiler on `windows-latest` runner.
6. Create GitHub Release; upload `agent.exe`, `agent.exe.sha256`, `installer.exe`.
7. POST release metadata to Simsim cloud (`/api/internal/pos-agent/releases`, admin token in repo secrets) so the agent's `/release/latest` endpoint returns the new version.

### 13.3 Versioning
SemVer. Pilot starts at `0.1.0`. First production-ready release is `1.0.0`.

---

## 14. Testing

### 14.1 Unit tests (Go)
- `internal/receipt`: golden-file tests — render a known receipt, compare to a checked-in `.bin` of expected ESC/POS bytes.
- `internal/escpos`: command builders.
- `internal/api`: handler tests with `httptest`. Cover auth, CORS, idempotency, rate limits.
- `internal/outbox`: enqueue/flush/backoff with an in-memory SQLite.
- `internal/pairing`: state machine.
- `internal/updater`: swap + rollback with a fake binary.

Target: 70%+ on `internal/`. CI gates on `go test ./...` passing.

### 14.2 Hardware tests (at the pilot store)
Karim has confirmed we test on the printer at the retailer's site. Required test sessions:

**Session A (first hardware contact, ~2h on-site or remote-relayed):**
1. Install printer in Windows. Confirm it appears in `Devices and Printers`.
2. Run agent in foreground (`agent.exe run --console`).
3. `agentctl test-print` — confirm a built-in self-test receipt prints cleanly.
4. Connect cash drawer to DK port. `agentctl drawer-open` — confirm it opens.
5. Pair against a staging terminal record. Confirm heartbeat appears in cloud DB.
6. Print a real-shaped receipt via `/print`. Confirm layout, character set, alignment, cut.

**Session B (integration, ~4h):**
1. Run actual `/pos` checkout against the agent. End-to-end: scan barcode (SP-8500 HID — should "just work"), add to cart, pay, print, drawer opens.
2. Yank the network cable. Confirm `/pos` and printing still work fully offline.
3. Reconnect. Confirm outbox flushes and heartbeat catches up.
4. Force-kill the agent. Confirm Windows service restarts it within ~30s. Confirm `/pos` shows amber pill and recovers.

**Session C (soak, 24–48h):**
- Leave running. Simulate a cashier shift: ~50 receipts spaced across the day, paper-swap mid-shift, one forced reboot.
- Review logs and telemetry the next day.

### 14.3 What we cannot test until on-site
- Real ESC/POS dialect quirks of the SP-331 (cut command variations, codepage support, drawer pulse timing).
- Whether the manufacturer's Windows driver does raw passthrough cleanly or mangles bytes — fallback is "Generic / Text Only" driver; document both paths.
- Arabic rendering (deferred — see §16).

---

## 15. Security considerations

- **Listen on loopback only.** Hard-coded; not configurable.
- **Token-protected** local API. Constant-time comparison.
- **CORS allowlist** — never `*`.
- **Mixed-content rule:** browsers treat `http://127.0.0.1` as a secure origin so HTTPS pages can call it without warnings. Verified pattern (Stripe Terminal, Discord, Spotify Web).
- **DPAPI machine-scope** for the terminal token. Anyone with admin on the box can read it; this is acceptable. The token only authorizes that one terminal.
- **Token revocation:** cloud `/unpair` invalidates server-side; agent gets `401` on next call and goes to unpaired state.
- **Pairing code** is single-use, 15-min TTL, hashed at rest, rate-limited.
- **No PII in telemetry.** No receipt bodies, no customer info, no item-level data leaves the agent.
- **Update verification:** SHA-256 from cloud must match downloaded binary. Future: cosign-style signature verification when EV cert is in place.
- **No remote code execution surface.** The agent does not accept arbitrary commands — only the fixed endpoint set above.

---

## 16. Open decisions for Karim

These need a yes/no before or during build. None of them block starting M1.

| # | Decision | Recommendation |
|---|---|---|
| 1 | **Pilot receipt language** — French-only, or French + Arabic? Arabic requires bitmap rendering (raster image to printer); ~2 extra days of work and significant testing risk. | French-only for v1 pilot. Arabic in a v1.1 release after the pilot proves the printing path. |
| 2 | **Receipt legal content for Algeria** — Are NIF / RC / ART numbers required on the printed ticket? VAT lines? | Need authoritative answer from the retailer or accountant. Add as fields in the receipt model, leave blank for v1 if confirmed unnecessary. |
| 3 | **Cash drawer kick policy** — Always open on cash payment; never open on card; manual button for "no sale" drawer opens? | Cash → auto-open; card → no open; manual button visible only for users with the "no-sale" permission flag. Wire the policy into POS web app, not the agent. |
| 4 | **Listen port `47291`** — fixed, OK? Or expose for IT customization? | Fixed. Customizing it adds support burden. Document it. |
| 5 | **Unsigned installer at pilot** — accept SmartScreen "Run anyway" UX, or block pilot on EV cert? | Accept unsigned for pilot. EV cert is a 2–4 week procurement, not worth the delay. |
| 6 | **Cloud DB schema additions** — new tables `pos_agent_pairing_codes`, `pos_agent_heartbeats`, `pos_agent_telemetry`, `pos_agent_releases`, plus columns on existing `Terminal` table for `last_seen_at`, `agent_version`. Approve before Mohamed sees the migration. | Approve as outlined; Mohamed reviews migration. |

---

## 17. Milestones & acceptance criteria

Each milestone ends with a runnable, demonstrable artifact. Karim approves before moving on.

### M1 — Skeleton + USB print path (Days 1–2)
- Repo scaffolded per §4.
- `agent.exe run --console` starts a local HTTP server.
- `POST /print` with a hardcoded test receipt → printer prints (tested against SP-331 at retailer, or a substitute).
- `POST /drawer/open` → drawer opens (or printer fires the pulse audibly if no drawer yet).
- ✅ **Acceptance:** receipt prints from `curl` on the pilot machine.

### M2 — Pairing + Windows service (Days 3–4)
- `agent.exe service install / start / stop / uninstall` works.
- Service auto-starts on boot.
- Cloud endpoints `/pairing-codes` and `/pair` deployed to staging.
- `agentctl pair --code XXXXXX` exchanges and stores token via DPAPI.
- Authenticated `/print` from the staging POS web app prints successfully.
- Terminal binding (Spec 4) page shows the agent's `last_seen_at`.
- ✅ **Acceptance:** can install service from binary, pair from CLI, print from web POS.

### M3 — Telemetry, heartbeat, drawer policy (Days 5–6)
- SQLite outbox + flusher.
- Heartbeat every 5 min; telemetry events on print success/failure.
- Cloud-side telemetry endpoint deployed; events visible in admin DB.
- Cash drawer policy wired into POS web app (cash auto-open, card no-open).
- POS status pill (green/amber/red) shipped behind a feature flag.
- ✅ **Acceptance:** disconnect network mid-shift, reconnect, all events flush; status pill behaves correctly.

### M4 — Auto-update + installer (Days 7–8)
- Inno Setup installer builds and runs cleanly on a fresh Windows VM.
- GitHub Actions release pipeline produces and uploads artifacts.
- Self-update flow tested: install `0.9.0`, publish `0.9.1`, agent updates within the maintenance window, rollback works on forced failure.
- ✅ **Acceptance:** uninstall agent, run installer, pair, print. Then trigger an update and confirm zero cashier intervention.

### M5 — Pilot integration & polish (Days 9–10)
- POS web app pairing modal (with Replit spec sign-off).
- Replit-specced status pill polished and out of feature flag.
- 24h soak test on pilot machine.
- README, install instructions PDF (FR), runbook (what to do if the agent is offline / printer is offline / re-pair needed).
- ✅ **Acceptance:** Karim runs an end-to-end shift simulation at the pilot store and signs off.

---

## 18. Out of scope (explicit)

To prevent scope creep mid-build, the following are **not** in this spec and require a separate spec:

- Price checker screens (in-aisle).
- Scales (weighed produce).
- Network-only or Serial-only printer transports.
- Multiple printers per terminal (kitchen ticket + customer ticket).
- Receipt email / SMS / PDF copies.
- Arabic receipt rendering (raster path).
- Mac or Linux POS hosts.
- Any administrative UI beyond the existing `/retailer/settings/pos-terminals` and the new pairing modal.
- Cosign / EV-signed releases.
- Multi-tenancy on a single PC (one agent = one terminal).

---

## 19. Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| SP-331 driver mangles raw bytes through Windows spooler | Medium | High | Document fallback to "Generic / Text Only" driver; validate in M1 |
| Cash drawer model unknown — DK pulse timing differs | Low | Low | ESC/POS pulse parameters are configurable per drawer; default works for ~95% |
| Antivirus flags unsigned `agent.exe` | Medium | Medium | Whitelisting instructions in install doc; EV cert in next phase |
| Windows Update reboots the PC mid-shift | Low | Medium | Service auto-starts on boot; cashier sees "POS hors ligne" pill briefly |
| Pairing code intercepted | Very low | Medium | 15-min TTL, single-use, rate-limited, hashed at rest |
| Operator pairs to wrong terminal | Medium | Low | POS verifies `terminal_id` from `/health` matches logged-in terminal; shows red "Mauvais terminal" pill |
| Disk full during update download | Low | Low | Pre-check 200MB free; abort cleanly |
| `outbox.db` corruption | Very low | Low | SQLite WAL mode; on fatal error, rename to `.corrupt` and start fresh |

---

## 20. Glossary

- **Agent** — the Go service running on the cashier's PC.
- **Terminal** — a Spec 4 entity in Simsim representing one POS register, bound to one store.
- **Terminal token** — opaque 32-byte secret that authenticates one agent to the cloud and authorizes the local POS web page to talk to the agent.
- **Pairing code** — short-lived 6-digit human-typed code that bootstraps a terminal token.
- **Outbox** — local SQLite queue of events to send to cloud when online.
- **DK port** — the printer's RJ11 connector for the cash drawer kick.
- **DPAPI** — Windows Data Protection API; OS-managed encryption for secrets at rest.

---

**End of spec.** Approve §16 decisions, hand to Claude Code, start M1.
