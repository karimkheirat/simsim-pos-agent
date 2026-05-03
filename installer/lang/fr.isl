; Simsim POS Agent — French custom messages overlay.
;
; Stock French wizard messages (Welcome, License, etc.) come from
; compiler:Languages\French.isl — bundled with Inno Setup. This file
; only carries app-specific [CustomMessages] referenced by our .iss
; files via {cm:KeyName}.
;
; AG2 wires the {cm:RunStatusServiceInstall} and {cm:RunStatusServiceStart}
; status messages used during the post-install [Run] sequence. AG3 will
; add printer-picker page strings; AG4 will add pair-code page strings.

[CustomMessages]
; --- AG2 + AG5: post-install [Run] status bar ---
RunStatusWriteConfig=Écriture de la configuration...
RunStatusServiceInstall=Installation du service Windows...
RunStatusServiceStart=Démarrage du service...
RunStatusPair=Jumelage de la caisse...

; --- AG5: success page customization ---
; %1 = terminal_label (Caisse 1), %2 = store_name
InstallSuccessPaired=✓ %1 (%2) est connectée.
InstallSuccessSkipped=Installation terminée. Lancez 'agentctl pair' pour jumeler cette caisse.
; %1 = cloud-supplied reason (or PairFailureReasonUnknown fallback);
; %2 = the 6-digit code the operator entered (so they can retry without re-typing)
InstallSuccessPairFailed=Installation terminée, mais le jumelage a échoué : %1. Lancez 'agentctl pair --code %2' depuis une invite de commande pour réessayer.
PairFailureReasonUnknown=raison inconnue

; --- AG6: uninstaller keep-data prompt ---
UninstallKeepDataPrompt=Conserver les données de configuration de la caisse (config, secrets DPAPI, journaux) sous C:\ProgramData\Simsim\POSAgent ?%n%nCliquez « Conserver » pour les garder en cas de réinstallation, ou « Supprimer » pour tout effacer.
UninstallKeepDataYes=Conserver
UninstallKeepDataNo=Supprimer

; --- AG3: printer picker page ---
PrinterPickerCaption=Choix de l'imprimante
PrinterPickerSubcaption=Sélectionnez l'imprimante de tickets thermique connectée à cet ordinateur.
PrinterPickerLabel=Imprimante de tickets :
PrinterPickerNonePresent=Aucune imprimante détectée sur cet ordinateur. L'agent sera installé sans imprimante configurée. Vous pourrez en ajouter une plus tard en modifiant C:\ProgramData\Simsim\POSAgent\config.json.
PrinterPickerDetectionFailed=Impossible de détecter les imprimantes installées (PowerShell a échoué). L'agent sera installé sans imprimante configurée. Vous pourrez en ajouter une plus tard en modifiant C:\ProgramData\Simsim\POSAgent\config.json.

; --- AG4: pair code entry page ---
PairCodeCaption=Jumelage de la caisse
PairCodeSubcaption=Entrez le code généré depuis le tableau de bord Simsim.
PairCodeLabel=Sur un autre ordinateur, ouvrez Simsim dans le navigateur et générez un code de jumelage pour cette caisse :%nhttps://web-production-6bb4d.up.railway.app/fr/retailer/settings/pos-terminals
PairCodeInputLabel=Code de jumelage (6 chiffres) :
PairCodeSkipCheckbox=Je jumellerai cette caisse plus tard
PairCodeValidationError=Le code doit comporter 6 chiffres.
