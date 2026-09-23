// Package receipt models the structured receipt sent by the Simsim POS
// web app and renders it to ESC/POS bytes via internal/escpos.
//
// The JSON shape on the wire is fixed by POS_AGENT_SPEC.md §7. The Go
// struct names mirror the JSON keys via snake_case json tags so an
// incoming /print request body can be unmarshaled directly into Receipt.
package receipt

import "time"

// Receipt is the full document submitted by the POS for one printed ticket.
type Receipt struct {
	Store         Store      `json:"store"`
	Terminal      Terminal   `json:"terminal"`
	Cashier       Cashier    `json:"cashier"`
	ReceiptNumber string     `json:"receipt_number"`
	IssuedAt      time.Time  `json:"issued_at"`
	Currency      string     `json:"currency"`
	Lines         []Line     `json:"lines"`
	Discounts     []Discount `json:"discounts"`
	Totals        Totals     `json:"totals"`
	Payment       Payment    `json:"payment"`
	FooterLines   []string   `json:"footer_lines"`

	// SIM-170 (DESIGN_SYSTEM §67.2/§67.5) — optional words the web app MAY
	// send so the paper reads in the till's language. Every one is optional:
	// absent, the renderer infers what it can from the fields above (see
	// render.go) and falls back to French defaults.

	// DocumentTitle is the document's name ("TICKET DE CAISSE", "TICKET DE
	// REMBOURSEMENT", "RAPPORT DE CAISSE"). Absent → inferred.
	DocumentTitle string `json:"document_title,omitempty"`
	// Shopper is the head's shopper line ("Client K7M2QX · Karim B.").
	Shopper string `json:"shopper,omitempty"`
	// Labels are the words of the totals and tender block.
	Labels *Labels `json:"labels,omitempty"`
}

// Labels are the fixed words the ticket prints around the figures. Any
// empty field falls back to the French default in render.go.
type Labels struct {
	Subtotal  string `json:"subtotal,omitempty"`  // "Sous-total"
	Discounts string `json:"discounts,omitempty"` // "Remises"
	Tax       string `json:"tax,omitempty"`       // "TVA"
	Total     string `json:"total,omitempty"`     // "TOTAL"
	Cash      string `json:"cash,omitempty"`      // "Espèces"
	Change    string `json:"change,omitempty"`    // "Monnaie"
}

// Store is the merchant identification block printed at the top.
type Store struct {
	Name         string `json:"name"`
	AddressLine1 string `json:"address_line_1"`
	AddressLine2 string `json:"address_line_2"`
	Phone        string `json:"phone"`
	TaxID        string `json:"tax_id"`
}

// Terminal identifies the POS register that produced the receipt.
type Terminal struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Cashier identifies the operator who rang up the sale.
type Cashier struct {
	Name string `json:"name"`
}

// Line is one item line on the receipt.
type Line struct {
	SKU           string  `json:"sku"`
	Name          string  `json:"name"`
	Qty           int     `json:"qty"`
	UnitPrice     float64 `json:"unit_price"`
	LineTotal     float64 `json:"line_total"`
	DiscountLabel *string `json:"discount_label"`

	// SIM-170 — optional.

	// Detail is the line's working printed under its name ("2 × 120 DA",
	// "0,342 kg × 380 DA/kg"). Absent → the renderer writes "qty × unit".
	Detail string `json:"detail,omitempty"`
	// Kind is "money" or "count" — how a rapport row's value prints
	// ("18 378 DA" vs "7"). Absent on a rapport row → a bare number.
	// Ticket lines are always money.
	Kind string `json:"kind,omitempty"`
	// Section is a rapport row's block title ("VENTES", "CAISSE"). A
	// change of section prints a dashed rule and the title in bold.
	Section string `json:"section,omitempty"`
	// Bold prints the row in bold (the rapport's "Écart").
	Bold bool `json:"bold,omitempty"`
}

// Discount is a top-level adjustment applied to the receipt subtotal.
type Discount struct {
	Label  string  `json:"label"`
	Amount float64 `json:"amount"`
}

// Totals carries the computed totals for the receipt. Values are floats
// to match the wire JSON; arithmetic is performed by the POS, not here.
type Totals struct {
	Subtotal      float64 `json:"subtotal"`
	DiscountTotal float64 `json:"discount_total"`
	TaxTotal      float64 `json:"tax_total"`
	GrandTotal    float64 `json:"grand_total"`
}

// Payment captures how the sale was settled. v1 supports cash only.
type Payment struct {
	Method   string  `json:"method"`
	Tendered float64 `json:"tendered"`
	Change   float64 `json:"change"`
}
