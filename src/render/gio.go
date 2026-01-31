package render

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/font/opentype"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"golang.org/x/image/math/fixed"
)

// GioRenderer renders terminal output using Gio
type GioRenderer struct {
	ops      *op.Ops
	shaper   *text.Shaper
	cellW    int
	cellH    int
	baseline int
	fontSize unit.Sp
	size     image.Point
}

// NewGioRenderer creates a new Gio renderer
func NewGioRenderer(ops *op.Ops, cols, rows int, fontSize unit.Sp) (*GioRenderer, error) {
	// Convert to Gio fonts and register
	collection := []font.FontFace{}

	// Regular
	regularFace, err := opentype.Parse(mustReadFont("JetBrainsMono-Regular.ttf"))
	if err != nil {
		return nil, err
	}
	collection = append(collection, font.FontFace{
		Font: font.Font{Typeface: "JetBrains Mono"},
		Face: regularFace,
	})

	// Bold
	boldFace, err := opentype.Parse(mustReadFont("JetBrainsMono-Bold.ttf"))
	if err != nil {
		return nil, err
	}
	collection = append(collection, font.FontFace{
		Font: font.Font{Typeface: "JetBrains Mono", Weight: font.Bold},
		Face: boldFace,
	})

	// Italic
	italicFace, err := opentype.Parse(mustReadFont("JetBrainsMono-Italic.ttf"))
	if err != nil {
		return nil, err
	}
	collection = append(collection, font.FontFace{
		Font: font.Font{Typeface: "JetBrains Mono", Style: font.Italic},
		Face: italicFace,
	})

	// Bold Italic
	boldItalicFace, err := opentype.Parse(mustReadFont("JetBrainsMono-BoldItalic.ttf"))
	if err != nil {
		return nil, err
	}
	collection = append(collection, font.FontFace{
		Font: font.Font{Typeface: "JetBrains Mono", Weight: font.Bold, Style: font.Italic},
		Face: boldItalicFace,
	})

	shaper := text.NewShaper(text.NoSystemFonts(), text.WithCollection(collection))

	// Calculate cell size (approximate based on font size)
	cellH := int(float32(fontSize) * 1.4)
	cellW := int(float32(fontSize) * 0.6)
	baseline := int(float32(fontSize) * 1.1)

	return &GioRenderer{
		ops:      ops,
		shaper:   shaper,
		cellW:    cellW,
		cellH:    cellH,
		baseline: baseline,
		fontSize: fontSize,
		size:     image.Point{X: cols * cellW, Y: rows * cellH},
	}, nil
}

func mustReadFont(name string) []byte {
	data, err := embeddedFonts.ReadFile("fonts/" + name)
	if err != nil {
		panic(err)
	}
	return data
}

// CreateFontCollection creates a Gio font collection with embedded JetBrains Mono fonts
func CreateFontCollection() []font.FontFace {
	collection := []font.FontFace{}

	// Regular
	regularFace, err := opentype.Parse(mustReadFont("JetBrainsMono-Regular.ttf"))
	if err != nil {
		panic(err)
	}
	collection = append(collection, font.FontFace{
		Font: font.Font{Typeface: "JetBrains Mono"},
		Face: regularFace,
	})

	// Bold
	boldFace, err := opentype.Parse(mustReadFont("JetBrainsMono-Bold.ttf"))
	if err != nil {
		panic(err)
	}
	collection = append(collection, font.FontFace{
		Font: font.Font{Typeface: "JetBrains Mono", Weight: font.Bold},
		Face: boldFace,
	})

	// Italic
	italicFace, err := opentype.Parse(mustReadFont("JetBrainsMono-Italic.ttf"))
	if err != nil {
		panic(err)
	}
	collection = append(collection, font.FontFace{
		Font: font.Font{Typeface: "JetBrains Mono", Style: font.Italic},
		Face: italicFace,
	})

	// Bold Italic
	boldItalicFace, err := opentype.Parse(mustReadFont("JetBrainsMono-BoldItalic.ttf"))
	if err != nil {
		panic(err)
	}
	collection = append(collection, font.FontFace{
		Font: font.Font{Typeface: "JetBrains Mono", Weight: font.Bold, Style: font.Italic},
		Face: boldItalicFace,
	})

	return collection
}

// SetOps updates the ops context for a new frame
func (r *GioRenderer) SetOps(ops *op.Ops) {
	r.ops = ops
}

// Shaper returns the text shaper
func (r *GioRenderer) Shaper() *text.Shaper {
	return r.shaper
}

// FontSize returns the font size
func (r *GioRenderer) FontSize() unit.Sp {
	return r.fontSize
}

// Size returns the pixel dimensions
func (r *GioRenderer) Size() image.Point {
	return r.size
}

// CellSize returns the cell dimensions
func (r *GioRenderer) CellSize() image.Point {
	return image.Point{X: r.cellW, Y: r.cellH}
}

// Clear fills with background color
func (r *GioRenderer) Clear(bg color.NRGBA) {
	rect := clip.Rect{Max: r.size}.Op()
	paint.FillShape(r.ops, bg, rect)
}

// FillRect fills a rectangle
func (r *GioRenderer) FillRect(rect image.Rectangle, c color.NRGBA) {
	clipRect := clip.Rect{Min: rect.Min, Max: rect.Max}.Op()
	paint.FillShape(r.ops, c, clipRect)
}

// DrawGlyph draws a character
func (r *GioRenderer) DrawGlyph(cellX, cellY int, ch rune, fg color.NRGBA, style CellStyle) {
	x := float32(cellX * r.cellW)
	y := float32(cellY*r.cellH + r.baseline)

	// Set up font
	f := font.Font{Typeface: "JetBrains Mono"}
	if style.Bold {
		f.Weight = font.Bold
	}
	if style.Italic {
		f.Style = font.Italic
	}

	// Shape the text
	params := text.Parameters{
		Font:     f,
		PxPerEm:  fixed.I(int(r.fontSize)),
		MaxLines: 1,
	}

	r.shaper.LayoutString(params, string(ch))

	// Position and draw
	stack := op.Offset(image.Pt(int(x), int(y))).Push(r.ops)
	for g, ok := r.shaper.NextGlyph(); ok; g, ok = r.shaper.NextGlyph() {
		path := r.shaper.Shape([]text.Glyph{g})
		outline := clip.Outline{Path: path}.Op().Push(r.ops)
		paint.ColorOp{Color: fg}.Add(r.ops)
		paint.PaintOp{}.Add(r.ops)
		outline.Pop()
	}
	stack.Pop()
}

// DrawCursor draws the cursor
func (r *GioRenderer) DrawCursor(cellX, cellY int, style CursorStyle, fg, bg color.NRGBA) {
	x := cellX * r.cellW
	y := cellY * r.cellH

	var rect image.Rectangle
	switch style {
	case CursorStyleBlock:
		rect = image.Rect(x, y, x+r.cellW, y+r.cellH)
	case CursorStyleUnderline:
		rect = image.Rect(x, y+r.cellH-2, x+r.cellW, y+r.cellH)
	case CursorStyleBar:
		rect = image.Rect(x, y, x+2, y+r.cellH)
	}

	r.FillRect(rect, fg)
}

// DrawUnderline draws an underline
func (r *GioRenderer) DrawUnderline(cellX, cellY int, fg color.NRGBA) {
	x := cellX * r.cellW
	y := cellY*r.cellH + r.cellH - 2
	rect := image.Rect(x, y, x+r.cellW, y+1)
	r.FillRect(rect, fg)
}

// DrawStrikethrough draws a strikethrough
func (r *GioRenderer) DrawStrikethrough(cellX, cellY int, fg color.NRGBA) {
	x := cellX * r.cellW
	y := cellY*r.cellH + r.cellH/2
	rect := image.Rect(x, y, x+r.cellW, y+1)
	r.FillRect(rect, fg)
}
