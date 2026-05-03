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
; --- AG2: post-install [Run] status bar (translation pending native review) ---
RunStatusServiceInstall=تثبيت خدمة Windows...
RunStatusServiceStart=بدء تشغيل الخدمة...

; --- AG3: printer picker page (translation pending native review) ---
PrinterPickerCaption=اختيار الطابعة
PrinterPickerSubcaption=اختر طابعة الإيصالات الحرارية المتصلة بهذا الكمبيوتر.
PrinterPickerLabel=طابعة الإيصالات:
PrinterPickerNonePresent=لم يتم اكتشاف أي طابعة على هذا الكمبيوتر. سيتم تثبيت العميل بدون طابعة مكونة. يمكنك إضافة طابعة لاحقًا عن طريق تعديل C:\ProgramData\Simsim\POSAgent\config.json.
PrinterPickerDetectionFailed=تعذر اكتشاف الطابعات المثبتة (فشل PowerShell). سيتم تثبيت العميل بدون طابعة مكونة. يمكنك إضافة طابعة لاحقًا عن طريق تعديل C:\ProgramData\Simsim\POSAgent\config.json.

; --- AG4: pair code entry page (translation pending native review) ---
PairCodeCaption=إقران هذا الجهاز
PairCodeSubcaption=أدخل الرمز الذي تم إنشاؤه من لوحة تحكم Simsim.
PairCodeLabel=على كمبيوتر آخر، افتح Simsim في المتصفح وأنشئ رمز إقران لهذا الجهاز:%nhttps://web-production-6bb4d.up.railway.app/fr/retailer/settings/pos-terminals
PairCodeInputLabel=رمز الإقران (6 أرقام):
PairCodeSkipCheckbox=سأقوم بإقران هذا الجهاز لاحقًا
PairCodeValidationError=يجب أن يتكون الرمز من 6 أرقام بالضبط.
