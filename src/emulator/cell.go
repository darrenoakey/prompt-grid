package emulator

import "image/color"

// AttrFlags represents text attributes
type AttrFlags uint8

const (
	AttrBold AttrFlags = 1 << iota
	AttrDim
	AttrItalic
	AttrUnderline
	AttrBlink
	AttrReverse
	AttrHidden
	AttrStrikethrough
)

// ColorType indicates how a color should be interpreted
type ColorType uint8

const (
	ColorDefault ColorType = iota // Use terminal default
	ColorIndexed                  // Use 256-color palette index
	ColorRGB                      // Use direct RGB
)

// Color represents a terminal color
type Color struct {
	Type  ColorType
	Index uint8 // For indexed colors (0-255)
	R, G, B uint8 // For RGB colors
}

// DefaultFG is the default foreground color
var DefaultFG = Color{Type: ColorDefault}

// DefaultBG is the default background color
var DefaultBG = Color{Type: ColorDefault}

// IndexedColor creates a 256-color palette color
func IndexedColor(index uint8) Color {
	return Color{Type: ColorIndexed, Index: index}
}

// RGBColor creates a direct RGB color
func RGBColor(r, g, b uint8) Color {
	return Color{Type: ColorRGB, R: r, G: g, B: b}
}

// ToNRGBA converts a Color to a color.NRGBA
func (c Color) ToNRGBA(defaultColor color.NRGBA) color.NRGBA {
	switch c.Type {
	case ColorDefault:
		return defaultColor
	case ColorIndexed:
		return palette256[c.Index]
	case ColorRGB:
		return color.NRGBA{R: c.R, G: c.G, B: c.B, A: 255}
	default:
		return defaultColor
	}
}

// Cell represents a single character cell in the terminal
type Cell struct {
	Rune  rune
	FG    Color
	BG    Color
	Attrs AttrFlags
}

// DefaultCell returns an empty cell with default attributes
func DefaultCell() Cell {
	return Cell{
		Rune:  ' ',
		FG:    DefaultFG,
		BG:    DefaultBG,
		Attrs: 0,
	}
}

// Clone returns a copy of the cell
func (c Cell) Clone() Cell {
	return c
}

// palette256 is the xterm 256-color palette
var palette256 = [256]color.NRGBA{
	// Standard colors (0-15)
	{0, 0, 0, 255},       // 0: Black
	{205, 0, 0, 255},     // 1: Red
	{0, 205, 0, 255},     // 2: Green
	{205, 205, 0, 255},   // 3: Yellow
	{0, 0, 238, 255},     // 4: Blue
	{205, 0, 205, 255},   // 5: Magenta
	{0, 205, 205, 255},   // 6: Cyan
	{229, 229, 229, 255}, // 7: White
	{127, 127, 127, 255}, // 8: Bright Black
	{255, 0, 0, 255},     // 9: Bright Red
	{0, 255, 0, 255},     // 10: Bright Green
	{255, 255, 0, 255},   // 11: Bright Yellow
	{92, 92, 255, 255},   // 12: Bright Blue
	{255, 0, 255, 255},   // 13: Bright Magenta
	{0, 255, 255, 255},   // 14: Bright Cyan
	{255, 255, 255, 255}, // 15: Bright White
}

func init() {
	// 216 color cube (16-231)
	levels := []uint8{0, 95, 135, 175, 215, 255}
	for r := 0; r < 6; r++ {
		for g := 0; g < 6; g++ {
			for b := 0; b < 6; b++ {
				index := 16 + r*36 + g*6 + b
				palette256[index] = color.NRGBA{
					R: levels[r],
					G: levels[g],
					B: levels[b],
					A: 255,
				}
			}
		}
	}

	// Grayscale (232-255)
	for i := 0; i < 24; i++ {
		level := uint8(8 + i*10)
		palette256[232+i] = color.NRGBA{
			R: level,
			G: level,
			B: level,
			A: 255,
		}
	}
}
