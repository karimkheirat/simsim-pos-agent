package escpos

import (
	"bytes"
	"testing"
)

func TestInit(t *testing.T) {
	got := Init()
	want := []byte{0x1B, 0x40}
	if !bytes.Equal(got, want) {
		t.Errorf("Init() = % X, want % X", got, want)
	}
}

func TestCodepage(t *testing.T) {
	tests := []struct {
		name string
		n    byte
		want []byte
	}{
		{"CP858", CP858, []byte{0x1B, 0x74, 19}},
		{"zero", 0x00, []byte{0x1B, 0x74, 0x00}},
		{"max", 0xFF, []byte{0x1B, 0x74, 0xFF}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Codepage(tt.n)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("Codepage(%#x) = % X, want % X", tt.n, got, tt.want)
			}
		})
	}
}

func TestBold(t *testing.T) {
	tests := []struct {
		name string
		on   bool
		want []byte
	}{
		{"on", true, []byte{0x1B, 0x45, 0x01}},
		{"off", false, []byte{0x1B, 0x45, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Bold(tt.on)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("Bold(%v) = % X, want % X", tt.on, got, tt.want)
			}
		})
	}
}

func TestDoubleSize(t *testing.T) {
	tests := []struct {
		name string
		on   bool
		want []byte
	}{
		{"on", true, []byte{0x1D, 0x21, 0x11}},
		{"off", false, []byte{0x1D, 0x21, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DoubleSize(tt.on)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("DoubleSize(%v) = % X, want % X", tt.on, got, tt.want)
			}
		})
	}
}

func TestDoubleHeight(t *testing.T) {
	tests := []struct {
		name string
		on   bool
		want []byte
	}{
		{"on", true, []byte{0x1D, 0x21, 0x01}},
		{"off", false, []byte{0x1D, 0x21, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DoubleHeight(tt.on)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("DoubleHeight(%v) = % X, want % X", tt.on, got, tt.want)
			}
		})
	}
}

func TestAlign(t *testing.T) {
	tests := []struct {
		name string
		a    Alignment
		want []byte
	}{
		{"Left", Left, []byte{0x1B, 0x61, 0x00}},
		{"Center", Center, []byte{0x1B, 0x61, 0x01}},
		{"Right", Right, []byte{0x1B, 0x61, 0x02}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Align(tt.a)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("Align(%v) = % X, want % X", tt.a, got, tt.want)
			}
		})
	}
}

func TestCutFull(t *testing.T) {
	got := CutFull()
	want := []byte{0x1D, 0x56, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("CutFull() = % X, want % X", got, want)
	}
}

func TestCutPartial(t *testing.T) {
	got := CutPartial()
	want := []byte{0x1D, 0x56, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("CutPartial() = % X, want % X", got, want)
	}
}

func TestDrawerKick(t *testing.T) {
	got := DrawerKick()
	want := []byte{0x1B, 0x70, 0x00, 0x32, 0xFA}
	if !bytes.Equal(got, want) {
		t.Errorf("DrawerKick() = % X, want % X", got, want)
	}
}

func TestAlignmentConstants(t *testing.T) {
	if Left != 0 || Center != 1 || Right != 2 {
		t.Errorf("Alignment constants = (%d, %d, %d), want (0, 1, 2)", Left, Center, Right)
	}
}

func TestCP858Constant(t *testing.T) {
	if CP858 != 19 {
		t.Errorf("CP858 = %d, want 19", CP858)
	}
}

func TestNew_Empty(t *testing.T) {
	if got := New().Bytes(); len(got) != 0 {
		t.Errorf("New().Bytes() = % X, want empty slice", got)
	}
}

func TestBuilder_Write(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"nil", nil, nil},
		{"empty", []byte{}, nil},
		{"single byte", []byte{0x41}, []byte{0x41}},
		{"multi byte", []byte("Hello"), []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F}},
		{"binary bytes", []byte{0x00, 0xFF, 0x7F}, []byte{0x00, 0xFF, 0x7F}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New().Write(tt.in).Bytes()
			if !bytes.Equal(got, tt.want) {
				t.Errorf("Write(% X).Bytes() = % X, want % X", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuilder_Text(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []byte
	}{
		{"empty", "", nil},
		{"single char", "A", []byte{0x41}},
		{"multi char", "Hello", []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F}},
		{"with newline", "line\n", []byte{0x6C, 0x69, 0x6E, 0x65, 0x0A}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New().Text(tt.in).Bytes()
			if !bytes.Equal(got, tt.want) {
				t.Errorf("Text(%q).Bytes() = % X, want % X", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuilder_WriteReturnsReceiver(t *testing.T) {
	b := New()
	if b.Write([]byte{0x01}) != b {
		t.Error("Write() did not return receiver")
	}
	if b.Text("x") != b {
		t.Error("Text() did not return receiver")
	}
}

func TestBuilder_Chained(t *testing.T) {
	got := New().
		Init().
		Codepage(CP858).
		Align(Center).
		Bold(true).
		Text("HEADER").
		Bold(false).
		Text("\n").
		DoubleSize(true).
		Text("BIG").
		DoubleSize(false).
		Text("\n").
		Align(Left).
		Text("body line").
		Text("\n").
		CutPartial().
		CutFull().
		DrawerKick().
		Bytes()

	var want []byte
	want = append(want, Init()...)
	want = append(want, Codepage(CP858)...)
	want = append(want, Align(Center)...)
	want = append(want, Bold(true)...)
	want = append(want, []byte("HEADER")...)
	want = append(want, Bold(false)...)
	want = append(want, []byte("\n")...)
	want = append(want, DoubleSize(true)...)
	want = append(want, []byte("BIG")...)
	want = append(want, DoubleSize(false)...)
	want = append(want, []byte("\n")...)
	want = append(want, Align(Left)...)
	want = append(want, []byte("body line")...)
	want = append(want, []byte("\n")...)
	want = append(want, CutPartial()...)
	want = append(want, CutFull()...)
	want = append(want, DrawerKick()...)

	if !bytes.Equal(got, want) {
		t.Errorf("chained Bytes() mismatch:\n got: % X\nwant: % X", got, want)
	}
}

func TestBuilder_IndependentInstances(t *testing.T) {
	a := New().Init().Bytes()
	b := New().CutFull().Bytes()
	if !bytes.Equal(a, []byte{0x1B, 0x40}) {
		t.Errorf("builder a = % X, want 1B 40", a)
	}
	if !bytes.Equal(b, []byte{0x1D, 0x56, 0x00}) {
		t.Errorf("builder b = % X, want 1D 56 00", b)
	}
}
