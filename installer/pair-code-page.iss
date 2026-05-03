// pair-code-page.iss -- AG4: custom wizard page for the pairing code.
//
// This file is #include'd from inside installer.iss's [Code] section.
// Pure Pascal, no section headers; comments use // (Pascal style) per
// the AG3 lesson -- ; comments here would compile-fail with "BEGIN
// expected" because [Code]-included files are textually inlined where
// Pascal grammar applies.
//
// Public surface (consumed by AG5's install steps):
//   function SelectedPairingCode: String;
//     Returns the 6-digit code the operator typed, or '' when they
//     ticked the "pair later" checkbox.
//   function SkipPairing: Boolean;
//     True if the operator chose to pair after install. AG5 uses this
//     to skip the `agentctl pair --code` step entirely.
//
// Page placement: after PrinterPickerPage (CreateCustomPage's first
// arg = PrinterPickerPage.ID), so wizard order is:
//   Welcome -> Printer selection -> Pair code -> Install location
//
// Validation: NextButtonClick (in installer.iss) calls pairCodeValidate
// when leaving this page. If the skip box is checked, validation is a
// no-op. Otherwise the code must be exactly 6 ASCII digits or the
// inline error label appears and Next is blocked.

var
  PairCodePage: TWizardPage;
  PairCodeInfoLabel: TNewStaticText;
  PairCodeInputLabel: TNewStaticText;
  PairCodeEdit: TNewEdit;
  PairCodeSkipCheck: TNewCheckBox;
  PairCodeErrorLabel: TNewStaticText;

function SelectedPairingCode: String;
begin
  if Assigned(PairCodeSkipCheck) and PairCodeSkipCheck.Checked then
    Result := ''
  else if Assigned(PairCodeEdit) then
    Result := Trim(PairCodeEdit.Text)
  else
    Result := '';
end;

function SkipPairing: Boolean;
begin
  Result := Assigned(PairCodeSkipCheck) and PairCodeSkipCheck.Checked;
end;

// pairCodeIsAllDigits returns true iff every char in s is in '0'..'9'.
// Empty string returns false (length-6 check upstream catches this too,
// but isolating the predicate keeps validation readable).
function pairCodeIsAllDigits(s: String): Boolean;
var
  i: Integer;
begin
  Result := False;
  if Length(s) = 0 then Exit;
  for i := 1 to Length(s) do
  begin
    if (s[i] < '0') or (s[i] > '9') then Exit;
  end;
  Result := True;
end;

// pairCodeOnSkipChange greys out the input + clears any error when the
// "I'll pair later" checkbox is ticked. Restores when unticked.
procedure pairCodeOnSkipChange(Sender: TObject);
begin
  PairCodeEdit.Enabled := not PairCodeSkipCheck.Checked;
  if PairCodeSkipCheck.Checked then
    PairCodeErrorLabel.Visible := False;
end;

// pairCodeOnPageActivate resets the inline error visibility when the
// page is (re)entered -- a stale error from a prior Next-Back cycle
// would otherwise persist and confuse the operator.
procedure pairCodeOnPageActivate;
begin
  PairCodeErrorLabel.Visible := False;
end;

procedure createPairCodePage;
begin
  PairCodePage := CreateCustomPage(
    PrinterPickerPage.ID,
    ExpandConstant('{cm:PairCodeCaption}'),
    ExpandConstant('{cm:PairCodeSubcaption}')
  );

  // Info / instruction label with the dashboard URL inlined. The URL
  // is plain text, not a clickable link -- operators copy it manually.
  PairCodeInfoLabel := TNewStaticText.Create(PairCodePage);
  PairCodeInfoLabel.Parent := PairCodePage.Surface;
  PairCodeInfoLabel.Caption := ExpandConstant('{cm:PairCodeLabel}');
  PairCodeInfoLabel.Top := 0;
  PairCodeInfoLabel.Left := 0;
  PairCodeInfoLabel.Width := PairCodePage.SurfaceWidth;
  PairCodeInfoLabel.AutoSize := False;
  PairCodeInfoLabel.WordWrap := True;
  PairCodeInfoLabel.Height := ScaleY(72);

  // Input field label.
  PairCodeInputLabel := TNewStaticText.Create(PairCodePage);
  PairCodeInputLabel.Parent := PairCodePage.Surface;
  PairCodeInputLabel.Caption := ExpandConstant('{cm:PairCodeInputLabel}');
  PairCodeInputLabel.Top := ScaleY(80);
  PairCodeInputLabel.Left := 0;
  PairCodeInputLabel.AutoSize := True;

  // 6-digit text input. MaxLength caps at 6 chars; per-char digit
  // restriction happens at validation time (operator can paste, etc.).
  PairCodeEdit := TNewEdit.Create(PairCodePage);
  PairCodeEdit.Parent := PairCodePage.Surface;
  PairCodeEdit.Top := ScaleY(100);
  PairCodeEdit.Left := 0;
  PairCodeEdit.Width := ScaleX(120);
  PairCodeEdit.MaxLength := 6;

  // "Pair later" escape hatch (M4 spec §3.7 partial-install case).
  PairCodeSkipCheck := TNewCheckBox.Create(PairCodePage);
  PairCodeSkipCheck.Parent := PairCodePage.Surface;
  PairCodeSkipCheck.Caption := ExpandConstant('{cm:PairCodeSkipCheckbox}');
  PairCodeSkipCheck.Top := ScaleY(140);
  PairCodeSkipCheck.Left := 0;
  PairCodeSkipCheck.Width := PairCodePage.SurfaceWidth;
  PairCodeSkipCheck.Checked := False;
  PairCodeSkipCheck.OnClick := @pairCodeOnSkipChange;

  // Inline validation error -- hidden until pairCodeValidate sets it
  // visible. clRed for emphasis; AutoSize lets the text length govern.
  PairCodeErrorLabel := TNewStaticText.Create(PairCodePage);
  PairCodeErrorLabel.Parent := PairCodePage.Surface;
  PairCodeErrorLabel.Caption := ExpandConstant('{cm:PairCodeValidationError}');
  PairCodeErrorLabel.Top := ScaleY(168);
  PairCodeErrorLabel.Left := 0;
  PairCodeErrorLabel.AutoSize := True;
  PairCodeErrorLabel.Font.Color := clRed;
  PairCodeErrorLabel.Visible := False;
end;

// pairCodeValidate is called from NextButtonClick. Returns True to
// allow the wizard to advance; False blocks and surfaces the error
// label. Skip-checked is always allowed (no validation).
function pairCodeValidate: Boolean;
var
  s: String;
begin
  if PairCodeSkipCheck.Checked then
  begin
    Result := True;
    Exit;
  end;
  s := Trim(PairCodeEdit.Text);
  if (Length(s) <> 6) or not pairCodeIsAllDigits(s) then
  begin
    PairCodeErrorLabel.Visible := True;
    // Note: TNewEdit.SetFocus isn't exposed in Inno's Pascal subset;
    // the visible error label is the primary feedback. Operator clicks
    // back into the input themselves.
    Result := False;
    Exit;
  end;
  PairCodeErrorLabel.Visible := False;
  Result := True;
end;
