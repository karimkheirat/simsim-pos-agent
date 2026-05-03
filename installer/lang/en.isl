; Simsim POS Agent — English custom messages overlay.
;
; Stock English wizard messages come from compiler:Default.isl (bundled
; with Inno Setup). This file only carries app-specific [CustomMessages]
; referenced by our .iss files via {cm:KeyName}.

[CustomMessages]
; --- AG2 + AG5: post-install [Run] status bar ---
RunStatusWriteConfig=Writing configuration...
RunStatusServiceInstall=Installing Windows service...
RunStatusServiceStart=Starting service...
RunStatusPair=Pairing this register...

; --- AG5: success page customization ---
; %1 = terminal_label, %2 = store_name
InstallSuccessPaired=✓ %1 (%2) is connected.
InstallSuccessSkipped=Installation complete. Run 'agentctl pair' to pair this register.
; %1 = cloud-supplied reason (or PairFailureReasonUnknown fallback);
; %2 = the 6-digit code the operator entered (so they can retry without re-typing)
InstallSuccessPairFailed=Installation complete, but pairing failed: %1. Run 'agentctl pair --code %2' from a command prompt to retry.
PairFailureReasonUnknown=unknown reason

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
