// printer-picker.iss — AG3: custom wizard page that picks the printer.
//
// This file is #include'd from inside installer.iss's [Code] section.
// It is NOT a standalone .iss — no section headers; pure Pascal that
// gets textually inlined into the parent script's [Code] block.
// Comments here use // (Pascal style) not ; (.iss INI style) because
// the contents land inside [Code].
//
// Public surface (consumed by AG5's install steps):
//   function SelectedPrinterName: String;
//     Returns the operator's chosen printer name, or '' if no printer
//     was detected / picked. AG5 writes this into config.json's
//     printer_name field before launching `agent service install`.
//
// Detection flow on page activation:
//   1. Shell `powershell.exe Get-Printer | Select -ExpandProperty Name`
//      to a temp file (Inno's Exec doesn't capture stdout natively).
//   2. Three states from the result:
//      a. Exec failed / non-zero exit -> DetectionFailed warning.
//      b. Zero printers reported     -> NonePresent warning.
//      c. >=1 printer                -> dropdown populated, default
//         selection picks the first heuristic match (POS / Receipt /
//         common thermal-printer brands), else the first printer.
//   3. In all three states the user can click Next; the agent install
//      continues either way. config.json's printer_name simply stays
//      blank when no printer is configured, and the agent surfaces
//      PRINTER_NOT_CONFIGURED on /print until the operator edits it.

var
  PrinterPickerPage: TWizardPage;
  PrinterCombo: TNewComboBox;
  PrinterStatusLabel: TLabel;
  // M13 print-verification — driver-name lookup + non-blocking warning.
  // PrinterDriverWarningLabel sits below the combo; it carries the
  // "Generic / Text Only is recommended" advisory when the selected
  // printer's driver is anything else. Empty caption = hidden.
  // SelectedPrinterDriverName is the resolved DriverName for whichever
  // printer is currently selected in the combo. AG5's [Run] does NOT
  // consume it (no schema for driver name in config.json); the
  // print-verification step echoes it in the operator dialog for
  // forensic context.
  PrinterDriverWarningLabel: TLabel;
  SelectedPrinterDriverName: String;

// SelectedPrinterName returns the picked printer, or '' when the page
// reported zero printers or detection failure.
function SelectedPrinterName: String;
begin
  if Assigned(PrinterCombo) and PrinterCombo.Visible and (PrinterCombo.ItemIndex >= 0) then
    Result := PrinterCombo.Items[PrinterCombo.ItemIndex]
  else
    Result := '';
end;

// SelectedPrinterDriver returns the resolved Windows driver name for
// the currently-selected printer, or '' when none is selected /
// detection failed. Exported so the print-verification dialog can
// surface "Imprimante: %1 (%2)" with the driver in %2.
function SelectedPrinterDriver: String;
begin
  Result := SelectedPrinterDriverName;
end;

// detectPrinters runs the PowerShell enumeration. Returns the list of
// printer names. Sets DetectionFailed=True if the enumeration itself
// could not be run (Exec returned False) or PowerShell exited non-zero.
// An empty list with DetectionFailed=False means "no printers installed."
function detectPrinters(var DetectionFailed: Boolean): TArrayOfString;
var
  TmpFile: String;
  Cmd: String;
  ResultCode: Integer;
  RawLines: TArrayOfString;
  Cleaned: TArrayOfString;
  i, count: Integer;
  L: String;
begin
  SetArrayLength(Result, 0);
  DetectionFailed := False;
  TmpFile := ExpandConstant('{tmp}\printers.txt');

  // /C powershell ... > tmpfile 2>&1 -- capture both stdout and stderr;
  // we only care about exit code + stdout content.
  Cmd := '/C powershell.exe -NoProfile -Command "Get-Printer | Select-Object -ExpandProperty Name" > "' + TmpFile + '" 2>&1';

  if not Exec(ExpandConstant('{cmd}'), Cmd, '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
  begin
    DetectionFailed := True;
    Exit;
  end;
  if ResultCode <> 0 then
  begin
    DetectionFailed := True;
    Exit;
  end;
  if not FileExists(TmpFile) then
  begin
    DetectionFailed := True;
    Exit;
  end;

  if not LoadStringsFromFile(TmpFile, RawLines) then
  begin
    DetectionFailed := True;
    Exit;
  end;

  // Filter blank lines (PowerShell sometimes emits trailing whitespace).
  SetArrayLength(Cleaned, GetArrayLength(RawLines));
  count := 0;
  for i := 0 to GetArrayLength(RawLines) - 1 do
  begin
    L := Trim(RawLines[i]);
    if L <> '' then
    begin
      Cleaned[count] := L;
      count := count + 1;
    end;
  end;
  SetArrayLength(Cleaned, count);
  Result := Cleaned;
end;

// detectDriverNameFor returns the Windows driver name registered for
// the given printer name, or '' if PowerShell fails / the printer
// can't be looked up.
//
// Shells the same `Get-Printer -Name "<name>"` pattern as
// `detectPrinters`, projecting the `DriverName` property. The driver
// name is the canonical string ("Generic / Text Only", "Generic /
// Text Only Driver", vendor-specific names like "Star TSP100 (TSP143)
// Cutter"). The print-verification step compares case-insensitively
// against the "Generic / Text Only" prefix.
//
// Failure modes (PowerShell exec failed, printer not found, name
// quoting issue) all fall through to '' — the caller treats unknown
// driver as "no warning surfaceable" rather than crashing the wizard.
function detectDriverNameFor(PrinterName: String): String;
var
  TmpFile, Cmd: String;
  ResultCode: Integer;
  RawLines: TArrayOfString;
  i: Integer;
  L: String;
begin
  Result := '';
  if PrinterName = '' then Exit;
  TmpFile := ExpandConstant('{tmp}\driver.txt');
  // Single-quote the printer name to PowerShell so spaces + special
  // chars in printer names don't break the command. We don't have to
  // worry about embedded single quotes in printer names (real-world
  // Windows printer names don't contain them; if a vendor ships such
  // a name we just fall through to '').
  Cmd := '/C powershell.exe -NoProfile -Command "(Get-Printer -Name ''' + PrinterName + ''' -ErrorAction SilentlyContinue).DriverName" > "' + TmpFile + '" 2>&1';
  if not Exec(ExpandConstant('{cmd}'), Cmd, '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then Exit;
  if ResultCode <> 0 then Exit;
  if not FileExists(TmpFile) then Exit;
  if not LoadStringsFromFile(TmpFile, RawLines) then Exit;
  for i := 0 to GetArrayLength(RawLines) - 1 do
  begin
    L := Trim(RawLines[i]);
    if L <> '' then
    begin
      Result := L;
      Exit;
    end;
  end;
end;

// isGenericTextOnlyDriver returns True iff the driver name matches
// the Windows-shipped "Generic / Text Only" driver. Case-insensitive
// to survive locale variants. Exact-prefix match (not contains)
// because some vendor names happen to embed "Generic" but don't
// behave the same way.
function isGenericTextOnlyDriver(DriverName: String): Boolean;
var
  L: String;
begin
  L := LowerCase(Trim(DriverName));
  Result := (L = 'generic / text only') or (L = 'generic / text only driver');
end;

// heuristicMatchIndex returns the index of the first printer whose name
// (case-insensitive) contains a thermal-printer keyword. Returns 0
// (first printer) if no heuristic match. Caller guarantees Names is
// non-empty.
function heuristicMatchIndex(Names: TArrayOfString): Integer;
var
  i: Integer;
  L: String;
begin
  for i := 0 to GetArrayLength(Names) - 1 do
  begin
    L := LowerCase(Names[i]);
    if (Pos('pos', L) > 0) or
       (Pos('receipt', L) > 0) or
       (Pos('tm-', L) > 0) or
       (Pos('sp-', L) > 0) or
       (Pos('star', L) > 0) or
       (Pos('epson', L) > 0) or
       (Pos('citizen', L) > 0) or
       (Pos('bixolon', L) > 0) then
    begin
      Result := i;
      Exit;
    end;
  end;
  Result := 0;
end;

// createPrinterPickerPage wires the custom wizard page after wpWelcome
// (so it appears between Welcome and Select-Install-Location). Called
// from InitializeWizard.
procedure createPrinterPickerPage;
begin
  PrinterPickerPage := CreateCustomPage(
    wpWelcome,
    ExpandConstant('{cm:PrinterPickerCaption}'),
    ExpandConstant('{cm:PrinterPickerSubcaption}')
  );

  // Status label sits at top -- repurposed for the dropdown label OR
  // the no-printers warning OR the detection-failed warning depending
  // on what the page activation finds.
  PrinterStatusLabel := TLabel.Create(PrinterPickerPage);
  PrinterStatusLabel.Parent := PrinterPickerPage.Surface;
  PrinterStatusLabel.Caption := ExpandConstant('{cm:PrinterPickerLabel}');
  PrinterStatusLabel.Top := 0;
  PrinterStatusLabel.Left := 0;
  PrinterStatusLabel.Width := PrinterPickerPage.SurfaceWidth;
  PrinterStatusLabel.WordWrap := True;
  PrinterStatusLabel.AutoSize := False;
  PrinterStatusLabel.Height := ScaleY(48);

  PrinterCombo := TNewComboBox.Create(PrinterPickerPage);
  PrinterCombo.Parent := PrinterPickerPage.Surface;
  PrinterCombo.Style := csDropDownList;
  PrinterCombo.Top := ScaleY(56);
  PrinterCombo.Left := 0;
  PrinterCombo.Width := PrinterPickerPage.SurfaceWidth;
  // M13 print-verification — fire the driver lookup whenever the
  // operator picks a different printer. We can't bind OnChange in a
  // single-pass [Code] section because TNewComboBox.OnChange isn't
  // a standard PascalScript property; instead we re-read the selection
  // when CurPageChanged fires Next (populatePrinterPicker handles
  // initial population, recheckPrinterDriverWarning is invoked from
  // NextButtonClick to refresh on click-through).

  // Non-blocking advisory label below the combo. Caption is set by
  // recheckPrinterDriverWarning; empty caption = invisible (no warn).
  PrinterDriverWarningLabel := TLabel.Create(PrinterPickerPage);
  PrinterDriverWarningLabel.Parent := PrinterPickerPage.Surface;
  PrinterDriverWarningLabel.Caption := '';
  PrinterDriverWarningLabel.Top := ScaleY(96);
  PrinterDriverWarningLabel.Left := 0;
  PrinterDriverWarningLabel.Width := PrinterPickerPage.SurfaceWidth;
  PrinterDriverWarningLabel.WordWrap := True;
  PrinterDriverWarningLabel.AutoSize := False;
  PrinterDriverWarningLabel.Height := ScaleY(64);
  // Amber-ish foreground for "advisory, not blocking" semantics.
  // Inno's TLabel.Font.Color uses Delphi clXXX constants; clMaroon
  // is the closest stock match to a warning amber without bundling
  // custom palette code.
  PrinterDriverWarningLabel.Font.Color := clMaroon;
end;

// recheckPrinterDriverWarning re-resolves the driver name for the
// currently-selected printer and updates PrinterDriverWarningLabel +
// SelectedPrinterDriverName globals. Called from populatePrinterPicker
// on page activation AND from NextButtonClick before leaving the
// printer-picker page (so a late selection change still gets a
// driver readout for the post-install verify dialog).
//
// Driver name unknown / lookup failed → warning hidden (empty caption).
// The verify-print step is the authoritative gate; an installer that
// can't read the driver name shouldn't surface a spurious advisory.
procedure recheckPrinterDriverWarning;
var
  Name: String;
begin
  Name := SelectedPrinterName;
  SelectedPrinterDriverName := detectDriverNameFor(Name);
  if SelectedPrinterDriverName = '' then
  begin
    PrinterDriverWarningLabel.Caption := '';
    Exit;
  end;
  if isGenericTextOnlyDriver(SelectedPrinterDriverName) then
  begin
    PrinterDriverWarningLabel.Caption := '';
    Exit;
  end;
  // FmtMessage so the warning string can interpolate the actual
  // driver name — operators see exactly what driver is currently
  // wired and can decide whether to swap it out before installing.
  //
  // The [...] literal MUST stay on the same line as FmtMessage(. A
  // continuation line that starts with '[' is read by Inno's
  // preprocessor as an INI section header and fails compile with
  // "Invalid section tag" (same bug class fixed in v0.3.3 for the
  // buildSuccessMessage FmtMessage calls in installer.iss).
  PrinterDriverWarningLabel.Caption := FmtMessage(ExpandConstant('{cm:PrinterPickerDriverWarning}'), [SelectedPrinterDriverName]);
end;

// populatePrinterPicker is invoked from CurPageChanged whenever the
// printer-picker page becomes active. Re-runs detection every time --
// idempotent and cheap enough at the wizard scale.
procedure populatePrinterPicker;
var
  Names: TArrayOfString;
  DetectionFailed: Boolean;
  i, defaultIdx: Integer;
begin
  Names := detectPrinters(DetectionFailed);
  PrinterCombo.Items.Clear;

  if DetectionFailed then
  begin
    PrinterCombo.Visible := False;
    PrinterStatusLabel.Caption := ExpandConstant('{cm:PrinterPickerDetectionFailed}');
    Exit;
  end;

  if GetArrayLength(Names) = 0 then
  begin
    PrinterCombo.Visible := False;
    PrinterStatusLabel.Caption := ExpandConstant('{cm:PrinterPickerNonePresent}');
    Exit;
  end;

  PrinterCombo.Visible := True;
  PrinterStatusLabel.Caption := ExpandConstant('{cm:PrinterPickerLabel}');
  for i := 0 to GetArrayLength(Names) - 1 do
    PrinterCombo.Items.Add(Names[i]);
  defaultIdx := heuristicMatchIndex(Names);
  PrinterCombo.ItemIndex := defaultIdx;
  // M13 print-verification — populate the driver readout for the
  // heuristic-chosen default. If the operator later picks a different
  // printer, NextButtonClick in installer.iss re-runs this lookup
  // before leaving the page.
  recheckPrinterDriverWarning;
end;
