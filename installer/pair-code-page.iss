; pair-code-page.iss — populated by AG4.
;
; Will provide:
;   - A custom wizard page that prompts for the 6-digit pairing code
;     generated from the retailer's web UI.
;   - Field validation: exactly 6 ASCII digits (mirrors
;     cmd/agentctl/pair.go's validatePairingCode).
;   - On installer finish: the entered code is passed to
;     `agentctl pair --code XXXXXX` (added to [Run] in AG4 alongside
;     the service install/start sequence).
;
; This file is intentionally empty in AG2 — placeholder, not yet
; #included from installer.iss.
