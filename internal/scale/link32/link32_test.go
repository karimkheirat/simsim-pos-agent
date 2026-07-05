package link32

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -update regenerates the golden files from the current encoder output.
// Inspect the diff before committing a regenerated golden.
var update = flag.Bool("update", false, "update golden files")

// weighedPLU is the canonical weight-sold fixture: 250.50 DZD/kg.
func weighedPLU() PLU {
	return PLU{
		LFCode:            "12345",
		Code:              "12345",
		Name:              "Tomates fraiches",
		UnitPriceCentimes: 25050,
		WeightUnit:        WeightUnitKg,
		BarcodeType:       2,
		Department:        0,
	}
}

// piecePLU is the piece-sold fixture with a multi-byte (Arabic) name.
func piecePLU() PLU {
	return PLU{
		LFCode:            "204",
		Code:              "204",
		Name:              "خبز الدار",
		UnitPriceCentimes: 5000,
		WeightUnit:        WeightUnitPCSKg,
		BarcodeType:       2,
		Department:        1,
	}
}

func goldenCompare(t *testing.T, got []byte, goldenName string) {
	t.Helper()
	goldenPath := filepath.Join("testdata", goldenName)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden: %s (%d bytes)", goldenPath, len(got))
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run `go test -update` to create)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output differs from golden %s — run `go test -update` to regenerate.\n  got: %q\n want: %q",
			goldenName, got, want)
	}
}

// ── Frame format (§9.2) ───────────────────────────────────────────────

func TestStartFrame_MatchesManualExample(t *testing.T) {
	// §9.2 worked example: `Starting command: 00080201`.
	if got := string(StartFrame()); got != "00080201" {
		t.Errorf("StartFrame = %q, want 00080201", got)
	}
}

func TestFrame_LengthIncludesItself(t *testing.T) {
	f, err := Frame("0110", []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	// 4 (length) + 4 (command) + 3 (record) = 11.
	if got := string(f); got != "00110110abc" {
		t.Errorf("Frame = %q, want 00110110abc", got)
	}
}

func TestFrame_RejectsBadCommandAndOversize(t *testing.T) {
	if _, err := Frame("110", nil); !errors.Is(err, ErrInvalidField) {
		t.Errorf("3-char command: err = %v, want ErrInvalidField", err)
	}
	if _, err := Frame("01x0", nil); !errors.Is(err, ErrInvalidField) {
		t.Errorf("non-digit command: err = %v, want ErrInvalidField", err)
	}
	if _, err := Frame("0110", make([]byte, 10000)); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("oversize record: err = %v, want ErrFrameTooLarge", err)
	}
}

// ── PLU record layout (Appendix 1) ────────────────────────────────────

func TestEncodePLURecord_FixedSizeAndTerminator(t *testing.T) {
	rec, err := EncodePLURecord(weighedPLU())
	if err != nil {
		t.Fatal(err)
	}
	// Appendix 1 widths sum to 101, plus one space per column (18)
	// plus CR LF = 121 bytes, always.
	if len(rec) != 121 {
		t.Errorf("record length = %d, want 121", len(rec))
	}
	if !bytes.HasSuffix(rec, []byte("\r\n")) {
		t.Errorf("record does not end in CR LF: %q", rec[len(rec)-2:])
	}
}

func TestEncodePLURecord_ColumnAlignment(t *testing.T) {
	rec, err := EncodePLURecord(weighedPLU())
	if err != nil {
		t.Fatal(err)
	}
	s := string(rec)
	// plu_no: width 4, value "0", right-aligned, then a space.
	if s[:5] != "   0 " {
		t.Errorf("plu_no column = %q, want %q", s[:5], "   0 ")
	}
	// name occupies bytes 5..41 right-aligned.
	name := s[5:41]
	if !strings.HasSuffix(name, "Tomates fraiches") || !strings.HasPrefix(name, " ") {
		t.Errorf("name column = %q, want right-aligned %q", name, "Tomates fraiches")
	}
	// lfcode bytes 42..48.
	if s[42:48] != " 12345" {
		t.Errorf("lfcode column = %q, want %q", s[42:48], " 12345")
	}
	// unit_price bytes 63..71: 25050 right-aligned in 8.
	if s[63:71] != "   25050" {
		t.Errorf("unit_price column = %q, want %q", s[63:71], "   25050")
	}
	// weight_unit byte 72.
	if s[72:73] != WeightUnitKg {
		t.Errorf("weight_unit = %q, want %q", s[72:73], WeightUnitKg)
	}
}

func TestEncodePLURecord_GoldenWeighed(t *testing.T) {
	rec, err := EncodePLURecord(weighedPLU())
	if err != nil {
		t.Fatal(err)
	}
	goldenCompare(t, rec, "golden_plu_weighed.bin")
}

func TestEncodePLURecord_GoldenPieceArabicName(t *testing.T) {
	rec, err := EncodePLURecord(piecePLU())
	if err != nil {
		t.Fatal(err)
	}
	goldenCompare(t, rec, "golden_plu_piece_arabic.bin")
}

func TestPLUFrame_Golden(t *testing.T) {
	f, err := PLUFrame(weighedPLU())
	if err != nil {
		t.Fatal(err)
	}
	// 121-byte record + 8 header bytes = 129 → length field "0129".
	if !bytes.HasPrefix(f, []byte("01290110")) {
		t.Errorf("frame header = %q, want 01290110", f[:8])
	}
	goldenCompare(t, f, "golden_plu_frame_weighed.bin")
}

func TestEncodePLURecord_Validation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PLU)
		want   error
	}{
		{"empty lfcode", func(p *PLU) { p.LFCode = "" }, ErrInvalidField},
		{"lfcode too long", func(p *PLU) { p.LFCode = "1234567" }, ErrInvalidField},
		{"lfcode non-digit", func(p *PLU) { p.LFCode = "12a45" }, ErrInvalidField},
		{"code too long", func(p *PLU) { p.Code = "12345678901" }, ErrInvalidField},
		{"empty name", func(p *PLU) { p.Name = "" }, ErrInvalidField},
		{"name over 36 bytes", func(p *PLU) { p.Name = strings.Repeat("x", 37) }, ErrFieldOverflow},
		{"name control byte", func(p *PLU) { p.Name = "a\r\nb" }, ErrInvalidField},
		{"negative price", func(p *PLU) { p.UnitPriceCentimes = -1 }, ErrInvalidField},
		{"price overflow", func(p *PLU) { p.UnitPriceCentimes = 100000000 }, ErrInvalidField},
		{"bad weight unit", func(p *PLU) { p.WeightUnit = "z" }, ErrInvalidField},
		{"barcode type range", func(p *PLU) { p.BarcodeType = 100 }, ErrInvalidField},
		{"department range", func(p *PLU) { p.Department = -1 }, ErrInvalidField},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := weighedPLU()
			tc.mutate(&p)
			if _, err := EncodePLURecord(p); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestTruncateName_NeverSplitsRune(t *testing.T) {
	// 13 Arabic characters ≈ 26 bytes — under the cap, unchanged.
	short := "خبز الدار طازج"
	if got := TruncateName(short); got != short {
		t.Errorf("short name changed: %q", got)
	}
	// 36 code points of 2-byte Arabic = 71 bytes (incl. spaces) — must
	// cut to ≤36 bytes on a rune boundary.
	long := strings.Repeat("م", 40)
	got := TruncateName(long)
	if len(got) > 36 {
		t.Errorf("truncated to %d bytes, want ≤ 36", len(got))
	}
	if !strings.HasPrefix(long, got) || len(got)%2 != 0 {
		t.Errorf("truncation split a rune: %q (%d bytes)", got, len(got))
	}
}

// ── ACK parsing + frame reading (§9.2) ────────────────────────────────

func TestParseAck_ManualExample(t *testing.T) {
	// §9.2 worked example: ACK 0102 with source 0210 000001 0000.
	ack, err := ParseAck([]byte("02100000010000"))
	if err != nil {
		t.Fatal(err)
	}
	if ack.Command != "0210" || ack.LFCode != "000001" || ack.ErrorCode != "0000" {
		t.Errorf("ack = %+v", ack)
	}
	if !ack.OK() {
		t.Error("ack.OK() = false, want true")
	}
	bad, err := ParseAck([]byte("01100001230012"))
	if err != nil {
		t.Fatal(err)
	}
	if bad.OK() {
		t.Error("nonzero error code reported OK")
	}
}

func TestParseAck_WrongLength(t *testing.T) {
	if _, err := ParseAck([]byte("short")); !errors.Is(err, ErrBadFrame) {
		t.Errorf("err = %v, want ErrBadFrame", err)
	}
}

func TestReadFrame_RoundTrip(t *testing.T) {
	ackRecord := []byte("01100012340000")
	f, err := Frame(CmdAck, ackRecord)
	if err != nil {
		t.Fatal(err)
	}
	cmd, rec, err := ReadFrame(bytes.NewReader(f))
	if err != nil {
		t.Fatal(err)
	}
	if cmd != CmdAck || !bytes.Equal(rec, ackRecord) {
		t.Errorf("ReadFrame = (%q, %q)", cmd, rec)
	}
}

func TestReadFrame_Malformed(t *testing.T) {
	if _, _, err := ReadFrame(strings.NewReader("abcd0202")); !errors.Is(err, ErrBadFrame) {
		t.Errorf("garbage length: err = %v, want ErrBadFrame", err)
	}
	if _, _, err := ReadFrame(strings.NewReader("00220202xx")); !errors.Is(err, ErrShortFrame) {
		t.Errorf("truncated frame: err = %v, want ErrShortFrame", err)
	}
	if _, _, err := ReadFrame(strings.NewReader("0004")); !errors.Is(err, ErrBadFrame) {
		t.Errorf("undersized length: err = %v, want ErrBadFrame", err)
	}
}
