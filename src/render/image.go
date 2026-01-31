package render

import (
	"embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/*.ttf
var embeddedFonts embed.FS

// FontSet holds the loaded fonts
type FontSet struct {
	Regular    *opentype.Font
	Bold       *opentype.Font
	Italic     *opentype.Font
	BoldItalic *opentype.Font
}

var (
	defaultFonts *FontSet
	fontsOnce    sync.Once
	fontsErr     error
)

// LoadFonts loads the embedded fonts
func LoadFonts() (*FontSet, error) {
	fontsOnce.Do(func() {
		fs := &FontSet{}

		loadFont := func(name string) (*opentype.Font, error) {
			data, err := embeddedFonts.ReadFile("fonts/" + name)
			if err != nil {
				return nil, err
			}
			return opentype.Parse(data)
		}

		fs.Regular, fontsErr = loadFont("JetBrainsMono-Regular.ttf")
		if fontsErr != nil {
			return
		}
		fs.Bold, fontsErr = loadFont("JetBrainsMono-Bold.ttf")
		if fontsErr != nil {
			return
		}
		fs.Italic, fontsErr = loadFont("JetBrainsMono-Italic.ttf")
		if fontsErr != nil {
			return
		}
		fs.BoldItalic, fontsErr = loadFont("JetBrainsMono-BoldItalic.ttf")
		if fontsErr != nil {
			return
		}

		defaultFonts = fs
	})

	return defaultFonts, fontsErr
}

// ImageRenderer renders terminal output to an image
type ImageRenderer struct {
	img      *image.RGBA
	cellW    int
	cellH    int
	baseline int
	fonts    *FontSet
	faces    map[CellStyle]font.Face
	fontSize float64
}

// NewImageRenderer creates a new image renderer
func NewImageRenderer(cols, rows int, fontSize float64) (*ImageRenderer, error) {
	fonts, err := LoadFonts()
	if err != nil {
		return nil, err
	}

	// Create a face to measure cell size
	opts := &opentype.FaceOptions{
		Size:    fontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	}

	regularFace, err := opentype.NewFace(fonts.Regular, opts)
	if err != nil {
		return nil, err
	}

	// Calculate cell size from font metrics
	metrics := regularFace.Metrics()
	cellH := (metrics.Height + 63) / 64 // Round up
	advance, _ := regularFace.GlyphAdvance('M')
	cellW := (advance + 63) / 64

	// Calculate baseline offset within cell
	ascent := (metrics.Ascent + 63) / 64
	baseline := int(ascent)

	r := &ImageRenderer{
		cellW:    int(cellW),
		cellH:    int(cellH),
		baseline: baseline,
		fonts:    fonts,
		faces:    make(map[CellStyle]font.Face),
		fontSize: fontSize,
	}

	// Pre-create faces for common styles
	r.faces[CellStyle{}] = regularFace

	// Create image
	width := cols * r.cellW
	height := rows * r.cellH
	r.img = image.NewRGBA(image.Rect(0, 0, width, height))

	return r, nil
}

func (r *ImageRenderer) getFace(style CellStyle) font.Face {
	if face, ok := r.faces[style]; ok {
		return face
	}

	var f *opentype.Font
	switch {
	case style.Bold && style.Italic:
		f = r.fonts.BoldItalic
	case style.Bold:
		f = r.fonts.Bold
	case style.Italic:
		f = r.fonts.Italic
	default:
		f = r.fonts.Regular
	}

	opts := &opentype.FaceOptions{
		Size:    r.fontSize,
		DPI:     72,
		Hinting: font.HintingFull,
	}

	face, err := opentype.NewFace(f, opts)
	if err != nil {
		return r.faces[CellStyle{}]
	}

	r.faces[style] = face
	return face
}

// Size returns the image dimensions
func (r *ImageRenderer) Size() image.Point {
	return r.img.Bounds().Size()
}

// CellSize returns the cell dimensions
func (r *ImageRenderer) CellSize() image.Point {
	return image.Point{X: r.cellW, Y: r.cellH}
}

// Clear fills the image with a background color
func (r *ImageRenderer) Clear(bg color.NRGBA) {
	draw.Draw(r.img, r.img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
}

// FillRect fills a rectangle
func (r *ImageRenderer) FillRect(rect image.Rectangle, c color.NRGBA) {
	draw.Draw(r.img, rect, image.NewUniform(c), image.Point{}, draw.Src)
}

// DrawGlyph draws a character at the given cell position
func (r *ImageRenderer) DrawGlyph(cellX, cellY int, ch rune, fg color.NRGBA, style CellStyle) {
	face := r.getFace(style)

	x := cellX * r.cellW
	y := cellY*r.cellH + r.baseline

	d := &font.Drawer{
		Dst:  r.img,
		Src:  image.NewUniform(fg),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(string(ch))
}

// DrawCursor draws the cursor
func (r *ImageRenderer) DrawCursor(cellX, cellY int, style CursorStyle, fg, bg color.NRGBA) {
	x := cellX * r.cellW
	y := cellY * r.cellH

	switch style {
	case CursorStyleBlock:
		rect := image.Rect(x, y, x+r.cellW, y+r.cellH)
		r.FillRect(rect, fg)
	case CursorStyleUnderline:
		rect := image.Rect(x, y+r.cellH-2, x+r.cellW, y+r.cellH)
		r.FillRect(rect, fg)
	case CursorStyleBar:
		rect := image.Rect(x, y, x+2, y+r.cellH)
		r.FillRect(rect, fg)
	}
}

// DrawUnderline draws an underline
func (r *ImageRenderer) DrawUnderline(cellX, cellY int, fg color.NRGBA) {
	x := cellX * r.cellW
	y := cellY*r.cellH + r.cellH - 2
	rect := image.Rect(x, y, x+r.cellW, y+1)
	r.FillRect(rect, fg)
}

// DrawStrikethrough draws a strikethrough
func (r *ImageRenderer) DrawStrikethrough(cellX, cellY int, fg color.NRGBA) {
	x := cellX * r.cellW
	y := cellY*r.cellH + r.cellH/2
	rect := image.Rect(x, y, x+r.cellW, y+1)
	r.FillRect(rect, fg)
}

// Image returns the underlying image
func (r *ImageRenderer) Image() *image.RGBA {
	return r.img
}

// WritePNG writes the image as PNG to a writer
func (r *ImageRenderer) WritePNG(w io.Writer) error {
	return png.Encode(w, r.img)
}
