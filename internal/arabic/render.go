package arabic

import (
	"image"
	"image/color"
	"image/draw"
	"sync"
	"unicode"

	"github.com/go-text/typesetting/bidi"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// Align is where the text renderer puts a line across the paper.
type Align int

const (
	Left Align = iota
	Center
	Right
)

// Layout describes the text line a picture replaces, in printer dots.
//
// The text renderers lay a line out in fixed columns of a monospaced
// printer font. RenderLine keeps those columns: a column c starts at
// c*CellW dots (shifted by the line's alignment, as the printer shifts
// text), so a price drawn in the picture sits under the price of the
// text line above it.
type Layout struct {
	// Width is the picture's width in dots: the printable width of the
	// roll (576 on 80 mm, 384 on 58 mm at 203 dpi), or the room a label
	// element has.
	Width int
	// CellW and CellH are the printer font's cell, magnification
	// included: ESC/POS Font A is 12x24 at x1, 12x48 at x1 wide x2 tall.
	CellW, CellH int
	// RowH is the picture's height: the paper the replaced line used.
	RowH int
	// Align is the line's justification on the paper.
	Align Align
	// Bold draws with the bold faces (Noto Sans Arabic Bold, Go Mono
	// Bold), as the printer's emphasized mode thickens its text.
	Bold bool
}

// Tunable proportions of the drawing inside a cell. They decide how big
// the Arabic letters are next to the printer's own Latin glyphs, and
// where the shared baseline sits. Calibrate on paper.
const (
	// arabicEmPerCell is the Arabic font's em size as a share of the
	// cell height. Noto Sans Arabic's letters sit low and tall; at 0.9
	// of a 24-dot cell the body of a letter is about the printer's
	// x-height and the descenders of ي ر و stay inside the picture.
	arabicEmPerCell = 0.9
	// baselinePerCell is the baseline's height from the top of the
	// cell, as a share of the cell: Font A's baseline is near 19/24.
	baselinePerCell = 0.72
	// inkThreshold is the coverage (0-255) from which an anti-aliased
	// dot prints black. Lower than half keeps thin joins from breaking.
	inkThreshold = 96
)

// mu serialises rendering: go-text faces cache glyph data and the
// shaper keeps buffers, and neither is safe for concurrent use.
var mu sync.Mutex

var shaper shaping.HarfbuzzShaper

// RenderLine draws s, a line laid out by a text renderer in columns of
// l.CellW dots, as a picture of l.Width x l.RowH dots: white is 255,
// black is 0. The caller only calls it for a line where Has(s) is true.
//
// The line is cut into fields at runs of two or more spaces (the gaps
// the text renderer pads columns with; a single space stays inside a
// field, as between the words of a name). Each field is shaped and
// ordered on its own (its paragraph direction is its first letter's,
// see bidiLevels), and drawn from its column. A field wider than its room (up to the next field, less one
// column) is squeezed horizontally to fit, so it never runs into the
// price. A line made of one field on a centred line is centred.
func RenderLine(s string, l Layout) (*image.Gray, error) {
	fs, err := loadFaces()
	if err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()

	img := image.NewGray(image.Rect(0, 0, l.Width, l.RowH))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.Gray{Y: 255}), image.Point{}, draw.Src)
	if l.Width <= 0 || l.RowH <= 0 || l.CellW <= 0 || l.CellH <= 0 {
		return img, nil
	}

	runes := []rune(s)
	n := len(runes)
	x0 := 0
	switch l.Align {
	case Center:
		x0 = (l.Width - n*l.CellW) / 2
	case Right:
		x0 = l.Width - n*l.CellW
	}
	if x0 < 0 {
		x0 = 0
	}

	sc := scalesFor(fs, l)
	fields := splitFields(runes)
	z := vector.NewRasterizer(l.Width, l.RowH)
	baseline := float64(l.RowH-l.CellH)/2 + float64(l.CellH)*baselinePerCell

	for i, f := range fields {
		glyphs, width := shapeField(fs, runes[f.start:f.end], l.Bold, sc)
		if len(glyphs) == 0 {
			continue
		}
		// The room this field may take: up to one column before the next
		// field, or to the paper's edge.
		roomEnd := l.Width
		if i+1 < len(fields) {
			roomEnd = x0 + (fields[i+1].start-1)*l.CellW
		}
		left := float64(x0 + f.start*l.CellW)
		if len(fields) == 1 && l.Align == Center {
			mid := float64(x0) + float64(f.start+f.end)*float64(l.CellW)/2
			left = mid - width/2
			if left < 0 {
				left = 0
			}
		}
		if len(fields) == 1 && l.Align == Right {
			left = float64(x0+f.end*l.CellW) - width
			if left < 0 {
				left = 0
			}
		}
		squeeze := 1.0
		if room := float64(roomEnd) - left; width > room && room > 0 {
			squeeze = room / width
		}
		for _, g := range glyphs {
			drawGlyph(z, g, left, baseline, squeeze)
		}
	}

	cover := image.NewAlpha(img.Bounds())
	z.Draw(cover, cover.Bounds(), image.Opaque, image.Point{})
	for i, a := range cover.Pix {
		if a >= inkThreshold {
			img.Pix[i] = 0
		}
	}
	return img, nil
}

// scales converts font units to dots, per face.
type scales struct {
	arX, arY     float64 // Noto Sans Arabic
	monoX, monoY float64 // Go Mono
}

func scalesFor(fs *faceSet, l Layout) scales {
	// The printer's cell is 1:2 (12x24) at x1; a magnified or narrower
	// cell stretches the drawing the same way the printer stretches text.
	aspect := float64(2*l.CellW) / float64(l.CellH)
	arY := float64(l.CellH) * arabicEmPerCell / float64(fs.arabic.Upem())
	monoX := float64(l.CellW) / fs.monoAdvance
	return scales{
		arX:   arY * aspect,
		arY:   arY,
		monoX: monoX,
		monoY: monoX / aspect,
	}
}

// field is a stretch of the line between gaps of two or more spaces.
type field struct{ start, end int } // rune indexes, end exclusive

func splitFields(runes []rune) []field {
	var out []field
	i, n := 0, len(runes)
	for i < n {
		for i < n && runes[i] == ' ' {
			i++
		}
		if i >= n {
			break
		}
		start := i
		end := i
		for i < n {
			if runes[i] == ' ' {
				j := i
				for j < n && runes[j] == ' ' {
					j++
				}
				if j-i >= 2 || j == n {
					break
				}
				i = j
				continue
			}
			i++
			end = i
		}
		out = append(out, field{start, end})
	}
	return out
}

// glyph is one shaped glyph, positioned in dots from the field's left edge
// and the baseline.
type glyph struct {
	face   *font.Face
	gid    font.GID
	x, y   float64 // pen position + shaper offset, dots (y up)
	sx, sy float64 // dots per font unit
}

// level is a stretch of the field at one bidi embedding level.
type level struct {
	start, end int
	lvl        int
}

// bidiLevels resolves the field's embedding levels. The paragraph
// direction is the field's first strong letter (rules P2-P3, what a
// browser does with dir="auto"): a name that starts in Arabic reads
// right to left with "1L" kept whole beside it; a French word that
// starts the field keeps it left to right.
//
// go-text's bidi package reports runs split only by direction, labelled
// with the level of their first rune. In a right-to-left paragraph every
// even run is level 2, so that is exact. In a left-to-right one a number
// in an Arabic context (level 2) and the Latin after it (level 0) come
// back as one run; they are split here. No explicit embeddings are used on
// a receipt, so the level-2 part is exactly the number at the start of
// such a run (digits with the separators between them, rule W4).
func bidiLevels(text []rune) []level {
	dir := bidi.LeftToRight
	if firstStrongRTL(text) {
		dir = bidi.RightToLeft
	}
	var p bidi.Paragraph
	runs := p.Segment(text, dir)
	var out []level
	for i := 0; i < runs.NumRuns(); i++ {
		r := runs.Run(i)
		lv := int(r.Level)
		if lv%2 == 1 || lv == 0 || dir == bidi.RightToLeft {
			out = append(out, level{r.Start, r.End, lv})
			continue
		}
		cut := r.Start
		for cut < r.End {
			c := text[cut]
			if unicode.IsDigit(c) {
				cut++
				continue
			}
			if (c == ',' || c == '.' || c == ':' || c == '/') && cut+1 < r.End && unicode.IsDigit(text[cut+1]) && cut > r.Start {
				cut++
				continue
			}
			break
		}
		if cut == r.Start {
			cut = r.End
		}
		out = append(out, level{r.Start, cut, lv})
		if cut < r.End {
			out = append(out, level{cut, r.End, 0})
		}
	}
	return out
}

// firstStrongRTL reports whether the first letter of text is Arabic.
func firstStrongRTL(text []rune) bool {
	for _, r := range text {
		if unicode.IsLetter(r) {
			return IsArabic(r)
		}
	}
	return false
}

// visualOrder applies rule L2: from the highest level down to 1, reverse
// every maximal sequence of runs at that level or higher.
func visualOrder(lv []level) []level {
	out := append([]level(nil), lv...)
	maxL := 0
	for _, r := range out {
		if r.lvl > maxL {
			maxL = r.lvl
		}
	}
	for L := maxL; L >= 1; L-- {
		for i := 0; i < len(out); {
			if out[i].lvl < L {
				i++
				continue
			}
			j := i
			for j < len(out) && out[j].lvl >= L {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				out[a], out[b] = out[b], out[a]
			}
			i = j
		}
	}
	return out
}

// usesArabicFace decides which face draws r: Arabic letters, marks and
// joiners use Noto Sans Arabic; a space inside a right-to-left run too
// (so the run is not cut and the shaper sees the whole word sequence);
// everything else uses Go Mono, falling back to Noto when Go Mono lacks it.
func usesArabicFace(fs *faceSet, r rune, rtl bool) bool {
	switch {
	case IsArabic(r), unicode.Is(unicode.Mn, r), r == 0x200C, r == 0x200D:
		return true
	case rtl && unicode.IsSpace(r):
		return true
	}
	if _, ok := fs.mono.NominalGlyph(r); ok {
		return false
	}
	_, ok := fs.arabic.NominalGlyph(r)
	return ok
}

// shapeField shapes text and returns its glyphs in visual order, left to
// right, with the field's width in dots.
func shapeField(fs *faceSet, text []rune, bold bool, sc scales) ([]glyph, float64) {
	var out []glyph
	pen := 0.0
	for _, run := range visualOrder(bidiLevels(text)) {
		rtl := run.lvl%2 == 1
		// Cut the run where the face changes.
		type piece struct {
			start, end int
			ar         bool
		}
		var pieces []piece
		for i := run.start; i < run.end; i++ {
			ar := usesArabicFace(fs, text[i], rtl)
			if len(pieces) > 0 && pieces[len(pieces)-1].ar == ar {
				pieces[len(pieces)-1].end = i + 1
				continue
			}
			pieces = append(pieces, piece{i, i + 1, ar})
		}
		if rtl {
			for a, b := 0, len(pieces)-1; a < b; a, b = a+1, b-1 {
				pieces[a], pieces[b] = pieces[b], pieces[a]
			}
		}
		for _, pc := range pieces {
			face, sx, sy := fs.mono, sc.monoX, sc.monoY
			script := language.Latin
			if pc.ar {
				face, sx, sy = fs.arabic, sc.arX, sc.arY
				script = language.Arabic
				if bold {
					face = fs.arabicBold
				}
			} else if bold {
				face = fs.monoBold
			}
			dir := di.DirectionLTR
			if rtl {
				dir = di.DirectionRTL
			}
			upem := int(face.Upem())
			o := shaper.Shape(shaping.Input{
				Text:      text,
				RunStart:  pc.start,
				RunEnd:    pc.end,
				Direction: dir,
				Face:      face,
				Size:      fixed.I(upem), // positions come back in font units
				Script:    script,
				Language:  language.NewLanguage("ar"),
			})
			for _, g := range o.Glyphs {
				out = append(out, glyph{
					face: face,
					gid:  g.GlyphID,
					x:    pen + fixedToF(g.XOffset)*sx,
					y:    fixedToF(g.YOffset) * sy,
					sx:   sx,
					sy:   sy,
				})
				pen += fixedToF(g.Advance) * sx
			}
		}
	}
	return out, pen
}

func fixedToF(v fixed.Int26_6) float64 { return float64(v) / 64 }

// drawGlyph adds g's outline to z, at left + g.x (squeezed) on baseline.
func drawGlyph(z *vector.Rasterizer, g glyph, left, baseline, squeeze float64) {
	out, ok := g.face.GlyphDataOutline(g.gid)
	if !ok {
		return
	}
	pt := func(p ot.SegmentPoint) (float32, float32) {
		x := left + (g.x+float64(p.X)*g.sx)*squeeze
		y := baseline - (g.y + float64(p.Y)*g.sy)
		return float32(x), float32(y)
	}
	open := false
	for _, seg := range out.Segments {
		switch seg.Op {
		case ot.SegmentOpMoveTo:
			if open {
				z.ClosePath()
			}
			z.MoveTo(pt(seg.Args[0]))
			open = true
		case ot.SegmentOpLineTo:
			z.LineTo(pt(seg.Args[0]))
		case ot.SegmentOpQuadTo:
			bx, by := pt(seg.Args[0])
			cx, cy := pt(seg.Args[1])
			z.QuadTo(bx, by, cx, cy)
		case ot.SegmentOpCubeTo:
			bx, by := pt(seg.Args[0])
			cx, cy := pt(seg.Args[1])
			dx, dy := pt(seg.Args[2])
			z.CubeTo(bx, by, cx, cy, dx, dy)
		}
	}
	if open {
		z.ClosePath()
	}
}
