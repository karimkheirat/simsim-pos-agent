package escpos

import (
	"bytes"
	"testing"
)

func TestSize(t *testing.T) {
	for _, tt := range []struct {
		w, h int
		want []byte
	}{
		{1, 1, []byte{0x1D, 0x21, 0x00}},
		{1, 2, []byte{0x1D, 0x21, 0x01}},
		{2, 2, []byte{0x1D, 0x21, 0x11}},
		{0, 9, []byte{0x1D, 0x21, 0x07}}, // clamped to 1..8
	} {
		if got := Size(tt.w, tt.h); !bytes.Equal(got, tt.want) {
			t.Errorf("Size(%d,%d) = % X, want % X", tt.w, tt.h, got, tt.want)
		}
	}
	if !bytes.Equal(Size(1, 1), DoubleSize(false)) {
		t.Errorf("Size(1,1) must equal normal size")
	}
}

func TestCode128(t *testing.T) {
	got, err := Code128("TC-1", 64, 2, HRINone)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x1D, 0x48, 0x00, // GS H 0 — no printer-drawn text
		0x1D, 0x68, 64, // GS h 64 — 8 mm tall
		0x1D, 0x77, 2, // GS w 2 — 2-dot modules
		0x1D, 0x6B, 73, 6, '{', 'B', 'T', 'C', '-', '1',
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Code128 = % X\nwant       % X", got, want)
	}
	esc, _ := Code128("a{b", 64, 2, HRINone)
	if !bytes.HasSuffix(esc, []byte{6, '{', 'B', 'a', '{', '{', 'b'}) {
		t.Errorf("'{' not escaped: % X", esc)
	}
	for _, bad := range []string{"", "é", "a\nb"} {
		if _, err := Code128(bad, 64, 2, HRINone); err == nil {
			t.Errorf("Code128(%q) accepted", bad)
		}
	}
	if n := Code128Modules("TC-S0417-P2-2026-0312"); n != 266 {
		t.Errorf("modules = %d, want 266", n)
	}
}
