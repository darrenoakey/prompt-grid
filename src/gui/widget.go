package gui

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"claude-term/src/emulator"
	"claude-term/src/render"
)

// TerminalWidget is a Gio widget that displays a terminal session
type TerminalWidget struct {
	state    *SessionState
	colors   render.SessionColor
	fontSize unit.Sp
	shaper   *text.Shaper
	cellW    int
	cellH    int
	focused  bool
}

// NewTerminalWidget creates a new terminal widget
func NewTerminalWidget(state *SessionState, colors render.SessionColor, fontSize unit.Sp, shaper *text.Shaper) *TerminalWidget {
	// Cell size based on font - monospace is typically 0.6 width ratio
	cellH := int(float32(fontSize) * 1.5)
	cellW := int(float32(fontSize) * 0.6)

	return &TerminalWidget{
		state:    state,
		colors:   colors,
		fontSize: fontSize,
		shaper:   shaper,
		cellW:    cellW,
		cellH:    cellH,
		focused:  true, // Terminal widget is focused by default
	}
}

// Layout renders the terminal widget
func (w *TerminalWidget) Layout(gtx layout.Context) layout.Dimensions {
	// Padding around content
	padding := 8

	// Calculate dimensions
	cols, rows := w.state.screen.Size()
	contentWidth := cols * w.cellW
	contentHeight := rows * w.cellH
	width := contentWidth + padding*2
	height := contentHeight + padding*2

	// Handle keyboard input
	w.handleInput(gtx)

	// Draw background for entire area
	size := image.Point{X: width, Y: height}
	rect := clip.Rect{Max: size}.Op()
	paint.FillShape(gtx.Ops, w.colors.Background, rect)

	// Offset for padding
	stack := op.Offset(image.Pt(padding, padding)).Push(gtx.Ops)

	// Draw cells
	w.renderCells(gtx)

	// Draw cursor
	w.renderCursor(gtx)

	stack.Pop()

	return layout.Dimensions{Size: size}
}

func (w *TerminalWidget) handleInput(gtx layout.Context) {
	// Set up clip area for input - this defines the clickable/focusable region
	areaStack := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)

	// Register this widget for pointer and key events
	event.Op(gtx.Ops, w)

	areaStack.Pop()

	// Process pointer events (for click-to-focus)
	for {
		ev, ok := gtx.Event(
			pointer.Filter{Target: w, Kinds: pointer.Press},
		)
		if !ok {
			break
		}
		if _, ok := ev.(pointer.Event); ok {
			// Request focus when clicked
			w.focused = true
		}
	}

	// Process focus events
	for {
		ev, ok := gtx.Event(
			key.FocusFilter{Target: w},
		)
		if !ok {
			break
		}
		if e, ok := ev.(key.FocusEvent); ok {
			w.focused = e.Focus
		}
	}

	// Process ALL key events for this window (not filtered by focus)
	// Terminal should receive keys when the window is active
	for {
		ev, ok := gtx.Event(
			key.Filter{},
		)
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok {
			if e.State == key.Press {
				w.handleKey(e)
			}
		}
	}
}

func (w *TerminalWidget) handleKey(e key.Event) {
	var data []byte

	// Handle special keys
	switch e.Name {
	case key.NameReturn, key.NameEnter:
		data = []byte{'\r'}
	case key.NameDeleteBackward:
		data = []byte{0x7f}
	case key.NameTab:
		data = []byte{'\t'}
	case key.NameEscape:
		data = []byte{0x1b}
	case key.NameUpArrow:
		data = []byte{0x1b, '[', 'A'}
	case key.NameDownArrow:
		data = []byte{0x1b, '[', 'B'}
	case key.NameRightArrow:
		data = []byte{0x1b, '[', 'C'}
	case key.NameLeftArrow:
		data = []byte{0x1b, '[', 'D'}
	case key.NameHome:
		data = []byte{0x1b, '[', 'H'}
	case key.NameEnd:
		data = []byte{0x1b, '[', 'F'}
	case key.NamePageUp:
		data = []byte{0x1b, '[', '5', '~'}
	case key.NamePageDown:
		data = []byte{0x1b, '[', '6', '~'}
	case key.NameDeleteForward:
		data = []byte{0x1b, '[', '3', '~'}
	default:
		// Regular character input
		if len(e.Name) == 1 {
			ch := e.Name[0]
			if e.Modifiers.Contain(key.ModCtrl) {
				// Ctrl+key
				if ch >= 'a' && ch <= 'z' {
					data = []byte{ch - 'a' + 1}
				} else if ch >= 'A' && ch <= 'Z' {
					data = []byte{ch - 'A' + 1}
				}
			} else {
				data = []byte(e.Name)
			}
		}
	}

	if len(data) > 0 {
		w.state.session.Write(data)
	}
}

func (w *TerminalWidget) renderCells(gtx layout.Context) {
	screen := w.state.screen
	cols, rows := screen.Size()

	// Create a theme for text rendering
	th := material.NewTheme()
	th.Shaper = w.shaper

	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			cell := screen.Cell(x, y)
			w.renderCell(gtx, th, x, y, cell)
		}
	}
}

func (w *TerminalWidget) renderCell(gtx layout.Context, th *material.Theme, x, y int, cell emulator.Cell) {
	px := x * w.cellW
	py := y * w.cellH

	// Draw background if not default
	if cell.BG.Type != emulator.ColorDefault {
		bg := cell.BG.ToNRGBA(w.colors.Background)
		rect := clip.Rect{
			Min: image.Point{X: px, Y: py},
			Max: image.Point{X: px + w.cellW, Y: py + w.cellH},
		}.Op()
		paint.FillShape(gtx.Ops, bg, rect)
	}

	// Skip empty cells
	if cell.Rune == 0 || cell.Rune == ' ' {
		return
	}

	// Get foreground color
	fg := cell.FG.ToNRGBA(w.colors.Foreground)

	// Handle reverse video
	if cell.Attrs&emulator.AttrReverse != 0 {
		bg := cell.BG.ToNRGBA(w.colors.Background)
		fg, bg = bg, fg
		rect := clip.Rect{
			Min: image.Point{X: px, Y: py},
			Max: image.Point{X: px + w.cellW, Y: py + w.cellH},
		}.Op()
		paint.FillShape(gtx.Ops, bg, rect)
	}

	// Draw the character using material label
	w.drawChar(gtx, th, px, py, cell.Rune, fg, cell.Attrs)

	// Draw underline if needed
	if cell.Attrs&emulator.AttrUnderline != 0 {
		underlineY := py + w.cellH - 2
		rect := clip.Rect{
			Min: image.Point{X: px, Y: underlineY},
			Max: image.Point{X: px + w.cellW, Y: underlineY + 1},
		}.Op()
		paint.FillShape(gtx.Ops, fg, rect)
	}

	// Draw strikethrough if needed
	if cell.Attrs&emulator.AttrStrikethrough != 0 {
		strikeY := py + w.cellH/2
		rect := clip.Rect{
			Min: image.Point{X: px, Y: strikeY},
			Max: image.Point{X: px + w.cellW, Y: strikeY + 1},
		}.Op()
		paint.FillShape(gtx.Ops, fg, rect)
	}
}

func (w *TerminalWidget) drawChar(gtx layout.Context, th *material.Theme, px, py int, r rune, fg color.NRGBA, attrs emulator.AttrFlags) {
	// Position the character
	stack := op.Offset(image.Pt(px, py)).Push(gtx.Ops)
	defer stack.Pop()

	// Create label with the character
	label := material.Label(th, w.fontSize, string(r))
	label.Color = fg

	// Set font weight/style
	if attrs&emulator.AttrBold != 0 {
		label.Font.Weight = font.Bold
	}
	if attrs&emulator.AttrItalic != 0 {
		label.Font.Style = font.Italic
	}

	// Layout the label within cell bounds
	cellGtx := gtx
	cellGtx.Constraints = layout.Exact(image.Point{X: w.cellW, Y: w.cellH})
	label.Layout(cellGtx)
}

func (w *TerminalWidget) renderCursor(gtx layout.Context) {
	cursor := w.state.screen.Cursor()
	// Only show cursor if terminal cursor is visible AND widget is focused
	if !cursor.Visible || !w.focused {
		return
	}

	px := cursor.X * w.cellW
	py := cursor.Y * w.cellH

	var rect clip.Op
	switch cursor.Style {
	case emulator.CursorBlock:
		rect = clip.Rect{
			Min: image.Point{X: px, Y: py},
			Max: image.Point{X: px + w.cellW, Y: py + w.cellH},
		}.Op()
	case emulator.CursorUnderline:
		rect = clip.Rect{
			Min: image.Point{X: px, Y: py + w.cellH - 2},
			Max: image.Point{X: px + w.cellW, Y: py + w.cellH},
		}.Op()
	case emulator.CursorBar:
		rect = clip.Rect{
			Min: image.Point{X: px, Y: py},
			Max: image.Point{X: px + 2, Y: py + w.cellH},
		}.Op()
	}

	paint.FillShape(gtx.Ops, w.colors.Cursor, rect)
}

// Focus sets focus on the widget
func (w *TerminalWidget) Focus() {
	w.focused = true
}

// IsFocused returns whether the widget is focused
func (w *TerminalWidget) IsFocused() bool {
	return w.focused
}
