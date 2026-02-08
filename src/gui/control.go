package gui

import (
	"fmt"
	"image"
	"image/color"
	"strconv"
	"strings"

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
	app            *App
	window         *app.Window
	shaper         *text.Shaper
	ops            op.Ops
	selected       string
	tabStates      map[string]*tabState       // Persistent tab states keyed by name
	termWidgets    map[string]*TerminalWidget // Persistent terminal widgets keyed by session name
	contextMenu    *contextMenuState          // Right-click context menu
	tabPanelBg     *tabPanelBackground        // For right-click on empty tab area
	renameState    *renameState               // For renaming sessions
}

// tabPanelBackground is a persistent target for right-click on empty tab area
type tabPanelBackground struct{}

// renameState tracks active rename operation
type renameState struct {
	active      bool
	sessionName string
	newName     string
	cursorPos   int
}

type tabState struct {
	name    string
	hovered bool
	pressed bool
}

type contextMenuState struct {
	visible     bool
	sessionName string
	position    image.Point
	items       []*menuItem
}

type menuItem struct {
	label   string
	action  func()
	hovered bool
}

// NewControlWindow creates the control center window
func NewControlWindow(application *App) *ControlWindow {
	win := &ControlWindow{
		app:         application,
		window:      new(app.Window),
		tabStates:   make(map[string]*tabState),
		termWidgets: make(map[string]*TerminalWidget),
		contextMenu: &contextMenuState{},
		tabPanelBg:  &tabPanelBackground{},
		renameState: &renameState{},
	}

	win.window.Option(
		app.Title("Claude-Term Control Center"),
		app.Size(unit.Dp(1000), unit.Dp(600)),
	)

	win.shaper = text.NewShaper(text.WithCollection(render.CreateFontCollection()))
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
	// Get current sessions
	sessions := w.app.ListSessions()

	// Ensure we have persistent tab state for each session
	for _, name := range sessions {
		if _, exists := w.tabStates[name]; !exists {
			w.tabStates[name] = &tabState{name: name}
		}
	}

	// Clean up stale tab states
	for name := range w.tabStates {
		found := false
		for _, s := range sessions {
			if s == name {
				found = true
				break
			}
		}
		if !found {
			delete(w.tabStates, name)
		}
	}

	if w.selected == "" && len(sessions) > 0 {
		w.selected = sessions[0]
	}

	// Handle rename keyboard input
	if w.renameState.active {
		w.handleRenameInput(gtx)
	}

	// Handle clicks outside context menu to dismiss it
	if w.contextMenu.visible {
		areaStack := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
		event.Op(gtx.Ops, w.contextMenu)
		for {
			ev, ok := gtx.Event(
				pointer.Filter{Target: w.contextMenu, Kinds: pointer.Press},
			)
			if !ok {
				break
			}
			if e, ok := ev.(pointer.Event); ok && e.Kind == pointer.Press {
				// Check if click is outside the menu
				menuWidth := 140
				menuHeight := len(w.contextMenu.items) * 28
				pos := w.contextMenu.position
				clickX, clickY := int(e.Position.X), int(e.Position.Y)
				if clickX < pos.X || clickX > pos.X+menuWidth || clickY < pos.Y || clickY > pos.Y+menuHeight {
					w.contextMenu.visible = false
				}
			}
		}
		areaStack.Pop()
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

	// Draw context menu on top of everything
	w.layoutContextMenu(gtx)
}

func (w *ControlWindow) layoutTabs(gtx layout.Context) layout.Dimensions {
	// Fixed width for tabs panel
	gtx.Constraints.Max.X = tabWidth
	gtx.Constraints.Min.X = tabWidth
	panelHeight := gtx.Constraints.Max.Y

	// Background
	rect := clip.Rect{Max: image.Point{X: tabWidth, Y: panelHeight}}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 40, G: 40, B: 40, A: 255}, rect)

	// Handle right-click on the tab panel background (empty area)
	// This must cover the entire panel but let tab events through
	bgAreaStack := clip.Rect{Max: image.Point{X: tabWidth, Y: panelHeight}}.Push(gtx.Ops)
	event.Op(gtx.Ops, w.tabPanelBg)
	for {
		ev, ok := gtx.Event(
			pointer.Filter{Target: w.tabPanelBg, Kinds: pointer.Press},
		)
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Kind == pointer.Press {
			isRightClick := e.Buttons.Contain(pointer.ButtonSecondary) ||
				(e.Modifiers.Contain(key.ModCtrl) && e.Buttons.Contain(pointer.ButtonPrimary))
			if isRightClick {
				// Right-click on empty area - show menu with "New Session" only
				w.showContextMenu("", image.Point{X: int(e.Position.X), Y: int(e.Position.Y)})
			}
		}
	}
	bgAreaStack.Pop()

	// Layout tabs vertically
	sessions := w.app.ListSessions()
	offsetY := 0
	for _, name := range sessions {
		tab := w.tabStates[name]
		if tab == nil {
			continue
		}
		stack := op.Offset(image.Pt(0, offsetY)).Push(gtx.Ops)
		d := w.layoutTab(gtx, tab, offsetY)
		stack.Pop()
		offsetY += d.Size.Y
	}

	// Layout Discord status at bottom
	statusHeight := 30
	statusY := panelHeight - statusHeight
	statusStack := op.Offset(image.Pt(0, statusY)).Push(gtx.Ops)
	w.layoutDiscordStatus(gtx, statusHeight)
	statusStack.Pop()

	return layout.Dimensions{Size: image.Point{X: tabWidth, Y: panelHeight}}
}

func (w *ControlWindow) layoutDiscordStatus(gtx layout.Context, height int) {
	// Background slightly lighter than sidebar
	rect := clip.Rect{Max: image.Point{X: tabWidth, Y: height}}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 50, G: 50, B: 50, A: 255}, rect)

	// Separator line at top
	sepRect := clip.Rect{Max: image.Point{X: tabWidth, Y: 1}}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 70, G: 70, B: 70, A: 255}, sepRect)

	// Status indicator circle
	circleX := 12
	circleY := height / 2
	circleRadius := 5

	var statusColor color.NRGBA
	var statusText string
	if w.app.IsDiscordConnected() {
		statusColor = color.NRGBA{R: 80, G: 200, B: 80, A: 255} // Green
		statusText = "Discord connected"
	} else {
		statusColor = color.NRGBA{R: 200, G: 80, B: 80, A: 255} // Red
		statusText = "Discord offline"
	}

	// Draw circle (approximate with small rect for simplicity)
	circleRect := clip.Rect{
		Min: image.Point{X: circleX - circleRadius, Y: circleY - circleRadius},
		Max: image.Point{X: circleX + circleRadius, Y: circleY + circleRadius},
	}.Op()
	paint.FillShape(gtx.Ops, statusColor, circleRect)

	// Status text
	th := material.NewTheme()
	th.Shaper = w.shaper
	label := material.Label(th, unit.Sp(11), statusText)
	label.Color = color.NRGBA{R: 160, G: 160, B: 160, A: 255}

	textStack := op.Offset(image.Pt(circleX+circleRadius+8, 8)).Push(gtx.Ops)
	labelGtx := gtx
	labelGtx.Constraints = layout.Constraints{
		Min: image.Point{X: 0, Y: 0},
		Max: image.Point{X: tabWidth - 30, Y: height},
	}
	label.Layout(labelGtx)
	textStack.Pop()
}

func (w *ControlWindow) layoutTab(gtx layout.Context, tab *tabState, offsetY int) layout.Dimensions {
	height := 40

	// Get session colors for this tab - use exact session colors
	state := w.app.GetSession(tab.name)
	var sessionBg, sessionFg color.NRGBA
	if state != nil {
		sessionBg = state.Colors().Background
		sessionFg = state.Colors().Foreground
	} else {
		sessionBg = color.NRGBA{R: 60, G: 60, B: 60, A: 255}
		sessionFg = color.NRGBA{R: 220, G: 220, B: 220, A: 255}
	}

	// Check if this tab is being renamed
	isRenaming := w.renameState.active && w.renameState.sessionName == tab.name

	// Handle input (only if not renaming)
	if !isRenaming {
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
					// Check for right-click (secondary button or control+click on macOS)
					isRightClick := e.Buttons.Contain(pointer.ButtonSecondary) ||
						(e.Modifiers.Contain(key.ModCtrl) && e.Buttons.Contain(pointer.ButtonPrimary))
					if isRightClick {
						// Right-click - show context menu
						w.showContextMenu(tab.name, image.Point{X: int(e.Position.X), Y: offsetY + int(e.Position.Y)})
					} else {
						// Left-click - select tab
						w.selected = tab.name
						w.contextMenu.visible = false // Close context menu on left click
					}
				}
			}
		}
		areaStack.Pop()
	}

	// Draw session background color for entire tab
	rect := clip.Rect{Max: image.Point{X: tabWidth, Y: height}}.Op()
	paint.FillShape(gtx.Ops, sessionBg, rect)

	// Add visual feedback for selected/hovered with semi-transparent overlay
	if tab.name == w.selected {
		// White highlight for selected
		highlightRect := clip.Rect{Max: image.Point{X: tabWidth, Y: height}}.Op()
		paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 40}, highlightRect)
	} else if tab.hovered && !isRenaming {
		// Subtle white highlight for hovered
		highlightRect := clip.Rect{Max: image.Point{X: tabWidth, Y: height}}.Op()
		paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 20}, highlightRect)
	}

	// Draw tab name or rename input
	th := material.NewTheme()
	th.Shaper = w.shaper

	if isRenaming {
		// Draw rename input field
		w.layoutRenameInput(gtx, sessionFg, height)
	} else {
		// Draw normal tab name
		label := material.Label(th, unit.Sp(14), tab.name)
		label.Color = sessionFg

		// Position label with padding
		stack := op.Offset(image.Pt(12, 10)).Push(gtx.Ops)
		labelGtx := gtx
		labelGtx.Constraints = layout.Constraints{
			Min: image.Point{X: 0, Y: 0},
			Max: image.Point{X: tabWidth - 24, Y: height - 10},
		}
		label.Layout(labelGtx)
		stack.Pop()
	}

	return layout.Dimensions{Size: image.Point{X: tabWidth, Y: height}}
}

// layoutRenameInput draws the rename text input
func (w *ControlWindow) layoutRenameInput(gtx layout.Context, fg color.NRGBA, height int) {
	// Draw input background (slightly darker)
	inputBg := clip.Rect{
		Min: image.Point{X: 8, Y: 6},
		Max: image.Point{X: tabWidth - 8, Y: height - 6},
	}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 20, G: 20, B: 20, A: 255}, inputBg)

	// Draw input border
	borderColor := color.NRGBA{R: 100, G: 150, B: 255, A: 255}
	for _, edge := range []clip.Rect{
		{Min: image.Point{X: 8, Y: 6}, Max: image.Point{X: tabWidth - 8, Y: 7}},                 // top
		{Min: image.Point{X: 8, Y: height - 7}, Max: image.Point{X: tabWidth - 8, Y: height - 6}}, // bottom
		{Min: image.Point{X: 8, Y: 6}, Max: image.Point{X: 9, Y: height - 6}},                   // left
		{Min: image.Point{X: tabWidth - 9, Y: 6}, Max: image.Point{X: tabWidth - 8, Y: height - 6}}, // right
	} {
		paint.FillShape(gtx.Ops, borderColor, edge.Op())
	}

	// Draw text
	th := material.NewTheme()
	th.Shaper = w.shaper
	label := material.Label(th, unit.Sp(14), w.renameState.newName)
	label.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	stack := op.Offset(image.Pt(12, 10)).Push(gtx.Ops)
	labelGtx := gtx
	labelGtx.Constraints = layout.Constraints{
		Min: image.Point{X: 0, Y: 0},
		Max: image.Point{X: tabWidth - 24, Y: height - 10},
	}
	label.Layout(labelGtx)
	stack.Pop()

	// Draw cursor (simple blinking not implemented, just static cursor)
	// Approximate cursor position based on character count
	charWidth := 8 // Approximate pixels per character at this font size
	cursorX := 12 + w.renameState.cursorPos*charWidth
	cursorRect := clip.Rect{
		Min: image.Point{X: cursorX, Y: 10},
		Max: image.Point{X: cursorX + 1, Y: height - 12},
	}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, cursorRect)
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

	// Get or create persistent widget for this session
	// Must persist across frames for event routing to work
	widget, ok := w.termWidgets[w.selected]
	if !ok {
		widget = NewTerminalWidget(state, state.Colors(), w.app.FontSize(), w.shaper)
		w.termWidgets[w.selected] = widget
	}

	// Handle keyboard input for the terminal (skip during rename to avoid stealing key events)
	if !w.renameState.active {
		w.handleTerminalInput(gtx, state)
	}

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

	// Process all key events
	for {
		ev, ok := gtx.Event(
			key.Filter{Optional: key.ModShift | key.ModCtrl},
		)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case key.EditEvent:
			if len(e.Text) > 0 {
				state.pty.Write([]byte(e.Text))
			}
		case key.Event:
			if e.State == key.Press {
				w.handleKeyEvent(state, e)
			}
		}
	}
	areaStack.Pop()
}

func (w *ControlWindow) handleKeyEvent(state *SessionState, e key.Event) {
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
		if len(e.Name) == 1 {
			ch := e.Name[0]
			if e.Modifiers.Contain(key.ModCtrl) {
				if ch >= 'A' && ch <= 'Z' {
					data = []byte{ch - 'A' + 1}
				} else if ch >= 'a' && ch <= 'z' {
					data = []byte{ch - 'a' + 1}
				}
			} else if e.Modifiers.Contain(key.ModShift) {
				data = []byte{shiftChar(ch)}
			} else {
				if ch >= 'A' && ch <= 'Z' {
					data = []byte{ch + 32}
				} else {
					data = []byte{ch}
				}
			}
		}
	}

	if len(data) > 0 {
		state.pty.Write(data)
	}
}

func shiftChar(ch byte) byte {
	if ch >= 'A' && ch <= 'Z' {
		return ch
	}
	if ch >= 'a' && ch <= 'z' {
		return ch - 32
	}
	shiftMap := map[byte]byte{
		'1': '!', '2': '@', '3': '#', '4': '$', '5': '%',
		'6': '^', '7': '&', '8': '*', '9': '(', '0': ')',
		'-': '_', '=': '+', '[': '{', ']': '}', '\\': '|',
		';': ':', '\'': '"', ',': '<', '.': '>', '/': '?',
		'`': '~',
	}
	if shifted, ok := shiftMap[ch]; ok {
		return shifted
	}
	return ch
}

func (w *ControlWindow) showContextMenu(sessionName string, pos image.Point) {
	w.contextMenu.visible = true
	w.contextMenu.sessionName = sessionName
	w.contextMenu.position = pos

	// Build menu items based on context
	items := []*menuItem{}

	// "New Session" is always available
	items = append(items, &menuItem{
		label: "New Session",
		action: func() {
			w.contextMenu.visible = false
			w.window.Invalidate()
			// Create new session with auto-generated name
			newName := w.nextSessionName()
			go func() {
				err := w.app.AddSession(newName, "")
				if err == nil {
					w.selected = newName
					w.window.Invalidate()
				}
			}()
		},
	})

	// Session-specific menu items only when clicking on a tab
	if sessionName != "" {
		items = append(items, &menuItem{
			label: "Rename",
			action: func() {
				w.contextMenu.visible = false
				w.startRename(sessionName)
				w.window.Invalidate()
			},
		})

		items = append(items, &menuItem{
			label: "Bring to Front",
			action: func() {
				w.contextMenu.visible = false
				w.window.Invalidate()
				go func() {
					if state := w.app.GetSession(sessionName); state != nil && state.window != nil {
						state.window.BringToFront()
					}
				}()
			},
		})

		items = append(items, &menuItem{
			label: "Close",
			action: func() {
				w.contextMenu.visible = false
				w.window.Invalidate()
				go w.app.CloseSession(sessionName)
			},
		})
	}

	w.contextMenu.items = items
}

// nextSessionName returns the next available "Session N" name
func (w *ControlWindow) nextSessionName() string {
	sessions := w.app.ListSessions()

	// Find all existing "Session N" numbers
	usedNumbers := make(map[int]bool)
	for _, name := range sessions {
		if strings.HasPrefix(name, "Session ") {
			numStr := strings.TrimPrefix(name, "Session ")
			if num, err := strconv.Atoi(numStr); err == nil {
				usedNumbers[num] = true
			}
		}
	}

	// Find the smallest available number
	for i := 1; ; i++ {
		if !usedNumbers[i] {
			return fmt.Sprintf("Session %d", i)
		}
	}
}

// startRename begins the rename operation for a session
func (w *ControlWindow) startRename(sessionName string) {
	w.renameState.active = true
	w.renameState.sessionName = sessionName
	w.renameState.newName = sessionName
	w.renameState.cursorPos = len(sessionName)
}

// cancelRename cancels the rename operation
func (w *ControlWindow) cancelRename() {
	w.renameState.active = false
	w.renameState.sessionName = ""
	w.renameState.newName = ""
}

// confirmRename applies the rename
func (w *ControlWindow) confirmRename() {
	if w.renameState.newName != "" && w.renameState.newName != w.renameState.sessionName {
		oldName := w.renameState.sessionName
		newName := w.renameState.newName
		err := w.app.RenameSession(oldName, newName)
		if err == nil {
			// Update selected if we renamed the selected session
			if w.selected == oldName {
				w.selected = newName
			}
			// Update term widget cache
			if widget, ok := w.termWidgets[oldName]; ok {
				delete(w.termWidgets, oldName)
				w.termWidgets[newName] = widget
			}
		}
	}
	w.cancelRename()
}

// handleRenameInput processes keyboard input during rename
func (w *ControlWindow) handleRenameInput(gtx layout.Context) {
	// Set up input area for the rename state
	areaStack := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, w.renameState)

	// Request keyboard focus so key.EditEvent (typed characters) are delivered here
	gtx.Execute(key.FocusCmd{Tag: w.renameState})

	for {
		ev, ok := gtx.Event(
			key.Filter{Optional: key.ModShift | key.ModCtrl},
		)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case key.EditEvent:
			// Insert typed text at cursor position
			if len(e.Text) > 0 {
				before := w.renameState.newName[:w.renameState.cursorPos]
				after := w.renameState.newName[w.renameState.cursorPos:]
				w.renameState.newName = before + e.Text + after
				w.renameState.cursorPos += len(e.Text)
			}
		case key.Event:
			if e.State == key.Press {
				switch e.Name {
				case key.NameReturn, key.NameEnter:
					w.confirmRename()
				case key.NameEscape:
					w.cancelRename()
				case key.NameDeleteBackward:
					if w.renameState.cursorPos > 0 {
						before := w.renameState.newName[:w.renameState.cursorPos-1]
						after := w.renameState.newName[w.renameState.cursorPos:]
						w.renameState.newName = before + after
						w.renameState.cursorPos--
					}
				case key.NameDeleteForward:
					if w.renameState.cursorPos < len(w.renameState.newName) {
						before := w.renameState.newName[:w.renameState.cursorPos]
						after := w.renameState.newName[w.renameState.cursorPos+1:]
						w.renameState.newName = before + after
					}
				case key.NameLeftArrow:
					if w.renameState.cursorPos > 0 {
						w.renameState.cursorPos--
					}
				case key.NameRightArrow:
					if w.renameState.cursorPos < len(w.renameState.newName) {
						w.renameState.cursorPos++
					}
				case key.NameHome:
					w.renameState.cursorPos = 0
				case key.NameEnd:
					w.renameState.cursorPos = len(w.renameState.newName)
				}
			}
		}
	}
	areaStack.Pop()
}

func (w *ControlWindow) layoutContextMenu(gtx layout.Context) {
	if !w.contextMenu.visible {
		return
	}

	itemHeight := 28
	menuWidth := 140
	menuHeight := len(w.contextMenu.items) * itemHeight

	// Menu position
	pos := w.contextMenu.position

	// Draw menu background with shadow
	shadowRect := clip.Rect{
		Min: image.Point{X: pos.X + 2, Y: pos.Y + 2},
		Max: image.Point{X: pos.X + menuWidth + 2, Y: pos.Y + menuHeight + 2},
	}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0, G: 0, B: 0, A: 80}, shadowRect)

	// Menu background
	menuRect := clip.Rect{
		Min: pos,
		Max: image.Point{X: pos.X + menuWidth, Y: pos.Y + menuHeight},
	}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 45, G: 45, B: 50, A: 255}, menuRect)

	// Border
	for _, edge := range []clip.Rect{
		{Min: pos, Max: image.Point{X: pos.X + menuWidth, Y: pos.Y + 1}},                                   // top
		{Min: image.Point{X: pos.X, Y: pos.Y + menuHeight - 1}, Max: image.Point{X: pos.X + menuWidth, Y: pos.Y + menuHeight}}, // bottom
		{Min: pos, Max: image.Point{X: pos.X + 1, Y: pos.Y + menuHeight}},                                   // left
		{Min: image.Point{X: pos.X + menuWidth - 1, Y: pos.Y}, Max: image.Point{X: pos.X + menuWidth, Y: pos.Y + menuHeight}}, // right
	} {
		paint.FillShape(gtx.Ops, color.NRGBA{R: 80, G: 80, B: 90, A: 255}, edge.Op())
	}

	// Draw menu items
	for i, item := range w.contextMenu.items {
		itemY := pos.Y + i*itemHeight
		itemRect := clip.Rect{
			Min: image.Point{X: pos.X + 1, Y: itemY + 1},
			Max: image.Point{X: pos.X + menuWidth - 1, Y: itemY + itemHeight},
		}

		// Handle item input
		itemStack := itemRect.Push(gtx.Ops)
		event.Op(gtx.Ops, item)

		for {
			ev, ok := gtx.Event(
				pointer.Filter{Target: item, Kinds: pointer.Press | pointer.Enter | pointer.Leave},
			)
			if !ok {
				break
			}
			switch e := ev.(type) {
			case pointer.Event:
				switch e.Kind {
				case pointer.Enter:
					item.hovered = true
				case pointer.Leave:
					item.hovered = false
				case pointer.Press:
					if item.action != nil {
						item.action()
					}
				}
			}
		}
		itemStack.Pop()

		// Draw hover highlight
		if item.hovered {
			hoverRect := clip.Rect{
				Min: image.Point{X: pos.X + 2, Y: itemY + 1},
				Max: image.Point{X: pos.X + menuWidth - 2, Y: itemY + itemHeight - 1},
			}.Op()
			paint.FillShape(gtx.Ops, color.NRGBA{R: 80, G: 120, B: 200, A: 255}, hoverRect)
		}

		// Draw item label
		th := material.NewTheme()
		th.Shaper = w.shaper
		label := material.Label(th, unit.Sp(13), item.label)
		if item.hovered {
			label.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		} else {
			label.Color = color.NRGBA{R: 220, G: 220, B: 220, A: 255}
		}

		labelStack := op.Offset(image.Pt(pos.X+12, itemY+6)).Push(gtx.Ops)
		labelGtx := gtx
		labelGtx.Constraints = layout.Constraints{
			Min: image.Point{},
			Max: image.Point{X: menuWidth - 24, Y: itemHeight},
		}
		label.Layout(labelGtx)
		labelStack.Pop()
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
