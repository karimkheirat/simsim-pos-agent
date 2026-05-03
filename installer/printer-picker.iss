; printer-picker.iss — populated by AG3.
;
; Will provide:
;   - A custom wizard page that lists installed Windows printers
;     (enumerated via WMI / sc / a tiny Pascal helper using
;      GetPrintersA from winspool.drv).
;   - Operator selects one; selection is written to config.json's
;     printer_name field via [INI] section or {app}\config.json
;     post-install patch.
;
; This file is intentionally empty in AG2 — its existence reserves the
; filename and surfaces in the directory listing as a placeholder. The
; main installer.iss does NOT #include it yet; that hook lands in AG3.
