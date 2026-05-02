package api

import (
	"time"

	"github.com/karimkheirat/simsim-pos-agent/internal/receipt"
)

// receiptFixture is the M1 hardcoded test receipt from POS_AGENT_SPEC.md §7.
// Used by /test-print to verify the end-to-end render → print path without
// requiring a structured request body. Lives in the production binary
// (intentionally not in a _test.go file) because /test-print is exposed
// from the running agent.
var receiptFixture = receipt.Receipt{
	Store: receipt.Store{
		Name:         "Hamoud Boualem - Centre Oran",
		AddressLine1: "12 Rue Larbi Ben M'hidi",
		AddressLine2: "Oran 31000",
		Phone:        "+213 41 ...",
		TaxID:        "NIF/RC line if applicable",
	},
	Terminal:      receipt.Terminal{ID: "trm_...", Label: "Caisse 1"},
	Cashier:       receipt.Cashier{Name: "Amine Benali"},
	ReceiptNumber: "2026-0428-0001",
	IssuedAt:      mustParseTime("2026-04-28T14:32:11+01:00"),
	Currency:      "DZD",
	Lines: []receipt.Line{
		{SKU: "HB-COLA-33", Name: "Hamoud Cola 33cl", Qty: 6, UnitPrice: 45, LineTotal: 270},
	},
	Discounts: []receipt.Discount{
		{Label: "Remise -5%", Amount: -13.50},
	},
	Totals: receipt.Totals{
		Subtotal:      270,
		DiscountTotal: -13.50,
		TaxTotal:      0,
		GrandTotal:    256.50,
	},
	Payment: receipt.Payment{
		Method:   "cash",
		Tendered: 300,
		Change:   43.50,
	},
	FooterLines: []string{
		"Merci de votre visite",
		"Conservez ce ticket",
	},
}

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic("api: invalid fixture timestamp: " + s)
	}
	return t
}
