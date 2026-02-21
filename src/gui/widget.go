package gui

import (
	"image"
	"image/color"
	"os/exec"
	"strings"

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

	"prompt-grid/src/emulator"
	"prompt-grid/src/render"
)

// TerminalWidget is a Gio widget that displays a terminal session
type TerminalWidget struct {
	state        *SessionState
	fontSize     unit.Sp
	shaper       *text.Shaper
	theme        *material.Theme // Persistent theme (avoids per-frame allocation)
	cellW        int
	cellH        int
	focused      bool
	requestFocus bool // Set by parent to request focus each frame
	skipKeyboard bool // When true, parent handles keyboard (used in control center)
}

// NewTerminalWidget creates a new terminal widget
func NewTerminalWidget(state *SessionState, colors render.SessionColor, fontSize unit.Sp, shaper *text.Shaper) *TerminalWidget {
	// Cell size based on font - monospace is typically 0.6 width ratio
	cellH := int(float32(fontSize) * 1.5)
	cellW := int(float32(fontSize) * 0.6)

	th := material.NewTheme()
	th.Shaper = shaper

	return &TerminalWidget{
		state:    state,
		fontSize: fontSize,
		shaper:   shaper,
		theme:    th,
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
	cols, rows := w.state.Screen().Size()
	contentWidth := cols * w.cellW
	contentHeight := rows * w.cellH
	width := contentWidth + padding*2
	height := contentHeight + padding*2

	// Handle keyboard input
	w.handleInput(gtx)

	// Draw background for entire area
	size := image.Point{X: width, Y: height}
	rect := clip.Rect{Max: size}.Op()
	paint.FillShape(gtx.Ops, w.state.colors.Background, rect)

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
	padding := 8

	// Set up clip area for input - this defines the clickable/focusable region
	areaStack := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)

	// Register this widget for pointer and key events
	event.Op(gtx.Ops, w)

	// Request keyboard focus within the same scope as event.Op registration
	if w.requestFocus {
		gtx.Execute(key.FocusCmd{Tag: w})
	}

	areaStack.Pop()

	// Process pointer events for selection and scrolling
	// All pointer event types must be in a single filter
	// ScrollY bounds are REQUIRED — without them Gio rejects all scroll events
	scrollMax := w.state.scrollback.Count()
	for {
		ev, ok := gtx.Event(
			pointer.Filter{
				Target:  w,
				Kinds:   pointer.Press | pointer.Drag | pointer.Release | pointer.Scroll,
				ScrollY: pointer.ScrollRange{Min: -scrollMax, Max: scrollMax},
			},
		)
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			switch e.Kind {
			case pointer.Scroll:
				// Scroll.Y: positive = scroll down (toward bottom), negative = scroll up (toward top/history)
				// We want: scroll up (negative Y) = increase offset (view history)
				// Convert pixels to lines - trackpad sends small increments
				delta := int(e.Scroll.Y / 3) // Positive Y = scroll down = decrease offset
				if delta == 0 && e.Scroll.Y != 0 {
					// Ensure at least 1 line scroll for small movements
					if e.Scroll.Y > 0 {
						delta = 1
					} else {
						delta = -1
					}
				}
				// Invert: scrolling down (positive delta) should decrease offset (toward live)
				// scrolling up (negative delta) should increase offset (toward history)
				w.state.AdjustScrollOffset(-delta)

			case pointer.Press, pointer.Drag, pointer.Release:
				// Convert pixel position to cell coordinates
				cellX := (int(e.Position.X) - padding) / w.cellW
				cellY := (int(e.Position.Y) - padding) / w.cellH

				// Clamp to screen bounds
				cols, rows := w.state.Screen().Size()
				if cellX < 0 {
					cellX = 0
				}
				if cellX >= cols {
					cellX = cols - 1
				}
				if cellY < 0 {
					cellY = 0
				}
				if cellY >= rows {
					cellY = rows - 1
				}

				switch e.Kind {
				case pointer.Press:
					w.focused = true
					if !w.skipKeyboard {
						// Request keyboard focus when clicked (standalone window only).
						// In control center, the parent handles keyboard focus.
						gtx.Execute(key.FocusCmd{Tag: w})
					}
					w.state.StartSelection(cellX, cellY)
				case pointer.Drag:
					w.state.UpdateSelection(cellX, cellY)
				case pointer.Release:
					w.state.EndSelection()
					// Only auto-copy if the user dragged (not just clicked).
					// A single click sets selStart==selEnd — auto-copying that one cell
					// silently overwrites whatever was in the system clipboard.
					if w.state.SelectionHasExtent() {
						selectedText := w.state.GetSelectedText()
						if len(selectedText) > 0 {
							go func() {
								cmd := exec.Command("pbcopy")
								cmd.Stdin = strings.NewReader(selectedText)
								cmd.Run()
							}()
						}
					} else {
						// Single click: clear point-selection so it does not render
						// highlighted or interfere with future Cmd+C.
						w.state.ClearSelection()
					}
				}
			}
		}
	}

	// When skipKeyboard is true, the parent (ControlWindow) handles all keyboard/clipboard.
	// The widget only handles pointer events (selection, scroll) above.
	if !w.skipKeyboard {
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

		// Process all key events
		// Tab must be explicitly named — Gio intercepts unnamed Tab as a focus-navigation SystemEvent
		// Cmd+C/V/X must be explicitly named — Gio intercepts them as system clipboard shortcuts
		for {
			ev, ok := gtx.Event(
				key.Filter{Optional: key.ModShift | key.ModCtrl | key.ModCommand},
				key.Filter{Name: key.NameTab},
				key.Filter{Name: "C", Required: key.ModCommand},
				key.Filter{Name: "V", Required: key.ModCommand},
				key.Filter{Name: "X", Required: key.ModCommand},
			)
			if !ok {
				break
			}
			switch e := ev.(type) {
			case key.EditEvent:
				// Clear selection on any text input
				w.state.ClearSelection()
				if len(e.Text) > 0 {
					w.state.pty.Write([]byte(e.Text))
				}
			case key.Event:
				if e.State == key.Press {
					// Handle Cmd+C for copy via pbcopy (cross-app compatible)
					if e.Modifiers.Contain(key.ModCommand) && e.Name == "C" {
						if w.state.HasSelection() {
							selectedText := w.state.GetSelectedText()
							if len(selectedText) > 0 {
								go func() {
									cmd := exec.Command("pbcopy")
									cmd.Stdin = strings.NewReader(selectedText)
									cmd.Run()
								}()
							}
						}
					} else if e.Modifiers.Contain(key.ModCommand) && e.Name == "X" {
						// Cmd+X for cut (copy via pbcopy, then clear selection)
						if w.state.HasSelection() {
							selectedText := w.state.GetSelectedText()
							if len(selectedText) > 0 {
								go func() {
									cmd := exec.Command("pbcopy")
									cmd.Stdin = strings.NewReader(selectedText)
									cmd.Run()
								}()
							}
							w.state.ClearSelection()
						}
					} else if e.Modifiers.Contain(key.ModCommand) && e.Name == "V" {
						// Cmd+V: paste via pbpaste so any MIME type works and clipboard is never altered.
						ptySess := w.state.pty
						go func() {
							out, err := exec.Command("pbpaste").Output()
							if err == nil && len(out) > 0 {
								ptySess.Write(out)
							}
						}()
					} else {
						w.state.ClearSelection()
						w.handleKeyEvent(e)
					}
				}
			}
		}

	}
}

func (w *TerminalWidget) handleKeyEvent(e key.Event) {
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
	case key.NameSpace:
		data = []byte{' '}
	default:
		// Handle regular character input
		if len(e.Name) == 1 {
			ch := e.Name[0]
			if e.Modifiers.Contain(key.ModCtrl) {
				// Ctrl+key: convert to control character
				if ch >= 'A' && ch <= 'Z' {
					data = []byte{ch - 'A' + 1}
				} else if ch >= 'a' && ch <= 'z' {
					data = []byte{ch - 'a' + 1}
				}
			} else if e.Modifiers.Contain(key.ModShift) {
				// Shift+key: uppercase or shift symbol
				data = []byte{shiftChar(ch)}
			} else {
				// Regular key: lowercase
				if ch >= 'A' && ch <= 'Z' {
					data = []byte{ch + 32} // Convert to lowercase
				} else {
					data = []byte{ch}
				}
			}
		}
	}

	if len(data) > 0 {
		w.state.pty.Write(data)
	}
}

func (w *TerminalWidget) renderCells(gtx layout.Context) {
	screen := w.state.Screen()
	cols, rows := screen.Size()
	scrollback := w.state.scrollback
	scrollOffset := w.state.ScrollOffset()
	scrollbackCount := scrollback.Count()

	for y := 0; y < rows; y++ {
		// Calculate which line to display at screen row y
		// When scrollOffset=0, we show the current screen (scrollbackCount + y maps to screen line y)
		// When scrollOffset>0, we're viewing history
		viewLine := scrollbackCount - scrollOffset + y

		for x := 0; x < cols; x++ {
			var cell emulator.Cell
			if viewLine < 0 {
				// Before any content - empty cell
				cell = emulator.Cell{}
			} else if viewLine < scrollbackCount {
				// Line from scrollback
				line := scrollback.Line(viewLine)
				if line != nil && x < len(line) {
					cell = line[x]
				}
			} else {
				// Line from current screen
				screenY := viewLine - scrollbackCount
				if screenY < rows {
					cell = screen.Cell(x, screenY)
				}
			}
			w.renderCell(gtx, w.theme, x, y, cell)
		}
	}
}

func (w *TerminalWidget) renderCell(gtx layout.Context, th *material.Theme, x, y int, cell emulator.Cell) {
	px := x * w.cellW
	py := y * w.cellH

	// Check if cell is selected
	isSelected := w.state.IsSelected(x, y)

	// Get colors - adjust indexed (ANSI) colors for contrast against session background
	fg := cell.FG.ToNRGBA(w.state.colors.Foreground)
	bg := cell.BG.ToNRGBA(w.state.colors.Background)
	if cell.FG.Type == emulator.ColorIndexed {
		fg = render.AdjustForContrast(fg, w.state.colors.Background)
	}

	// Handle reverse video
	if cell.Attrs&emulator.AttrReverse != 0 {
		fg, bg = bg, fg
	}

	// Invert colors for selection
	if isSelected {
		fg, bg = bg, fg
	}

	// Draw background if needed
	if cell.BG.Type != emulator.ColorDefault || isSelected || cell.Attrs&emulator.AttrReverse != 0 {
		rect := clip.Rect{
			Min: image.Point{X: px, Y: py},
			Max: image.Point{X: px + w.cellW, Y: py + w.cellH},
		}.Op()
		paint.FillShape(gtx.Ops, bg, rect)
	}

	// Skip empty cells (but still draw background for selection)
	if cell.Rune == 0 || cell.Rune == ' ' {
		return
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
	cursor := w.state.Screen().Cursor()
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

	paint.FillShape(gtx.Ops, w.state.colors.Cursor, rect)
}

// Focus sets focus on the widget
func (w *TerminalWidget) Focus() {
	w.focused = true
}

// IsFocused returns whether the widget is focused
func (w *TerminalWidget) IsFocused() bool {
	return w.focused
}
