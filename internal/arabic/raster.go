package arabic

import (
	_ "embed"
	"image"
	"image/draw"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/DejaVuSans.ttf
var regularTTF []byte

//go:embed fonts/DejaVuSans-Bold.ttf
var boldTTF []byte

var (
	parseOnce sync.Once
	regular   *opentype.Font
	bold      *opentype.Font
	parseErr  error
)

func fonts() (*opentype.Font, *opentype.Font, error) {
	parseOnce.Do(func() {
		regular, parseErr = opentype.Parse(regularTTF)
		if parseErr == nil {
			bold, parseErr = opentype.Parse(boldTTF)
		}
	})
	return regular, bold, parseErr
}

// Align is where a single-part row sits across the paper.
type Align int

const (
	Left Align = iota
	Center
	Right
)

// Style is how a row is drawn. Scale is the printer's size multiplier the
// row would have had as text (1 for body, 2 for the store name and TOTAL).
type Style struct {
	Bold  bool
	Scale int
	Align Align
	Dots  int // printable width in dots (576 on 80 mm, 384 on 58 mm)
	// CellW, when set, is the width in dots of one text column: the parts
	// after the first end where they would end as text (column × CellW),
	// so they line up with the text rows around them. Zero: last part at
	// the right edge, middle parts centred.
	CellW int
}

// bodyPx is the font's pixel size at Scale 1: about the height of the
// printer's own Font A (24 dots), so an Arabic row sits among the text rows
// at the same size.
const bodyPx = 22

// columnGap splits a laid-out row into its parts: the renderer pads a label
// and its value apart with spaces, and a run of two or more spaces is that
// padding, never a word space.
var columnGap = regexp.MustCompile(` {2,}`)

// Row draws one receipt row as an ESC/POS raster image (GS v 0) the full
// width of the paper, ending ready for the next row. A row laid out as
// "label   value" keeps its label at the left edge and its value at the
// right; any other row sits where Style.Align puts it.
func Row(s string, st Style) ([]byte, error) {
	img, err := RowImage(s, st)
	if err != nil {
		return nil, err
	}
	return raster(img), nil
}

// RowImage draws the row as Row does and returns the 1-bit picture
// (black below 128) for printers that take bitmaps another way (TSPL).
func RowImage(s string, st Style) (*image.Gray, error) {
	reg, bld, err := fonts()
	if err != nil {
		return nil, err
	}
	f := reg
	if st.Bold {
		f = bld
	}
	if st.Scale < 1 {
		st.Scale = 1
	}
	if st.Dots <= 0 {
		st.Dots = 576
	}
	width := (st.Dots + 7) / 8 * 8

	parts := columnGap.Split(strings.TrimSpace(s), -1)
	px := float64(bodyPx * st.Scale)
	var face font.Face
	var texts [][]rune
	var widths []int
	// Shrink until the parts fit side by side with a gap.
	for ; px >= 10; px -= 2 {
		face, err = opentype.NewFace(f, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		texts, widths = texts[:0], widths[:0]
		total := 0
		for _, p := range parts {
			v := Visual(p)
			w := font.MeasureString(face, string(v)).Ceil()
			texts = append(texts, v)
			widths = append(widths, w)
			total += w
		}
		if total+(len(parts)-1)*int(px/2) <= width || px-2 < 10 {
			break
		}
		face.Close()
	}

	m := face.Metrics()
	ascent := m.Ascent.Ceil()
	height := ascent + m.Descent.Ceil() + 4
	img := image.NewGray(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	d := &font.Drawer{Dst: img, Src: image.Black, Face: face}
	place := func(text []rune, x int) {
		d.Dot = fixed.P(x, ascent+2)
		d.DrawString(string(text))
	}
	switch len(parts) {
	case 1:
		x := 0
		switch st.Align {
		case Center:
			x = (width - widths[0]) / 2
		case Right:
			x = width - widths[0]
		}
		place(texts[0], x)
	default:
		place(texts[0], 0)
		if st.CellW > 0 {
			// Re-place every part after the first at its column's end.
			trimmed := strings.TrimSpace(s)
			ends := columnEnds(trimmed)
			for k := 1; k < len(texts) && k < len(ends); k++ {
				x := ends[k]*st.CellW - widths[k]
				if x+widths[k] > width {
					x = width - widths[k]
				}
				place(texts[k], x)
			}
			break
		}
		place(texts[len(texts)-1], width-widths[len(widths)-1])
		// Middle parts, if any, centred in what is left.
		if len(parts) > 2 {
			mid := []rune(strings.Join(func() []string {
				var ss []string
				for _, t := range texts[1 : len(texts)-1] {
					ss = append(ss, string(t))
				}
				return ss
			}(), " "))
			w := font.MeasureString(face, string(mid)).Ceil()
			place(mid, (width-w)/2)
		}
	}
	face.Close()
	return img, nil
}

// TSPLBitmap encodes img as the data of a TSPL BITMAP command: row-major,
// 8 dots a byte, most significant bit first, and — TSPL's convention — a
// 0 bit prints black. Returns the width in bytes and the height in dots.
func TSPLBitmap(img *image.Gray) (data []byte, widthBytes, height int) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	bw := (w + 7) / 8
	data = make([]byte, 0, bw*h)
	for y := 0; y < h; y++ {
		for xb := 0; xb < bw; xb++ {
			b := byte(0xFF)
			for bit := 0; bit < 8; bit++ {
				x := xb*8 + bit
				if x < w && img.GrayAt(x, y).Y < 128 {
					b &^= 0x80 >> bit
				}
			}
			data = append(data, b)
		}
	}
	return data, bw, h
}

// raster encodes img as GS v 0 (normal density) followed by nothing: the
// image advances the paper by its own height.
func raster(img *image.Gray) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	bw := w / 8
	out := make([]byte, 0, 8+bw*h)
	out = append(out, 0x1D, 0x76, 0x30, 0x00, byte(bw), byte(bw>>8), byte(h), byte(h>>8))
	for y := 0; y < h; y++ {
		for xb := 0; xb < bw; xb++ {
			var b byte
			for bit := 0; bit < 8; bit++ {
				if img.GrayAt(xb*8+bit, y).Y < 128 {
					b |= 0x80 >> bit
				}
			}
			out = append(out, b)
		}
	}
	return out
}

// columnEnds returns, for each part of a laid-out row (parts split by runs
// of two or more spaces), the rune column just past its last character.
func columnEnds(s string) []int {
	var ends []int
	rs := []rune(s)
	i := 0
	for i < len(rs) {
		j := i
		for j < len(rs) && !(rs[j] == ' ' && j+1 < len(rs) && rs[j+1] == ' ') {
			j++
		}
		ends = append(ends, j)
		for j < len(rs) && rs[j] == ' ' {
			j++
		}
		i = j
	}
	return ends
}
