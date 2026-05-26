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

// M13 print-verification — globals capturing the post-pair test-print
// outcome for wpFinished.
//
// PrintVerifyAttempted:
//   False = the verify step was skipped (pair failed / pair skipped /
//           wpFinished reached without running ssPostInstall).
//   True  = the verify step ran (regardless of the outcome). When
//           True, PrintVerifyConfirmed tells us whether the operator
//           confirmed Oui.
// PrintVerifyConfirmed:
//   True  = operator answered Oui to "Did it print correctly?".
//           Cloud has the green-status stamp.
//   False = operator answered Non, hit max retries, or the
//           test-print pipeline itself failed. Cloud has been told
//           verified=false (amber/null).
// PrintVerifyErrorClass: free-form failure tag the cloud logs.
//   Conventional values match agentctl verify-print's --fail:
//     OPERATOR_REJECTED, MAX_RETRIES_EXCEEDED, TEST_PRINT_FAILED,
//     AGENT_UNREACHABLE.
//
// wpFinished consumes these three to extend the message text.
  PrintVerifyAttempted:   Boolean;
  PrintVerifyConfirmed:   Boolean;
  PrintVerifyErrorClass:  String;

const
  // Retry cap on the printer-swap loop. Confirmed at plan-approval
  // time; sized for an operator with up to ~3 printers to cycle
  // through, with room for a misclick or two.
  PRINT_VERIFY_MAX_RETRIES = 5;

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

// --- M13 print-verification helpers ---

// rewritePrinterAndRestartService reconfigures the agent for a new
// printer name and bounces the Windows service so /test-print picks
// up the change. Used by the print-verification retry loop when the
// operator picks a different printer mid-flow.
//
// Three Exec calls — write-config, service stop, service start —
// each waited on synchronously. Failures are logged in the LogLabel
// caption but don't abort the loop; the operator can still attempt
// another retry, and the verify-print --fail path covers the
// terminal failure case.
function rewritePrinterAndRestartService(NewName: String): Boolean;
var
  ResultCode: Integer;
  AgentExe: String;
begin
  AgentExe := ExpandConstant('{app}\bin\agent.exe');

  // 1. Rewrite config.json — empty --printer arg would leave the
  // existing value, so we always pass NewName explicitly.
  if not Exec(AgentExe,
              'write-config --printer "' + NewName + '"',
              '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
  begin
    Result := False;
    Exit;
  end;
  if ResultCode <> 0 then
  begin
    Result := False;
    Exit;
  end;

  // 2. Stop the service. SC may return non-zero if the service is
  // already stopped — tolerate it; the start step is what matters.
  Exec(AgentExe, 'service stop', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);

  // 3. Start fresh. THIS one must succeed — the operator's next
  // /test-print needs a running agent with the new printer wired.
  if not Exec(AgentExe, 'service start', '', SW_HIDE,
              ewWaitUntilTerminated, ResultCode) then
  begin
    Result := False;
    Exit;
  end;
  if ResultCode <> 0 then
  begin
    Result := False;
    Exit;
  end;
  // Give the service a moment to spin up its loopback listener
  // before the next /test-print attempt.
  Sleep(2000);
  Result := True;
end;

// askPickAnotherPrinter shows a dialog with the available printers
// and lets the operator pick a different one. Returns the chosen
// name on Oui, '' on Cancel.
//
// Uses Inno's stock InputQuery via SelectFromList isn't bundled —
// we reuse `detectPrinters` and present the list as a hand-rolled
// modal dialog through a TForm. Kept compact: this surface only
// fires when the operator answers Non to the verify dialog, which
// is the off-path. Most pilots clear on Oui first attempt.
function askPickAnotherPrinter(CurrentPrinter: String): String;
var
  F: TSetupForm;
  Combo: TNewComboBox;
  OkBtn, CancelBtn: TNewButton;
  Names: TArrayOfString;
  DetectionFailed: Boolean;
  i, defaultIdx: Integer;
  Lbl: TLabel;
begin
  Result := '';
  Names := detectPrinters(DetectionFailed);
  if DetectionFailed or (GetArrayLength(Names) = 0) then
  begin
    MsgBox(ExpandConstant('{cm:TestPrintNoOtherPrinters}'), mbInformation, MB_OK);
    Exit;
  end;

  // Fail-safe wrapper. This hand-rolled modal has never run in the
  // field; ANY runtime failure (form creation, control wiring,
  // ShowModal) must degrade to "no printer picked" (Result='') rather
  // than propagate out of CurStepChanged and abort/rollback the
  // install. The inner try/finally still frees the form even when the
  // outer except fires. PascalScript can't combine except+finally in
  // one block, so they're nested.
  try
    F := TSetupForm.Create(nil);
    try
      F.Caption := ExpandConstant('{cm:TestPrintRetryPickerCaption}');
      F.ClientWidth := ScaleX(420);
      F.ClientHeight := ScaleY(180);
      // poScreenCenter, NOT poOwnerFormCenter: the form is created
      // with a nil owner, so "center on owner" has no reference form
      // and can drop the window at (0,0) / off-screen. Screen-center
      // is correct and owner-independent.
      F.Position := poScreenCenter;

      Lbl := TLabel.Create(F);
      Lbl.Parent := F;
      Lbl.Caption := ExpandConstant('{cm:TestPrintRetryPickerBody}');
      Lbl.Left := ScaleX(16);
      Lbl.Top := ScaleY(16);
      Lbl.Width := ScaleX(388);
      Lbl.Height := ScaleY(40);
      Lbl.WordWrap := True;
      Lbl.AutoSize := False;

      Combo := TNewComboBox.Create(F);
      Combo.Parent := F;
      Combo.Style := csDropDownList;
      Combo.Left := ScaleX(16);
      Combo.Top := ScaleY(72);
      Combo.Width := ScaleX(388);
      defaultIdx := 0;
      for i := 0 to GetArrayLength(Names) - 1 do
      begin
        Combo.Items.Add(Names[i]);
        if Names[i] = CurrentPrinter then
          defaultIdx := i;
      end;
      Combo.ItemIndex := defaultIdx;

      OkBtn := TNewButton.Create(F);
      OkBtn.Parent := F;
      OkBtn.Caption := ExpandConstant('{cm:TestPrintRetryPickerOk}');
      OkBtn.Left := ScaleX(220);
      OkBtn.Top := ScaleY(130);
      OkBtn.Width := ScaleX(90);
      OkBtn.ModalResult := mrOk;

      CancelBtn := TNewButton.Create(F);
      CancelBtn.Parent := F;
      CancelBtn.Caption := ExpandConstant('{cm:TestPrintRetryPickerCancel}');
      CancelBtn.Left := ScaleX(314);
      CancelBtn.Top := ScaleY(130);
      CancelBtn.Width := ScaleX(90);
      CancelBtn.ModalResult := mrCancel;

      // Only a positive OK + a real selection yields a printer name.
      // Any other ShowModal result (mrCancel, the window [X], or an
      // unexpected value) leaves Result='' → caller treats as cancel.
      // The ItemIndex >= 0 guard defends against an empty-selection
      // index error (can't happen given defaultIdx, but cheap).
      if (F.ShowModal = mrOk) and (Combo.ItemIndex >= 0) then
        Result := Combo.Items[Combo.ItemIndex];
    finally
      F.Free;
    end;
  except
    // Swallow any modal runtime failure → treat as "no printer picked".
    Result := '';
  end;
end;

// runVerifyPrintStep is the post-pair print-verification step:
//   1. agentctl test-print  (canned receipt → printer)
//   2. Operator dialog: Oui / Non — choisir une autre imprimante
//   3. On Oui  → agentctl verify-print --ok  → done.
//   4. On Non  → swap printer + restart service + back to (1),
//                up to PRINT_VERIFY_MAX_RETRIES times.
//   5. Operator cancels OR cap hit → agentctl verify-print --fail.
//
// All three globals (PrintVerifyAttempted / Confirmed / ErrorClass)
// are set before this returns; wpFinished consumes them.
procedure runVerifyPrintStep;
var
  Attempts: Integer;
  AgentctlExe: String;
  ResultCode: Integer;
  CurrentPrinter, CurrentDriver, NextPrinter: String;
  DialogTitle, DialogBody: String;
  Reply: Integer;
  ButtonLabels: TArrayOfString;
begin
  PrintVerifyAttempted := True;
  PrintVerifyConfirmed := False;
  PrintVerifyErrorClass := '';

  AgentctlExe := ExpandConstant('{app}\bin\agentctl.exe');
  CurrentPrinter := SelectedPrinterName;
  CurrentDriver := SelectedPrinterDriver;

  // Fail-safe: the whole test-print / confirm / retry loop runs inside
  // try/except. Any unhandled runtime failure raised below (the
  // hand-rolled modal, TaskDialogMsgBox, Exec, etc.) is caught at the
  // matching `except` after the loop, degraded to verified=false, and
  // the procedure returns normally so the install FINISHES. The `Exit`
  // branches inside the loop leave the procedure cleanly without
  // triggering `except` (Exit is not an exception). The loop body keeps
  // its existing indentation so this hardening stays a two-marker diff.
  try
  Attempts := 0;
  while Attempts < PRINT_VERIFY_MAX_RETRIES do
  begin
    Attempts := Attempts + 1;

    // 1. Fire test print.
    WizardForm.StatusLabel.Caption := ExpandConstant('{cm:RunStatusTestPrint}');
    if not Exec(AgentctlExe, 'test-print', '', SW_HIDE,
                ewWaitUntilTerminated, ResultCode) then
    begin
      // agentctl couldn't be launched (process spawn failure). Bail
      // hard — no point looping.
      PrintVerifyErrorClass := 'AGENT_UNREACHABLE';
      Exec(AgentctlExe, 'verify-print --fail AGENT_UNREACHABLE', '',
           SW_HIDE, ewWaitUntilTerminated, ResultCode);
      Exit;
    end;
    if ResultCode <> 0 then
    begin
      // /test-print returned non-200. Could be printer offline,
      // PRINTER_NOT_CONFIGURED, etc. Surface a dialog so the operator
      // can swap printers and try again — the test-print failure
      // looks indistinguishable from a "Non" answer at this layer.
      // [...] literal kept on the same line as FmtMessage( — a line
      // starting with '[' is read as an INI section header by Inno's
      // preprocessor ("Invalid section tag"). Same bug class as v0.3.3.
      Reply := MsgBox(
        FmtMessage(ExpandConstant('{cm:TestPrintFireFailedBody}'), [CurrentPrinter]),
        mbError,
        MB_RETRYCANCEL);
      if Reply <> IDRETRY then
      begin
        PrintVerifyErrorClass := 'TEST_PRINT_FAILED';
        Exec(AgentctlExe, 'verify-print --fail TEST_PRINT_FAILED', '',
             SW_HIDE, ewWaitUntilTerminated, ResultCode);
        Exit;
      end;
      // Loop with a swap. If swap fails or operator cancels, we go
      // to the verify-print --fail branch below.
      NextPrinter := askPickAnotherPrinter(CurrentPrinter);
      if NextPrinter = '' then
      begin
        PrintVerifyErrorClass := 'TEST_PRINT_FAILED';
        Exec(AgentctlExe, 'verify-print --fail TEST_PRINT_FAILED', '',
             SW_HIDE, ewWaitUntilTerminated, ResultCode);
        Exit;
      end;
      if not rewritePrinterAndRestartService(NextPrinter) then
      begin
        PrintVerifyErrorClass := 'TEST_PRINT_FAILED';
        Exec(AgentctlExe, 'verify-print --fail TEST_PRINT_FAILED', '',
             SW_HIDE, ewWaitUntilTerminated, ResultCode);
        Exit;
      end;
      CurrentPrinter := NextPrinter;
      CurrentDriver := detectDriverNameFor(NextPrinter);
      Continue;
    end;

    // 2. Test print fired. Ask the operator if it printed correctly.
    DialogTitle := ExpandConstant('{cm:TestPrintConfirmTitle}');
    // [...] literal kept on the same line as FmtMessage( (see above).
    DialogBody := FmtMessage(ExpandConstant('{cm:TestPrintConfirmBody}'), [CurrentPrinter, CurrentDriver]);
    // Inno's TaskDialogMsgBox supports custom button labels — we use
    // it for the Oui / Non-choose-another pair so the dialog matches
    // the operator's mental model better than a generic Yes/No.
    SetArrayLength(ButtonLabels, 2);
    ButtonLabels[0] := ExpandConstant('{cm:TestPrintConfirmYes}');
    ButtonLabels[1] := ExpandConstant('{cm:TestPrintConfirmNoChooseOther}');
    Reply := TaskDialogMsgBox(
      DialogTitle, DialogBody, mbConfirmation, MB_YESNO, ButtonLabels, -1);

    if Reply = IDYES then
    begin
      // 3. Oui — operator confirmed. Stamp cloud + exit loop.
      if not Exec(AgentctlExe, 'verify-print --ok', '', SW_HIDE,
                  ewWaitUntilTerminated, ResultCode) then
      begin
        PrintVerifyErrorClass := 'CLOUD_REPORT_FAILED';
        Exit;
      end;
      if ResultCode <> 0 then
      begin
        // Cloud rejected (network failure, auth issue). Don't lose
        // the operator's Oui — flag explicitly so wpFinished surfaces
        // "test printed, but cloud confirmation didn't reach Simsim;
        // retry from the dashboard."
        PrintVerifyErrorClass := 'CLOUD_REPORT_FAILED';
        Exit;
      end;
      PrintVerifyConfirmed := True;
      Exit;
    end;

    // 4. Non — operator wants to swap printers.
    NextPrinter := askPickAnotherPrinter(CurrentPrinter);
    if NextPrinter = '' then
    begin
      PrintVerifyErrorClass := 'OPERATOR_REJECTED';
      Exec(AgentctlExe, 'verify-print --fail OPERATOR_REJECTED', '',
           SW_HIDE, ewWaitUntilTerminated, ResultCode);
      Exit;
    end;
    if not rewritePrinterAndRestartService(NextPrinter) then
    begin
      PrintVerifyErrorClass := 'OPERATOR_REJECTED';
      Exec(AgentctlExe, 'verify-print --fail OPERATOR_REJECTED', '',
           SW_HIDE, ewWaitUntilTerminated, ResultCode);
      Exit;
    end;
    CurrentPrinter := NextPrinter;
    CurrentDriver := detectDriverNameFor(NextPrinter);
  end;

  // 5. Retry cap hit — neither verified nor explicitly cancelled.
  PrintVerifyErrorClass := 'MAX_RETRIES_EXCEEDED';
  Exec(AgentctlExe, 'verify-print --fail MAX_RETRIES_EXCEEDED', '',
       SW_HIDE, ewWaitUntilTerminated, ResultCode);
  except
    // Any exception escaping the loop above lands here. Degrade to
    // "unverified" (wpFinished then shows the existing "impression non
    // vérifiée" warning), best-effort report it to the cloud, and
    // return normally so the install completes cleanly.
    PrintVerifyConfirmed := False;
    PrintVerifyErrorClass := 'VERIFY_STEP_EXCEPTION';
    Exec(AgentctlExe, 'verify-print --fail VERIFY_STEP_EXCEPTION', '',
         SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  // ssPostInstall fires after [Files] copy + [Run] entries complete.
  // The three [Run] entries (write-config, service install/start) have
  // already run; if any of them failed, Inno would have rolled back
  // and we wouldn't reach here. Now we run the conditional pair step.
  if (CurStep = ssPostInstall) and not SkipPairing then
  begin
    runPairStep;
    // Only attempt print verification when pairing succeeded — there's
    // no terminal token for the cloud /api/pos-agent/print-verified
    // call otherwise. wpFinished still surfaces an "install OK, pair
    // failed" message via buildSuccessMessage; the operator runs the
    // test print later from the retailer settings page.
    if PairResultCode = 0 then
    begin
      // Final safety net at the installer-engine boundary. Even though
      // runVerifyPrintStep guards its own loop, an unhandled exception
      // reaching this ssPostInstall handler can make Inno abort/rollback
      // the install. Catch here too: degrade to "attempted, unverified"
      // and let wpFinished surface the warning. The install ALWAYS
      // finishes.
      try
        runVerifyPrintStep;
      except
        PrintVerifyAttempted := True;
        PrintVerifyConfirmed := False;
        if PrintVerifyErrorClass = '' then
          PrintVerifyErrorClass := 'VERIFY_STEP_EXCEPTION';
      end;
    end;
  end;
end;

// buildSuccessMessage chooses the wpFinished label text based on whether
// the operator skipped pairing, the pair succeeded, or it failed. Format
// %1/%2 placeholders feed Inno's localized %Format substitution.
function buildSuccessMessage: String;
var
  Reason: String;
  Base: String;
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
    //
    // The [...] literal MUST stay on the same line as FmtMessage(. A
    // continuation line that starts with '[' is read by Inno's
    // preprocessor as an INI section header and fails compile with
    // "Invalid section tag" (this is what broke v0.3.2's CI build).
    Base := FmtMessage(ExpandConstant('{cm:InstallSuccessPaired}'), [PairedTerminalLabel, PairedStoreName]);
    // M13 print-verification — append the verification outcome to
    // the success message. wpFinished is the only post-install
    // signal the operator sees; surfacing this here is what
    // prevents the "cashier discovers garbled receipts on first
    // customer" failure mode.
    if PrintVerifyAttempted then
    begin
      if PrintVerifyConfirmed then
        Result := Base + #13#10 + ExpandConstant('{cm:InstallSuccessPrintVerified}')
      else
        Result := Base + #13#10 + ExpandConstant('{cm:InstallSuccessPrintNotVerified}');
    end
    else
      Result := Base;
    Exit;
  end;
  if PairFailureReason = '' then
    Reason := ExpandConstant('{cm:PairFailureReasonUnknown}')
  else
    Reason := PairFailureReason;
  Result := FmtMessage(ExpandConstant('{cm:InstallSuccessPairFailed}'), [Reason, SelectedPairingCode]);
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
