package render

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	"claude-term/src/emulator"
)

func TestCellStyleFromAttrs(t *testing.T) {
	tests := []struct {
		name  string
		attrs emulator.AttrFlags
		want  CellStyle
	}{
		{"none", 0, CellStyle{}},
		{"bold", emulator.AttrBold, CellStyle{Bold: true}},
		{"italic", emulator.AttrItalic, CellStyle{Italic: true}},
		{"underline", emulator.AttrUnderline, CellStyle{Underline: true}},
		{"combined", emulator.AttrBold | emulator.AttrItalic, CellStyle{Bold: true, Italic: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CellStyleFromAttrs(tt.attrs)
			if got != tt.want {
				t.Errorf("CellStyleFromAttrs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLuminance(t *testing.T) {
	black := color.NRGBA{R: 0, G: 0, B: 0, A: 255}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	if l := Luminance(black); l != 0 {
		t.Errorf("Luminance(black) = %f, want 0", l)
	}
	if l := Luminance(white); l < 0.99 || l > 1.01 {
		t.Errorf("Luminance(white) = %f, want ~1.0", l)
	}
}

func TestAdjustForContrast(t *testing.T) {
	lightBg := color.NRGBA{R: 200, G: 220, B: 150, A: 255} // Light green
	darkBg := color.NRGBA{R: 30, G: 40, B: 20, A: 255}     // Dark green

	// White on light bg should be darkened (insufficient contrast)
	white := color.NRGBA{R: 229, G: 229, B: 229, A: 255}
	adjusted := AdjustForContrast(white, lightBg)
	if adjusted.R >= white.R {
		t.Errorf("White on light bg should be darkened: got R=%d, was R=%d", adjusted.R, white.R)
	}

	// Black on light bg should stay (sufficient contrast)
	black := color.NRGBA{R: 0, G: 0, B: 0, A: 255}
	adjusted = AdjustForContrast(black, lightBg)
	if adjusted != black {
		t.Errorf("Black on light bg should not change: got %v, want %v", adjusted, black)
	}

	// Black on dark bg should be lightened (insufficient contrast)
	adjusted = AdjustForContrast(black, darkBg)
	if adjusted.R <= black.R {
		t.Errorf("Black on dark bg should be lightened: got R=%d, was R=%d", adjusted.R, black.R)
	}

	// White on dark bg should stay (sufficient contrast)
	adjusted = AdjustForContrast(white, darkBg)
	if adjusted != white {
		t.Errorf("White on dark bg should not change: got %v, want %v", adjusted, white)
	}
}

func TestSessionColorDefaults(t *testing.T) {
	// Check that light background sessions get pure black foreground
	// and dark background sessions get pure white foreground
	for i := 0; i < SessionColorCount(); i++ {
		sc := GetSessionColor(i)
		bgLum := Luminance(sc.Background)
		if bgLum >= 0.5 {
			// Light background: foreground should be pure black
			if sc.Foreground.R != 0 || sc.Foreground.G != 0 || sc.Foreground.B != 0 {
				t.Errorf("Session %d (light bg): foreground = {%d,%d,%d}, want {0,0,0}",
					i, sc.Foreground.R, sc.Foreground.G, sc.Foreground.B)
			}
		} else {
			// Dark background: foreground should be pure white
			if sc.Foreground.R != 255 || sc.Foreground.G != 255 || sc.Foreground.B != 255 {
				t.Errorf("Session %d (dark bg): foreground = {%d,%d,%d}, want {255,255,255}",
					i, sc.Foreground.R, sc.Foreground.G, sc.Foreground.B)
			}
		}
	}
}

func TestDefaultColorScheme(t *testing.T) {
	colors := DefaultColorScheme()

	if colors.Foreground.A != 255 {
		t.Error("Foreground should be opaque")
	}
	if colors.Background.A != 255 {
		t.Error("Background should be opaque")
	}
	if colors.Cursor.A != 255 {
		t.Error("Cursor should be opaque")
	}
}

func TestLoadFonts(t *testing.T) {
	fonts, err := LoadFonts()
	if err != nil {
		t.Fatalf("LoadFonts() error = %v", err)
	}

	if fonts.Regular == nil {
		t.Error("Regular font not loaded")
	}
	if fonts.Bold == nil {
		t.Error("Bold font not loaded")
	}
	if fonts.Italic == nil {
		t.Error("Italic font not loaded")
	}
	if fonts.BoldItalic == nil {
		t.Error("BoldItalic font not loaded")
	}
}

func TestNewImageRenderer(t *testing.T) {
	r, err := NewImageRenderer(80, 24, 14)
	if err != nil {
		t.Fatalf("NewImageRenderer() error = %v", err)
	}

	size := r.Size()
	if size.X <= 0 || size.Y <= 0 {
		t.Errorf("Size() = %v, expected positive dimensions", size)
	}

	cellSize := r.CellSize()
	if cellSize.X <= 0 || cellSize.Y <= 0 {
		t.Errorf("CellSize() = %v, expected positive dimensions", cellSize)
	}
}

func TestImageRendererClear(t *testing.T) {
	r, err := NewImageRenderer(10, 5, 14)
	if err != nil {
		t.Fatalf("NewImageRenderer() error = %v", err)
	}

	bg := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	r.Clear(bg)

	img := r.Image()
	// Check a few pixels
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			gotR, gotG, gotB, gotA := img.At(x, y).RGBA()
			wantR, wantG, wantB, wantA := bg.RGBA()
			if gotR != wantR || gotG != wantG || gotB != wantB || gotA != wantA {
				t.Errorf("Pixel at (%d,%d) = RGBA(%d,%d,%d,%d), want RGBA(%d,%d,%d,%d)",
					x, y, gotR, gotG, gotB, gotA, wantR, wantG, wantB, wantA)
			}
		}
	}
}

func TestImageRendererFillRect(t *testing.T) {
	r, err := NewImageRenderer(10, 5, 14)
	if err != nil {
		t.Fatalf("NewImageRenderer() error = %v", err)
	}

	r.Clear(color.NRGBA{A: 255})

	red := color.NRGBA{R: 255, A: 255}
	rect := image.Rect(5, 5, 15, 15)
	r.FillRect(rect, red)

	img := r.Image()
	// Check inside rect
	gotR, gotG, gotB, gotA := img.At(10, 10).RGBA()
	wantR, wantG, wantB, wantA := red.RGBA()
	if gotR != wantR || gotG != wantG || gotB != wantB || gotA != wantA {
		t.Errorf("Inside rect = RGBA(%d,%d,%d,%d), want RGBA(%d,%d,%d,%d)",
			gotR, gotG, gotB, gotA, wantR, wantG, wantB, wantA)
	}
}

func TestImageRendererDrawGlyph(t *testing.T) {
	r, err := NewImageRenderer(10, 5, 14)
	if err != nil {
		t.Fatalf("NewImageRenderer() error = %v", err)
	}

	bg := color.NRGBA{A: 255}
	r.Clear(bg)

	fg := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	r.DrawGlyph(0, 0, 'A', fg, CellStyle{})

	// Just verify no panic and something was drawn
	// (checking specific pixels is fragile due to font rendering)
	img := r.Image()
	hasNonBg := false
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if img.At(x, y) != bg {
				hasNonBg = true
				break
			}
		}
	}
	if !hasNonBg {
		t.Error("Expected glyph to draw some pixels")
	}
}

func TestImageRendererWritePNG(t *testing.T) {
	r, err := NewImageRenderer(10, 5, 14)
	if err != nil {
		t.Fatalf("NewImageRenderer() error = %v", err)
	}

	r.Clear(color.NRGBA{R: 100, G: 150, B: 200, A: 255})

	var buf bytes.Buffer
	err = r.WritePNG(&buf)
	if err != nil {
		t.Errorf("WritePNG() error = %v", err)
	}

	// Check PNG magic bytes
	data := buf.Bytes()
	if len(data) < 8 {
		t.Fatal("PNG output too short")
	}
	pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	for i, b := range pngMagic {
		if data[i] != b {
			t.Errorf("PNG magic byte %d = %x, want %x", i, data[i], b)
		}
	}
}

func TestRenderScreen(t *testing.T) {
	screen := emulator.NewScreen(10, 5)

	// Write some content
	screen.Write('H')
	screen.Write('i')

	// Create renderer
	r, err := NewImageRenderer(10, 5, 14)
	if err != nil {
		t.Fatalf("NewImageRenderer() error = %v", err)
	}

	colors := DefaultColorScheme()

	// Should not panic
	RenderScreen(r, screen, colors)

	// Verify image was created
	img := r.Image()
	if img == nil {
		t.Error("Image should not be nil")
	}
}

func TestRenderScreenWithColors(t *testing.T) {
	screen := emulator.NewScreen(10, 5)

	// Set red foreground
	attrs := emulator.DefaultCell()
	attrs.FG = emulator.IndexedColor(1) // Red
	screen.SetAttrs(attrs)
	screen.Write('R')

	r, err := NewImageRenderer(10, 5, 14)
	if err != nil {
		t.Fatalf("NewImageRenderer() error = %v", err)
	}

	colors := DefaultColorScheme()
	RenderScreen(r, screen, colors)

	// Just verify no panic - color verification is visual
}

func TestRenderScreenWithCursor(t *testing.T) {
	screen := emulator.NewScreen(10, 5)

	// Cursor should be visible by default at 0,0
	r, err := NewImageRenderer(10, 5, 14)
	if err != nil {
		t.Fatalf("NewImageRenderer() error = %v", err)
	}

	colors := DefaultColorScheme()
	RenderScreen(r, screen, colors)

	// Check that something was drawn at cursor position
	img := r.Image()
	cellSize := r.CellSize()

	// The cursor block should have the cursor color
	cursorPixel := img.At(cellSize.X/2, cellSize.Y/2)
	if cursorPixel == colors.Background {
		t.Error("Cursor should be visible")
	}
}
