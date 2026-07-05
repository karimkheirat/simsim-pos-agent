// Package link32 builds raw Aclas LS2 "Link32" protocol frames for
// label scales. It is a pure builder in the spirit of internal/escpos:
// no I/O beyond an io.Reader frame-reading helper, no TCP connection,
// no state.
//
// SOURCE OF TRUTH — everything here is implemented strictly from the
// Aclas LS2X user manual (Pinnacle Technology Corp.), sections:
//
//	§9.1  "Interface Criterion of Link 32 based on TCP/IP Protocol"
//	§9.2  "Handshaking Flowchart of Label Scale and Background"
//	      (record package format + command list + worked examples)
//	§12.1 "Appendix 1: TXP (XU) File Format" (the PLU column table)
//
// Frame format (§9.2 "Record package format"):
//
//  1. Package length   4 bytes
//  2. Command          4 bytes
//  3. Record           uncertain length
//
// The manual's worked examples show the frames are ASCII decimal, and
// that the length covers the WHOLE package including the length field
// itself: the bare starting command is "00080201" (length "0008" =
// 4 length chars + 4 command chars), and the ACK example "0022 0102
// 0210 000001 0000" is 22 chars total.
//
// The manual documents the handshake between Link32 (the vendor PC
// software) and a "background" (backend). The agent replaces Link32 /
// the background in that exchange when pushing PLUs to a scale. Every
// aspect of that role adaptation that the manual does not spell out is
// marked TODO(verify-on-hardware) — grep for that marker to see every
// open item. Hardware verification happens before any store use.
package link32

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// Command codes from the §9.2 command list and flowchart. The manual
// writes them as 4-char ASCII decimal strings inside frames; the
// flowchart abbreviates them without the leading zero (e.g. "(201)").
const (
	// CmdStart is the "Starting command" (0201) that opens an exchange.
	// In the manual's flowchart the connecting party (Link32) sends it.
	CmdStart = "0201"

	// CmdAck is the "ACK command" (0202) sent by the PLU-receiving
	// party after each PLU record. Record: command(4) LFCode(6) error(4).
	CmdAck = "0202"

	// CmdBackgroundAck (0102) is the background's ACK in the sale-
	// account branch. Same record shape as CmdAck per the §9.2 example.
	CmdBackgroundAck = "0102"

	// CmdPLURecord (0110) — "Background transfers one PLU to Link32".
	// This is the PLU-download frame the agent sends to the scale.
	CmdPLURecord = "0110"

	// CmdRequireSaleUpload (0120) and the sale-record commands are
	// documented but out of scope for PLU download; listed so the
	// command space is complete in one place.
	CmdRequireSaleUpload = "0120"
	CmdSaleRecord        = "0210"
	CmdSaleRecordsOver   = "0220"
)

// Errors returned by the builders. Detect with errors.Is.
var (
	ErrFieldOverflow = errors.New("link32: field overflows its column width")
	ErrInvalidField  = errors.New("link32: invalid field value")
	ErrFrameTooLarge = errors.New("link32: frame exceeds 9999 bytes (4-digit length)")
	ErrShortFrame    = errors.New("link32: frame shorter than declared length")
	ErrBadFrame      = errors.New("link32: malformed frame")
)

// Weight-unit codes from the Appendix 1 "Weight Unit" column:
//
//	1:g, 2:10g, 3:100g, 4:kg, 5:oz, 6:lb, 7:500g, 8:600g,
//	9:PCS(g), A:PCS(Kg), B:PCS(oz), C:PCS(Lb)
const (
	WeightUnitG     = "1"
	WeightUnit10G   = "2"
	WeightUnit100G  = "3"
	WeightUnitKg    = "4"
	WeightUnitOz    = "5"
	WeightUnitLb    = "6"
	WeightUnit500G  = "7"
	WeightUnit600G  = "8"
	WeightUnitPCSG  = "9"
	WeightUnitPCSKg = "A"
	WeightUnitPCSOz = "B"
	WeightUnitPCSLb = "C"
)

// validWeightUnits gates the WeightUnit field to exactly the documented
// codes — a typo must not silently reach the scale.
var validWeightUnits = map[string]bool{
	WeightUnitG: true, WeightUnit10G: true, WeightUnit100G: true,
	WeightUnitKg: true, WeightUnitOz: true, WeightUnitLb: true,
	WeightUnit500G: true, WeightUnit600G: true, WeightUnitPCSG: true,
	WeightUnitPCSKg: true, WeightUnitPCSOz: true, WeightUnitPCSLb: true,
}

// PLU carries the caller-supplied values for one PLU record. Fields not
// present here take the Appendix 1 default column (see pluColumns).
type PLU struct {
	// LFCode uniquely identifies the commodity on the scale and is the
	// number the operator keys in for indirect PLU lookup. 1–6 ASCII
	// digits (column width 6).
	LFCode string

	// Code is the barcode commodity number ("Code" column, width 10,
	// "refer to barcode coding list"). 1–10 ASCII digits.
	Code string

	// Name is the product display name, ≤ 36 bytes after encoding.
	// Longer values are truncated at a rune boundary, never mid-rune.
	//
	// TODO(verify-on-hardware): the manual does not document the text
	// encoding for the 36-byte Name column. Non-ASCII names (Arabic)
	// pass through as UTF-8 bytes here; whether the scale's LFZK font
	// tables render that correctly must be verified on the device.
	Name string

	// UnitPriceCentimes is the integer unit price in the "no decimal
	// fraction" mode documented for the Unit Price column: "12.34 is
	// expressed as 1234 (=12.34*100)". Centimes map to that directly.
	// Range 0..99999999 (column width 8).
	UnitPriceCentimes int

	// WeightUnit is one of the WeightUnit* codes above (column width 1).
	WeightUnit string

	// BarcodeType per Appendix 2's coding list, 0..99 (column width 2).
	BarcodeType int

	// Department 0..99 (column width 2).
	Department int
}

// column is one fixed-width field of the PLU record, in wire order.
// value is resolved per-PLU; static defaults come straight from the
// Appendix 1 "Default" column.
type column struct {
	name  string
	width int
	value func(p PLU) string
}

func staticCol(name string, width int, v string) column {
	return column{name, width, func(PLU) string { return v }}
}

// pluColumns is THE single table describing the PLU record layout —
// Appendix 1's column list, in order, with its widths and defaults.
// Any hardware-verification fix to the layout happens here and nowhere
// else.
var pluColumns = []column{
	// "PLU No." — "reserved to be compatible with old version and has
	// no real meaning". Encoded as 0.
	staticCol("plu_no", 4, "0"),
	{"name", 36, func(p PLU) string { return p.Name }},
	{"lfcode", 6, func(p PLU) string { return p.LFCode }},
	{"code", 10, func(p PLU) string { return p.Code }},
	{"barcode_type", 2, func(p PLU) string { return strconv.Itoa(p.BarcodeType) }},
	{"unit_price", 8, func(p PLU) string { return strconv.Itoa(p.UnitPriceCentimes) }},
	{"weight_unit", 1, func(p PLU) string { return p.WeightUnit }},
	{"department", 2, func(p PLU) string { return strconv.Itoa(p.Department) }},
	// Container weight; 0 = none (manual default).
	staticCol("tare", 6, "0"),
	// Shelf life in days; Appendix 1 default 15.
	// TODO(verify-on-hardware): confirm 15 days is right for the pilot
	// stores' weighed produce, or surface as config later.
	staticCol("shelf_time", 3, "15"),
	// 0 = "normal mode (common weighing mode)".
	staticCol("package_type", 1, "0"),
	staticCol("package_weight", 6, "0"),
	// Fixed-weight/-price tolerance; Appendix 1 default 5. Unused in
	// package_type 0 but encoded per the default column.
	staticCol("package_tolerance", 2, "5"),
	// 0 = "no use this message".
	staticCol("message1", 3, "0"),
	staticCol("message2", 3, "0"), // reserved
	staticCol("multi_label", 3, "0"),
	staticCol("rebate", 3, "0"),
	staticCol("pcs_type", 2, "0"),
}

// EncodePLURecord renders one PLU into the Appendix 1 record layout:
// every column right-aligned in its fixed width, one space after every
// column, CR LF terminator ("Return (0xd) and new line (0xa) are added
// after every PLU as separator").
//
// TODO(verify-on-hardware): Appendix 1 documents this layout for the
// .txp/.txu PLU FILE; the §9.2 command list does not spell out the
// record layout of a 0110 frame. This is the only PLU record format
// the manual documents, so 0110 frames carry exactly one such record —
// to be confirmed against the device.
//
// TODO(verify-on-hardware): "columns are right aligned" is applied to
// every column including the textual Name; if the scale expects names
// left-aligned, flip it here.
func EncodePLURecord(p PLU) ([]byte, error) {
	if err := validatePLU(p); err != nil {
		return nil, err
	}
	buf := make([]byte, 0, recordSize())
	for _, col := range pluColumns {
		v := col.value(p)
		if len(v) > col.width {
			return nil, fmt.Errorf("%w: %s %q > %d bytes", ErrFieldOverflow, col.name, v, col.width)
		}
		for i := len(v); i < col.width; i++ {
			buf = append(buf, ' ')
		}
		buf = append(buf, v...)
		buf = append(buf, ' ') // "One space is added after every column"
	}
	buf = append(buf, '\r', '\n')
	return buf, nil
}

// recordSize returns the fixed encoded size of one PLU record.
func recordSize() int {
	n := 2 // CR LF
	for _, c := range pluColumns {
		n += c.width + 1
	}
	return n
}

// validatePLU enforces the Appendix 1 value ranges before layout, so a
// bad value surfaces as a named error instead of a corrupt column.
func validatePLU(p PLU) error {
	if !isDigits(p.LFCode) || len(p.LFCode) < 1 || len(p.LFCode) > 6 {
		return fmt.Errorf("%w: lfcode %q (want 1-6 ASCII digits)", ErrInvalidField, p.LFCode)
	}
	if !isDigits(p.Code) || len(p.Code) < 1 || len(p.Code) > 10 {
		return fmt.Errorf("%w: code %q (want 1-10 ASCII digits)", ErrInvalidField, p.Code)
	}
	if p.Name == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalidField)
	}
	if len(p.Name) > 36 {
		return fmt.Errorf("%w: name %q > 36 bytes (call TruncateName first)", ErrFieldOverflow, p.Name)
	}
	for _, b := range []byte(p.Name) {
		if b < 0x20 { // CR/LF/control bytes would corrupt the fixed-width record
			return fmt.Errorf("%w: name contains control byte 0x%02x", ErrInvalidField, b)
		}
	}
	if p.UnitPriceCentimes < 0 || p.UnitPriceCentimes > 99999999 {
		return fmt.Errorf("%w: unit_price %d out of range 0..99999999", ErrInvalidField, p.UnitPriceCentimes)
	}
	if !validWeightUnits[p.WeightUnit] {
		return fmt.Errorf("%w: weight_unit %q not a documented code", ErrInvalidField, p.WeightUnit)
	}
	if p.BarcodeType < 0 || p.BarcodeType > 99 {
		return fmt.Errorf("%w: barcode_type %d out of range 0..99", ErrInvalidField, p.BarcodeType)
	}
	if p.Department < 0 || p.Department > 99 {
		return fmt.Errorf("%w: department %d out of range 0..99", ErrInvalidField, p.Department)
	}
	return nil
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// TruncateName shortens name to at most 36 bytes without splitting a
// UTF-8 rune. The cloud already truncates to 36 code points; this is
// the byte-level guard for multi-byte scripts (Arabic names can exceed
// 36 bytes at 36 code points).
func TruncateName(name string) string {
	if len(name) <= 36 {
		return name
	}
	cut := 36
	for cut > 0 && !utf8.RuneStart(name[cut]) {
		cut--
	}
	return name[:cut]
}

// Frame wraps a record in the §9.2 package format: 4-digit ASCII total
// length (including the length field itself, per the manual's
// "00080201" starting-command example), 4-digit ASCII command, record.
func Frame(command string, record []byte) ([]byte, error) {
	if len(command) != 4 || !isDigits(command) {
		return nil, fmt.Errorf("%w: command %q (want 4 ASCII digits)", ErrInvalidField, command)
	}
	total := 4 + 4 + len(record)
	if total > 9999 {
		return nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, total)
	}
	buf := make([]byte, 0, total)
	buf = append(buf, fmt.Sprintf("%04d", total)...)
	buf = append(buf, command...)
	buf = append(buf, record...)
	return buf, nil
}

// StartFrame returns the bare starting command package "00080201".
func StartFrame() []byte {
	f, _ := Frame(CmdStart, nil) // constant input; cannot fail
	return f
}

// PLUFrame encodes p and wraps it in a 0110 "transfer one PLU" frame.
func PLUFrame(p PLU) ([]byte, error) {
	rec, err := EncodePLURecord(p)
	if err != nil {
		return nil, err
	}
	return Frame(CmdPLURecord, rec)
}

// Ack is the parsed record of an ACK frame (0202 / 0102). Per §9.2:
//
//	Command code   4  — the command being responded to
//	LFCode         6
//	Error code     4  — "0000" means no error
type Ack struct {
	Command   string
	LFCode    string
	ErrorCode string
}

// OK reports whether the ACK carries the documented no-error code.
func (a Ack) OK() bool { return a.ErrorCode == "0000" }

// ParseAck decodes the 14-byte record of an ACK frame.
func ParseAck(record []byte) (Ack, error) {
	if len(record) != 14 {
		return Ack{}, fmt.Errorf("%w: ack record is %d bytes, want 14", ErrBadFrame, len(record))
	}
	return Ack{
		Command:   string(record[0:4]),
		LFCode:    string(record[4:10]),
		ErrorCode: string(record[10:14]),
	}, nil
}

// ReadFrame reads one complete package from r: the 4-digit length,
// then the remainder. Returns the command and record. It performs no
// interpretation beyond the §9.2 package format.
func ReadFrame(r io.Reader) (command string, record []byte, err error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", nil, fmt.Errorf("link32: read length: %w", err)
	}
	total, aerr := strconv.Atoi(string(lenBuf[:]))
	if aerr != nil || total < 8 {
		return "", nil, fmt.Errorf("%w: length field %q", ErrBadFrame, lenBuf)
	}
	rest := make([]byte, total-4)
	if _, err := io.ReadFull(r, rest); err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrShortFrame, err)
	}
	return string(rest[:4]), rest[4:], nil
}
