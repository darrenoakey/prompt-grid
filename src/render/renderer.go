package render

import (
	"image"
	"image/color"

	"claude-term/src/emulator"
)

// CellStyle represents text rendering style
type CellStyle struct {
	Bold          bool
	Italic        bool
	Underline     bool
	Strikethrough bool
}

// CellStyleFromAttrs converts emulator attributes to CellStyle
func CellStyleFromAttrs(attrs emulator.AttrFlags) CellStyle {
	return CellStyle{
		Bold:          attrs&emulator.AttrBold != 0,
		Italic:        attrs&emulator.AttrItalic != 0,
		Underline:     attrs&emulator.AttrUnderline != 0,
		Strikethrough: attrs&emulator.AttrStrikethrough != 0,
	}
}

// CursorStyle represents cursor appearance
type CursorStyle int

const (
	CursorStyleBlock CursorStyle = iota
	CursorStyleUnderline
	CursorStyleBar
)

// Renderer is the interface for rendering terminal output
type Renderer interface {
	// Size returns the pixel dimensions
	Size() image.Point

	// CellSize returns the dimensions of a single cell
	CellSize() image.Point

	// Clear fills the canvas with a background color
	Clear(bg color.NRGBA)

	// FillRect fills a rectangle with a color
	FillRect(r image.Rectangle, c color.NRGBA)

	// DrawGlyph draws a single character at the given cell position
	DrawGlyph(cellX, cellY int, r rune, fg color.NRGBA, style CellStyle)

	// DrawCursor draws the cursor at the given cell position
	DrawCursor(cellX, cellY int, style CursorStyle, fg, bg color.NRGBA)

	// DrawUnderline draws an underline at the given cell position
	DrawUnderline(cellX, cellY int, fg color.NRGBA)

	// DrawStrikethrough draws a strikethrough at the given cell position
	DrawStrikethrough(cellX, cellY int, fg color.NRGBA)
}

// DefaultColors contains default terminal colors
type DefaultColors struct {
	Foreground color.NRGBA
	Background color.NRGBA
	Cursor     color.NRGBA
}

// DefaultColorScheme returns a dark terminal color scheme
func DefaultColorScheme() DefaultColors {
	return DefaultColors{
		Foreground: color.NRGBA{R: 204, G: 204, B: 204, A: 255},
		Background: color.NRGBA{R: 30, G: 30, B: 30, A: 255},
		Cursor:     color.NRGBA{R: 255, G: 255, B: 255, A: 255},
	}
}

// RenderScreen renders a screen to a renderer
func RenderScreen(r Renderer, screen *emulator.Screen, colors DefaultColors) {
	cols, rows := screen.Size()
	cellSize := r.CellSize()

	// Clear background
	r.Clear(colors.Background)

	// Draw cells
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			cell := screen.Cell(x, y)
			if cell.Rune == 0 || cell.Rune == ' ' {
				// Skip empty cells unless they have a background
				if cell.BG.Type == emulator.ColorDefault {
					continue
				}
			}

			// Draw background if not default
			if cell.BG.Type != emulator.ColorDefault {
				bg := cell.BG.ToNRGBA(colors.Background)
				rect := image.Rect(
					x*cellSize.X, y*cellSize.Y,
					(x+1)*cellSize.X, (y+1)*cellSize.Y,
				)
				r.FillRect(rect, bg)
			}

			// Skip drawing space character
			if cell.Rune == 0 || cell.Rune == ' ' {
				continue
			}

			// Get foreground color
			fg := cell.FG.ToNRGBA(colors.Foreground)

			// Handle reverse video
			if cell.Attrs&emulator.AttrReverse != 0 {
				bg := cell.BG.ToNRGBA(colors.Background)
				fg, bg = bg, fg
				rect := image.Rect(
					x*cellSize.X, y*cellSize.Y,
					(x+1)*cellSize.X, (y+1)*cellSize.Y,
				)
				r.FillRect(rect, bg)
			}

			// Draw glyph
			style := CellStyleFromAttrs(cell.Attrs)
			r.DrawGlyph(x, y, cell.Rune, fg, style)

			// Draw decorations
			if style.Underline {
				r.DrawUnderline(x, y, fg)
			}
			if style.Strikethrough {
				r.DrawStrikethrough(x, y, fg)
			}
		}
	}

	// Draw cursor
	cursor := screen.Cursor()
	if cursor.Visible {
		cursorStyle := CursorStyleBlock
		switch cursor.Style {
		case emulator.CursorUnderline:
			cursorStyle = CursorStyleUnderline
		case emulator.CursorBar:
			cursorStyle = CursorStyleBar
		}
		r.DrawCursor(cursor.X, cursor.Y, cursorStyle, colors.Cursor, colors.Background)
	}
}
