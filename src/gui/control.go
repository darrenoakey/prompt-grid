package gui

import (
	"image"
	"image/color"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"claude-term/src/render"
)

const tabWidth = 200

// ControlWindow is the control center showing all sessions
type ControlWindow struct {
	app        *App
	window     *app.Window
	shaper     *text.Shaper
	ops        op.Ops
	selected   string
	tabs       []tabState
}

type tabState struct {
	name    string
	hovered bool
	pressed bool
}

// NewControlWindow creates the control center window
func NewControlWindow(application *App) *ControlWindow {
	win := &ControlWindow{
		app:    application,
		window: new(app.Window),
	}

	win.window.Option(
		app.Title("Claude-Term Control Center"),
		app.Size(unit.Dp(1000), unit.Dp(600)),
	)

	win.shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(render.CreateFontCollection()))
	return win
}

// Run starts the control window event loop
func (w *ControlWindow) Run() error {
	for {
		switch e := w.window.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&w.ops, e)
			w.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (w *ControlWindow) layout(gtx layout.Context) {
	// Update tabs from sessions
	sessions := w.app.ListSessions()
	w.tabs = make([]tabState, len(sessions))
	for i, name := range sessions {
		w.tabs[i] = tabState{name: name}
	}

	if w.selected == "" && len(sessions) > 0 {
		w.selected = sessions[0]
	}

	// Split layout: tabs | separator | terminal
	layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return w.layoutTabs(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return w.layoutSeparator(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return w.layoutTerminal(gtx)
		}),
	)
}

func (w *ControlWindow) layoutTabs(gtx layout.Context) layout.Dimensions {
	// Fixed width for tabs panel
	gtx.Constraints.Max.X = tabWidth
	gtx.Constraints.Min.X = tabWidth

	// Background
	rect := clip.Rect{Max: image.Point{X: tabWidth, Y: gtx.Constraints.Max.Y}}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 40, G: 40, B: 40, A: 255}, rect)

	// Layout tabs vertically
	var dims layout.Dimensions
	offsetY := 0
	for i := range w.tabs {
		idx := i
		stack := op.Offset(image.Pt(0, offsetY)).Push(gtx.Ops)
		d := w.layoutTab(gtx, idx)
		stack.Pop()
		offsetY += d.Size.Y
		dims.Size.Y = offsetY
	}
	dims.Size.X = tabWidth

	return dims
}

func (w *ControlWindow) layoutTab(gtx layout.Context, idx int) layout.Dimensions {
	tab := &w.tabs[idx]
	height := 40

	// Get session colors for this tab
	state := w.app.GetSession(tab.name)
	var sessionBg color.NRGBA
	if state != nil {
		sessionBg = state.Colors().Background
	} else {
		sessionBg = color.NRGBA{R: 60, G: 60, B: 60, A: 255}
	}
	// Always use light text on the dark sidebar
	textColor := color.NRGBA{R: 220, G: 220, B: 220, A: 255}

	// Handle input
	areaStack := clip.Rect{Max: image.Point{X: tabWidth, Y: height}}.Push(gtx.Ops)
	event.Op(gtx.Ops, tab)

	for {
		ev, ok := gtx.Event(
			pointer.Filter{Target: tab, Kinds: pointer.Press | pointer.Enter | pointer.Leave},
		)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case pointer.Event:
			switch e.Kind {
			case pointer.Enter:
				tab.hovered = true
			case pointer.Leave:
				tab.hovered = false
			case pointer.Press:
				w.selected = tab.name
			}
		}
	}
	areaStack.Pop()

	// Draw dark background first
	baseBg := color.NRGBA{R: 30, G: 30, B: 30, A: 255}
	rect := clip.Rect{Max: image.Point{X: tabWidth, Y: height}}.Op()
	paint.FillShape(gtx.Ops, baseBg, rect)

	// Draw color indicator bar on the left
	colorBarWidth := 6
	colorRect := clip.Rect{
		Min: image.Point{X: 0, Y: 4},
		Max: image.Point{X: colorBarWidth, Y: height - 4},
	}.Op()
	paint.FillShape(gtx.Ops, sessionBg, colorRect)

	// Highlight selected/hovered
	if tab.name == w.selected {
		highlightRect := clip.Rect{Max: image.Point{X: tabWidth, Y: height}}.Op()
		paint.FillShape(gtx.Ops, color.NRGBA{R: 60, G: 60, B: 80, A: 100}, highlightRect)
	} else if tab.hovered {
		highlightRect := clip.Rect{Max: image.Point{X: tabWidth, Y: height}}.Op()
		paint.FillShape(gtx.Ops, color.NRGBA{R: 50, G: 50, B: 50, A: 100}, highlightRect)
	}

	// Draw tab name using material label
	th := material.NewTheme()
	th.Shaper = w.shaper
	label := material.Label(th, unit.Sp(14), tab.name)
	label.Color = textColor

	// Position label with padding (after color bar)
	stack := op.Offset(image.Pt(colorBarWidth+10, 10)).Push(gtx.Ops)
	labelGtx := gtx
	labelGtx.Constraints = layout.Constraints{
		Min: image.Point{X: 0, Y: 0},
		Max: image.Point{X: tabWidth - colorBarWidth - 20, Y: height - 10},
	}
	label.Layout(labelGtx)
	stack.Pop()

	return layout.Dimensions{Size: image.Point{X: tabWidth, Y: height}}
}

func (w *ControlWindow) layoutSeparator(gtx layout.Context) layout.Dimensions {
	// Vertical separator line
	rect := clip.Rect{Max: image.Point{X: 1, Y: gtx.Constraints.Max.Y}}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 60, G: 60, B: 60, A: 255}, rect)
	return layout.Dimensions{Size: image.Point{X: 1, Y: gtx.Constraints.Max.Y}}
}

func (w *ControlWindow) layoutTerminal(gtx layout.Context) layout.Dimensions {
	// Get selected session
	state := w.app.GetSession(w.selected)

	// Fill the entire area with session background or default
	var bgColor color.NRGBA
	if state != nil {
		bgColor = state.Colors().Background
	} else {
		bgColor = color.NRGBA{R: 30, G: 30, B: 30, A: 255}
	}
	rect := clip.Rect{Max: gtx.Constraints.Max}.Op()
	paint.FillShape(gtx.Ops, bgColor, rect)

	if state == nil {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}

	// Create widget for this session with padding
	widget := NewTerminalWidget(state, state.Colors(), w.app.FontSize(), w.shaper)

	// Handle keyboard input for the terminal
	w.handleTerminalInput(gtx, state)

	// Add padding around terminal
	padding := 8
	stack := op.Offset(image.Pt(padding, padding)).Push(gtx.Ops)
	paddedGtx := gtx
	paddedGtx.Constraints.Max.X -= padding * 2
	paddedGtx.Constraints.Max.Y -= padding * 2
	paddedGtx.Constraints.Min = image.Point{}
	widget.Layout(paddedGtx)
	stack.Pop()

	return layout.Dimensions{Size: gtx.Constraints.Max}
}

func (w *ControlWindow) handleTerminalInput(gtx layout.Context, state *SessionState) {
	// Request keyboard focus for terminal area
	areaStack := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, state)

	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: state},
		)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case key.Event:
			if e.State == key.Press {
				w.handleKey(state, e)
			}
		}
	}
	areaStack.Pop()
}

func (w *ControlWindow) handleKey(state *SessionState, e key.Event) {
	var data []byte

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
	default:
		if len(e.Name) == 1 {
			ch := e.Name[0]
			if e.Modifiers.Contain(key.ModCtrl) {
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
		state.session.Write(data)
	}
}

// Close closes the control window
func (w *ControlWindow) Close() {
	w.window.Perform(system.ActionClose)
}

// Invalidate requests a redraw
func (w *ControlWindow) Invalidate() {
	w.window.Invalidate()
}
