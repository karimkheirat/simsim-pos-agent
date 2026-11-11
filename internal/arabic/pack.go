package arabic

import "image"

// isInk reports whether a picture dot prints (black).
func isInk(img *image.Gray, x, y int) bool {
	return img.GrayAt(x, y).Y < 128
}

// Pack turns img into rows of bytes, eight dots a byte, the leftmost dot
// in the most significant bit, every row padded to whole bytes. ink is the
// bit value a printed (black) dot takes: 1 for ESC/POS raster images, the
// TSPL BITMAP polarity for TSPL (see tspl.BitmapInkBit).
func Pack(img *image.Gray, ink byte) (widthBytes int, data []byte) {
	b := img.Bounds()
	return packRange(img, 0, (b.Dx()+7)/8, ink)
}

// PackCropped is Pack limited to the byte columns that hold ink, so a
// short word on a wide roll does not ship a row of blank bytes. xByte is
// the first byte column kept (its dot position is xByte*8). An image with
// no ink returns widthBytes 0.
func PackCropped(img *image.Gray, ink byte) (xByte, widthBytes int, data []byte) {
	b := img.Bounds()
	first, last := -1, -1
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if isInk(img, b.Min.X+x, b.Min.Y+y) {
				if first < 0 || x < first {
					first = x
				}
				if x > last {
					last = x
				}
			}
		}
	}
	if first < 0 {
		return 0, 0, nil
	}
	xByte = first / 8
	end := last/8 + 1
	wb, d := packRange(img, xByte, end, ink)
	return xByte, wb, d
}

func packRange(img *image.Gray, fromByte, toByte int, ink byte) (int, []byte) {
	b := img.Bounds()
	wb := toByte - fromByte
	data := make([]byte, wb*b.Dy())
	if ink == 0 {
		for i := range data {
			data[i] = 0xFF
		}
	}
	for y := 0; y < b.Dy(); y++ {
		for bx := 0; bx < wb; bx++ {
			for bit := 0; bit < 8; bit++ {
				x := (fromByte+bx)*8 + bit
				if x >= b.Dx() || !isInk(img, b.Min.X+x, b.Min.Y+y) {
					continue
				}
				mask := byte(0x80) >> bit
				if ink == 1 {
					data[y*wb+bx] |= mask
				} else {
					data[y*wb+bx] &^= mask
				}
			}
		}
	}
	return wb, data
}

// Rotate turns img clockwise by deg (0, 90, 180 or 270), as TSPL rotates
// a TEXT element about its origin. Any other angle returns img unchanged.
func Rotate(img *image.Gray, deg int) *image.Gray {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var out *image.Gray
	switch deg {
	case 90:
		out = image.NewGray(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out.SetGray(h-1-y, x, img.GrayAt(b.Min.X+x, b.Min.Y+y))
			}
		}
	case 180:
		out = image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out.SetGray(w-1-x, h-1-y, img.GrayAt(b.Min.X+x, b.Min.Y+y))
			}
		}
	case 270:
		out = image.NewGray(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out.SetGray(y, w-1-x, img.GrayAt(b.Min.X+x, b.Min.Y+y))
			}
		}
	default:
		return img
	}
	return out
}

// Unpack is Pack's inverse: it turns packed rows back into a picture
// (white 255, black 0). It lets a test or a person look at what a print
// job will draw.
func Unpack(widthBytes, height int, data []byte, ink byte) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, widthBytes*8, height))
	for y := 0; y < height; y++ {
		for x := 0; x < widthBytes*8; x++ {
			i := y*widthBytes + x/8
			bit := byte(0)
			if i < len(data) && data[i]&(0x80>>(x%8)) != 0 {
				bit = 1
			}
			v := uint8(255)
			if bit == ink {
				v = 0
			}
			img.Pix[y*img.Stride+x] = v
		}
	}
	return img
}
