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

; --- AG6: uninstaller keep-data prompt ---
UninstallKeepDataPrompt=Keep this register's configuration data (config, DPAPI secrets, logs) under C:\ProgramData\Simsim\POSAgent?%n%nClick 'Keep' to preserve them in case of reinstallation, or 'Delete' to remove everything.
UninstallKeepDataYes=Keep
UninstallKeepDataNo=Delete

; --- AG3: printer picker page ---
PrinterPickerCaption=Printer selection
PrinterPickerSubcaption=Select the thermal receipt printer connected to this computer.
PrinterPickerLabel=Receipt printer:
PrinterPickerNonePresent=No printer detected on this computer. The agent will be installed without a configured printer. You can add one later by editing C:\ProgramData\Simsim\POSAgent\config.json.
PrinterPickerDetectionFailed=Could not detect installed printers (PowerShell failed). The agent will be installed without a configured printer. You can add one later by editing C:\ProgramData\Simsim\POSAgent\config.json.

; --- AG4: pair code entry page ---
PairCodeCaption=Pair this register
PairCodeSubcaption=Enter the code generated from the Simsim dashboard.
PairCodeLabel=On another computer, open Simsim in your browser and generate a pairing code for this register:%nhttps://opensimsim.co/fr/retailer/settings/pos-terminals
PairCodeInputLabel=Pairing code (6 digits):
PairCodeSkipCheckbox=I'll pair this register later
PairCodeValidationError=The code must be exactly 6 digits.

; --- M13 print-verification: printer-picker driver advisory ---
; %1 = driver name as reported by Get-Printer.
PrinterPickerDriverWarning=Notice: the current driver for this printer is "%1". For reliable receipt printing, the "Generic / Text Only" driver is recommended. You can proceed; a test print will run at the end of installation.

; --- M13 print-verification: post-pair test-print step ---
RunStatusTestPrint=Printing a test receipt...
TestPrintConfirmTitle=Print verification
; %1 = printer name, %2 = driver name
TestPrintConfirmBody=A test receipt was just printed.%n%nPrinter: %1%nDriver: %2%n%nDid it print correctly?
TestPrintConfirmYes=Yes
TestPrintConfirmNoChooseOther=No — choose another printer
; %1 = current printer name
TestPrintFireFailedBody=Failed to send the test print to printer "%1". Verify the printer is powered on and connected, then try again.
TestPrintRetryPickerCaption=Choose another printer
TestPrintRetryPickerBody=Select the printer to use for the next test print. The agent will be reconfigured and the service restarted before retrying.
TestPrintRetryPickerOk=Retry
TestPrintRetryPickerCancel=Cancel
TestPrintNoOtherPrinters=No other printers detected. Plug in and install a printer in Windows, then re-run setup.

; --- M13 print-verification: wpFinished suffix ---
InstallSuccessPrintVerified=✓ Test receipt confirmed by the operator.
InstallSuccessPrintNotVerified=⚠ Printing was NOT confirmed. Before the first customer receipt, open the Simsim dashboard and run "Print a test receipt" from the terminal page.
