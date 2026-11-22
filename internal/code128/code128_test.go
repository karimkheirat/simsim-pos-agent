package code128

import (
	"testing"

	"github.com/karimkheirat/simsim-pos-agent/internal/escpos"
)

func TestModules_WidthMatchesESCPOS(t *testing.T) {
	for _, s := range []string{"A", "TC-S0417-P1-2026-0312", "TR-S0001-P12-2026-1234"} {
		m, err := Modules(s)
		if err != nil {
			t.Fatal(err)
		}
		if len(m) != escpos.Code128Modules(s) {
			t.Errorf("%q: %d modules, escpos says %d", s, len(m), escpos.Code128Modules(s))
		}
		if !m[0] || !m[len(m)-1] {
			t.Errorf("%q: a symbol starts and ends on a bar", s)
		}
	}
}

// "PJJ123C" in set B: 104 + 48·1 + 42·2 + 42·3 + 17·4 + 18·5 + 19·6 + 35·7
// = 879, and 879 mod 103 = 55.
func TestModules_CheckCharacter(t *testing.T) {
	m, err := Modules("PJJ123C")
	if err != nil {
		t.Fatal(err)
	}
	// The check symbol sits before the 13-module stop.
	got := m[len(m)-13-11 : len(m)-13]
	var want []bool
	bar := true
	for _, w := range patterns[55] {
		for n := 0; n < int(w-'0'); n++ {
			want = append(want, bar)
		}
		bar = !bar
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("check symbol is not 55")
		}
	}
}

func TestModules_RefusesNonASCII(t *testing.T) {
	if _, err := Modules("café"); err == nil {
		t.Fatal("want an error")
	}
	if _, err := Modules(""); err == nil {
		t.Fatal("want an error")
	}
}
