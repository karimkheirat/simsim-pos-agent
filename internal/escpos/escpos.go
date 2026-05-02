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
