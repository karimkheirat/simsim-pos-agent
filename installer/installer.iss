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
; Post-install: register and start the Windows service. waituntilterminated
; so we know each step finished before the next one starts. runhidden
; keeps the spawned cmd window invisible to the operator.
;
; Exit code branching + retry / failure messaging lands in AG5. For now
; these declarations are the wiring; failure surfaces only as Inno's
; default "could not run" dialog.
Filename: "{app}\bin\agent.exe"; Parameters: "service install"; \
  StatusMsg: "{cm:RunStatusServiceInstall}"; \
  Flags: runhidden waituntilterminated
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
