package escpos

import (
	"bytes"
	"testing"
)

func TestRasterImage_Bytes(t *testing.T) {
	data := []byte{0x80, 0x01, 0xFF, 0x00}
	got := RasterImage(2, 2, data)
	want := []byte{0x1D, 0x76, 0x30, 0x00, 0x02, 0x00, 0x02, 0x00, 0x80, 0x01, 0xFF, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % X\nwant % X", got, want)
	}
}

// 576 dots = 72 bytes; 300 rows needs the high height byte.
func TestRasterImage_HeaderLittleEndian(t *testing.T) {
	got := RasterImage(72, 300, make([]byte, 72*300))
	want := []byte{0x1D, 0x76, 0x30, 0x00, 72, 0x00, 0x2C, 0x01}
	if !bytes.Equal(got[:8], want) || len(got) != 8+72*300 {
		t.Fatalf("header % X len %d", got[:8], len(got))
	}
	b := New().RasterImage(48, 32, make([]byte, 48*32)).Bytes()
	if !bytes.Equal(b[:8], []byte{0x1D, 0x76, 0x30, 0x00, 48, 0, 32, 0}) {
		t.Fatalf("builder header % X", b[:8])
	}
}
