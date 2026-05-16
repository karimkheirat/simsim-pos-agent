package label

// Test fixtures shared between render_test.go and the golden-bytes
// regeneration helper. Defining them in one place keeps the goldens
// reproducible — if you change a fixture, regenerate the goldens with
// `go test -run TestRegenerateGoldens -tags regenerate`.

// priceTagFixture — 50x40mm label with a product name (Font3), a
// price line (Font4 + magnified), and an EAN-13 barcode. Mirrors
// the v1 web template seed for "price-tag-default".
func priceTagFixture() Label {
	return Label{
		Size:      SizeMM{Width: 50, Height: 40},
		Gap:       GapMM{Gap: 2, Offset: 0},
		Direction: 1,
		Density:   8,
		Speed:     4,
		Codepage:  1252,
		Elements: []Element{
			{
				Type: ElementText, X: 10, Y: 10, Value: "Hamoud Boualem 1L",
				Font: "3", XScale: 1, YScale: 1,
			},
			{
				Type: ElementText, X: 10, Y: 50, Value: "150 DZD",
				Font: "4", XScale: 2, YScale: 2,
			},
			{
				Type: ElementBarcode, X: 10, Y: 130, Value: "9780201379624",
				Symbology: BarcodeEAN13, Height: 80, Narrow: 2, Wide: 2, Readable: true,
			},
		},
	}
}

// shelfLabelFixture — 60x40mm label with product name, price, and a
// CODE128 SKU barcode. Mirrors "shelf-label-default" seed.
func shelfLabelFixture() Label {
	return Label{
		Size:      SizeMM{Width: 60, Height: 40},
		Gap:       GapMM{Gap: 2, Offset: 0},
		Direction: 1,
		Density:   8,
		Speed:     4,
		Codepage:  1252,
		Elements: []Element{
			{
				Type: ElementText, X: 10, Y: 10, Value: "Café Bistro",
				Font: "3", XScale: 1, YScale: 1,
			},
			{
				Type: ElementText, X: 10, Y: 50, Value: "250 DZD / kg",
				Font: "3", XScale: 1, YScale: 1,
			},
			{
				Type: ElementBarcode, X: 10, Y: 120, Value: "SKU-12345",
				Symbology: BarcodeCODE128, Height: 80, Narrow: 2, Wide: 2, Readable: true,
			},
		},
	}
}

// weighedProductFixture — 50x40mm label with a QR-code payload.
// Exercises the QR render branch + capability gate.
func weighedProductFixture() Label {
	return Label{
		Size:      SizeMM{Width: 50, Height: 40},
		Gap:       GapMM{Gap: 2, Offset: 0},
		Direction: 1,
		Density:   8,
		Speed:     4,
		Codepage:  1252,
		Elements: []Element{
			{
				Type: ElementText, X: 10, Y: 10, Value: "Tomates Olama",
				Font: "3", XScale: 1, YScale: 1,
			},
			{
				Type: ElementText, X: 10, Y: 50, Value: "0.480 kg — 96 DZD",
				Font: "2", XScale: 1, YScale: 1,
			},
			{
				Type: ElementQRCode, X: 250, Y: 30, Value: "https://opensimsim.co/r/STK-7821",
				ECC: "M", Cell: 4, Mode: "A",
			},
		},
	}
}
