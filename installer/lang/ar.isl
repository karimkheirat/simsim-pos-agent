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
; --- AG2 + AG5: post-install [Run] status bar (translation pending native review) ---
RunStatusWriteConfig=كتابة التكوين...
RunStatusServiceInstall=تثبيت خدمة Windows...
RunStatusServiceStart=بدء تشغيل الخدمة...
RunStatusPair=إقران هذا الجهاز...

; --- AG5: success page customization (translation pending native review) ---
; %1 = terminal_label, %2 = store_name
InstallSuccessPaired=✓ %1 (%2) متصل.
InstallSuccessSkipped=اكتمل التثبيت. قم بتشغيل 'agentctl pair' لإقران هذا الجهاز.
; %1 = cloud reason; %2 = the 6-digit code
InstallSuccessPairFailed=اكتمل التثبيت، ولكن فشل الإقران: %1. قم بتشغيل 'agentctl pair --code %2' من موجه الأوامر لإعادة المحاولة.
PairFailureReasonUnknown=سبب غير معروف

; --- AG6: uninstaller keep-data prompt (translation pending native review) ---
UninstallKeepDataPrompt=هل تريد الاحتفاظ ببيانات تكوين هذا الجهاز (config، أسرار DPAPI، السجلات) ضمن C:\ProgramData\Simsim\POSAgent؟%n%nانقر «احتفاظ» للاحتفاظ بها في حالة إعادة التثبيت، أو «حذف» لمسح كل شيء.
UninstallKeepDataYes=احتفاظ
UninstallKeepDataNo=حذف

; --- AG3: printer picker page (translation pending native review) ---
PrinterPickerCaption=اختيار الطابعة
PrinterPickerSubcaption=اختر طابعة الإيصالات الحرارية المتصلة بهذا الكمبيوتر.
PrinterPickerLabel=طابعة الإيصالات:
PrinterPickerNonePresent=لم يتم اكتشاف أي طابعة على هذا الكمبيوتر. سيتم تثبيت العميل بدون طابعة مكونة. يمكنك إضافة طابعة لاحقًا عن طريق تعديل C:\ProgramData\Simsim\POSAgent\config.json.
PrinterPickerDetectionFailed=تعذر اكتشاف الطابعات المثبتة (فشل PowerShell). سيتم تثبيت العميل بدون طابعة مكونة. يمكنك إضافة طابعة لاحقًا عن طريق تعديل C:\ProgramData\Simsim\POSAgent\config.json.

; --- AG4: pair code entry page (translation pending native review) ---
PairCodeCaption=إقران هذا الجهاز
PairCodeSubcaption=أدخل الرمز الذي تم إنشاؤه من لوحة تحكم Simsim.
PairCodeLabel=على كمبيوتر آخر، افتح Simsim في المتصفح وأنشئ رمز إقران لهذا الجهاز:%nhttps://opensimsim.co/fr/retailer/settings/pos-terminals
PairCodeInputLabel=رمز الإقران (6 أرقام):
PairCodeSkipCheckbox=سأقوم بإقران هذا الجهاز لاحقًا
PairCodeValidationError=يجب أن يتكون الرمز من 6 أرقام بالضبط.

; --- M13 print-verification: printer-picker driver advisory (translation pending native review) ---
; %1 = driver name
PrinterPickerDriverWarning=تنبيه: برنامج تشغيل هذه الطابعة الحالي هو «%1». لطباعة موثوقة للإيصالات، يُنصح ببرنامج التشغيل «Generic / Text Only». يمكنك المتابعة؛ ستتم طباعة اختبار في نهاية التثبيت.

; --- M13 print-verification: post-pair test-print step (translation pending native review) ---
RunStatusTestPrint=جارٍ طباعة إيصال اختباري...
TestPrintConfirmTitle=التحقق من الطباعة
; %1 = printer name, %2 = driver name
TestPrintConfirmBody=تمت طباعة إيصال اختباري للتو.%n%nالطابعة: %1%nبرنامج التشغيل: %2%n%nهل طُبع بشكل صحيح؟
TestPrintConfirmYes=نعم
TestPrintConfirmNoChooseOther=لا — اختر طابعة أخرى
; %1 = current printer name
TestPrintFireFailedBody=فشل إرسال اختبار الطباعة إلى الطابعة «%1». تحقق من أن الطابعة قيد التشغيل ومتصلة، ثم أعد المحاولة.
TestPrintRetryPickerCaption=اختر طابعة أخرى
TestPrintRetryPickerBody=اختر الطابعة المراد استخدامها في اختبار الطباعة التالي. ستتم إعادة تكوين العميل وإعادة تشغيل الخدمة قبل المحاولة الجديدة.
TestPrintRetryPickerOk=إعادة المحاولة
TestPrintRetryPickerCancel=إلغاء
TestPrintNoOtherPrinters=لم يتم اكتشاف طابعات أخرى. وصِّل طابعة وثبِّتها في Windows، ثم أعد تشغيل برنامج الإعداد.

; --- M13 print-verification: wpFinished suffix (translation pending native review) ---
InstallSuccessPrintVerified=✓ تم تأكيد طباعة الإيصال الاختباري من قِبل المشغّل.
InstallSuccessPrintNotVerified=⚠ لم يتم تأكيد الطباعة. قبل أول إيصال للعميل، افتح لوحة تحكم Simsim وشغِّل «اطبع إيصالًا اختباريًا» من صفحة الكاسة.
