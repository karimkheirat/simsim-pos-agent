// Package scale abstracts the in-store label-scale transport, the way
// internal/printer abstracts print transports. Production uses a TCP
// connection to an Aclas LS2-series scale on the store LAN; tests use
// the Mock backend.
//
// The wire encoding lives in the internal/scale/link32 sub-package
// (pure byte-building, like internal/escpos); this package owns the
// session/transport and the mapping from the cloud's ScalePluEntry
// shape onto link32's Appendix-1 PLU record fields.
package scale

import (
	"context"
	"fmt"
	"strings"

	"github.com/karimkheirat/simsim-pos-agent/internal/scale/link32"
)

// PLU mirrors the main repo's ScalePluEntry (src/lib/scale/plu-payload.ts)
// — the per-product entry the cloud's sync job POSTs to the agent. JSON
// tags match the TypeScript field names verbatim so the payload passes
// through without a mapping layer.
type PLU struct {
	// PLU is the product's scale code (InventoryRecord.priceEmbeddedCode
	// cloud-side). ASCII digits.
	PLU string `json:"plu"`
	// Name is the display name, ≤36 code points (cloud-truncated).
	Name string `json:"name"`
	// PriceCentimes is the integer unit price in centimes — matches the
	// LS2 Unit Price column's "no decimal fraction" mode directly.
	PriceCentimes int `json:"priceCentimes"`
	// SoldBy is "weight" or "piece".
	SoldBy string `json:"soldBy"`
	// MeasureUnit is "kg" or "l" (per-measureUnit price).
	MeasureUnit string `json:"measureUnit"`
}

// Result is the per-PLU outcome of a SendPLUs call.
type Result struct {
	PLU   string `json:"plu"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Scale is the transport interface satisfied by every backend.
type Scale interface {
	// SendPLUs downloads the given PLU entries to the scale. It returns
	// one Result per entry, in order. Per-PLU problems (encoding, a
	// scale-side error code) are reported in the Results with err nil;
	// a non-nil error means the session itself failed (dial, timeout,
	// protocol breakdown) — Results are still returned for whatever was
	// attempted, with unattempted entries marked as such.
	SendPLUs(ctx context.Context, entries []PLU) ([]Result, error)

	// IsReachable reports whether the scale currently accepts TCP
	// connections. Implementations should not block longer than ~200ms
	// (same contract as printer.Printer.IsReachable).
	IsReachable() bool

	// Name returns the scale identifier ("scale:<ip>:<port>" for TCP,
	// "mock" for the test backend).
	Name() string
}

// DefaultBarcodeType is the Appendix 2 barcode coding-list entry used
// for every downloaded PLU: type 02 = EAN-13 with 2-digit department,
// 5-digit commodity number, 5-digit total price — the classic
// price-embedded weighed-goods layout.
//
// TODO(verify-on-hardware): confirm type 02 matches the barcode format
// the store's POS expects to scan back, and that the cloud's
// priceEmbeddedCode digit count fits the 5-digit commodity field.
const DefaultBarcodeType = 2

// DefaultDepartment is the department every PLU is filed under.
//
// TODO(verify-on-hardware): department 0 pending a real department
// scheme; the barcode's leading "DD" digits come from this.
const DefaultDepartment = 0

// toLink32 maps one cloud entry onto the link32 PLU record fields.
// Returns a descriptive error when the entry cannot be represented in
// the documented protocol (bad code, unsupported unit) — surfaced as a
// per-PLU Result, never a session failure.
func toLink32(e PLU) (link32.PLU, error) {
	code := strings.TrimSpace(e.PLU)
	if code == "" {
		return link32.PLU{}, fmt.Errorf("plu is empty")
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return link32.PLU{}, fmt.Errorf("plu %q must be ASCII digits", code)
		}
	}
	// The same code serves as the operator-typed LFCode (≤6 digits)
	// and the barcode commodity number (Code, ≤10 digits).
	//
	// TODO(verify-on-hardware): the manual defines LFCode ("uniquely
	// to identify every life commodity") and Code ("refer to barcode
	// coding list") as separate columns; the cloud sends one code. If
	// hardware wants them distinct, split here.
	if len(code) > 6 {
		return link32.PLU{}, fmt.Errorf("plu %q exceeds 6 digits (LFCode column width)", code)
	}

	unit, err := weightUnitFor(e.SoldBy, e.MeasureUnit)
	if err != nil {
		return link32.PLU{}, err
	}

	return link32.PLU{
		LFCode:            code,
		Code:              code,
		Name:              link32.TruncateName(strings.TrimSpace(e.Name)),
		UnitPriceCentimes: e.PriceCentimes,
		WeightUnit:        unit,
		BarcodeType:       DefaultBarcodeType,
		Department:        DefaultDepartment,
	}, nil
}

// weightUnitFor maps the cloud's (soldBy, measureUnit) pair to a
// documented LS2 weight-unit code. Only combinations the manual can
// represent are allowed — anything else is a per-PLU error rather than
// a silently mislabeled product on the shop floor.
//
// TODO(verify-on-hardware): piece-sold items use PCS(Kg) ('A') rather
// than PCS(g) ('9') — both are documented; which one the store's
// labels should carry needs a device check.
func weightUnitFor(soldBy, measureUnit string) (string, error) {
	switch soldBy {
	case "weight":
		if measureUnit == "kg" {
			return link32.WeightUnitKg, nil
		}
		// The LS2 unit list has no liter (or other volume) code — a
		// weight-scale can't price by volume.
		return "", fmt.Errorf("soldBy=weight with measureUnit %q not representable on the scale (only kg)", measureUnit)
	case "piece":
		return link32.WeightUnitPCSKg, nil
	default:
		return "", fmt.Errorf("soldBy %q unknown (want weight or piece)", soldBy)
	}
}
