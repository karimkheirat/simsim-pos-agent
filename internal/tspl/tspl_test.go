package tspl

import (
	"bytes"
	"strings"
	"testing"
)

// ── Standalone command builders ───────────────────────────────────────

func TestCLS(t *testing.T) {
	got := CLS()
	want := []byte("CLS\r\n")
	if !bytes.Equal(got, want) {
		t.Errorf("CLS() = %q, want %q", got, want)
	}
}

func TestSize(t *testing.T) {
	tests := []struct {
		name             string
		widthMM, heightMM int
		want             string
	}{
		{"40x30", 40, 30, "SIZE 40 mm,30 mm\r\n"},
		{"50x40", 50, 40, "SIZE 50 mm,40 mm\r\n"},
		{"60x40", 60, 40, "SIZE 60 mm,40 mm\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Size(tt.widthMM, tt.heightMM)
			if string(got) != tt.want {
				t.Errorf("Size(%d,%d) = %q, want %q", tt.widthMM, tt.heightMM, got, tt.want)
			}
		})
	}
}

func TestGap(t *testing.T) {
	tests := []struct {
		gapMM, offsetMM int
		want            string
	}{
		{2, 0, "GAP 2 mm,0 mm\r\n"},
		{3, 0, "GAP 3 mm,0 mm\r\n"},
	}
	for _, tt := range tests {
		got := Gap(tt.gapMM, tt.offsetMM)
		if string(got) != tt.want {
			t.Errorf("Gap(%d,%d) = %q, want %q", tt.gapMM, tt.offsetMM, got, tt.want)
		}
	}
}

func TestDirectionCmd(t *testing.T) {
	if string(DirectionCmd(Direction0)) != "DIRECTION 0\r\n" {
		t.Errorf("Direction0 wrong")
	}
	if string(DirectionCmd(Direction1)) != "DIRECTION 1\r\n" {
		t.Errorf("Direction1 wrong")
	}
}

func TestDensity(t *testing.T) {
	tests := []struct {
		level int
		want  string
	}{
		{0, "DENSITY 0\r\n"},
		{8, "DENSITY 8\r\n"},
		{15, "DENSITY 15\r\n"},
	}
	for _, tt := range tests {
		got := Density(tt.level)
		if string(got) != tt.want {
			t.Errorf("Density(%d) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestSpeed(t *testing.T) {
	if string(Speed(4)) != "SPEED 4\r\n" {
		t.Errorf("Speed(4) wrong")
	}
	if string(Speed(6)) != "SPEED 6\r\n" {
		t.Errorf("Speed(6) wrong")
	}
}

func TestCodepage(t *testing.T) {
	if string(Codepage("1252")) != "CODEPAGE 1252\r\n" {
		t.Errorf("Codepage 1252 wrong")
	}
	if string(Codepage("UTF-8")) != "CODEPAGE UTF-8\r\n" {
		t.Errorf("Codepage UTF-8 wrong")
	}
}

func TestText(t *testing.T) {
	// Pin the wire shape used by every label template: positional
	// args, double-quoted font, double-quoted payload.
	got := Text(10, 20, Font2, 0, 1, 1, "Hello")
	want := `TEXT 10,20,"2",0,1,1,"Hello"` + "\r\n"
	if string(got) != want {
		t.Errorf("Text(...) = %q\nwant %q", got, want)
	}
}

func TestText_SanitizesCRLF(t *testing.T) {
	// CR/LF injected in the payload must be scrubbed so the printer's
	// line parser can't be confused / hijacked.
	got := Text(0, 0, Font2, 0, 1, 1, "line1\r\nDOWNLOAD attack")
	if bytes.Count(got, []byte("\r\n")) != 1 {
		t.Errorf("Text payload with embedded CRLF produced %d line terminators; want 1\n%q",
			bytes.Count(got, []byte("\r\n")), got)
	}
	if bytes.Contains(got, []byte("\r\nDOWNLOAD")) {
		t.Errorf("Text payload leaked embedded CRLF: %q", got)
	}
}

func TestText_SanitizesQuotes(t *testing.T) {
	// A literal " in the payload would break the quoted form; we scrub
	// to ' so the printer still parses the line.
	got := Text(0, 0, Font2, 0, 1, 1, `say "hi"`)
	if bytes.Contains(got, []byte(`"hi"`)) {
		t.Errorf("Text payload preserved inner double-quotes: %q", got)
	}
	if !bytes.Contains(got, []byte(`'hi'`)) {
		t.Errorf("Text payload did not scrub inner quotes to ': %q", got)
	}
}

func TestBarcodeCode128(t *testing.T) {
	got := BarcodeCode128(10, 20, 80, 2, 0, 2, 2, "ABC123")
	want := `BARCODE 10,20,"128",80,2,0,2,2,"ABC123"` + "\r\n"
	if string(got) != want {
		t.Errorf("BarcodeCode128 = %q\nwant %q", got, want)
	}
}

func TestBarcodeEAN13_DialectStandard(t *testing.T) {
	got := BarcodeEAN13(DialectStandard, 30, 40, 80, 2, 0, 2, 2, "9780201379624")
	want := `BARCODE 30,40,"EAN13",80,2,0,2,2,"9780201379624"` + "\r\n"
	if string(got) != want {
		t.Errorf("EAN13 standard = %q\nwant %q", got, want)
	}
}

func TestBarcodeEAN13_DialectRongta(t *testing.T) {
	got := BarcodeEAN13(DialectRongta, 30, 40, 80, 2, 0, 2, 2, "9780201379624")
	want := `BARCODE 30,40,"EAN-13",80,2,0,2,2,"9780201379624"` + "\r\n"
	if string(got) != want {
		t.Errorf("EAN13 rongta = %q\nwant %q", got, want)
	}
}

func TestQRCode(t *testing.T) {
	got := QRCode(50, 60, QRModeM, 4, "A", 0, "https://opensimsim.co")
	want := `QRCODE 50,60,"M",4,"A",0,"https://opensimsim.co"` + "\r\n"
	if string(got) != want {
		t.Errorf("QRCode = %q\nwant %q", got, want)
	}
}

func TestPrint(t *testing.T) {
	if string(Print(1, 1)) != "PRINT 1,1\r\n" {
		t.Errorf("Print(1,1) wrong")
	}
	if string(Print(3, 1)) != "PRINT 3,1\r\n" {
		t.Errorf("Print(3,1) wrong")
	}
}

// ── Builder ───────────────────────────────────────────────────────────

func TestNew_DialectAndEmptyBuffer(t *testing.T) {
	b := New()
	if b.Dialect() != DialectStandard {
		t.Errorf("default dialect = %q, want %q", b.Dialect(), DialectStandard)
	}
	if len(b.Bytes()) != 0 {
		t.Errorf("New().Bytes() = % X, want empty", b.Bytes())
	}
}

func TestNewWithDialect(t *testing.T) {
	b := NewWithDialect(DialectRongta)
	if b.Dialect() != DialectRongta {
		t.Errorf("dialect = %q, want %q", b.Dialect(), DialectRongta)
	}
}

func TestBuilder_Write(t *testing.T) {
	got := New().Write([]byte("CUSTOM\r\n")).Bytes()
	if string(got) != "CUSTOM\r\n" {
		t.Errorf("Write = %q, want CUSTOM\\r\\n", got)
	}
}

func TestBuilder_WriteReturnsReceiver(t *testing.T) {
	b := New()
	if b.Write([]byte("x")) != b {
		t.Error("Write did not return receiver")
	}
	if b.CLS() != b {
		t.Error("CLS did not return receiver")
	}
	if b.TextSimple(0, 0, Font2, "y") != b {
		t.Error("TextSimple did not return receiver")
	}
}

func TestBuilder_Chained_PriceTag(t *testing.T) {
	// End-to-end byte-level pin of a representative price-tag template:
	// CLS → SIZE → GAP → DIRECTION → DENSITY → SPEED → CODEPAGE →
	// TEXT(name) → TEXT(price) → BARCODE(EAN13) → PRINT.
	//
	// If any per-command line shape changes, this assertion catches it.
	got := New().
		CLS().
		Size(50, 40).
		Gap(2, 0).
		Direction(Direction1).
		Density(8).
		Speed(4).
		Codepage("1252").
		TextSimple(10, 10, Font3, "Hamoud Boualem 1L").
		TextSimple(10, 50, Font4, "150 DZD").
		BarcodeEAN13(10, 100, 80, 2, 0, 2, 2, "9780201379624").
		Print(1, 1).
		Bytes()

	want := strings.Join([]string{
		"CLS",
		"SIZE 50 mm,40 mm",
		"GAP 2 mm,0 mm",
		"DIRECTION 1",
		"DENSITY 8",
		"SPEED 4",
		"CODEPAGE 1252",
		`TEXT 10,10,"3",0,1,1,"Hamoud Boualem 1L"`,
		`TEXT 10,50,"4",0,1,1,"150 DZD"`,
		`BARCODE 10,100,"EAN13",80,2,0,2,2,"9780201379624"`,
		"PRINT 1,1",
		"",
	}, "\r\n")

	if string(got) != want {
		t.Errorf("price-tag chain mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuilder_Chained_Rongta_EAN13(t *testing.T) {
	// Rongta builder must emit "EAN-13" (with hyphen) from BarcodeEAN13.
	got := NewWithDialect(DialectRongta).
		BarcodeEAN13(0, 0, 80, 2, 0, 2, 2, "9780201379624").
		Bytes()
	if !strings.Contains(string(got), `"EAN-13"`) {
		t.Errorf("Rongta dialect did not emit \"EAN-13\": %q", got)
	}
	if strings.Contains(string(got), `"EAN13"`) {
		t.Errorf("Rongta dialect leaked \"EAN13\" identifier: %q", got)
	}
}

func TestBuilder_TextCP1252_Transcodes(t *testing.T) {
	// French diacritic must be transcoded to its CP1252 byte (0xE9).
	got := New().TextCP1252(0, 0, Font2, 0, 1, 1, "Café").Bytes()
	if !bytes.Contains(got, []byte{0xE9}) {
		t.Errorf("TextCP1252 did not emit 0xE9 for 'é': % X", got)
	}
}

func TestBuilder_IndependentInstances(t *testing.T) {
	a := New().CLS().Bytes()
	b := New().Print(1, 1).Bytes()
	if string(a) != "CLS\r\n" {
		t.Errorf("builder a = %q, want CLS\\r\\n", a)
	}
	if string(b) != "PRINT 1,1\r\n" {
		t.Errorf("builder b = %q, want PRINT 1,1\\r\\n", b)
	}
}

// TestEveryLineEndsWithCRLF — the TSPL printer's parser is line-oriented;
// a line missing CRLF gets concatenated with the next one and silently
// corrupts the job. Pin that every helper terminates correctly.
func TestEveryLineEndsWithCRLF(t *testing.T) {
	commands := [][]byte{
		CLS(),
		Size(40, 30),
		Gap(2, 0),
		DirectionCmd(Direction1),
		Density(8),
		Speed(4),
		Codepage("1252"),
		Text(0, 0, Font2, 0, 1, 1, "x"),
		BarcodeCode128(0, 0, 80, 2, 0, 2, 2, "x"),
		BarcodeEAN13(DialectStandard, 0, 0, 80, 2, 0, 2, 2, "9780201379624"),
		BarcodeEAN13(DialectRongta, 0, 0, 80, 2, 0, 2, 2, "9780201379624"),
		QRCode(0, 0, QRModeM, 4, "A", 0, "x"),
		Print(1, 1),
	}
	for i, c := range commands {
		if !bytes.HasSuffix(c, []byte("\r\n")) {
			t.Errorf("command[%d] missing CRLF suffix: %q", i, c)
		}
		// Also: only ONE CRLF — embedded CRLFs in payloads should have
		// been sanitized.
		if bytes.Count(c, []byte("\r\n")) != 1 {
			t.Errorf("command[%d] has %d CRLFs, want 1: %q",
				i, bytes.Count(c, []byte("\r\n")), c)
		}
	}
}
