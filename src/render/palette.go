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

		// Text color: white for dark backgrounds, black for light
		var fg color.NRGBA
		if value < 0.5 {
			fg = color.NRGBA{R: 220, G: 220, B: 220, A: 255} // Light gray for dark bg
		} else {
			fg = color.NRGBA{R: 30, G: 30, B: 30, A: 255} // Dark for light bg
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

// GetSessionColor returns a specific session color by index
func GetSessionColor(index int) SessionColor {
	return sessionPalette[index%len(sessionPalette)]
}

// SessionColorCount returns the number of session colors in the palette
func SessionColorCount() int {
	return len(sessionPalette)
}
