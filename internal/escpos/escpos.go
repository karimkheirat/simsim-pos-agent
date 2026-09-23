// Package escpos builds raw ESC/POS command byte sequences for thermal
// printers. It is a pure builder: no I/O, no printer connection, no state
// beyond the accumulated byte buffer of a Builder.
//
// The command set implemented here matches POS_AGENT_SPEC.md §6.3.
package escpos

// Codepage values for the ESC t n command.
const (
	// CP858 — Latin-1 + euro symbol. ESC/POS codepage index 19.
	CP858 byte = 19
)

// Alignment selects justification for the ESC a n command.
type Alignment byte

const (
	Left   Alignment = 0
	Center Alignment = 1
	Right  Alignment = 2
)

// doubleSizeOn is the GS ! n parameter for 2x height + 2x width.
// Low nibble: height magnification (1 = 2x). High nibble: width magnification (1 = 2x).
const doubleSizeOn byte = 0x11

// Init returns ESC @ — initialize printer (1B 40).
func Init() []byte {
	return []byte{0x1B, 0x40}
}

// Codepage returns ESC t n — select character codepage (1B 74 n).
func Codepage(n byte) []byte {
	return []byte{0x1B, 0x74, n}
}

// Bold returns ESC E n — toggle emphasized (bold) printing (1B 45 n).
// on=true emits n=1; on=false emits n=0.
func Bold(on bool) []byte {
	var n byte
	if on {
		n = 1
	}
	return []byte{0x1B, 0x45, n}
}

// DoubleSize returns GS ! n — character size (1D 21 n).
// on=true emits 2x height + 2x width (0x11); on=false emits normal size (0x00).
func DoubleSize(on bool) []byte {
	var n byte
	if on {
		n = doubleSizeOn
	}
	return []byte{0x1D, 0x21, n}
}

// DoubleHeight returns GS ! n with the height bit only (1D 21 01 / 00).
// on=true emits 2x height, normal width; on=false emits normal size.
func DoubleHeight(on bool) []byte {
	var n byte
	if on {
		n = 0x01
	}
	return []byte{0x1D, 0x21, n}
}

// Align returns ESC a n — justification (1B 61 n) for n in {0, 1, 2}.
func Align(a Alignment) []byte {
	return []byte{0x1B, 0x61, byte(a)}
}

// CutFull returns GS V 0 — full paper cut (1D 56 00).
func CutFull() []byte {
	return []byte{0x1D, 0x56, 0x00}
}

// CutPartial returns GS V 1 — partial paper cut (1D 56 01).
func CutPartial() []byte {
	return []byte{0x1D, 0x56, 0x01}
}

// DrawerKick returns ESC p m t1 t2 — pulse drawer pin 2 (DK1) for ~50ms
// (1B 70 00 32 FA). Spec §6.3 fixes the timing parameters.
func DrawerKick() []byte {
	return []byte{0x1B, 0x70, 0x00, 0x32, 0xFA}
}

// Builder accumulates ESC/POS bytes via chainable methods. The zero value
// is not usable — call New.
type Builder struct {
	buf []byte
}

// New returns an empty Builder ready to accumulate commands.
func New() *Builder {
	return &Builder{}
}

// Bytes returns the accumulated byte slice. The returned slice aliases the
// builder's internal buffer; callers that need to retain it across further
// builder calls should copy it.
func (b *Builder) Bytes() []byte {
	return b.buf
}

// Write appends raw bytes (e.g. text payload between commands).
func (b *Builder) Write(data []byte) *Builder {
	b.buf = append(b.buf, data...)
	return b
}

// Text appends s as raw bytes; convenience wrapper for Write([]byte(s)).
func (b *Builder) Text(s string) *Builder {
	return b.Write([]byte(s))
}

// TextCP858 transcodes s from UTF-8 to CP858 and appends the result.
// Equivalent to b.Write(ToCP858(s)).
func (b *Builder) TextCP858(s string) *Builder {
	return b.Write(ToCP858(s))
}

// Init appends ESC @.
func (b *Builder) Init() *Builder {
	b.buf = append(b.buf, Init()...)
	return b
}

// Codepage appends ESC t n.
func (b *Builder) Codepage(n byte) *Builder {
	b.buf = append(b.buf, Codepage(n)...)
	return b
}

// Bold appends ESC E n.
func (b *Builder) Bold(on bool) *Builder {
	b.buf = append(b.buf, Bold(on)...)
	return b
}

// DoubleSize appends GS ! n.
func (b *Builder) DoubleSize(on bool) *Builder {
	b.buf = append(b.buf, DoubleSize(on)...)
	return b
}

// DoubleHeight appends GS ! 01 / 00.
func (b *Builder) DoubleHeight(on bool) *Builder {
	b.buf = append(b.buf, DoubleHeight(on)...)
	return b
}

// Align appends ESC a n.
func (b *Builder) Align(a Alignment) *Builder {
	b.buf = append(b.buf, Align(a)...)
	return b
}

// CutFull appends GS V 0.
func (b *Builder) CutFull() *Builder {
	b.buf = append(b.buf, CutFull()...)
	return b
}

// CutPartial appends GS V 1.
func (b *Builder) CutPartial() *Builder {
	b.buf = append(b.buf, CutPartial()...)
	return b
}

// DrawerKick appends ESC p 0 50 250.
func (b *Builder) DrawerKick() *Builder {
	b.buf = append(b.buf, DrawerKick()...)
	return b
}

// Size returns GS ! n with explicit width and height multipliers, each
// clamped to 1..8 (the ESC/POS range). Size(1, 1) is normal size and is
// byte-identical to DoubleSize(false).
func Size(width, height int) []byte {
	clamp := func(v int) byte {
		if v < 1 {
			v = 1
		}
		if v > 8 {
			v = 8
		}
		return byte(v - 1)
	}
	return []byte{0x1D, 0x21, clamp(width)<<4 | clamp(height)}
}

// CharSpacing returns ESC SP n — extra dots to the right of every
// character (1B 20 n). 0 is the printer default.
func CharSpacing(dots byte) []byte {
	return []byte{0x1B, 0x20, dots}
}

// HRIPosition values for GS H n (where the printer prints the barcode's
// human-readable text).
const (
	HRINone  byte = 0
	HRIBelow byte = 2
)

// ErrBarcodeData is returned by Code128 when the data cannot be encoded
// in Code 128 set B (printable ASCII 0x20–0x7E only) or is empty/too long.
type ErrBarcodeData struct{ Reason string }

func (e ErrBarcodeData) Error() string { return "escpos: code128: " + e.Reason }

// Code128Modules returns how many modules (narrowest bar widths) a Code
// 128 set B symbol for data occupies, quiet zones excluded: start (11) +
// 11 per character + check (11) + stop (13). Multiply by the GS w module
// width to get dots.
func Code128Modules(data string) int {
	return 11 + 11*len(data) + 11 + 13
}

// Code128 returns the bytes to print data as a Code 128 barcode in code
// set B: GS H (HRI position), GS h (height in dots), GS w (module width in
// dots), GS k 73 n "{B" data. A literal '{' in data is sent as "{{", the
// escape GS k function B requires.
func Code128(data string, heightDots, moduleDots, hri byte) ([]byte, error) {
	if data == "" {
		return nil, ErrBarcodeData{"empty"}
	}
	payload := []byte{'{', 'B'}
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c < 0x20 || c > 0x7E {
			return nil, ErrBarcodeData{"non-printable or non-ASCII byte"}
		}
		payload = append(payload, c)
		if c == '{' {
			payload = append(payload, '{')
		}
	}
	if len(payload) > 255 {
		return nil, ErrBarcodeData{"too long"}
	}
	out := []byte{
		0x1D, 0x48, hri,
		0x1D, 0x68, heightDots,
		0x1D, 0x77, moduleDots,
		0x1D, 0x6B, 73, byte(len(payload)),
	}
	return append(out, payload...), nil
}

// Size appends GS ! n with the given multipliers.
func (b *Builder) Size(width, height int) *Builder {
	b.buf = append(b.buf, Size(width, height)...)
	return b
}

// CharSpacing appends ESC SP n.
func (b *Builder) CharSpacing(dots byte) *Builder {
	b.buf = append(b.buf, CharSpacing(dots)...)
	return b
}
