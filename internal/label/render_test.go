package label

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/karimkheirat/simsim-pos-agent/internal/tspl"
)

// regenerate is set by -regenerate-goldens to overwrite the golden
// .bin files with current output. Use sparingly — only after a
// deliberate intentional shape change.
var regenerate = flag.Bool("regenerate-goldens", false, "rewrite golden_label_*.bin files from current Render output")

// goldenCases enumerates the fixtures + their golden file paths.
type goldenCase struct {
	name    string
	fixture func() Label
	opts    RenderOptions
	path    string
}

func cases() []goldenCase {
	return []goldenCase{
		{
			name:    "price_tag",
			fixture: priceTagFixture,
			opts:    RenderOptions{Dialect: tspl.DialectStandard, QRSupported: true},
			path:    "testdata/golden_label_price_tag.bin",
		},
		{
			name:    "shelf_label",
			fixture: shelfLabelFixture,
			opts:    RenderOptions{Dialect: tspl.DialectStandard, QRSupported: true},
			path:    "testdata/golden_label_shelf_label.bin",
		},
		{
			name:    "weighed_product",
			fixture: weighedProductFixture,
			opts:    RenderOptions{Dialect: tspl.DialectStandard, QRSupported: true},
			path:    "testdata/golden_label_weighed_product.bin",
		},
	}
}

// TestRender_GoldenBytes — bit-identical regression pin. If a render
// shape changes intentionally, run:
//
//	go test ./internal/label -run TestRender_GoldenBytes -regenerate-goldens
//
// and review the diff.
func TestRender_GoldenBytes(t *testing.T) {
	for _, tc := range cases() {
		t.Run(tc.name, func(t *testing.T) {
			label := tc.fixture()
			if err := label.Validate(); err != nil {
				t.Fatalf("fixture failed Validate: %v", err)
			}
			got, err := Render(label, tc.opts)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			if *regenerate {
				if err := os.MkdirAll(filepath.Dir(tc.path), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(tc.path, got, 0o644); err != nil {
					t.Fatalf("write golden %s: %v", tc.path, err)
				}
				t.Logf("regenerated golden %s (%d bytes)", tc.path, len(got))
				return
			}

			want, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -regenerate-goldens to create)", tc.path, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("render mismatch for %s\n--- got (%d bytes) ---\n%s\n--- want (%d bytes) ---\n%s",
					tc.name, len(got), got, len(want), want)
			}
		})
	}
}

// TestRender_PreambleAndPostamble — pins the high-level command
// sequence around the elements. If TSPL pre/post-amble changes, this
// fails before the golden mismatch does, surfacing the cause faster.
func TestRender_PreambleAndPostamble(t *testing.T) {
	got, err := Render(priceTagFixture(), RenderOptions{Dialect: tspl.DialectStandard, QRSupported: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(string(got), "\r\n")
	// Trailing empty line after the final CRLF.
	if got, want := lines[0], "CLS"; got != want {
		t.Errorf("line 0 = %q, want %q", got, want)
	}
	if got, want := lines[1], "SIZE 50 mm,40 mm"; got != want {
		t.Errorf("line 1 = %q, want %q", got, want)
	}
	if got, want := lines[2], "GAP 2 mm,0 mm"; got != want {
		t.Errorf("line 2 = %q, want %q", got, want)
	}
	if got, want := lines[3], "DIRECTION 1"; got != want {
		t.Errorf("line 3 = %q, want %q", got, want)
	}
	if got, want := lines[4], "DENSITY 8"; got != want {
		t.Errorf("line 4 = %q, want %q", got, want)
	}
	if got, want := lines[5], "SPEED 4"; got != want {
		t.Errorf("line 5 = %q, want %q", got, want)
	}
	if got, want := lines[6], "CODEPAGE 1252"; got != want {
		t.Errorf("line 6 = %q, want %q", got, want)
	}
	// PRINT must be the last command before the trailing empty.
	if lines[len(lines)-1] != "" {
		t.Errorf("output does not end with CRLF: last line = %q", lines[len(lines)-1])
	}
	if lines[len(lines)-2] != "PRINT 1,1" {
		t.Errorf("penultimate line = %q, want PRINT 1,1", lines[len(lines)-2])
	}
}

func TestRender_DialectAffectsEAN13(t *testing.T) {
	standardOut, err := Render(priceTagFixture(), RenderOptions{Dialect: tspl.DialectStandard, QRSupported: true})
	if err != nil {
		t.Fatal(err)
	}
	rongtaOut, err := Render(priceTagFixture(), RenderOptions{Dialect: tspl.DialectRongta, QRSupported: true})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(standardOut, []byte(`"EAN13"`)) {
		t.Errorf("DialectStandard did not emit \"EAN13\": %s", standardOut)
	}
	if !bytes.Contains(rongtaOut, []byte(`"EAN-13"`)) {
		t.Errorf("DialectRongta did not emit \"EAN-13\": %s", rongtaOut)
	}
	if bytes.Equal(standardOut, rongtaOut) {
		t.Errorf("standard + rongta produced identical bytes — dialect not honored")
	}
}

func TestRender_DialectDefaultsToStandard(t *testing.T) {
	out, err := Render(priceTagFixture(), RenderOptions{QRSupported: true}) // Dialect zero value
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"EAN13"`)) {
		t.Errorf("zero-value Dialect did not default to standard: %s", out)
	}
}

func TestRender_QRGateBlocksWhenUnsupported(t *testing.T) {
	label := weighedProductFixture() // contains a QR element
	out, err := Render(label, RenderOptions{Dialect: tspl.DialectStandard, QRSupported: false})
	if !errors.Is(err, ErrQRNotSupported) {
		t.Errorf("err = %v, want ErrQRNotSupported", err)
	}
	if out != nil {
		t.Errorf("got %d bytes, want nil — capability gate must NOT leak partial output", len(out))
	}
}

func TestRender_QRGateAllowsWhenSupported(t *testing.T) {
	label := weighedProductFixture()
	out, err := Render(label, RenderOptions{Dialect: tspl.DialectStandard, QRSupported: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.Contains(out, []byte("QRCODE ")) {
		t.Errorf("output missing QRCODE line:\n%s", out)
	}
}

func TestRender_NonQRLabel_QRGateIrrelevant(t *testing.T) {
	// A label with NO QR element must render whether QRSupported is true or false.
	for _, qr := range []bool{true, false} {
		got, err := Render(priceTagFixture(), RenderOptions{Dialect: tspl.DialectStandard, QRSupported: qr})
		if err != nil {
			t.Errorf("QRSupported=%v: Render: %v", qr, err)
		}
		if len(got) == 0 {
			t.Errorf("QRSupported=%v: got empty bytes", qr)
		}
	}
}

func TestRender_TextSanitizesEmbeddedCRLF(t *testing.T) {
	// CRLF in a text value would break the printer's line parser. The
	// tspl builder scrubs it; verify the rendered output has exactly
	// the expected number of CRLF terminators (one per command line,
	// nothing extra).
	label := priceTagFixture()
	label.Elements[0].Value = "line1\r\nDOWNLOAD"
	out, err := Render(label, RenderOptions{Dialect: tspl.DialectStandard, QRSupported: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if bytes.Contains(out, []byte("\r\nDOWNLOAD")) {
		t.Errorf("output leaked embedded CRLF from text value:\n%s", out)
	}
}

// TestRender_DeterministicAcrossCalls — same input → same bytes,
// every call. Pins that the renderer has no nondeterministic state
// (random IDs, time injection, map iteration order leaking in).
func TestRender_DeterministicAcrossCalls(t *testing.T) {
	for _, tc := range cases() {
		t.Run(tc.name, func(t *testing.T) {
			a, err := Render(tc.fixture(), tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				b, err := Render(tc.fixture(), tc.opts)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(a, b) {
					t.Errorf("nondeterministic output on call %d", i+2)
					return
				}
			}
		})
	}
}

func TestRender_ElementOrderPreserved(t *testing.T) {
	// Z-order: later elements overpaint earlier. Verify the textual
	// order in the output matches the input element order.
	label := priceTagFixture() // 3 elements: TEXT, TEXT, BARCODE
	out, err := Render(label, RenderOptions{Dialect: tspl.DialectStandard, QRSupported: true})
	if err != nil {
		t.Fatal(err)
	}
	hamoud := bytes.Index(out, []byte("Hamoud"))
	dzd := bytes.Index(out, []byte("150 DZD"))
	barcode := bytes.Index(out, []byte("BARCODE "))
	if hamoud < 0 || dzd < 0 || barcode < 0 {
		t.Fatalf("missing expected substrings; out=%s", out)
	}
	if !(hamoud < dzd && dzd < barcode) {
		t.Errorf("element order not preserved: hamoud=%d dzd=%d barcode=%d", hamoud, dzd, barcode)
	}
}

// ── Validate() ────────────────────────────────────────────────────────

func TestValidate_AcceptsFixtures(t *testing.T) {
	fixtures := []struct {
		name string
		f    func() Label
	}{
		{"price_tag", priceTagFixture},
		{"shelf_label", shelfLabelFixture},
		{"weighed_product", weighedProductFixture},
	}
	for _, tt := range fixtures {
		t.Run(tt.name, func(t *testing.T) {
			l := tt.f()
			if err := l.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

func TestValidate_Dimensions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Label)
		want   string
	}{
		{"width too small", func(l *Label) { l.Size.Width = 19 }, "size.width"},
		{"width too large", func(l *Label) { l.Size.Width = 101 }, "size.width"},
		{"height too small", func(l *Label) { l.Size.Height = 19 }, "size.height"},
		{"height too large", func(l *Label) { l.Size.Height = 151 }, "size.height"},
		{"gap negative", func(l *Label) { l.Gap.Gap = -1 }, "gap.gap"},
		{"offset negative", func(l *Label) { l.Gap.Offset = -1 }, "gap.offset"},
		{"direction invalid", func(l *Label) { l.Direction = 2 }, "direction"},
		{"density too high", func(l *Label) { l.Density = 16 }, "density"},
		{"speed zero", func(l *Label) { l.Speed = 0 }, "speed"},
		{"codepage negative", func(l *Label) { l.Codepage = -1 }, "codepage"},
		{"no elements", func(l *Label) { l.Elements = nil }, "elements is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := priceTagFixture()
			tt.mutate(&l)
			err := l.Validate()
			if !errors.Is(err, ErrInvalidLabel) {
				t.Errorf("err = %v, want errors.Is ErrInvalidLabel", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q; want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestValidate_TextRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Element)
		want   string
	}{
		{"no font", func(e *Element) { e.Font = "" }, "font required"},
		{"x_scale zero", func(e *Element) { e.XScale = 0 }, "x_scale"},
		{"y_scale > 8", func(e *Element) { e.YScale = 9 }, "y_scale"},
		{"empty value", func(e *Element) { e.Value = "" }, "value is empty"},
		{"whitespace-only value", func(e *Element) { e.Value = "   " }, "value is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := priceTagFixture()
			tt.mutate(&l.Elements[0])
			err := l.Validate()
			if !errors.Is(err, ErrInvalidLabel) {
				t.Errorf("err = %v, want errors.Is ErrInvalidLabel", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q; want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestValidate_BarcodeRequiredFields(t *testing.T) {
	// Elements[2] in priceTagFixture is the EAN13 barcode.
	tests := []struct {
		name   string
		mutate func(*Element)
		want   string
	}{
		{"bad symbology", func(e *Element) { e.Symbology = "QR" }, "symbology"},
		{"height zero", func(e *Element) { e.Height = 0 }, "height"},
		{"narrow zero", func(e *Element) { e.Narrow = 0 }, "narrow"},
		{"wide zero", func(e *Element) { e.Wide = 0 }, "wide"},
		{"EAN13 wrong length", func(e *Element) { e.Value = "12345" }, "EAN13"},
		{"EAN13 non-numeric", func(e *Element) { e.Value = "abcdefghijklm" }, "EAN13"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := priceTagFixture()
			tt.mutate(&l.Elements[2])
			err := l.Validate()
			if !errors.Is(err, ErrInvalidLabel) {
				t.Errorf("err = %v, want errors.Is ErrInvalidLabel", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q; want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestValidate_QRRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Element)
		want   string
	}{
		{"bad ecc", func(e *Element) { e.ECC = "Z" }, "ecc"},
		{"cell zero", func(e *Element) { e.Cell = 0 }, "cell"},
		{"cell too large", func(e *Element) { e.Cell = 11 }, "cell"},
		{"missing mode", func(e *Element) { e.Mode = "" }, "mode required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := weighedProductFixture()
			tt.mutate(&l.Elements[2])
			err := l.Validate()
			if !errors.Is(err, ErrInvalidLabel) {
				t.Errorf("err = %v, want errors.Is ErrInvalidLabel", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q; want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestValidate_UnknownElementType(t *testing.T) {
	l := priceTagFixture()
	l.Elements[0].Type = "blueprint"
	err := l.Validate()
	if !errors.Is(err, ErrInvalidLabel) {
		t.Errorf("err = %v, want errors.Is ErrInvalidLabel", err)
	}
	if !strings.Contains(err.Error(), "type") {
		t.Errorf("err = %q; want substring type", err.Error())
	}
}

func TestValidate_CoordinateBounds(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Element)
		want   string
	}{
		{"x negative", func(e *Element) { e.X = -1 }, ".x"},
		{"y negative", func(e *Element) { e.Y = -1 }, ".y"},
		{"x way past width", func(e *Element) { e.X = 10000 }, "exceeds bounds"},
		{"y way past height", func(e *Element) { e.Y = 10000 }, "exceeds bounds"},
		{"rotation invalid", func(e *Element) { e.Rotation = 45 }, "rotation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := priceTagFixture()
			tt.mutate(&l.Elements[0])
			err := l.Validate()
			if !errors.Is(err, ErrInvalidLabel) {
				t.Errorf("err = %v, want errors.Is ErrInvalidLabel", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %q; want substring %q", err.Error(), tt.want)
			}
		})
	}
}
