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
; --- AG2: post-install [Run] status bar ---
RunStatusServiceInstall=Installation du service Windows...
RunStatusServiceStart=Démarrage du service...

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
