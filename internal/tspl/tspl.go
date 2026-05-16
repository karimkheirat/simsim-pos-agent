// Package tspl builds raw TSPL2 command byte sequences for thermal LABEL
// printers (NOT receipt printers — those use ESC/POS via internal/escpos).
//
// TSPL2 is line-oriented ASCII: each command is a single line terminated
// by CRLF (\r\n). The printer interprets commands sequentially and emits
// the assembled label image when PRINT is sent.
//
// Builder usage mirrors the existing escpos.Builder pattern: chainable
// methods accumulate bytes into an internal buffer, Bytes() returns the
// assembled stream. The builder is a pure byte assembler — no I/O, no
// printer connection, no state beyond the buffer.
//
// Reference: TSPL2 Programming Manual (TSC Auto ID Technology), the
// shared base dialect honored by every printer in
// docs/printer-compatibility.md §3 (Rongta RP-410/420/426A,
// Xprinter XP-DT426B/DT108B/470B, Aclas PP7X/PP8X, TSC TDP/TTP families).
//
// Dialect notes — see BarcodeEAN13 for the standard/rongta EAN-13 split.
// Other commands in this package are emitted with the most-compatible
// subset; vendor extensions (e.g. Rongta-only QR sub-modes) are not used.
package tspl

import (
	"fmt"
	"strconv"
	"strings"
)

// Dialect selects vendor-specific command variants. Most TSPL commands
// are identical across vendors; the per-vendor splits (EAN-13 hyphen,
// codepage selector flavours) are confined to the named dialects.
type Dialect string

const (
	// DialectStandard is the TSC/Xprinter/Aclas TSPL2 baseline. EAN-13
	// barcodes use the "EAN13" identifier (no hyphen).
	DialectStandard Dialect = "standard"

	// DialectRongta is the Rongta variant. EAN-13 barcodes use "EAN-13"
	// (hyphen) — Rongta firmware does NOT accept "EAN13".
	DialectRongta Dialect = "rongta"
)

// Direction selects label print direction (DIRECTION 0|1 — flips the
// origin between bottom-left (0) and top-left (1) of the gap-sensed
// label area). Defaults to Direction1 (top-left) which matches the
// portrait orientation cashiers see in label templates.
type Direction int

const (
	Direction0 Direction = 0
	Direction1 Direction = 1
)

// QRMode is the eccLevel passed to TSPL2's QRCODE command.
//   - QRModeL — ~7% error correction (smallest output)
//   - QRModeM — ~15% (recommended default for price tags)
//   - QRModeQ — ~25%
//   - QRModeH — ~30% (largest output, best survival of label damage)
type QRMode string

const (
	QRModeL QRMode = "L"
	QRModeM QRMode = "M"
	QRModeQ QRMode = "Q"
	QRModeH QRMode = "H"
)

// FontName is the TSPL font identifier used by the TEXT command.
// TSPL2 supports printer-internal bitmap fonts numbered as ASCII strings.
//   - Font1 — 8x12 dots ASCII
//   - Font2 — 12x20
//   - Font3 — 16x24
//   - Font4 — 24x32
//   - Font5 — 32x48
//   - FontROMAN — bitmap roman/serif variant available on most TSPL printers
//
// Font availability varies by printer firmware; Font2 + Font3 are the
// safe defaults for the v1 label templates (price tag + shelf label).
type FontName string

const (
	Font1     FontName = "1"
	Font2     FontName = "2"
	Font3     FontName = "3"
	Font4     FontName = "4"
	Font5     FontName = "5"
	FontROMAN FontName = "ROMAN.TTF"
)

// crlf is the per-line terminator. Every TSPL command emits one line.
const crlf = "\r\n"

// sanitizeArg replaces \r and \n in a user-supplied value with spaces.
// TSPL is line-oriented; embedding CR/LF in a quoted argument would
// break the printer's parser and (worst case) inject a downstream
// command. Used by every method that interpolates a string value.
//
// We pick space rather than removal so column widths (visible label
// layout) are preserved.
func sanitizeArg(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// quoted wraps s in double quotes after sanitizing CR/LF. TSPL has no
// in-string escape sequence; values that contain a literal " are also
// scrubbed (replaced with ') so the quoted form parses correctly.
func quoted(s string) string {
	clean := sanitizeArg(s)
	clean = strings.ReplaceAll(clean, `"`, `'`)
	return `"` + clean + `"`
}

// ── Standalone command builders (each returns a single CRLF-terminated line) ──

// CLS returns the CLS command — clear the image buffer. Always issued
// at the start of a label to reset any state left from a previous job.
func CLS() []byte {
	return []byte("CLS" + crlf)
}

// Size returns the SIZE command with width and height in millimetres.
// Common label sizes for the pilot: 40x30, 50x30, 50x40, 60x40.
func Size(widthMM, heightMM int) []byte {
	return []byte(fmt.Sprintf("SIZE %d mm,%d mm%s", widthMM, heightMM, crlf))
}

// Gap returns the GAP command — distance between gap-sensed labels.
// gapMM is typically 2 or 3 for pre-cut labels; offsetMM is usually 0.
func Gap(gapMM, offsetMM int) []byte {
	return []byte(fmt.Sprintf("GAP %d mm,%d mm%s", gapMM, offsetMM, crlf))
}

// DirectionCmd returns the DIRECTION command (0 or 1). Most cashier
// templates render top-down, matching Direction1.
func DirectionCmd(d Direction) []byte {
	return []byte(fmt.Sprintf("DIRECTION %d%s", int(d), crlf))
}

// Density returns the DENSITY command (0..15). Higher = darker print.
// Pilot default is 8 (TSPL2 spec default); 10 helps on aged ribbons.
func Density(level int) []byte {
	return []byte(fmt.Sprintf("DENSITY %d%s", level, crlf))
}

// Speed returns the SPEED command in inches/second. Typical values
// 2..6. Pilot default is 4 — slow enough that low-end printers
// (Aclas PP7X) keep registration on barcode jobs.
func Speed(ips int) []byte {
	return []byte(fmt.Sprintf("SPEED %d%s", ips, crlf))
}

// Codepage returns the CODEPAGE command — character map for TEXT
// payloads. Pass "1252" for Windows-1252 (Latin-1 + Euro), matching
// the codepage transcoder in this package.
func Codepage(name string) []byte {
	return []byte("CODEPAGE " + name + crlf)
}

// Text returns a TEXT command. Positions the string at (x, y) dots
// using the given printer-internal font. rotation must be one of
// {0, 90, 180, 270}. xMul, yMul are integer magnification (1..8).
//
// The string is sanitized (CR/LF removed) and double-quoted; the
// printer rejects unquoted strings containing spaces.
func Text(x, y int, font FontName, rotation, xMul, yMul int, s string) []byte {
	line := fmt.Sprintf("TEXT %d,%d,%s,%d,%d,%d,%s%s",
		x, y, quoted(string(font)), rotation, xMul, yMul, quoted(s), crlf)
	return []byte(line)
}

// BarcodeCode128 returns a BARCODE command for a CODE128 symbol at
// (x, y) dots. heightDots = bar height; humanReadable=0 hides the
// printed text below the bars, =1 / =2 / =3 are positioning variants.
// rotation in {0,90,180,270}; narrow, wide are bar widths in dots
// (set both to 2 for the standard 2:6 ratio at 203dpi).
//
// data is sanitized and double-quoted.
func BarcodeCode128(x, y, heightDots, humanReadable, rotation, narrow, wide int, data string) []byte {
	line := fmt.Sprintf("BARCODE %d,%d,\"128\",%d,%d,%d,%d,%d,%s%s",
		x, y, heightDots, humanReadable, rotation, narrow, wide, quoted(data), crlf)
	return []byte(line)
}

// BarcodeEAN13 returns a BARCODE command for an EAN-13 symbol. The
// barcode identifier string depends on dialect:
//   - DialectStandard → "EAN13"
//   - DialectRongta   → "EAN-13"
//
// Other parameters mirror BarcodeCode128 — heightDots, humanReadable
// (0|1|2|3), rotation, narrow, wide.
//
// data is sanitized and double-quoted; the printer validates the
// 12-or-13 digit EAN-13 content itself and refuses non-numeric input.
func BarcodeEAN13(dialect Dialect, x, y, heightDots, humanReadable, rotation, narrow, wide int, data string) []byte {
	id := "EAN13"
	if dialect == DialectRongta {
		id = "EAN-13"
	}
	line := fmt.Sprintf("BARCODE %d,%d,%s,%d,%d,%d,%d,%d,%s%s",
		x, y, quoted(id), heightDots, humanReadable, rotation, narrow, wide, quoted(data), crlf)
	return []byte(line)
}

// QRCode returns a QRCODE command at (x, y) dots. eccLevel is QRModeL/M/Q/H.
// cellWidth is module size in dots (4 ≈ scannable at 30cm on 203dpi).
// mode "A" is auto-detect; rotation in {0,90,180,270}.
//
// data is sanitized and double-quoted.
func QRCode(x, y int, eccLevel QRMode, cellWidth int, mode string, rotation int, data string) []byte {
	line := fmt.Sprintf("QRCODE %d,%d,%s,%d,%s,%d,%s%s",
		x, y, quoted(string(eccLevel)), cellWidth, quoted(mode), rotation, quoted(data), crlf)
	return []byte(line)
}

// Print returns the PRINT command — emit the assembled image.
// quantity is the number of identical labels to print; copies is the
// number of times to repeat each unique label (almost always 1).
//
// Issue Print as the final command in the buffer; subsequent CLS
// starts a fresh image.
func Print(quantity, copies int) []byte {
	return []byte("PRINT " + strconv.Itoa(quantity) + "," + strconv.Itoa(copies) + crlf)
}

// ── Builder ───────────────────────────────────────────────────────────

// Builder accumulates TSPL2 byte sequences via chainable methods.
// The zero value is not usable — call New.
//
// Dialect is set at construction (default DialectStandard) and only
// affects BarcodeEAN13. Other commands are dialect-agnostic.
type Builder struct {
	buf     []byte
	dialect Dialect
}

// New returns an empty Builder using DialectStandard.
func New() *Builder {
	return &Builder{dialect: DialectStandard}
}

// NewWithDialect returns an empty Builder using the given dialect.
// Pass DialectRongta when targeting Rongta-firmware label printers
// (the EAN-13 hyphen split — see BarcodeEAN13).
func NewWithDialect(d Dialect) *Builder {
	return &Builder{dialect: d}
}

// Dialect returns the dialect this builder was constructed with.
func (b *Builder) Dialect() Dialect { return b.dialect }

// Bytes returns the accumulated byte slice. The returned slice aliases
// the builder's internal buffer; callers that need to retain it across
// further builder calls should copy it.
func (b *Builder) Bytes() []byte { return b.buf }

// Write appends raw bytes (e.g. a hand-rolled TSPL line). Caller must
// ensure the appended slice is CRLF-terminated.
func (b *Builder) Write(data []byte) *Builder {
	b.buf = append(b.buf, data...)
	return b
}

// CLS appends the CLS line.
func (b *Builder) CLS() *Builder { return b.Write(CLS()) }

// Size appends SIZE w mm,h mm.
func (b *Builder) Size(widthMM, heightMM int) *Builder { return b.Write(Size(widthMM, heightMM)) }

// Gap appends GAP g mm,o mm.
func (b *Builder) Gap(gapMM, offsetMM int) *Builder { return b.Write(Gap(gapMM, offsetMM)) }

// Direction appends DIRECTION n.
func (b *Builder) Direction(d Direction) *Builder { return b.Write(DirectionCmd(d)) }

// Density appends DENSITY n.
func (b *Builder) Density(level int) *Builder { return b.Write(Density(level)) }

// Speed appends SPEED n.
func (b *Builder) Speed(ips int) *Builder { return b.Write(Speed(ips)) }

// Codepage appends CODEPAGE <name>.
func (b *Builder) Codepage(name string) *Builder { return b.Write(Codepage(name)) }

// Text appends a TEXT line. Convenience wrappers for the most common
// shape (Font2, no rotation, 1x1 magnification) live as TextSimple.
func (b *Builder) Text(x, y int, font FontName, rotation, xMul, yMul int, s string) *Builder {
	return b.Write(Text(x, y, font, rotation, xMul, yMul, s))
}

// TextSimple appends a TEXT line with rotation=0 and xMul=yMul=1.
// Sugar for the common case in label templates.
func (b *Builder) TextSimple(x, y int, font FontName, s string) *Builder {
	return b.Text(x, y, font, 0, 1, 1, s)
}

// TextCP1252 appends a TEXT line after transcoding s from UTF-8 to
// CP1252 via ToCP1252. The printer's CODEPAGE 1252 must be selected
// for the transcoded bytes to render as the intended glyphs.
//
// Arabic and other non-CP1252 runes become '?' per the codepage map's
// fallback policy (see ToCP1252).
func (b *Builder) TextCP1252(x, y int, font FontName, rotation, xMul, yMul int, s string) *Builder {
	transcoded := string(ToCP1252(s))
	return b.Text(x, y, font, rotation, xMul, yMul, transcoded)
}

// BarcodeCode128 appends a CODE128 BARCODE line.
func (b *Builder) BarcodeCode128(x, y, heightDots, humanReadable, rotation, narrow, wide int, data string) *Builder {
	return b.Write(BarcodeCode128(x, y, heightDots, humanReadable, rotation, narrow, wide, data))
}

// BarcodeEAN13 appends an EAN-13 BARCODE line honoring the builder's
// dialect (Rongta gets "EAN-13"; others get "EAN13").
func (b *Builder) BarcodeEAN13(x, y, heightDots, humanReadable, rotation, narrow, wide int, data string) *Builder {
	return b.Write(BarcodeEAN13(b.dialect, x, y, heightDots, humanReadable, rotation, narrow, wide, data))
}

// QRCode appends a QRCODE line.
func (b *Builder) QRCode(x, y int, eccLevel QRMode, cellWidth int, mode string, rotation int, data string) *Builder {
	return b.Write(QRCode(x, y, eccLevel, cellWidth, mode, rotation, data))
}

// Print appends the PRINT line. Issue last; subsequent CLS starts a
// fresh image.
func (b *Builder) Print(quantity, copies int) *Builder {
	return b.Write(Print(quantity, copies))
}
