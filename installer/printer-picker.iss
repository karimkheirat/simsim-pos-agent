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

// SelectedPrinterName returns the picked printer, or '' when the page
// reported zero printers or detection failure.
function SelectedPrinterName: String;
begin
  if Assigned(PrinterCombo) and PrinterCombo.Visible and (PrinterCombo.ItemIndex >= 0) then
    Result := PrinterCombo.Items[PrinterCombo.ItemIndex]
  else
    Result := '';
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
end;
