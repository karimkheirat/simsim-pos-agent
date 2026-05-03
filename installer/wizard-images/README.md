# Wizard images

Placeholder. Logo + welcome banner deferred to a design pass with Karim.

In the interim, `installer.iss` does **not** set `WizardImageFile` or
`WizardSmallImageFile`, so Inno Setup's defaults apply (the bundled
generic blue-graphic for the welcome page, the small icon for the
title bar). Operators see a generic-but-functional wizard.

Once design lands, drop the assets here and add the references to
`installer.iss`'s `[Setup]` section:

```
WizardImageFile=installer\wizard-images\banner.bmp        ; 164x314 (large/welcome)
WizardSmallImageFile=installer\wizard-images\logo.bmp      ; 55x58   (header strip)
```

Both files must be `.bmp` per Inno Setup's wizard-image format.
24-bit recommended. Inno also accepts 4 / 8-bit.
