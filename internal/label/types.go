// Package label is the agent-side wire shape + render pipeline for
// TSPL thermal LABEL printing. The web app resolves a stored template
// (variables substituted) into a Label JSON document; this package
// validates it and renders it to TSPL bytes via internal/tspl.
//
// The agent has NO template knowledge — templates live in the web's
// Prisma schema (M13 Track B PR 3 / B.4). The agent only knows how
// to validate + render a resolved Label.
//
// Coordinate model: all element X/Y values are in DOTS (the TSPL
// convention). At 203dpi — the standard for every printer in
// docs/printer-compatibility.md §3 — 1mm ≈ 8 dots. Validate enforces
// X/Y ≥ 0 and bounded by Size.{Width,Height} * dotsPerMM.
package label

import (
	"errors"
	"fmt"
	"strings"
)

// dotsPerMM is the assumed printer resolution for in-bounds checking.
// 203dpi → 8 dots/mm is the de-facto standard for the printers in
// docs/printer-compatibility.md §3 (Rongta, Xprinter, Aclas, TSC
// budget families). 300dpi models exist (TSC TTP-244 Pro) but the
// bounds check is intentionally conservative — we'd rather reject
// a clearly-out-of-bounds element than silently let the printer
// clip the artwork.
const dotsPerMM = 8

// Dimension caps. width 20-100mm + height 20-150mm covers every
// label stock the pilot uses (price tag 40x30mm, shelf label 60x40mm,
// weighed product 50x40mm), with margin for future formats.
const (
	MinWidthMM  = 20
	MaxWidthMM  = 100
	MinHeightMM = 20
	MaxHeightMM = 150
)

// ElementType discriminates the union members of Element.
type ElementType string

const (
	ElementText    ElementType = "text"
	ElementBarcode ElementType = "barcode"
	ElementQRCode  ElementType = "qrcode"
)

// BarcodeSymbology is the constrained set of barcode symbologies the
// label render supports. CODE128 + EAN13 cover the v1 templates (SKU
// labels via CODE128; consumer EAN-13 from Open Food Facts).
type BarcodeSymbology string

const (
	BarcodeCODE128 BarcodeSymbology = "CODE128"
	BarcodeEAN13   BarcodeSymbology = "EAN13"
)

// SizeMM is the label's physical dimensions in millimetres.
// Width and Height are required and bounded by Min/Max{Width,Height}MM.
type SizeMM struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// GapMM is the inter-label gap and offset (TSPL GAP command).
// Most pre-cut label stock uses Gap=2 or Gap=3, Offset=0.
type GapMM struct {
	Gap    int `json:"gap"`
	Offset int `json:"offset"`
}

// Label is the wire shape consumed by Render. Field names match the
// JSON keys the web client produces.
//
// Printer-state fields (Size, Gap, Direction, Density, Speed, Codepage)
// map 1:1 to TSPL pre-amble commands. Elements are the drawable items.
type Label struct {
	Size      SizeMM    `json:"size"`
	Gap       GapMM     `json:"gap"`
	Direction int       `json:"direction"`
	Density   int       `json:"density"`
	Speed     int       `json:"speed"`
	Codepage  int       `json:"codepage"`
	Elements  []Element `json:"elements"`
}

// Element is the discriminated union of every drawable label primitive.
// Type selects which subset of fields is meaningful — Validate enforces
// the per-type required-field set so the renderer can switch on Type
// safely.
//
// Why one struct (not an interface): JSON encoding/decoding is dramatically
// simpler with a single struct + Type discriminator. The per-type field
// subset is documented + enforced by Validate; renderers consume the
// resolved fields directly.
//
// Common fields (all element types):
//   - Type
//   - X, Y       — origin in DOTS (203dpi convention)
//   - Rotation   — {0, 90, 180, 270} degrees; default 0
//   - Value      — the rendered payload (text content, barcode data, QR data)
//
// Text-specific:
//   - Font      — TSPL font name ("1".."5" or "ROMAN.TTF")
//   - XScale, YScale — integer magnification 1..8 (default 1)
//
// Barcode-specific:
//   - Symbology — CODE128 or EAN13
//   - Height    — bar height in dots
//   - Narrow, Wide — narrow/wide bar widths in dots (typ. 2/2)
//   - Readable  — emit human-readable digits below bars
//
// QR-specific:
//   - ECC  — error correction level: "L" | "M" | "Q" | "H"
//   - Cell — module size in dots
//   - Mode — TSPL mode token (typ. "A" for auto)
type Element struct {
	Type ElementType `json:"type"`

	// Common positioning + payload.
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Rotation int    `json:"rotation,omitempty"`
	Value    string `json:"value"`

	// Text-only.
	Font   string `json:"font,omitempty"`
	XScale int    `json:"x_scale,omitempty"`
	YScale int    `json:"y_scale,omitempty"`

	// Barcode-only.
	Symbology BarcodeSymbology `json:"symbology,omitempty"`
	Height    int              `json:"height,omitempty"`
	Narrow    int              `json:"narrow,omitempty"`
	Wide      int              `json:"wide,omitempty"`
	Readable  bool             `json:"readable,omitempty"`

	// QR-only.
	ECC  string `json:"ecc,omitempty"`
	Cell int    `json:"cell,omitempty"`
	Mode string `json:"mode,omitempty"`
}

// Sentinel errors. Callers can detect via errors.Is; handler maps them
// to 400 LABEL_INVALID (Validate failures) or 400
// LABEL_REQUIRES_UNSUPPORTED_CAPABILITY (capability shortfalls).
var (
	// ErrInvalidLabel wraps every Validate failure for caller detection.
	ErrInvalidLabel = errors.New("label: invalid")

	// ErrQRNotSupported is returned by Render when a Label contains a
	// QR element but the printer's capabilities do NOT include QR.
	// Callers must detect this BEFORE rendering (capability gate) so
	// they can surface LABEL_REQUIRES_UNSUPPORTED_CAPABILITY with the
	// specific feature name.
	ErrQRNotSupported = errors.New("label: qrcode element requires QRSupported capability")
)

// validRotations lists the rotation values TSPL accepts.
var validRotations = map[int]struct{}{0: {}, 90: {}, 180: {}, 270: {}}

// validQRECC lists the QR error-correction levels TSPL accepts.
var validQRECC = map[string]struct{}{"L": {}, "M": {}, "Q": {}, "H": {}}

// Validate enforces the per-element-type contract + dimension bounds.
// On success returns nil; on failure returns an error that wraps
// ErrInvalidLabel with a human-readable detail (used as the 400
// LABEL_INVALID message).
func (l *Label) Validate() error {
	// ── Dimensions ──
	if l.Size.Width < MinWidthMM || l.Size.Width > MaxWidthMM {
		return fmt.Errorf("%w: size.width %d out of range [%d, %d]mm",
			ErrInvalidLabel, l.Size.Width, MinWidthMM, MaxWidthMM)
	}
	if l.Size.Height < MinHeightMM || l.Size.Height > MaxHeightMM {
		return fmt.Errorf("%w: size.height %d out of range [%d, %d]mm",
			ErrInvalidLabel, l.Size.Height, MinHeightMM, MaxHeightMM)
	}
	if l.Gap.Gap < 0 {
		return fmt.Errorf("%w: gap.gap %d must be >= 0", ErrInvalidLabel, l.Gap.Gap)
	}
	if l.Gap.Offset < 0 {
		return fmt.Errorf("%w: gap.offset %d must be >= 0", ErrInvalidLabel, l.Gap.Offset)
	}
	if l.Direction != 0 && l.Direction != 1 {
		return fmt.Errorf("%w: direction %d invalid (want 0 or 1)", ErrInvalidLabel, l.Direction)
	}
	if l.Density < 0 || l.Density > 15 {
		return fmt.Errorf("%w: density %d out of range [0, 15]", ErrInvalidLabel, l.Density)
	}
	if l.Speed < 1 || l.Speed > 12 {
		return fmt.Errorf("%w: speed %d out of range [1, 12]", ErrInvalidLabel, l.Speed)
	}
	// Codepage 0 is fine (printer keeps current); positive values pass through.
	if l.Codepage < 0 {
		return fmt.Errorf("%w: codepage %d must be >= 0", ErrInvalidLabel, l.Codepage)
	}

	// ── Elements ──
	if len(l.Elements) == 0 {
		return fmt.Errorf("%w: elements is empty (no drawable items)", ErrInvalidLabel)
	}

	maxX := l.Size.Width * dotsPerMM
	maxY := l.Size.Height * dotsPerMM
	for i, e := range l.Elements {
		if err := e.validate(i, maxX, maxY); err != nil {
			return err
		}
	}
	return nil
}

func (e *Element) validate(idx, maxX, maxY int) error {
	prefix := fmt.Sprintf("elements[%d]", idx)

	// Common positioning + rotation.
	if e.X < 0 {
		return fmt.Errorf("%w: %s.x %d must be >= 0", ErrInvalidLabel, prefix, e.X)
	}
	if e.Y < 0 {
		return fmt.Errorf("%w: %s.y %d must be >= 0", ErrInvalidLabel, prefix, e.Y)
	}
	if e.X > maxX {
		return fmt.Errorf("%w: %s.x %d exceeds bounds %d dots", ErrInvalidLabel, prefix, e.X, maxX)
	}
	if e.Y > maxY {
		return fmt.Errorf("%w: %s.y %d exceeds bounds %d dots", ErrInvalidLabel, prefix, e.Y, maxY)
	}
	if _, ok := validRotations[e.Rotation]; !ok {
		return fmt.Errorf("%w: %s.rotation %d invalid (want 0, 90, 180, 270)", ErrInvalidLabel, prefix, e.Rotation)
	}
	if strings.TrimSpace(e.Value) == "" {
		return fmt.Errorf("%w: %s.value is empty", ErrInvalidLabel, prefix)
	}

	// Per-type required fields.
	switch e.Type {
	case ElementText:
		if e.Font == "" {
			return fmt.Errorf("%w: %s.font required for text element", ErrInvalidLabel, prefix)
		}
		if e.XScale < 1 || e.XScale > 8 {
			return fmt.Errorf("%w: %s.x_scale %d out of range [1, 8]", ErrInvalidLabel, prefix, e.XScale)
		}
		if e.YScale < 1 || e.YScale > 8 {
			return fmt.Errorf("%w: %s.y_scale %d out of range [1, 8]", ErrInvalidLabel, prefix, e.YScale)
		}
	case ElementBarcode:
		switch e.Symbology {
		case BarcodeCODE128, BarcodeEAN13:
			// ok
		default:
			return fmt.Errorf("%w: %s.symbology %q invalid (want CODE128 or EAN13)", ErrInvalidLabel, prefix, e.Symbology)
		}
		if e.Height < 1 {
			return fmt.Errorf("%w: %s.height %d must be >= 1", ErrInvalidLabel, prefix, e.Height)
		}
		if e.Narrow < 1 {
			return fmt.Errorf("%w: %s.narrow %d must be >= 1", ErrInvalidLabel, prefix, e.Narrow)
		}
		if e.Wide < 1 {
			return fmt.Errorf("%w: %s.wide %d must be >= 1", ErrInvalidLabel, prefix, e.Wide)
		}
		if e.Symbology == BarcodeEAN13 && !isNumericLen(e.Value, 12, 13) {
			return fmt.Errorf("%w: %s.value %q invalid for EAN13 (want 12 or 13 digits)",
				ErrInvalidLabel, prefix, e.Value)
		}
	case ElementQRCode:
		if _, ok := validQRECC[e.ECC]; !ok {
			return fmt.Errorf("%w: %s.ecc %q invalid (want L, M, Q, or H)", ErrInvalidLabel, prefix, e.ECC)
		}
		if e.Cell < 1 || e.Cell > 10 {
			return fmt.Errorf("%w: %s.cell %d out of range [1, 10]", ErrInvalidLabel, prefix, e.Cell)
		}
		if e.Mode == "" {
			return fmt.Errorf("%w: %s.mode required for qrcode element (typ. \"A\")", ErrInvalidLabel, prefix)
		}
	default:
		return fmt.Errorf("%w: %s.type %q invalid (want text, barcode, or qrcode)", ErrInvalidLabel, prefix, e.Type)
	}

	return nil
}

// isNumericLen returns true if s has length in [min, max] and every
// rune is an ASCII digit. Used for EAN-13 validation (12 or 13 digits;
// the printer computes the check digit when 12 are supplied).
func isNumericLen(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
