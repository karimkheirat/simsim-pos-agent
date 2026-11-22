// Package code128 draws a Code 128 (code set B) symbol as bars, for
// printers that print pictures rather than barcode commands (TSPL BITMAP)
// and for previews. It encodes exactly what escpos.Code128 asks an ESC/POS
// printer for — start B, the data, the mod-103 check, stop — so a ticket
// scans the same whichever printer drew it.
package code128

import "errors"

// patterns are the 107 Code 128 symbols as bar/space widths in modules,
// bar first. Index 104 is START B, 106 is STOP (seven elements).
var patterns = [...]string{
	"212222", "222122", "222221", "121223", "121322", "131222", "122213", "122312", "132212", "221213",
	"221312", "231212", "112232", "122132", "122231", "113222", "123122", "123221", "223211", "221132",
	"221231", "213212", "223112", "312131", "311222", "321122", "321221", "312212", "322112", "322211",
	"212123", "212321", "232121", "111323", "131123", "131321", "112313", "132113", "132311", "211313",
	"231113", "231311", "112133", "112331", "132131", "113123", "113321", "133121", "313121", "211331",
	"231131", "213113", "213311", "213131", "311123", "311321", "331121", "312113", "312311", "332111",
	"314111", "221411", "431111", "111224", "111422", "121124", "121421", "141122", "141221", "112214",
	"112412", "122114", "122411", "142112", "142211", "241211", "221114", "413111", "241112", "134111",
	"111242", "121142", "121241", "114212", "124112", "124211", "411212", "421112", "421211", "212141",
	"214121", "412121", "111143", "111341", "131141", "114113", "114311", "411113", "411311", "113141",
	"114131", "311141", "411131", "211412", "211214", "211232", "2331112",
}

const (
	startB = 104
	stop   = 106
)

// ErrData is returned for empty data or a byte outside printable ASCII.
var ErrData = errors.New("code128: data must be printable ASCII")

// Modules returns the symbol as one bool per module (true = bar), quiet
// zones excluded. Its length is 11 + 11*len(data) + 11 + 13, the same count
// escpos.Code128Modules gives.
func Modules(data string) ([]bool, error) {
	if data == "" {
		return nil, ErrData
	}
	codes := []int{startB}
	sum := startB
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c < 0x20 || c > 0x7E {
			return nil, ErrData
		}
		v := int(c) - 32
		codes = append(codes, v)
		sum += v * (i + 1)
	}
	codes = append(codes, sum%103, stop)

	var out []bool
	for _, code := range codes {
		bar := true
		for _, w := range patterns[code] {
			for n := 0; n < int(w-'0'); n++ {
				out = append(out, bar)
			}
			bar = !bar
		}
	}
	return out, nil
}
