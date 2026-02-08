package gui

import (
	"sync/atomic"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"

	"claude-term/src/pty"
	"claude-term/src/render"
)

// Window counter for positioning
var windowCount int32

// TerminalWindow is a window displaying a single terminal session
type TerminalWindow struct {
	app     *App
	state   *SessionState
	window  *app.Window
	widget  *TerminalWidget
	shaper  *text.Shaper
	ops     op.Ops
}

// NewTerminalWindow creates a new terminal window
func NewTerminalWindow(application *App, state *SessionState) *TerminalWindow {
	// Calculate window size based on terminal dimensions plus padding
	cols, rows := state.screen.Size()
	cellW := int(float32(application.FontSize()) * 0.6)
	cellH := int(float32(application.FontSize()) * 1.5)
	padding := 16 // 8px on each side
	width := cols*cellW + padding
	height := rows*cellH + padding

	win := &TerminalWindow{
		app:    application,
		state:  state,
		window: new(app.Window),
	}

	// Track window count (for future positioning if Gio adds support)
	atomic.AddInt32(&windowCount, 1)

	// Set window options
	win.window.Option(
		app.Title(state.name),
		app.Size(unit.Dp(width), unit.Dp(height)),
	)

	// Create shaper with embedded fonts
	win.shaper = text.NewShaper(text.WithCollection(render.CreateFontCollection()))
	win.widget = NewTerminalWidget(state, state.Colors(), application.FontSize(), win.shaper)

	return win
}

// Run starts the window event loop
func (w *TerminalWindow) Run() error {
	var lastWidth, lastHeight int

	for {
		switch e := w.window.Event().(type) {
		case app.DestroyEvent:
			atomic.AddInt32(&windowCount, -1)
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&w.ops, e)

			// Handle resize - calculate new terminal dimensions
			padding := 16
			newWidth := e.Size.X - padding
			newHeight := e.Size.Y - padding
			if newWidth != lastWidth || newHeight != lastHeight {
				lastWidth = newWidth
				lastHeight = newHeight

				// Calculate new cols/rows based on cell size
				cellW := int(float32(w.app.FontSize()) * 0.6)
				cellH := int(float32(w.app.FontSize()) * 1.5)
				if cellW > 0 && cellH > 0 {
					newCols := newWidth / cellW
					newRows := newHeight / cellH
					if newCols > 0 && newRows > 0 {
						w.state.screen.Resize(newCols, newRows)
						w.state.pty.Resize(pty.Size{Cols: uint16(newCols), Rows: uint16(newRows)})
					}
				}
			}

			w.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (w *TerminalWindow) layout(gtx layout.Context) {
	w.widget.Layout(gtx)
}

// Close closes the window
func (w *TerminalWindow) Close() {
	w.window.Perform(system.ActionClose)
}

// BringToFront raises the window to the front
func (w *TerminalWindow) BringToFront() {
	w.window.Perform(system.ActionRaise)
}

// Invalidate requests a redraw
func (w *TerminalWindow) Invalidate() {
	w.window.Invalidate()
}

// Name returns the session name
func (w *TerminalWindow) Name() string {
	return w.state.name
}

// SetTitle updates the window title
func (w *TerminalWindow) SetTitle(title string) {
	w.window.Option(app.Title(title))
}
