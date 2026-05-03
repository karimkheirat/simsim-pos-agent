; Simsim POS Agent — Arabic custom messages overlay.
;
; Stock Arabic wizard messages come from compiler:Languages\Arabic.isl
; (bundled with Inno Setup), which already declares RightToLeft=yes.
; The override here is for app-specific [CustomMessages] only.
;
; AG2: keys defined for the post-install [Run] status bar. Real Arabic
; translations land in AG3/AG4 alongside the wizard pages — for now the
; values are French copies so the build doesn't fail on missing keys
; and the cm: lookups resolve to *something* operator-readable.

[LangOptions]
; Redundant with stock Arabic.isl but explicit for clarity — confirms
; the RTL layout reaches our custom strings unchanged.
RightToLeft=yes

[CustomMessages]
; --- AG2: post-install [Run] status bar (translation pending) ---
RunStatusServiceInstall=Installation du service Windows...
RunStatusServiceStart=Démarrage du service...

; --- AG3 placeholder (printer picker) ---
; PrinterPickerPageTitle=...
; PrinterPickerPageDescription=...

; --- AG4 placeholder (pair code entry) ---
; PairCodePageTitle=...
; PairCodePageDescription=...
; PairCodeInvalidFormat=...
