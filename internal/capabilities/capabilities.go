// Package capabilities reports what a configured thermal printer can
// actually do — paper width, cut, drawer kick, supported barcodes, etc.
//
// M13 Track A.5a — exposed by GET /capabilities on the agent's loopback
// API and consumed web-side to gate UI affordances (label barcode
// filters, drawer toggle visibility, paper-width-aware preview).
//
// The lookup table is hand-maintained from docs/printer-compatibility.md
// in the web repo (the source-of-truth living matrix). Unknown printer
// names fall back to conservative defaults that work on every model in
// §2.2 of that matrix.
//
// Note on `PaperWidthMM`: the value returned from `Lookup` is a per-model
// HINT only. The /capabilities handler overlays the agent's configured
// PaperWidthMM (config.json) on top before returning to the web client.
// The hint is informational — useful in admin UI as "this model
// typically ships with 80mm but you've configured 58mm" but not
// load-bearing on the print path.
package capabilities

import "strings"

// PrinterCapabilities is the shape returned by GET /capabilities.
// Field names match the wire JSON shape (snake_case via json tags); the
// Go field names follow conventional camelCase.
type PrinterCapabilities struct {
	// PaperWidthMM is 58 or 80 in v1. The agent's renderer branches on
	// this; the web client uses it to size receipt previews.
	PaperWidthMM int `json:"paper_width_mm"`

	// CutSupported gates emission of the GS V 0 (full cut) command after
	// each receipt. When false the renderer emits extra paper feed lines
	// so the cashier can tear manually.
	CutSupported bool `json:"cut_supported"`

	// DrawerSupported gates the drawer-kick UI toggle and the
	// /drawer/open endpoint at the web client. The agent still HONORS
	// open_drawer_after on /print regardless — false here means "the
	// printer has no drawer port wired", not "refuse to emit the kick."
	DrawerSupported bool `json:"drawer_supported"`

	// BarcodeTypes lists ESC/POS barcode symbology names supported by
	// the printer for the receipt path. Used by Track B's label
	// renderer to filter the cashier's template format dropdown.
	// Lowercase-or-uppercase is canonical here — we emit uppercase.
	BarcodeTypes []string `json:"barcode_types"`

	// Codepages lists ESC/POS codepages the printer can switch to. v1
	// ships everything in CP858 (Latin-1 + euro); future French
	// diacritics outside CP858 would require an additional codepage.
	Codepages []string `json:"codepages"`

	// QRSupported gates QR generation for the receipt + label paths.
	// Most Algeria-realistic thermal printers (TM-T20, RP-58, XP-58)
	// do NOT have native QR support; the T88VI does but we don't
	// flag it on by default in v1.
	QRSupported bool `json:"qr_supported"`

	// FirmwareVersion is reserved for future runtime queries. The
	// agent has no way to query firmware today; omitted via omitempty
	// until we add an ESC/POS GS I / ID query path.
	FirmwareVersion string `json:"firmware_version,omitempty"`

	// RawStatus is reserved for future telemetry payloads (full ASB
	// response from the printer, codepage table dump, etc.). Omitted
	// until a source exists.
	RawStatus string `json:"raw_status,omitempty"`

	// Source describes where this row came from. "model_lookup" means
	// the printer's Name() matched an entry in the hardcoded table;
	// "fallback" means it didn't and conservative defaults were
	// returned. UI consumers should treat fallback as "low-confidence
	// — let the admin override."
	Source Source `json:"source"`
}

// Source is the provenance of a capability row.
type Source string

const (
	// SourceModelLookup — the printer name matched a row in the
	// hardcoded family table.
	SourceModelLookup Source = "model_lookup"

	// SourceFallback — no model matched; conservative defaults
	// returned.
	SourceFallback Source = "fallback"
)

// Default barcode + codepage sets for v1. Every printer in the lookup
// table (and the fallback) reports these because every model in
// docs/printer-compatibility.md §2 supports the ESC/POS barcode
// subset the agent emits.
var (
	defaultBarcodes  = []string{"CODE128", "EAN13"}
	defaultCodepages = []string{"CP858"}
)

// Default barcode + codepage sets for TSPL label printers. CP1252
// is the TSPL2 default codepage; QR + CODE128 + EAN13 are all
// standard-TSPL2 commands supported by every model in
// docs/printer-compatibility.md §3.
var (
	defaultLabelBarcodes  = []string{"CODE128", "EAN13", "QR"}
	defaultLabelCodepages = []string{"CP1252"}
)

// modelEntry pairs a case-insensitive substring with a factory for the
// capabilities returned when a printer's Name() matches. Entries are
// matched in declaration order — MOST SPECIFIC FIRST (e.g., "RP-332"
// before "RP-330", "TM-T88VI" before "TM-T88"). Spec §3.A.5a.
//
// Factory (not stored struct) so every Lookup call gets fresh slices —
// callers may mutate the returned PrinterCapabilities without affecting
// other callers or the table.
type modelEntry struct {
	substring string
	factory   func() PrinterCapabilities
}

// modelTable is the ordered list of known printer-name patterns and
// their capabilities. Order matters — Lookup returns the first match.
//
// All v1 entries currently report identical capabilities (80mm, cut,
// drawer, CODE128 + EAN13, CP858, no QR). The structure is in place
// so adding model-specific differences (e.g., TM-T88VI's QR support,
// 58mm RP-58 variants) is a one-line change.
//
// Sourced from docs/printer-compatibility.md §2.1–§2.2 in the web
// repo. When that matrix updates, mirror here.
var modelTable = []modelEntry{
	// ── Star Micronics ──────────────────────────────────────────
	// SP-331 is AB Market's pilot printer. End-to-end-validated.
	{substring: "sp-331", factory: defaultModelCaps},

	// ── Epson — most specific FIRST so longer model names win ──
	// TM-T88VI/VII have native QR support — flagged here for the
	// future v2 entry. v1 conservatively reports QRSupported: false
	// because we haven't validated the QR emission path against a
	// physical T88VI. Listed before TM-T88 so the substring match
	// wins for V6/V7-suffixed names.
	{substring: "tm-t88vii", factory: defaultModelCaps}, // TODO v2: QR
	{substring: "tm-t88vi", factory: defaultModelCaps},  // TODO v2: QR
	{substring: "tm-t88v", factory: defaultModelCaps},
	{substring: "tm-t88", factory: defaultModelCaps},
	{substring: "tm-t20iii", factory: defaultModelCaps},
	{substring: "tm-t20ii", factory: defaultModelCaps},
	{substring: "tm-t20", factory: defaultModelCaps},
	{substring: "tm-u220", factory: defaultModelCaps},

	// ── Rongta — RP-332 before RP-330 so "RP-330" doesn't match a
	// printer actually named "RP-332".
	{substring: "rp-332", factory: defaultModelCaps},
	{substring: "rp-330", factory: defaultModelCaps},
	{substring: "rp-80", factory: defaultModelCaps},
	{substring: "rp-58", factory: defaultModelCaps},

	// ── Xprinter ────────────────────────────────────────────────
	{substring: "xp-n160", factory: defaultModelCaps},
	{substring: "xp-80", factory: defaultModelCaps},
	{substring: "xp-58", factory: defaultModelCaps},
}

// defaultModelCaps is the per-model factory for v1 — every model in
// the table currently shares the same conservative-but-validated set.
// Cloning per-call so the slices stay independent (callers may mutate
// the returned struct's slices; we don't want a Lookup result to
// alias another's).
func defaultModelCaps() PrinterCapabilities {
	return PrinterCapabilities{
		PaperWidthMM:    80,
		CutSupported:    true,
		DrawerSupported: true,
		BarcodeTypes:    append([]string(nil), defaultBarcodes...),
		Codepages:       append([]string(nil), defaultCodepages...),
		QRSupported:     false,
		Source:          SourceModelLookup,
	}
}

// fallbackCaps mirrors defaultModelCaps but with Source=Fallback. The
// chosen defaults are the most common Algeria-realistic configuration
// (80mm, cut + drawer, CODE128 + EAN13). Conservative in the sense
// of "matches the most common pilot store hardware"; an unknown 58mm
// printer would silently get 80mm columns, but that's correctable via
// the agent's PaperWidthMM config (the renderer reads the config,
// NOT this hint).
func fallbackCaps() PrinterCapabilities {
	return PrinterCapabilities{
		PaperWidthMM:    80,
		CutSupported:    true,
		DrawerSupported: true,
		BarcodeTypes:    append([]string(nil), defaultBarcodes...),
		Codepages:       append([]string(nil), defaultCodepages...),
		QRSupported:     false,
		Source:          SourceFallback,
	}
}

// Lookup returns the PrinterCapabilities for the given printer name.
// Matching is case-insensitive substring against modelTable entries in
// order; first match wins. Returns the fallback caps for an empty name
// or no match.
//
// The returned PrinterCapabilities is a fresh value — every call
// allocates new slices via the entry's factory, so callers may mutate
// the BarcodeTypes / Codepages slices without affecting the table or
// other callers' results.
func Lookup(printerName string) PrinterCapabilities {
	name := strings.ToLower(printerName)
	if name == "" {
		return fallbackCaps()
	}
	for _, entry := range modelTable {
		if strings.Contains(name, entry.substring) {
			return entry.factory()
		}
	}
	return fallbackCaps()
}

// labelModelTable is the TSPL label-printer counterpart of modelTable.
// Substring matching is case-insensitive; most-specific entries come
// first (e.g. xp-dt426b before xp-dt426).
//
// Sourced from docs/printer-compatibility.md §3 in the web repo:
//   - Rongta:   RP-410, RP-420, RP-426A
//   - Xprinter: XP-DT426B, XP-DT108B, XP-470B
//   - Aclas:    PP7X, PP8X
//   - TSC:      TDP-244, TTP-244 Pro
//
// All v1 entries report identical TSPL caps: 60mm width, NO cut, NO
// drawer (label printers don't ship cash drawers), CODE128 + EAN13 +
// QR, CP1252. Structure mirrors modelTable so per-model differences
// can be one-line factories in v2.
var labelModelTable = []modelEntry{
	// ── Rongta — RP-426A before RP-420 (substring order is irrelevant
	// here since "rp-420" is not a substring of "rp-426a", but kept
	// most-specific-first to match the receipt table convention) ──
	{substring: "rp-426a", factory: defaultLabelModelCaps},
	{substring: "rp-420", factory: defaultLabelModelCaps},
	{substring: "rp-410", factory: defaultLabelModelCaps},

	// ── Xprinter — XP-DT426B before XP-DT108B before XP-470B ──
	{substring: "xp-dt426b", factory: defaultLabelModelCaps},
	{substring: "xp-dt108b", factory: defaultLabelModelCaps},
	{substring: "xp-470b", factory: defaultLabelModelCaps},

	// ── Aclas — PP7X / PP8X ─────────────────────────────────────
	{substring: "pp8x", factory: defaultLabelModelCaps},
	{substring: "pp7x", factory: defaultLabelModelCaps},

	// ── TSC — TTP-244 Pro before TDP-244 ────────────────────────
	{substring: "ttp-244 pro", factory: defaultLabelModelCaps},
	{substring: "ttp-244", factory: defaultLabelModelCaps},
	{substring: "tdp-244", factory: defaultLabelModelCaps},
}

// defaultLabelModelCaps is the per-model factory for v1 TSPL label
// printers. Cloning per-call so caller slice mutations stay isolated
// (matches receiptCaps pattern).
//
// Defaults:
//   - PaperWidthMM: 60     — common 60x40mm label stock
//   - CutSupported: false  — label printers tear at the gap
//   - DrawerSupported: false — no cash drawer on label printers
//   - BarcodeTypes: CODE128 + EAN13 + QR
//   - Codepages: CP1252 (TSPL2 default)
//   - QRSupported: true    — TSPL2 native QRCODE command
//   - Source: model_lookup
func defaultLabelModelCaps() PrinterCapabilities {
	return PrinterCapabilities{
		PaperWidthMM:    60,
		CutSupported:    false,
		DrawerSupported: false,
		BarcodeTypes:    append([]string(nil), defaultLabelBarcodes...),
		Codepages:       append([]string(nil), defaultLabelCodepages...),
		QRSupported:     true,
		Source:          SourceModelLookup,
	}
}

// labelFallbackCaps is the TSPL counterpart of fallbackCaps. Same
// conservative defaults as defaultLabelModelCaps but with Source =
// SourceFallback so admin UIs can flag low-confidence rows.
func labelFallbackCaps() PrinterCapabilities {
	return PrinterCapabilities{
		PaperWidthMM:    60,
		CutSupported:    false,
		DrawerSupported: false,
		BarcodeTypes:    append([]string(nil), defaultLabelBarcodes...),
		Codepages:       append([]string(nil), defaultLabelCodepages...),
		QRSupported:     true,
		Source:          SourceFallback,
	}
}

// LookupLabel returns the PrinterCapabilities for the given TSPL
// LABEL printer name (NOT a receipt printer — those use Lookup).
// Matching is case-insensitive substring against labelModelTable
// entries in order; first match wins. Returns the label-fallback
// caps for an empty name or no match.
//
// The split between Lookup and LookupLabel reflects the two-printer
// architecture: a pilot deployment has separate ESC/POS receipt and
// TSPL label printers; each has its own capability profile. The
// agent's /capabilities response returns the receipt profile at the
// top level and the label profile under the `label` sibling key
// (M13 Track B PR 1 additive shape, Q2 decision).
func LookupLabel(printerName string) PrinterCapabilities {
	name := strings.ToLower(printerName)
	if name == "" {
		return labelFallbackCaps()
	}
	for _, entry := range labelModelTable {
		if strings.Contains(name, entry.substring) {
			return entry.factory()
		}
	}
	return labelFallbackCaps()
}
