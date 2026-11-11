package tspl

import (
	"bytes"
	"testing"
)

func TestBitmap_Bytes(t *testing.T) {
	// The data is binary: a CR, LF or quote byte inside it is image.
	data := []byte{0x0D, 0x0A, '"', 0xFF}
	got := Bitmap(16, 40, 2, 2, BitmapOverwrite, data)
	want := append([]byte("BITMAP 16,40,2,2,0,"), 0x0D, 0x0A, '"', 0xFF, '\r', '\n')
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	b := New().Bitmap(0, 0, 1, 1, BitmapOverwrite, []byte{0x00}).Bytes()
	if !bytes.Equal(b, []byte("BITMAP 0,0,1,1,0,\x00\r\n")) {
		t.Fatalf("builder %q", b)
	}
}

// TSPL's polarity is the reverse of ESC/POS: a 0 bit prints.
func TestBitmapInkBit(t *testing.T) {
	if BitmapInkBit != 0 {
		t.Fatalf("BitmapInkBit = %d; the TSPL2 manual's BITMAP draws with 0 bits", BitmapInkBit)
	}
}
