package label

import (
	"fmt"
	"strconv"

	"github.com/karimkheirat/simsim-pos-agent/internal/tspl"
)

// RenderOptions carries the per-render context the label package
// can't infer from the Label alone: the TSPL dialect (which decides
// the EAN-13 hyphen split) and the printer's QR capability gate.
//
// Why not import internal/capabilities here: the label package stays
// decoupled from the capability lookup table — handlers are
// responsible for resolving the printer's capabilities once and
// passing the narrow subset the renderer cares about. Keeps the
// package boundary clean.
type RenderOptions struct {
	// Dialect selects EAN-13 command identifier ("EAN13" vs "EAN-13").
	// Defaults to tspl.DialectStandard when zero-value.
	Dialect tspl.Dialect

	// QRSupported gates QR element rendering. When false, a Label
	// containing a QR element returns ErrQRNotSupported BEFORE any
	// TSPL bytes are emitted — so a partially-printed label can't
	// reach the spool.
	QRSupported bool
}

// Render converts a validated Label into the TSPL byte stream the
// label printer consumes. Callers MUST call Label.Validate first;
// Render does NOT re-validate (the wrapped handler does both, in the
// right order for error mapping).
//
// Capability gate: if any QR element appears and opts.QRSupported is
// false, Render returns ErrQRNotSupported with no bytes emitted.
// All other capability checks (CutSupported, DrawerSupported) are
// irrelevant on label printers and not surfaced.
//
// Output structure:
//
//	CLS                                  # clear image buffer
//	SIZE <w> mm,<h> mm                   # label dimensions
//	GAP <gap> mm,<offset> mm             # inter-label gap
//	DIRECTION <d>                        # 0 or 1
//	DENSITY <n>                          # 0..15
//	SPEED <ips>                          # 1..12
//	CODEPAGE <name>                      # printer codepage (e.g. 1252)
//	<element>...                         # TEXT / BARCODE / QRCODE per type
//	PRINT 1,1                            # emit + advance one label
//
// Each command line is CRLF-terminated by the internal/tspl builder.
func Render(l Label, opts RenderOptions) ([]byte, error) {
	if opts.Dialect == "" {
		opts.Dialect = tspl.DialectStandard
	}

	// Capability gate first — surface the unsupported-feature error
	// BEFORE any bytes hit the buffer (so a 400 response never leaves
	// a partial job in the printer queue).
	for _, e := range l.Elements {
		if e.Type == ElementQRCode && !opts.QRSupported {
			return nil, ErrQRNotSupported
		}
	}

	b := tspl.NewWithDialect(opts.Dialect)

	// Pre-amble — set printer state. Order matches the TSC TSPL2
	// Programming Manual's reference label-init sequence.
	b.CLS().
		Size(l.Size.Width, l.Size.Height).
		Gap(l.Gap.Gap, l.Gap.Offset).
		Direction(tspl.Direction(l.Direction)).
		Density(l.Density).
		Speed(l.Speed).
		Codepage(strconv.Itoa(l.Codepage))

	// Elements — render in order so a template's z-order is preserved
	// (later elements overpaint earlier ones at the same coordinates).
	for i, e := range l.Elements {
		if err := appendElement(b, e, opts.Dialect); err != nil {
			return nil, fmt.Errorf("element[%d]: %w", i, err)
		}
	}

	// Post-amble — emit one copy of the assembled image.
	b.Print(1, 1)
	return b.Bytes(), nil
}

// appendElement dispatches on Element.Type and appends the matching
// TSPL command to the builder.
//
// Variable sanitization (CRLF / inner-quote scrubbing) is already
// performed by the tspl builder methods (PR 1 — see sanitizeArg in
// internal/tspl/tspl.go); no need to repeat it here.
func appendElement(b *tspl.Builder, e Element, dialect tspl.Dialect) error {
	switch e.Type {
	case ElementText:
		// TextCP1252 transcodes the UTF-8 value to CP1252 (TSPL's
		// printer codepage) — so French diacritics render as the
		// intended glyphs. Arabic falls back to '?' per the codepage
		// transcoder's contract.
		b.TextCP1252(e.X, e.Y, tspl.FontName(e.Font), e.Rotation, e.XScale, e.YScale, e.Value)
		return nil

	case ElementBarcode:
		hr := 0
		if e.Readable {
			hr = 2 // human-readable, centered below bars (TSPL convention)
		}
		switch e.Symbology {
		case BarcodeCODE128:
			b.BarcodeCode128(e.X, e.Y, e.Height, hr, e.Rotation, e.Narrow, e.Wide, e.Value)
		case BarcodeEAN13:
			// BarcodeEAN13 honors the builder's dialect (set at
			// NewWithDialect above); no extra plumbing here.
			b.BarcodeEAN13(e.X, e.Y, e.Height, hr, e.Rotation, e.Narrow, e.Wide, e.Value)
		default:
			// Should not be reachable post-Validate, but defensive.
			return fmt.Errorf("unknown symbology %q", e.Symbology)
		}
		return nil

	case ElementQRCode:
		b.QRCode(e.X, e.Y, tspl.QRMode(e.ECC), e.Cell, e.Mode, e.Rotation, e.Value)
		return nil
	}

	// Should not be reachable post-Validate, but defensive.
	return fmt.Errorf("unknown element type %q", e.Type)
}
