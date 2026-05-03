; Simsim POS Agent — Inno Setup installer script
;
; Compile from the agent repo root:
;   "/c/Program Files (x86)/Inno Setup 6/iscc.exe" \
;     installer/installer.iss /DAppVersion=0.3.0
;
; Output: build/installer/simsim-pos-agent-setup-<version>.exe
;
; Sub-task wiring:
;   - AG2 (this file): Setup, Languages, Files, Run, UninstallRun.
;   - AG3 will add the printer-picker wizard page (printer-picker.iss).
;   - AG4 will add the pair-code-entry wizard page (pair-code-page.iss).
;   - AG5 will add post-install error handling (exit code branches on the
;     [Run] entries below).

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif

[Setup]
; AppId is the immutable installer identity — used by Inno Setup to
; recognize prior installations during upgrade. Generated once at AG2
; bootstrap and MUST NEVER CHANGE across releases. Changing it would
; cause the wizard to treat an upgrade as a fresh install (orphaning
; the previous Program Files directory + service registration).
AppId={{FEFAA9FE-0F3B-4357-B1B0-F0F67D343398}
AppName=Simsim POS Agent
AppVersion={#AppVersion}
AppVerName=Simsim POS Agent {#AppVersion}
AppPublisher=Simsim
AppPublisherURL=https://opensimsim.co
AppSupportURL=https://opensimsim.co
DefaultDirName={commonpf}\Simsim\POSAgent
DefaultGroupName=Simsim POS Agent
DisableProgramGroupPage=no
PrivilegesRequired=admin
; Inno Setup 6.3+ deprecated "x64"; "x64os" is the explicit identifier
; for "64-bit Windows OS" (vs "x64compatible" which would also accept
; 32-bit Windows on 64-bit-capable hardware — not what we want).
ArchitecturesAllowed=x64os
ArchitecturesInstallIn64BitMode=x64os
OutputDir=..\build\installer
OutputBaseFilename=simsim-pos-agent-setup-{#AppVersion}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ShowLanguageDialog=auto
; Wizard images (logo, banner) deferred to a design pass — see
; wizard-images/README.md. Inno Setup defaults applied in the interim.

[Languages]
; Stock Inno-bundled .isl files for standard wizard messages, plus our
; per-language [CustomMessages] overlays for app-specific strings the
; AG3/AG4 wizard pages will reference. Comma-separated MessagesFile
; means later files override earlier — our overlays sit on top of stock.
Name: "french";  MessagesFile: "compiler:Languages\French.isl,lang\fr.isl"
Name: "arabic";  MessagesFile: "compiler:Languages\Arabic.isl,lang\ar.isl"
Name: "english"; MessagesFile: "compiler:Default.isl,lang\en.isl"

[Files]
; Both binaries land under {app}\bin so {app} stays clean for any future
; data subfolders. ignoreversion: always copy, never compare timestamps —
; the build always produces a fresh binary, no need to preserve disk copy.
Source: "..\build\agent.exe";    DestDir: "{app}\bin"; Flags: ignoreversion
Source: "..\build\agentctl.exe"; DestDir: "{app}\bin"; Flags: ignoreversion

[Run]
; Post-install sequence (AG5):
;   1. write-config: seed config.json with AG3 printer choice + cloud URL.
;   2. service install: register the Windows service.
;   3. service start: bring the service up.
;
; All three are blocking — failure on any of them surfaces Inno's stock
; "Could not run" dialog with Retry / Cancel. Cancel triggers rollback
; (Inno auto-removes the files it copied). This is intentional: a
; broken service install is not a usable install.
;
; The pair step (AG4 code) is NOT here — it lives in CurStepChanged
; (ssPostInstall) below so we can warn-and-continue on pair failure
; rather than rolling back. See M4 spec §3.7 (partial-install case).

; Step 1: write config.json. {code:GetPrinterArg} substitutes the
; AG3-selected printer name (or empty string if no printer detected);
; empty-string semantics: write-config preserves any existing value.
Filename: "{app}\bin\agent.exe"; \
  Parameters: "write-config --printer ""{code:GetPrinterArg}"" --cloud-base-url ""{code:GetCloudBaseURLArg}"""; \
  StatusMsg: "{cm:RunStatusWriteConfig}"; \
  Flags: runhidden waituntilterminated

; Step 2: register the service.
Filename: "{app}\bin\agent.exe"; Parameters: "service install"; \
  StatusMsg: "{cm:RunStatusServiceInstall}"; \
  Flags: runhidden waituntilterminated

; Step 3: start it.
Filename: "{app}\bin\agent.exe"; Parameters: "service start"; \
  StatusMsg: "{cm:RunStatusServiceStart}"; \
  Flags: runhidden waituntilterminated

[UninstallRun]
; Reverse of install order: unpair (best-effort, surfaces revoked-token
; to cloud) → stop service → uninstall service. RunOnceId guards each
; entry so a re-uninstall doesn't re-execute. Order matters: stop must
; happen before uninstall (post-AG7 the service.Uninstall call also
; stops, but the explicit step keeps Inno's progress UI honest).
Filename: "{app}\bin\agentctl.exe"; Parameters: "unpair"; \
  RunOnceId: "agentctl-unpair"; Flags: runhidden
Filename: "{app}\bin\agent.exe"; Parameters: "service stop"; \
  RunOnceId: "agent-service-stop"; Flags: runhidden
Filename: "{app}\bin\agent.exe"; Parameters: "service uninstall"; \
  RunOnceId: "agent-service-uninstall"; Flags: runhidden

[Icons]
Name: "{group}\Simsim POS Agent — Statut"; Filename: "{app}\bin\agentctl.exe"; \
  Parameters: "status"; WorkingDir: "{app}\bin"
Name: "{group}\Désinstaller Simsim POS Agent"; Filename: "{uninstallexe}"

[Code]
// Single [Code] section for the whole installer. Per-page logic lives
// in dedicated #include'd files for readability — the included files
// are pure Pascal (no section headers) and get textually inlined here.

#include "printer-picker.iss"
#include "pair-code-page.iss"

// AG5: globals capturing the pair step's result for the success page.
// PairResultCode = 0  -> pair succeeded; PairedStoreName/TerminalLabel
//                       are populated from agentctl's stdout.
// PairResultCode <> 0 -> pair failed; PairFailureReason holds the
//                       cloud's French error message (parsed from the
//                       "Erreur: ..." line in agentctl's stderr-merged
//                       output). Empty if parsing failed.
// SkipPairing  -> the operator deferred pairing; the other vars are
//                 ignored on the success page.
var
  PairResultCode:      Integer;
  PairedStoreName:     String;
  PairedTerminalLabel: String;
  PairFailureReason:   String;

// --- {code:...} substitution helpers for [Run] parameters ---

// GetPrinterArg returns the AG3 printer choice for write-config. May be
// empty — write-config's --printer "" path leaves printer_name unchanged.
function GetPrinterArg(Param: String): String;
begin
  Result := SelectedPrinterName;
end;

// GetCloudBaseURLArg returns the production cloud base URL. Hardcoded
// here for now; matches config.Defaults().CloudBaseURL in Go-land.
// If the URL ever moves to a #define, swap this for {#CloudBaseURL}.
function GetCloudBaseURLArg(Param: String): String;
begin
  Result := 'https://web-production-6bb4d.up.railway.app';
end;

// --- Wizard lifecycle ---
//
// Forward order matters: Pascal needs functions defined before they're
// called. buildSuccessMessage and runPairStep appear above the lifecycle
// hooks (InitializeWizard / CurPageChanged / NextButtonClick / CurStepChanged)
// that reference them.

// runPairStep execs `agentctl pair --code XXXXXX`, captures stdout to
// {tmp}\pair.txt, and parses the M2 success-block format to extract
// the cloud-supplied store name and terminal label for the success
// page. Failures are recorded (not raised) — the installer continues.
//
// Stdout format (from cmd/agentctl/pair.go M2):
//   "✓ Appareil jumelé avec succès."
//   "  Magasin    : <store_name>"
//   "  Caisse     : <terminal_label>"
//   "  ID terminal: <terminal_id>"
// Brittle: if those French labels change, parsing falls back to empty
// strings and the success page degrades to "(unknown) is connected."
// Future hardening: add `agentctl pair --output-json` and parse JSON.
procedure runPairStep;
var
  TmpFile, Cmd, L: String;
  ResultCode, i, p: Integer;
  Lines: TArrayOfString;
begin
  TmpFile := ExpandConstant('{tmp}\pair.txt');
  // cmd /C ""<exe>" pair --code XXXXXX > tmpfile 2>&1"
  // Doubled outer quotes per cmd's argument-parsing rules when the
  // command itself contains a quoted path.
  Cmd := '/C ""' + ExpandConstant('{app}\bin\agentctl.exe') +
         '" pair --code ' + SelectedPairingCode +
         ' > "' + TmpFile + '" 2>&1"';

  WizardForm.StatusLabel.Caption := ExpandConstant('{cm:RunStatusPair}');

  if not Exec(ExpandConstant('{cmd}'), Cmd, '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
  begin
    PairResultCode := -1;
    Exit;
  end;
  PairResultCode := ResultCode;
  if not FileExists(TmpFile) then
    Exit;
  if not LoadStringsFromFile(TmpFile, Lines) then
    Exit;

  // On success, parse "Magasin :" and "Caisse :" lines for the success
  // page. On failure, parse the "Erreur: <french-message>" line that
  // cmd/agentctl/pair.go's printPairError emits — that's the cloud's
  // own French message, surfaced through *CloudError.Message().
  for i := 0 to GetArrayLength(Lines) - 1 do
  begin
    L := Lines[i];
    if (ResultCode = 0) and (Pos('Magasin', L) > 0) then
    begin
      p := Pos(':', L);
      if p > 0 then
        PairedStoreName := Trim(Copy(L, p + 1, Length(L) - p));
    end
    else if (ResultCode = 0) and (Pos('Caisse', L) > 0) then
    begin
      p := Pos(':', L);
      if p > 0 then
        PairedTerminalLabel := Trim(Copy(L, p + 1, Length(L) - p));
    end
    else if (ResultCode <> 0) and (Pos('Erreur:', L) > 0) then
    begin
      p := Pos(':', L);
      if p > 0 then
        PairFailureReason := Trim(Copy(L, p + 1, Length(L) - p));
    end;
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  // ssPostInstall fires after [Files] copy + [Run] entries complete.
  // The three [Run] entries (write-config, service install/start) have
  // already run; if any of them failed, Inno would have rolled back
  // and we wouldn't reach here. Now we run the conditional pair step.
  if (CurStep = ssPostInstall) and not SkipPairing then
    runPairStep;
end;

// buildSuccessMessage chooses the wpFinished label text based on whether
// the operator skipped pairing, the pair succeeded, or it failed. Format
// %1/%2 placeholders feed Inno's localized %Format substitution.
function buildSuccessMessage: String;
var
  Reason: String;
begin
  if SkipPairing then
  begin
    Result := ExpandConstant('{cm:InstallSuccessSkipped}');
    Exit;
  end;
  if PairResultCode = 0 then
  begin
    // Literal array constructor at the call site: required pattern.
    // Passing an Args variable to FmtMessage's 'array of String' open-
    // array parameter triggers a Type Mismatch in PascalScript Unicode
    // (RemObjects PascalScript #129). v0.3.0/v0.3.1 used the variable
    // pattern and crashed here.
    Result := FmtMessage(ExpandConstant('{cm:InstallSuccessPaired}'),
                         [PairedTerminalLabel, PairedStoreName]);
    Exit;
  end;
  if PairFailureReason = '' then
    Reason := ExpandConstant('{cm:PairFailureReasonUnknown}')
  else
    Reason := PairFailureReason;
  Result := FmtMessage(ExpandConstant('{cm:InstallSuccessPairFailed}'),
                       [Reason, SelectedPairingCode]);
end;

// --- Wizard lifecycle hooks ---

procedure InitializeWizard;
begin
  createPrinterPickerPage;
  createPairCodePage;
end;

procedure CurPageChanged(CurPageID: Integer);
begin
  if CurPageID = PrinterPickerPage.ID then
    populatePrinterPicker;
  if CurPageID = PairCodePage.ID then
    pairCodeOnPageActivate;
  if CurPageID = wpFinished then
    WizardForm.FinishedLabel.Caption := buildSuccessMessage;
end;

function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;
  if CurPageID = PairCodePage.ID then
    Result := pairCodeValidate;
end;

// --- AG6: uninstaller flow ---
//
// Default uninstall removes only what's under {app} (Inno's automatic
// file tracking) plus the [UninstallRun] entries (unpair → service stop
// → service uninstall). The data tree at {commonappdata}\Simsim\POSAgent
// (config.json, secrets.dat, logs) survives by default — supports the
// uninstall-then-reinstall workflow without losing the operator's
// pairing or printer configuration.
//
// The InitializeUninstall prompt lets the operator opt OUT of that
// preservation by choosing "Supprimer" — in which case usPostUninstall
// recursively rmdirs the data tree.

// KeepData defaults to True (safe). Set by InitializeUninstall based on
// the operator's response to the keep-data prompt. Read by
// CurUninstallStepChanged at usPostUninstall.
var
  KeepData: Boolean;

function InitializeUninstall: Boolean;
var
  Labels: TArrayOfString;
  Choice: Integer;
begin
  Result := True;     // proceed with uninstall regardless
  KeepData := True;   // safe default if operator dismisses the dialog

  SetArrayLength(Labels, 2);
  Labels[0] := ExpandConstant('{cm:UninstallKeepDataYes}');
  Labels[1] := ExpandConstant('{cm:UninstallKeepDataNo}');

  // TaskDialogMsgBox lets us override the Yes/No labels with the
  // localized "Conserver" / "Supprimer" pair. ShieldButton = -1 ->
  // no UAC shield on either button (no privilege escalation involved).
  Choice := TaskDialogMsgBox('',
    ExpandConstant('{cm:UninstallKeepDataPrompt}'),
    mbConfirmation,
    MB_YESNO,
    Labels,
    -1);

  case Choice of
    IDYES: KeepData := True;
    IDNO:  KeepData := False;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  DataDir: String;
begin
  // usPostUninstall fires after [UninstallRun] entries + Inno's {app}
  // file removal. The service is stopped and uninstalled; the binary
  // tree under Program Files is gone. Now we conditionally rmdir the
  // data tree under ProgramData.
  if (CurUninstallStep = usPostUninstall) and not KeepData then
  begin
    DataDir := ExpandConstant('{commonappdata}\Simsim\POSAgent');
    if DirExists(DataDir) then
      // DelTree(Path, IsDir=True, DeleteFiles=True, DeleteSubdirsAlso=True).
      // Returns False on partial failure — we don't surface that to
      // the operator; any leftover is harmless and they can rmdir
      // manually if they care.
      DelTree(DataDir, True, True, True);
  end;
end;
