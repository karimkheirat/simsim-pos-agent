; Simsim POS Agent — English custom messages overlay.
;
; Stock English wizard messages come from compiler:Default.isl (bundled
; with Inno Setup). This file only carries app-specific [CustomMessages]
; referenced by our .iss files via {cm:KeyName}.

[CustomMessages]
; --- AG2: post-install [Run] status bar ---
RunStatusServiceInstall=Installing Windows service...
RunStatusServiceStart=Starting service...

; --- AG3: printer picker page ---
PrinterPickerCaption=Printer selection
PrinterPickerSubcaption=Select the thermal receipt printer connected to this computer.
PrinterPickerLabel=Receipt printer:
PrinterPickerNonePresent=No printer detected on this computer. The agent will be installed without a configured printer. You can add one later by editing C:\ProgramData\Simsim\POSAgent\config.json.
PrinterPickerDetectionFailed=Could not detect installed printers (PowerShell failed). The agent will be installed without a configured printer. You can add one later by editing C:\ProgramData\Simsim\POSAgent\config.json.

; --- AG4: pair code entry page ---
PairCodeCaption=Pair this register
PairCodeSubcaption=Enter the code generated from the Simsim dashboard.
PairCodeLabel=On another computer, open Simsim in your browser and generate a pairing code for this register:%nhttps://web-production-6bb4d.up.railway.app/fr/retailer/settings/pos-terminals
PairCodeInputLabel=Pairing code (6 digits):
PairCodeSkipCheckbox=I'll pair this register later
PairCodeValidationError=The code must be exactly 6 digits.
