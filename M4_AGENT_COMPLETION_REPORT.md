# M4 Agent Completion Report

**Date:** 2026-05-03
**Branch:** main
**Tag at verification:** `v0.3.3` (commit `91e3a9d`)
**Verifier:** Karim Kheirat ran the SmartScreen / UAC / wizard / SCM / web UI flows on the pilot Windows host. Claude Code (agent session) ran all build / iscc / git / curl / agentctl flows from a non-elevated Bash shell on the same host.
**Cloud counterpart:** `https://web-production-6bb4d.up.railway.app` (M2-era endpoints; the M4 cloud half — release feed + in-app download — is not yet built; see "Cross-half status" below).

**Result: PASS on the agent half.** AG1–AG9 ✅. M4 cloud half (CL1–CL3) is **pending separate work** in the simsim repo.

---

## Per-sub-task results

### AG1 — Build-injected `Version` constant + `--version` flag

**Status:** ✅ pass
**Commit:** `4fe8a24` — *feat: wire build-injected Version constant + --version flag*

`var Version = "dev"` declared at package main (capitalized for `-ldflags` reach). Top-level `--version` flag handled before subcommand dispatch via `flag.NewFlagSet("agent-top", flag.ContinueOnError)`. `config.Defaults().Version` left as informational baseline (build-time `Version` overrides — comment added inline).

Verified in v0.3.3 install: `agentctl status` reported `Version 0.3.3`, browser dashboard pill showed `EN LIGNE v0.3.3` — confirming the `-X main.Version=0.3.3` injection in the GitHub Actions build flowed through to runtime and into the heartbeat payload.

### AG7 — `service uninstall` stops a running service first

**Status:** ✅ pass
**Commit:** `c900143` — *fix: stop running service before uninstall*

The M2 wart documented in `M2_AGENT_COMPLETION_REPORT.md` Step 14: `Uninstall` was a thin wrapper around `ksvc.Control(svc, "uninstall")` and would orphan a running process. Refactored to `uninstallWithDeps` taking `statusImpl` + `stopWithTimeout` (10s); on `running`, stops first, then uninstalls. 6 unit tests in [internal/service/uninstall_test.go](internal/service/uninstall_test.go) cover the running / stopped / status-error / stop-failure / stop-timeout / uninstall-failure matrix.

Verified during AG9 uninstall: `Settings → Apps → Uninstall` cleanly removed the service (subsequent `sc query SimsimPOSAgent` returned exit 1060 — service does not exist) with no orphan process held against the named mutex.

### AG2 — Inno Setup project scaffold

**Status:** ✅ pass
**Commit:** `3b3c0d7` — *feat: bootstrap Inno Setup installer scaffold*

[installer/installer.iss](installer/installer.iss) with locked AppId GUID `{FEFAA9FE-0F3B-4357-B1B0-F0F67D343398}` (immutable across releases — changing it would orphan upgrades). Tri-language overlay files at [installer/lang/fr.isl](installer/lang/fr.isl), [installer/lang/ar.isl](installer/lang/ar.isl), [installer/lang/en.isl](installer/lang/en.isl), each wired via comma-separated `MessagesFile:` (stock `compiler:Languages\X.isl` + custom overlay). Architecture identifier `x64os` (Inno 6.3+ deprecated `x64`). DefaultDirName `{commonpf}\Simsim\POSAgent` — Program Files, fixing the M2 user-profile-path discovery from A8 Step 3.

Verified by Karim launching the wizard end-to-end in v0.3.3 with the English-locale auto-detection picking the right MessagesFile chain.

### AG3 — Printer detection wizard page

**Status:** ✅ pass
**Commit:** `fa465a1` — *fix: include AG3 printer-picker.iss content (was omitted from AG4 commit)*

Note: the AG3 file content was inadvertently bundled into the AG4 commit (`35e36c4`); `fa465a1` is the explicit fix-up that brings the file under git tracking with an explanatory message. Forward-only history per Karim's preference (no rebase). [installer/printer-picker.iss](installer/printer-picker.iss) is pure Pascal, `#include`'d into the parent `[Code]` section, comments use `//` not `;` (the AG3 lesson logged inline). Three states from PowerShell `Get-Printer` enumeration: detection-failed warning, no-printers warning, dropdown with heuristic-default selection (POS / Receipt / TM- / SP- / Star / Epson / Citizen / Bixolon).

Verified during AG9 in v0.3.3: page rendered, populated from real Windows printer list, "Microsoft Print to PDF" auto-selected via the heuristic fallback (no thermal printer attached to Karim's laptop), Next advanced cleanly.

### AG4 — Pair-code-entry wizard page

**Status:** ✅ pass
**Commit:** `35e36c4` — *feat: pairing code entry wizard page*

[installer/pair-code-page.iss](installer/pair-code-page.iss): 6-digit `TNewEdit` (MaxLength=6), "I'll pair later" checkbox, inline red `clRed` validation error label, page lifecycle hooks (validation on Next, error reset on page activate, input disable when skip checked). Pre-AG5 lesson recorded inline: `TNewEdit.SetFocus` is not exposed in Inno's PascalScript subset, so focus-restore on validation error was dropped — the visible error label is the primary feedback.

Validation matrix verified by Karim during AG4 dry-run (6 cases, all green): empty / 5-digit / 7-digit / non-digit / valid 6-digit / skip-checked. Verified in v0.3.3 end-to-end with a real cloud-issued code accepted on first try.

### AG5 — `agent write-config` + installer `[Run]` sequence + success-page customization

**Status:** ✅ pass
**Commit:** `6be99bf` — *feat: AG5 install + service registration + pair flow*

Load-bearing sub-task. Three pieces:

1. **`agent write-config` subcommand** ([cmd/agent/write_config.go](cmd/agent/write_config.go)) — takes `--config / --printer / --cloud-base-url`. Empty-string overrides preserve existing values (re-run-safe). Always re-injects build-time `Version`. Atomic write via exported `config.WriteAtomic` (lifted from `secrets.go` for reuse). 6 in-process tests covering fresh / preserve-unrelated / empty-override-preserve / atomic / parent-dir-create / validation-error.

2. **Installer `[Run]` sequence** ([installer/installer.iss:67-98](installer/installer.iss)) — three blocking steps with localized status messages: `write-config` → `service install` → `service start`. Cancel triggers Inno's auto-rollback. The pair step is intentionally **not** in `[Run]` — it lives in `CurStepChanged(ssPostInstall)` so failures warn-and-continue rather than rolling back (per spec §3.7 partial-install case).

3. **Success-page customization** — `runPairStep` execs `agentctl pair --code XXXXXX`, captures stdout to `{tmp}\pair.txt`, parses the M2 success-block format (`Magasin :` / `Caisse :`) for the success label OR the error format (`Erreur:`) for the failure label. Pin test added in [cmd/agentctl/pair_test.go](cmd/agentctl/pair_test.go) `TestPairOutput_PinsInstallerParserContract` to lock the literal substrings the installer parses — converts silent contract drift into detected drift.

The success-page rendering itself shipped broken in v0.3.0–v0.3.2; finally working in v0.3.3 (see "AG9 — and the four-tag wpFinished saga" below). End-to-end exercised on Karim's laptop in v0.3.3: write-config wrote `printer_name + cloud_base_url + version`, service installed and started, pair succeeded, success page rendered.

### AG6 — Uninstaller keep-data prompt

**Status:** ✅ pass
**Commit:** `60a748a` — *feat: AG6 uninstaller keep-data prompt*

`InitializeUninstall` calls `TaskDialogMsgBox` with custom button labels `Conserver` / `Supprimer` (localized via `{cm:UninstallKeepDataYes}` / `{cm:UninstallKeepDataNo}`). Default-safe (operator dismissal → `KeepData = True`). `CurUninstallStepChanged(usPostUninstall)` recursively `DelTree`s `{commonappdata}\Simsim\POSAgent` only when `KeepData = False`.

`TaskDialogMsgBox`'s `ButtonLabels` parameter is declared as `TArrayOfString` (typed-parameter form), not `array of String` (open-array form). Per the AG9 investigation (see below), the open-array form is what triggers the PascalScript Type Mismatch bug; the typed form accepts variables cleanly. AG6's keep-data prompt is therefore unaffected by the v0.3.0–v0.3.2 wpFinished saga.

Verified during AG9 uninstall: prompt rendered with localized labels, `Supprimer` chosen, recursive cleanup confirmed (`C:\ProgramData\Simsim\POSAgent` removed). Minor wart: parent directory `C:\ProgramData\Simsim` left as empty shell — see "Known issues."

### AG8 — GitHub Actions release pipeline

**Status:** ✅ pass
**Commit:** `c215f2d` — *feat(ci): AG8 — GitHub Actions release workflow*

[.github/workflows/release.yml](.github/workflows/release.yml). Two trigger modes:

- Push of `v*.*.*` tag → full pipeline (build + iscc + optional sign + softprops/action-gh-release with installer attached + `make_latest: true`)
- `workflow_dispatch` (manual) with default `version: 0.0.0-manual` input → build + compile only, no release. Useful for verifying the pipeline without burning a version number.

Single windows-latest job. Build via `actions/setup-go@v5` go-version `'1.22'` + `-ldflags "-X main.Version=…"`. Inno Setup installed via `choco install innosetup --no-progress --yes` (chocolatey is pre-installed on the runner; chosen over `pajk/inno-setup-action` for reliability + zero third-party action surface). iscc invoked via full path with `MSYS_NO_PATHCONV=1` (same Git Bash quirk as local dev). Code signing wired but conditional on `secrets.CODE_SIGNING_CERT_BASE64` presence — skipped under M4 (unsigned per spec §2). `permissions: contents: write` set explicitly (default token is read-only).

Verified across four release tag pushes (v0.3.0 → v0.3.3) — pipeline is reliable, ~2-3 min runtime, GitHub Release creation worked first try with no permissions or token issues.

### AG9 — End-to-end manual verification

**Status:** ✅ pass on v0.3.3 (after the four-tag wpFinished saga)

Karim ran the full operator flow on his Windows pilot host:

1. **File Explorer download** of `simsim-pos-agent-setup-0.3.3.exe` from the GitHub Release page
2. **SmartScreen "unrecognized app"** → `Run anyway` (M4 ships unsigned per spec §2)
3. **UAC accept** → wizard launched
4. **Welcome page** — English locale auto-detected
5. **Printer selection page** — populated from real Windows printers, "Microsoft Print to PDF" auto-selected
6. **Pair code page** — fresh 6-digit code generated at `/retailer/settings/pos-terminals`, typed in, accepted
7. **Install location page** — defaulted to `C:\Program Files\Simsim\POSAgent`
8. **Wizard ran** install + write-config + service install + service start + agentctl pair sequence
9. **Success page** rendered cleanly with the expected label (no Type Mismatch dialog — v0.3.3 fix held)
10. **Finish clicked** without screenshot capture — minor verification gap, non-blocking

Post-install evidence on the pilot host:

| Probe | Result |
|---|---|
| `sc query SimsimPOSAgent` | `STATE: 4 RUNNING` |
| `agentctl status` | `✓ Jumelé` with store name + terminal label + `Version 0.3.3` |
| `curl http://127.0.0.1:47291/health` | `paired:true`, `store_id` + `terminal_id` populated, printer block reachable |
| Browser dashboard | 🟢 `EN LIGNE v0.3.3` for the bound terminal |

This is the load-bearing assertion for M4: a real agent built by GitHub Actions, downloaded by a real user from the public release page, installed by the wizard end-to-end without operator command-line involvement, paired against the deployed cloud, heartbeating live.

**Uninstall verified:**

| Probe | Result |
|---|---|
| Settings → Apps → Simsim POS Agent → Uninstall | UAC accept → wizard ran |
| Keep-data prompt | Rendered with `Conserver` / `Supprimer` labels |
| `Supprimer` chosen | Recursive cleanup ran |
| `sc query SimsimPOSAgent` after uninstall | Exit 1060 — service does not exist |
| `C:\ProgramData\Simsim\POSAgent` after uninstall | Directory removed |
| `C:\ProgramData\Simsim` (parent) | Left as empty shell — minor wart, documented below |

---

## AG9 — and the four-tag wpFinished saga

The success-page rendering shipped broken three times before working in v0.3.3. Captured here in full because the lessons are durable.

| Tag | Commit | What I did | What happened |
|---|---|---|---|
| `v0.3.0` | `c215f2d` (AG8 baseline) | Original AG5 success page using `var Args: array of String; SetArrayLength(Args,2); Args[0]:=...; FmtMessage(s, Args);` | Install end-to-end success, but wpFinished page popped a `Runtime error (at 67:249): Type Mismatch` dialog before Finish |
| `v0.3.1` | `7577b6b` | Renamed local `Args: array of String` → `Args: TArrayOfString` based on rushed armchair diagnosis (~85% confidence) that named vs anonymous types differed | **No-op fix.** `TArrayOfString` is just a type alias for `array of String`. Identical crash on Karim's laptop. |
| `v0.3.2` | `dd72e72` | Real diagnosis after Karim said "stop guessing, investigate properly": web-searched, found RemObjects PascalScript issue #129. Refactored both `FmtMessage` calls to use literal array constructors `[a, b]` at the call site (the only proven-working pattern). | iscc compile failed in CI: `Error on line 263: Invalid section tag.` The `[...]` array literal had landed at the start of a continuation line; Inno's preprocessor reads any line starting with `[` as an INI section header — same footgun documented in AG5. **Did not local-compile before pushing the tag.** |
| `v0.3.3` | `91e3a9d` | Reproduced CI failure locally first, then collapsed both `FmtMessage(...)` calls onto single ~110-char lines so `[...]` never appears at line-start. Local iscc verified clean before push. | Built green, installed clean, success page rendered on first try. |

**Net cost:** 4 release tags, ~30 minutes elapsed, three identical SmartScreen + UAC + wizard cycles for Karim before the working install.

---

## Discipline lessons

Both saved to memory for future installer work, surfaced here for the M4 retro:

### 1. Local iscc compile is mandatory before any release tag with installer changes

Any change to `installer/*.iss` files must be verified by a local `iscc.exe` compile before pushing a release tag:

```bash
MSYS_NO_PATHCONV=1 "/c/Program Files (x86)/Inno Setup 6/iscc.exe" \
  /Q installer/installer.iss /DAppVersion=X.Y.Z-localcheck
```

Clean exit + a `simsim-pos-agent-setup-X.Y.Z-localcheck.exe` artifact is the success signal. v0.3.2 shipped a Pascal preprocessor footgun (line-start `[` misread as section header) that a 5-second local compile would have caught instantly. **Lesson: trust no installer change without a local compile. CI is a backstop, not a verifier.**

### 2. Sub-95% confidence on a fix means research the docs before shipping

v0.3.1 was a no-op rename based on reasoning by codebase convention ("every other function uses `TArrayOfString`, only this one uses `array of String`") with self-rated 85% confidence. The 15% uncertainty was the warning signal. The correct diagnosis was 30 seconds of WebSearch away (RemObjects PascalScript issue #129 — Unicode-mode bug with open-array string params).

**Lesson: when a fix touches an external library / framework / runtime (Inno Setup, kardianos/service, x/sys, etc.), and confidence is below "I have seen the official docs say X", do the research first. Hedging language ("~85%", "matches the pattern", "by convention") is a signal from current-me to do more research before the next tool call.**

---

## Cross-half status

**M4 cloud half (CL1–CL3) is NOT yet built.** Specifically:

- **CL1** — `GET /api/pos-agent/release/latest` endpoint returning the current version + installer download URL is **not implemented** on the cloud side.
- **CL2** — In-app download button at `/retailer/settings/pos-terminals → "Télécharger l'agent"` that pulls from CL1 and serves the installer is **not implemented** on the cloud side.
- **CL3** — Optional in-app version-mismatch banner / nudge on the dashboard is **not implemented** on the cloud side.

For AG9 verification, Karim downloaded `simsim-pos-agent-setup-0.3.3.exe` **directly from the public GitHub Release page** (`github.com/karimkheirat/simsim-pos-agent/releases/tag/v0.3.3`), not through the cloud's in-app flow. The agent half's release pipeline (AG8) produces release artifacts with the correct filename pattern and `make_latest: true`, so CL1/CL2 will have a stable feed to consume when implemented.

**Sign-off scope:** PASS on the agent half only. Cloud half is pending separate work in the simsim repo and does not block agent-side shipping — operators can install today via the manual GitHub Releases path; the in-app flow is operator UX polish.

---

## Inno Setup commercial license

M4 ships under Inno Setup's **non-commercial license**. The commercial license is a one-time $300 purchase from jrsoftware.org. Karim's call: deferred to **post-revenue** — the non-commercial license covers the pilot at the Hamoud Boualem store. Tracked as **pre-launch ops** (must be resolved before the platform is sold to additional retailers commercially).

---

## Known issues

### Empty `C:\ProgramData\Simsim` left after uninstall

AG6's `DelTree` recursively removes `C:\ProgramData\Simsim\POSAgent` when `Supprimer` is chosen, but the parent `C:\ProgramData\Simsim` (now empty) is not removed. Harmless — operators can `rmdir` manually if it bothers them, and a re-install repopulates `Simsim\POSAgent` cleanly. Listed under "Deferred (M5+)" as polish.

### `cmd/agentctl pair` stdout parser is brittle

The installer's `runPairStep` parses agentctl's success stdout with `Pos('Magasin', L)` and `Pos('Caisse', L)` substring matches against the M2 French success block. If those labels change, the success page degrades to "(unknown) is connected." with no crash — but the operator-visible cosmetic is wrong. The pin test in [cmd/agentctl/pair_test.go](cmd/agentctl/pair_test.go) `TestPairOutput_PinsInstallerParserContract` converts silent drift into detected drift, but the right long-term fix is `agentctl pair --output-json` consumed by the installer. Listed under "Deferred (M5+)."

---

## Deferred

### M5+

- **Auto-update flow** — agent self-replacement, polling `/release/latest` (CL1), staged binary swap with SHA-256 verification, restart via kardianos. Spec §3.6.
- **EV code-signing certificate** — eliminates the SmartScreen "unrecognized app" warning. Pre-launch ops, post-revenue.
- **`cmd/agentctl pair --output-json`** — replaces the brittle stdout parser in the installer. M5 polish.
- **Empty `C:\ProgramData\Simsim` parent cleanup** — minor AG6 wart. M5 polish.
- **Real Algerian SMS provider** — for pairing-related operator notifications, if the Spec 4 pairing UX adds SMS confirmation. Pre-launch ops.
- **`simsim.co` domain switch** — production cloud currently at `web-production-6bb4d.up.railway.app`. Pre-launch ops.
- **Native Arabic translation review** — `installer/lang/ar.isl` strings are best-effort flagged for native review. Pre-launch ops.
- **Inno Setup commercial license** — see above. Pre-launch ops, post-revenue.

### Cloud half (separate session in simsim repo)

- **CL1** — `GET /api/pos-agent/release/latest` endpoint
- **CL2** — In-app download button on the pos-terminals settings page
- **CL3** — Optional in-app version-mismatch banner

---

## Code state summary

**Branch:** `main`, in sync with `origin/main` at `91e3a9d`.

**M4 commits (v0.2.x → v0.3.3, in chronological order):**

```
4fe8a24 feat: wire build-injected Version constant + --version flag        (AG1)
c900143 fix: stop running service before uninstall                          (AG7)
3b3c0d7 feat: bootstrap Inno Setup installer scaffold                       (AG2)
35e36c4 feat: pairing code entry wizard page                                (AG4)
fa465a1 fix: include AG3 printer-picker.iss content                         (AG3 fix-up)
6be99bf feat: AG5 install + service registration + pair flow                (AG5)
60a748a feat: AG6 uninstaller keep-data prompt                              (AG6)
c215f2d feat(ci): AG8 — GitHub Actions release workflow                     (AG8)
615a590 Merge pull request #1 from karimkheirat/claude/m4-installer         (PR merge)
7577b6b fix(installer): use TArrayOfString in buildSuccessMessage           (v0.3.1, no-op)
dd72e72 fix(installer): use literal array constructors in FmtMessage calls  (v0.3.2, broken by line-start [)
91e3a9d fix(installer): keep FmtMessage literal arrays on same line as call (v0.3.3, working fix)
```

**Tags pushed:** `v0.3.0`, `v0.3.1`, `v0.3.2`, `v0.3.3`. `v0.3.0`–`v0.3.2` are historical artifacts of the wpFinished saga; **`v0.3.3` is the canonical M4 release** for any operator install.

**New files:**

| File | Purpose |
|---|---|
| `.github/workflows/release.yml` | AG8 release pipeline |
| `installer/installer.iss` | AG2/AG5/AG6 main Inno Setup script |
| `installer/printer-picker.iss` | AG3 included Pascal page |
| `installer/pair-code-page.iss` | AG4 included Pascal page |
| `installer/lang/fr.isl` | French custom messages overlay |
| `installer/lang/en.isl` | English custom messages overlay |
| `installer/lang/ar.isl` | Arabic custom messages overlay (best-effort, native review pending) |
| `cmd/agent/write_config.go` | AG5 `write-config` subcommand |
| `cmd/agent/write_config_test.go` | 6 in-process tests for write-config |
| `internal/service/uninstall_test.go` | AG7 stop-before-uninstall test matrix |

**Dependencies:** unchanged from M2. Go 1.22 floor maintained throughout (no kardianos/x/sys bumps).

---

## Sign-off

**Agent half: PASS.** All M4 sub-tasks (AG1–AG9) completed and verified end-to-end on Karim's pilot Windows host using the GitHub Actions–built v0.3.3 installer downloaded from the public release page.

**Cloud half:** pending separate work (CL1–CL3) in the simsim repo. The release pipeline produces the artifacts the cloud half will consume; agent half does not block operator shipping today via the manual GitHub Releases path.

M4 is shipped on the agent side. Next milestone for the agent: M5 (auto-update flow + agentctl pair JSON output + minor polish). Next milestone for the cloud: CL1–CL3 in-app download flow.
