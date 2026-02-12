package render

import (
	"image/color"
	"math"
	"math/rand"
)

// SessionColor holds colors for a terminal session
type SessionColor struct {
	Background color.NRGBA
	Foreground color.NRGBA
	Cursor     color.NRGBA
}

// Luminance computes the perceptual luminance of a color (0-1 range)
func Luminance(c color.NRGBA) float64 {
	return 0.2126*float64(c.R)/255 + 0.7152*float64(c.G)/255 + 0.0722*float64(c.B)/255
}

// AdjustForContrast adjusts a foreground color to ensure readability against a background.
// If the luminance contrast is insufficient (< 0.5), shifts the color toward readable range.
// Matches terminal_util's ANSI color adjustment algorithm.
func AdjustForContrast(fg, bg color.NRGBA) color.NRGBA {
	fgLum := Luminance(fg)
	bgLum := Luminance(bg)

	if math.Abs(fgLum-bgLum) >= 0.5 {
		return fg // Already has sufficient contrast
	}

	// Shift color channels by ~0.3 (77/255) to improve contrast
	if bgLum < 0.5 {
		// Dark background: lighten the foreground
		return color.NRGBA{
			R: clampAdd(fg.R, 77),
			G: clampAdd(fg.G, 77),
			B: clampAdd(fg.B, 77),
			A: fg.A,
		}
	}
	// Light background: darken the foreground
	return color.NRGBA{
		R: clampSub(fg.R, 77),
		G: clampSub(fg.G, 77),
		B: clampSub(fg.B, 77),
		A: fg.A,
	}
}

func clampAdd(v, delta uint8) uint8 {
	sum := int(v) + int(delta)
	if sum > 255 {
		return 255
	}
	return uint8(sum)
}

func clampSub(v, delta uint8) uint8 {
	diff := int(v) - int(delta)
	if diff < 0 {
		return 0
	}
	return uint8(diff)
}

// sessionPalette contains 128 pre-generated session colors
var sessionPalette []SessionColor

func init() {
	sessionPalette = generateSessionPalette(128)
}

// generateSessionPalette creates n evenly distributed colors using HSV
func generateSessionPalette(n int) []SessionColor {
	palette := make([]SessionColor, n)
	saturation := 0.4 // Muted saturation like terminal_util

	for i := 0; i < n; i++ {
		// Evenly spaced hue across the spectrum
		hue := float64(i) / float64(n)

		// Alternate brightness: light for even, dark for odd
		var value float64
		if i%2 == 0 {
			value = 0.85 // Light background
		} else {
			value = 0.25 // Dark background
		}

		// Convert HSV to RGB
		r, g, b := hsvToRGB(hue, saturation, value)

		bg := color.NRGBA{
			R: uint8(r * 255),
			G: uint8(g * 255),
			B: uint8(b * 255),
			A: 255,
		}

		// Text color: pure white for dark backgrounds, pure black for light
		// (matches terminal_util: NSColor.whiteColor() / NSColor.blackColor())
		var fg color.NRGBA
		if value < 0.5 {
			fg = color.NRGBA{R: 255, G: 255, B: 255, A: 255} // White for dark bg
		} else {
			fg = color.NRGBA{R: 0, G: 0, B: 0, A: 255} // Black for light bg
		}

		// Cursor is the same as foreground
		palette[i] = SessionColor{
			Background: bg,
			Foreground: fg,
			Cursor:     fg,
		}
	}

	return palette
}

// hsvToRGB converts HSV (0-1 range) to RGB (0-1 range)
func hsvToRGB(h, s, v float64) (r, g, b float64) {
	if s == 0 {
		return v, v, v
	}

	h = h * 6
	if h >= 6 {
		h = 0
	}

	i := math.Floor(h)
	f := h - i
	p := v * (1 - s)
	q := v * (1 - s*f)
	t := v * (1 - s*(1-f))

	switch int(i) {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}

	return
}

// RandomSessionColor returns a random session color from the palette
func RandomSessionColor() SessionColor {
	return sessionPalette[rand.Intn(len(sessionPalette))]
}

// RandomSessionColorIndex returns a random palette index
func RandomSessionColorIndex() int {
	return rand.Intn(len(sessionPalette))
}

// GetSessionColor returns a specific session color by index
func GetSessionColor(index int) SessionColor {
	return sessionPalette[index%len(sessionPalette)]
}

// SessionColorCount returns the number of session colors in the palette
func SessionColorCount() int {
	return len(sessionPalette)
}
