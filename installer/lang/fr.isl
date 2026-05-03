; Simsim POS Agent — French custom messages overlay.
;
; Stock French wizard messages (Welcome, License, etc.) come from
; compiler:Languages\French.isl — bundled with Inno Setup. This file
; only carries app-specific [CustomMessages] referenced by our .iss
; files via {cm:KeyName}.
;
; AG2 wires the {cm:RunStatusServiceInstall} and {cm:RunStatusServiceStart}
; status messages used during the post-install [Run] sequence. AG3 will
; add printer-picker page strings; AG4 will add pair-code page strings.

[CustomMessages]
; --- AG2: post-install [Run] status bar ---
RunStatusServiceInstall=Installation du service Windows...
RunStatusServiceStart=Démarrage du service...

; --- AG3 placeholder (printer picker) ---
; PrinterPickerPageTitle=...
; PrinterPickerPageDescription=...

; --- AG4 placeholder (pair code entry) ---
; PairCodePageTitle=...
; PairCodePageDescription=...
; PairCodeInvalidFormat=...
