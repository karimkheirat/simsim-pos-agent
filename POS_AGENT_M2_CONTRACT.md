# POS Agent M2 — Shared Wire Contract

**Status:** authoritative for M2. Treat as immutable during the build.
**Lives in:** both `simsim` and `simsim-pos-agent` repos at the root.
**Purpose:** prevent drift between the two parallel Claude Code sessions building the cloud and agent halves of M2.

If either session needs to deviate from this contract, **stop and surface the question.** Do not pick.

---

## Amendment 1 (post-recon, before C1)

Cloud-session recon against the live `simsim` schema surfaced four discrepancies. These resolutions apply across the contract:

1. **Model name.** This contract refers to the conceptual entity as `Terminal`. The actual Prisma model in `simsim` is `PosTerminal` (table `pos_terminals`). Cloud-side code uses `PosTerminal` / `pos_terminals` everywhere. Agent-facing wire field names (`terminal_id`, `terminal_token`, `terminal_label`) are unchanged.
2. **Heartbeat writes a new column, not the existing `last_seen_at`.** The existing `pos_terminals.last_seen_at` is bumped by POS web app authenticated requests (Spec 4 §6.1) and reflects POS web tab liveness. The agent heartbeat writes to a **new** column `agent_last_seen_at`. The two life-signs stay distinguishable. The C7 status pill reads `agent_last_seen_at`, not `last_seen_at`. The agent-side wire payload does not change — this is purely a cloud-side storage decision.
3. **FK column types use plain `String` (no `@db.Uuid`).** The contract's §6 SQL writes `UUID` for the new tables' FK columns; in practice they must match existing `simsim` PKs which are `String @default(uuid())` (stored as TEXT). The new tables' FK columns are `String` in Prisma; the SQL types are `TEXT`. Migrating existing PKs to `UUID` is out of scope for M2.
4. **No new column-type annotations.** The new schema additions use plain `DateTime` (no `@db.Timestamptz()`) and plain `String` for IPs (no `@db.Inet`), matching existing `simsim` convention. Migrating the schema to richer Postgres types is out of scope for M2.

These resolutions take precedence over the original wording in §6 below.

---

## 1. Base URLs

- **Production:** `https://web-production-6bb4d.up.railway.app`
- **Custom domain (if/when wired):** `https://opensimsim.co`

The agent reads `cloud_base_url` from its `config.json`. M2 uses the Railway URL.

---

## 2. Endpoints (authoritative list for M2)

| # | Method | Path | Auth | Purpose |
|---|---|---|---|---|
| 1 | `POST` | `/api/pos-agent/pairing-codes` | Retailer session cookie (existing Spec 1 token) | Generate a pairing code for a terminal owned by the user's store |
| 2 | `POST` | `/api/pos-agent/pair` | None — the pairing code itself is the credential | Exchange a pairing code for a long-lived terminal token |
| 3 | `POST` | `/api/pos-agent/heartbeat` | Terminal token | Update last-seen + agent state |
| 4 | `POST` | `/api/pos-agent/unpair` | Terminal token | Revoke this terminal's token; agent returns to unpaired state |

Endpoints for telemetry, releases, and admin pairing-code revocation are **out of scope for M2**. M3 adds telemetry; M4 adds releases. Reserve the URL prefix `/api/pos-agent/` for this whole family.

---

## 3. Authentication

### 3.1 Retailer session (endpoint 1)

Standard Spec 1 retailer auth — whatever cookie/token the existing `/retailer/settings/pos-terminals` page already uses to call internal Simsim APIs. Cloud-side: reuse the existing middleware, don't invent a new one.

### 3.2 Terminal token (endpoints 3 and 4)

- Header: `X-Terminal-Token: <token>`
- Token format: 32 random bytes, base64url-encoded (no padding). ~43 ASCII characters.
- Generated on `/pair` exchange, returned **once** in the response, never returned again.
- Stored in the cloud DB hashed with SHA-256 (raw bytes hashed, then hex-encoded for the column). The plaintext is never persisted server-side after the `/pair` response is sent.
- Compared on incoming requests with constant-time comparison against the hash.
- Revocable via `/unpair` (sets `revoked_at`); revoked tokens fail with `401 UNAUTHENTICATED`.

### 3.3 Pairing code (endpoint 2)

- 6 digits, numeric, generated with `crypto/rand` (Go) / `crypto.randomInt(0, 1000000)` (Node) and zero-padded.
- TTL: 15 minutes from issuance, enforced server-side.
- Single-use: marked `consumed_at` on first successful exchange.
- Stored hashed (SHA-256 of the 6-character ASCII string). Plaintext is shown to the operator on screen and never re-displayed.
- Bound to one `terminal_id` and one `store_id` at issuance.

---

## 4. Endpoint contracts

### 4.1 `POST /api/pos-agent/pairing-codes`

**Auth:** retailer session.

**Request body:**
```json
{ "terminal_id": "trm_..." }
```

The session must own the store that owns the terminal. Otherwise `403`.

**Success — `201 Created`:**
```json
{
  "ok": true,
  "data": {
    "code": "428193",
    "expires_at": "2026-05-02T14:15:00Z",
    "terminal_id": "trm_...",
    "store_id": "f0040929-..."
  }
}
```

**Errors:**
- `400 INVALID_REQUEST` — missing or malformed `terminal_id`.
- `403 FORBIDDEN` — terminal not owned by session's store.
- `404 NOT_FOUND` — terminal does not exist.
- `429 RATE_LIMITED` — > 5 codes per terminal per hour.

**Side effects:** invalidates any prior unconsumed code for the same terminal (only one active code per terminal at a time).

---

### 4.2 `POST /api/pos-agent/pair`

**Auth:** none. The code is the credential.

**Request body:**
```json
{
  "code": "428193",
  "agent_version": "0.2.0",
  "machine_id": "stable-hash-of-machine"
}
```

`machine_id` is informational (helps support diagnose "did the agent move PCs"). The server stores it but does not bind security on it.

**Success — `200 OK`:**
```json
{
  "ok": true,
  "data": {
    "terminal_id": "trm_...",
    "terminal_token": "Xj7n...43chars",
    "store_id": "f0040929-...",
    "store_name": "Hamoud Boualem - Centre Oran",
    "terminal_label": "Caisse 1"
  }
}
```

The `terminal_token` is shown only here. The agent must persist it immediately.

**Errors:**
- `400 INVALID_REQUEST` — missing fields.
- `401 INVALID_CODE` — code does not exist, has expired, or has been consumed.
- `429 RATE_LIMITED` — > 20 attempts per IP per hour (covers brute-force on 6-digit space).

**Side effects:** marks the pairing code consumed; creates a `pos_agent_terminal_token` row; sets `terminal.last_paired_at`.

---

### 4.3 `POST /api/pos-agent/heartbeat`

**Auth:** `X-Terminal-Token`.

**Request body:**
```json
{
  "agent_version": "0.2.0",
  "os_version": "Windows 11 23H2",
  "uptime_seconds": 12345,
  "printer": {
    "configured": true,
    "reachable": true,
    "name": "SP-331",
    "last_error": null
  }
}
```

**Success — `200 OK`:**
```json
{ "ok": true, "data": { "received_at": "2026-05-02T14:30:00Z" } }
```

**Errors:**
- `401 UNAUTHENTICATED` — token missing, invalid, or revoked.
- `400 INVALID_REQUEST` — malformed payload.

**Side effects:** updates `pos_terminals.agent_last_seen_at = now()`, `pos_terminals.agent_version`, `pos_terminals.printer_status_json`. Does NOT touch `pos_terminals.last_seen_at` (which is reserved for POS web app liveness per Spec 4 §6.1).

---

### 4.4 `POST /api/pos-agent/unpair`

**Auth:** `X-Terminal-Token`.

**Request body:** empty `{}` accepted; null body accepted.

**Success — `200 OK`:**
```json
{ "ok": true, "data": {} }
```

**Errors:**
- `401 UNAUTHENTICATED` — token missing or already revoked.

**Side effects:** sets `pos_agent_terminal_token.revoked_at = now()`. The agent must clear local secrets after a successful response and return to unpaired state.

---

## 5. Error envelope (canonical)

All errors from `/api/pos-agent/*`:

```json
{
  "ok": false,
  "error": {
    "code": "INVALID_CODE",
    "message": "Code invalide ou expiré"
  }
}
```

`message` is in **French**, suitable for direct display in the agent's CLI / future UI. The `code` is the machine-readable enum below.

### 5.1 Error code enum (M2)

| Code | Use |
|---|---|
| `INVALID_REQUEST` | Malformed payload, missing required field |
| `INVALID_CODE` | Pairing code: nonexistent, expired, or consumed |
| `UNAUTHENTICATED` | Terminal token: missing, invalid, or revoked |
| `FORBIDDEN` | Caller authenticated but lacks permission for this resource |
| `NOT_FOUND` | Resource does not exist |
| `RATE_LIMITED` | Too many requests in the rate window |
| `INTERNAL` | Unhandled server error |

The agent maps these onto its own internal error taxonomy (`internal/cloud/errors.go`).

---

## 6. Database schema additions (cloud-side)

Three new tables, three new columns on the existing `pos_terminals` table.

Per Amendment 1: model is `PosTerminal`; FK columns are `TEXT` (Prisma `String`) to match existing PKs; no `@db.Timestamptz` / `@db.Inet` annotations; heartbeat writes `agent_last_seen_at`, not `last_seen_at`.

```sql
-- 6.1 New table: pairing codes
CREATE TABLE pos_agent_pairing_codes (
  id              TEXT PRIMARY KEY,                                                  -- uuid generated app-side
  terminal_id     TEXT NOT NULL REFERENCES pos_terminals(id) ON DELETE CASCADE,
  store_id        TEXT NOT NULL REFERENCES stores(id)        ON DELETE CASCADE,
  code_hash       BYTEA NOT NULL,                                                    -- sha256(plaintext)
  expires_at      TIMESTAMP NOT NULL,
  consumed_at     TIMESTAMP,
  consumed_by_ip  TEXT,
  created_at      TIMESTAMP NOT NULL DEFAULT now(),
  created_by      TEXT NOT NULL REFERENCES users(id)
);

CREATE INDEX idx_pos_agent_pairing_codes_terminal_active
  ON pos_agent_pairing_codes (terminal_id)
  WHERE consumed_at IS NULL;

-- 6.2 New table: terminal tokens
CREATE TABLE pos_agent_terminal_tokens (
  id              TEXT PRIMARY KEY,                                                  -- uuid generated app-side
  terminal_id     TEXT NOT NULL REFERENCES pos_terminals(id) ON DELETE CASCADE,
  store_id        TEXT NOT NULL REFERENCES stores(id)        ON DELETE CASCADE,
  token_hash      BYTEA NOT NULL UNIQUE,                                             -- sha256(token bytes)
  agent_version   TEXT,
  machine_id      TEXT,
  paired_at       TIMESTAMP NOT NULL DEFAULT now(),
  revoked_at      TIMESTAMP
);

CREATE INDEX idx_pos_agent_terminal_tokens_active
  ON pos_agent_terminal_tokens (terminal_id)
  WHERE revoked_at IS NULL;

-- 6.3 New table: pairing attempt log (for rate limiting + audit)
CREATE TABLE pos_agent_pairing_attempts (
  id           BIGSERIAL PRIMARY KEY,
  ip           TEXT NOT NULL,
  attempted_at TIMESTAMP NOT NULL DEFAULT now(),
  outcome      TEXT NOT NULL                     -- 'success' | 'invalid_code' | 'rate_limited'
);

CREATE INDEX idx_pos_agent_pairing_attempts_ip_time
  ON pos_agent_pairing_attempts (ip, attempted_at DESC);

-- 6.4 Columns added to existing pos_terminals table
-- Note: last_seen_at already exists (Spec 4) and is NOT touched here.
ALTER TABLE pos_terminals
  ADD COLUMN agent_version       TEXT,
  ADD COLUMN agent_last_seen_at  TIMESTAMP,
  ADD COLUMN last_paired_at      TIMESTAMP,
  ADD COLUMN printer_status_json JSONB;
```

The agent never sees these tables directly; they are cloud-side. Listed here so both halves know exactly what state lives where.

---

## 7. Concurrency & idempotency

- Two agents pairing with the same code simultaneously: the second `/pair` request fails with `INVALID_CODE` because the first call set `consumed_at`. Use `UPDATE ... WHERE consumed_at IS NULL RETURNING ...` to make this race-safe.
- A retailer requesting a second pairing code while one is still active: the older code is invalidated (its `consumed_at` is set to a sentinel like `now() - interval '1 day'`, or it's deleted). One active code per terminal at a time.
- Heartbeat is naturally idempotent — it overwrites `last_seen_at`. No special handling needed.

---

## 8. Wire format conventions

- All timestamps in ISO-8601 with timezone (`2026-05-02T14:30:00Z` or `+02:00`).
- All UUIDs in canonical hyphenated form.
- All IDs prefixed where existing entities use prefixes (`trm_`, `usr_`, etc.) — match what `simsim` already emits for terminals.
- All JSON snake_case.
- All POST request bodies are `application/json`. The cloud accepts an empty body or `{}` interchangeably for endpoints that take no parameters.

---

## 9. Open questions deferred to M3+

The following are explicitly **not** in M2 and must not creep in:

- Telemetry events / outbox (M3).
- Auto-update / `/release/latest` endpoint (M4).
- Admin endpoints to revoke a token without going through `/unpair` (M3 or later).
- Push notifications from cloud → agent (out of scope entirely; agent only polls).
- Per-store agent key rotation (out of scope; tokens live until `/unpair` or admin revoke).
- Multi-printer-per-terminal support (out of scope).

---

**End of contract.**
