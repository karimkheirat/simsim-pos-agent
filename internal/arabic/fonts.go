package arabic

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"

	"github.com/go-text/typesetting/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
)

// The Arabic letters are drawn with Noto Sans Arabic (SIL Open Font
// License 1.1, fonts/OFL.txt), unhinted static Regular and Bold from the
// notofonts/arabic release NotoSansArabic-v2.013. Everything else on an
// Arabic line (digits, Latin, "DA") is drawn with Go Mono (BSD licence,
// shipped in golang.org/x/image), a monospaced face whose advance is set
// to the printer's column width so the line's columns stay on the grid of
// the text lines around it.

//go:embed fonts/NotoSansArabic-Regular.ttf
var notoRegular []byte

//go:embed fonts/NotoSansArabic-Bold.ttf
var notoBold []byte

// faces are parsed once, on the first Arabic line.
type faceSet struct {
	arabic, arabicBold *font.Face
	mono, monoBold     *font.Face
	monoAdvance        float64 // advance of a Go Mono glyph, font units
}

var (
	facesOnce sync.Once
	facesVal  *faceSet
	facesErr  error
)

func loadFaces() (*faceSet, error) {
	facesOnce.Do(func() {
		parse := func(name string, b []byte) *font.Face {
			if facesErr != nil {
				return nil
			}
			f, err := font.ParseTTF(bytes.NewReader(b))
			if err != nil {
				facesErr = fmt.Errorf("arabic: parse %s: %w", name, err)
			}
			return f
		}
		fs := &faceSet{
			arabic:     parse("NotoSansArabic-Regular", notoRegular),
			arabicBold: parse("NotoSansArabic-Bold", notoBold),
			mono:       parse("gomono", gomono.TTF),
			monoBold:   parse("gomonobold", gomonobold.TTF),
		}
		if facesErr != nil {
			return
		}
		gid, ok := fs.mono.NominalGlyph('0')
		if !ok {
			facesErr = fmt.Errorf("arabic: gomono has no '0'")
			return
		}
		fs.monoAdvance = float64(fs.mono.HorizontalAdvance(gid))
		facesVal = fs
	})
	return facesVal, facesErr
}
