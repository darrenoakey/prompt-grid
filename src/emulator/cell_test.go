package emulator

import (
	"image/color"
	"testing"
)

func TestDefaultCell(t *testing.T) {
	c := DefaultCell()
	if c.Rune != ' ' {
		t.Errorf("Rune = %q, want ' '", c.Rune)
	}
	if c.FG != DefaultFG {
		t.Errorf("FG = %v, want DefaultFG", c.FG)
	}
	if c.BG != DefaultBG {
		t.Errorf("BG = %v, want DefaultBG", c.BG)
	}
	if c.Attrs != 0 {
		t.Errorf("Attrs = %d, want 0", c.Attrs)
	}
}

func TestIndexedColor(t *testing.T) {
	c := IndexedColor(5)
	if c.Type != ColorIndexed {
		t.Errorf("Type = %v, want ColorIndexed", c.Type)
	}
	if c.Index != 5 {
		t.Errorf("Index = %d, want 5", c.Index)
	}
}

func TestRGBColor(t *testing.T) {
	c := RGBColor(255, 128, 64)
	if c.Type != ColorRGB {
		t.Errorf("Type = %v, want ColorRGB", c.Type)
	}
	if c.R != 255 || c.G != 128 || c.B != 64 {
		t.Errorf("RGB = (%d, %d, %d), want (255, 128, 64)", c.R, c.G, c.B)
	}
}

func TestColorToNRGBA(t *testing.T) {
	defaultColor := color.NRGBA{200, 200, 200, 255}

	tests := []struct {
		name  string
		color Color
		want  color.NRGBA
	}{
		{
			name:  "default",
			color: DefaultFG,
			want:  defaultColor,
		},
		{
			name:  "indexed red (1)",
			color: IndexedColor(1),
			want:  color.NRGBA{205, 0, 0, 255},
		},
		{
			name:  "RGB",
			color: RGBColor(100, 150, 200),
			want:  color.NRGBA{100, 150, 200, 255},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.color.ToNRGBA(defaultColor)
			if got != tt.want {
				t.Errorf("ToNRGBA() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPalette256(t *testing.T) {
	// Test some known values
	tests := []struct {
		index uint8
		r, g, b uint8
	}{
		{0, 0, 0, 0},         // Black
		{1, 205, 0, 0},       // Red
		{15, 255, 255, 255},  // Bright white
		{232, 8, 8, 8},       // First grayscale
		{255, 238, 238, 238}, // Last grayscale
	}

	for _, tt := range tests {
		c := palette256[tt.index]
		if c.R != tt.r || c.G != tt.g || c.B != tt.b {
			t.Errorf("palette256[%d] = (%d, %d, %d), want (%d, %d, %d)",
				tt.index, c.R, c.G, c.B, tt.r, tt.g, tt.b)
		}
	}
}

func TestCellClone(t *testing.T) {
	c := Cell{
		Rune:  'A',
		FG:    IndexedColor(1),
		BG:    RGBColor(0, 0, 255),
		Attrs: AttrBold | AttrItalic,
	}

	clone := c.Clone()
	if clone != c {
		t.Error("Clone() != original")
	}
}

func TestAttrFlags(t *testing.T) {
	var attrs AttrFlags

	attrs |= AttrBold
	if attrs&AttrBold == 0 {
		t.Error("AttrBold not set")
	}

	attrs |= AttrItalic
	if attrs&AttrItalic == 0 {
		t.Error("AttrItalic not set")
	}

	attrs &^= AttrBold
	if attrs&AttrBold != 0 {
		t.Error("AttrBold should be cleared")
	}
	if attrs&AttrItalic == 0 {
		t.Error("AttrItalic should still be set")
	}
}
