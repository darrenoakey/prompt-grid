package gui

import (
	_ "embed"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"prompt-grid/src/pty"
	"prompt-grid/src/render"
)

const sidebarWidth = 240
const headerHeight = 56

//go:embed logo.png
var logoBytes []byte

// ControlWindow is the control center showing all sessions
type ControlWindow struct {
	app            *App
	window         *app.Window
	shaper         *text.Shaper
	theme          *material.Theme            // Persistent theme (avoids per-frame allocation)
	ops            op.Ops
	selected       string
	tabStates      map[string]*tabState       // Persistent tab states keyed by name
	termWidgets    map[string]*TerminalWidget // Persistent terminal widgets keyed by session name
	contextMenu      *contextMenuState          // Right-click context menu
	menuOverlay      *menuOverlay               // Fullscreen overlay to catch clicks outside menu
	tabPanelBg       *tabPanelBackground        // For right-click on empty tab area
	renameState      *renameState               // For renaming sessions
	newSessionState  *newSessionState           // For creating new sessions with inline name input
	focusTerminal    bool                       // One-shot: request focus for terminal widget next frame
	lastTermSize     image.Point               // Last terminal area size (pixels) for resize detection
	lastWindowSize   image.Point               // Last window size for tracking changes
	logoImage        image.Image               // Embedded logo
	searchEditor     widget.Editor             // Search input
	searchQuery      string                    // Current search query
	newSessionBtn    *sessionButton            // "NEW SESSION" button target
}

// sessionButton is a persistent target for the NEW SESSION button
type sessionButton struct{}

// menuOverlay is used to catch clicks outside the context menu
type menuOverlay struct{}

// tabPanelBackground is a persistent target for right-click on empty tab area
type tabPanelBackground struct{}

// renameState tracks active rename operation
type renameState struct {
	active      bool
	sessionName string
	newName     string
	cursorPos   int
}

// newSessionState tracks new session creation with inline name input
type newSessionState struct {
	active    bool
	name      string
	cursorPos int
}

type tabState struct {
	name    string
	hovered bool
	pressed bool
}

type contextMenuState struct {
	visible         bool
	sessionName     string
	position        image.Point
	items           []*menuItem
	activeSubmenuIdx int // Index of parent item whose submenu is open (-1 = none)
}

type menuItem struct {
	label   string
	action  func()
	hovered bool
	submenu []*menuItem // If non-nil, hovering shows a submenu
}

// NewControlWindow creates the control center window
func NewControlWindow(application *App) *ControlWindow {
	win := &ControlWindow{
		app:             application,
		window:          new(app.Window),
		tabStates:       make(map[string]*tabState),
		termWidgets:     make(map[string]*TerminalWidget),
		contextMenu:     &contextMenuState{},
		menuOverlay:     &menuOverlay{},
		tabPanelBg:      &tabPanelBackground{},
		renameState:     &renameState{},
		newSessionState: &newSessionState{},
		newSessionBtn:   &sessionButton{},
	}

	// Load embedded logo
	var err error
	win.logoImage, _, err = image.Decode(strings.NewReader(string(logoBytes)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load logo: %v\n", err)
	}

	// Restore window size from config, or use default
	width, height := 1000, 600
	if application.config != nil {
		if w, h, ok := application.config.GetControlCenterSize(); ok {
			width, height = w, h
		}
	}

	win.window.Option(
		app.Title("prompt-grid"),
		app.Size(unit.Dp(width), unit.Dp(height)),
	)

	win.shaper = text.NewShaper(text.WithCollection(render.CreateFontCollection()))
	win.theme = material.NewTheme()
	win.theme.Shaper = win.shaper

	// Initialize search editor
	win.searchEditor.SingleLine = true
	win.searchEditor.Submit = false

	return win
}

// Run starts the control window event loop
func (w *ControlWindow) Run() error {
	var frameCount int
	for {
		switch e := w.window.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			frameCount++
			if frameCount%10000 == 0 {
				fmt.Fprintf(os.Stderr, "DIAG: control window frame %d\n", frameCount)
			}
			gtx := app.NewContext(&w.ops, e)
			w.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

// setSelected updates the selected session and persists it to config
func (w *ControlWindow) setSelected(name string) {
	w.selected = name
	if w.app.config != nil {
		w.app.config.SetLastSelected(name)
		w.app.saveConfig()
	}
}

func (w *ControlWindow) layout(gtx layout.Context) {
	// Track window size changes and save to config
	currentSize := image.Point{X: gtx.Constraints.Max.X, Y: gtx.Constraints.Max.Y}
	if currentSize != w.lastWindowSize && currentSize.X > 0 && currentSize.Y > 0 {
		w.lastWindowSize = currentSize
		if w.app.config != nil {
			w.app.config.SetControlCenterSize(currentSize.X, currentSize.Y)
			w.app.saveConfig()
		}
	}

	// Get current sessions
	sessions := w.app.ListSessions()

	// Ensure we have persistent tab state for each session
	for _, name := range sessions {
		if _, exists := w.tabStates[name]; !exists {
			w.tabStates[name] = &tabState{name: name}
		}
	}

	// Clean up stale tab states and term widgets
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
	for name := range w.termWidgets {
		found := false
		for _, s := range sessions {
			if s == name {
				found = true
				break
			}
		}
		if !found {
			delete(w.termWidgets, name)
		}
	}

	// Auto-select session: try last selected, then first available
	if len(sessions) > 0 {
		if w.selected == "" {
			// Try to restore last selected session from config
			if w.app.config != nil {
				lastSelected := w.app.config.GetLastSelected()
				found := false
				for _, s := range sessions {
					if s == lastSelected {
						w.selected = lastSelected
						found = true
						break
					}
				}
				if !found {
					w.selected = sessions[0]
				}
			} else {
				w.selected = sessions[0]
			}
			w.focusTerminal = true
		} else {
			// Fix stale selection (e.g., after async rename)
			found := false
			for _, s := range sessions {
				if s == w.selected {
					found = true
					break
				}
			}
			if !found {
				w.selected = sessions[0]
				w.focusTerminal = true
			}
		}
	}

	// Handle keyboard input: rename handler OR new session handler OR terminal forwarding
	if w.renameState.active {
		w.handleRenameInput(gtx)
	} else if w.newSessionState.active {
		w.handleNewSessionInput(gtx)
	} else {
		w.handleTerminalKeyboard(gtx)
	}

	// Handle clicks to dismiss context menu (fullscreen overlay to catch all clicks)
	if w.contextMenu.visible {
		// Create a fullscreen overlay to intercept ALL clicks
		areaStack := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
		event.Op(gtx.Ops, w.menuOverlay)

		for {
			ev, ok := gtx.Event(
				pointer.Filter{Target: w.menuOverlay, Kinds: pointer.Press},
			)
			if !ok {
				break
			}
			if e, ok := ev.(pointer.Event); ok && e.Kind == pointer.Press {
				// Check if click is outside the menu (and submenu if visible)
				menuWidth := 180
				itemHeight := 28
				menuHeight := len(w.contextMenu.items) * itemHeight
				pos := w.contextMenu.position
				clickX, clickY := int(e.Position.X), int(e.Position.Y)

				inMain := clickX >= pos.X && clickX <= pos.X+menuWidth &&
					clickY >= pos.Y && clickY <= pos.Y+menuHeight

				// Check if click is in submenu area (may span multiple columns)
				inSub := false
				idx := w.contextMenu.activeSubmenuIdx
				if idx >= 0 && idx < len(w.contextMenu.items) {
					parent := w.contextMenu.items[idx]
					if parent.submenu != nil {
						subX := pos.X + menuWidth
						submenuY := pos.Y + idx*itemHeight
						subW := 200
						maxScreenY := gtx.Constraints.Max.Y
						maxItemsPerCol := maxScreenY / itemHeight
						if maxItemsPerCol < 1 {
							maxItemsPerCol = 1
						}
						totalItems := len(parent.submenu)
						numCols := (totalItems + maxItemsPerCol - 1) / maxItemsPerCol
						if numCols < 1 {
							numCols = 1
						}
						itemsPerCol := (totalItems + numCols - 1) / numCols

						for col := 0; col < numCols; col++ {
							colStart := col * itemsPerCol
							colEnd := colStart + itemsPerCol
							if colEnd > totalItems {
								colEnd = totalItems
							}
							colItems := colEnd - colStart
							colX := subX + col*subW
							colHeight := colItems * itemHeight
							colY := submenuY
							if colY+colHeight > maxScreenY {
								colY = maxScreenY - colHeight
							}
							if colY < 0 {
								colY = 0
							}
							if clickX >= colX && clickX <= colX+subW &&
								clickY >= colY && clickY <= colY+colHeight {
								inSub = true
								break
							}
						}
					}
				}

				if !inMain && !inSub {
					w.contextMenu.visible = false
				}
			}
		}
		areaStack.Pop()
	}

	// New layout: [Header] over [Sidebar | Sep | (Terminal over StatusBar)]
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return w.layoutHeader(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			// Main area: sidebar | separator | terminal with status bar
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return w.layoutSidebar(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return w.layoutSeparator(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					// Terminal area with status bar at bottom
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return w.layoutTerminal(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return w.layoutStatusBar(gtx)
						}),
					)
				}),
			)
		}),
	)

	// Draw context menu on top of everything
	w.layoutContextMenu(gtx)
}

func (w *ControlWindow) layoutHeader(gtx layout.Context) layout.Dimensions {
	// Header bar background
	headerBg := color.NRGBA{R: 20, G: 20, B: 20, A: 255}
	rect := clip.Rect{Max: image.Point{X: gtx.Constraints.Max.X, Y: headerHeight}}.Op()
	paint.FillShape(gtx.Ops, headerBg, rect)

	// Logo: anchored left, centered vertically
	if w.logoImage != nil {
		logoMaxW := 160
		logoMaxH := 40
		// Calculate aspect-ratio-aware fit
		imgBounds := w.logoImage.Bounds()
		imgW := float32(imgBounds.Dx())
		imgH := float32(imgBounds.Dy())
		scaleW := float32(logoMaxW) / imgW
		scaleH := float32(logoMaxH) / imgH
		scale := scaleW
		if scaleH < scaleW {
			scale = scaleH
		}
		finalW := int(imgW * scale)
		finalH := int(imgH * scale)

		logoOp := paint.NewImageOp(w.logoImage)
		logoX := 16
		logoY := (headerHeight - finalH) / 2
		logoStack := op.Offset(image.Pt(logoX, logoY)).Push(gtx.Ops)
		logoGtx := gtx
		logoGtx.Constraints.Max = image.Point{X: finalW, Y: finalH}
		logoGtx.Constraints.Min = image.Point{X: finalW, Y: finalH}
		widget.Image{Src: logoOp, Fit: widget.Contain}.Layout(logoGtx)
		logoStack.Pop()
	}

	// Search bar: anchored center horizontally and vertically
	searchWidth := 400
	searchHeight := 32
	searchX := (gtx.Constraints.Max.X - searchWidth) / 2
	searchY := (headerHeight - searchHeight) / 2
	searchStack := op.Offset(image.Pt(searchX, searchY)).Push(gtx.Ops)
	searchGtx := gtx
	searchGtx.Constraints.Max.X = searchWidth
	searchGtx.Constraints.Min.X = searchWidth
	w.layoutSearchBar(searchGtx)
	searchStack.Pop()

	// Discord status: anchored right, centered vertically
	statusGtx := gtx
	statusGtx.Constraints.Max = image.Point{X: 200, Y: headerHeight}
	statusText := "Discord: offline"
	if w.app.IsDiscordConnected() {
		statusText = "Discord: online"
	}
	// Approximate width: 8px dot + 8px gap + text width (roughly 7px per char for 12sp)
	approxWidth := 8 + 8 + len(statusText)*7
	statusX := gtx.Constraints.Max.X - approxWidth - 16
	statusY := (headerHeight - 12) / 2 // 12sp text height approximation

	statusStack := op.Offset(image.Pt(statusX, statusY)).Push(gtx.Ops)
	w.layoutDiscordStatusHeader(statusGtx)
	statusStack.Pop()

	return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Max.X, Y: headerHeight}}
}

func (w *ControlWindow) layoutSearchBar(gtx layout.Context) layout.Dimensions {
	// Update search query from editor
	for {
		ev, ok := w.searchEditor.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.ChangeEvent); ok {
			w.searchQuery = strings.ToLower(w.searchEditor.Text())
		}
	}

	searchHeight := 32
	// Search bar styling
	searchBg := color.NRGBA{R: 12, G: 12, B: 12, A: 255}
	borderColor := color.NRGBA{R: 51, G: 51, B: 51, A: 255}

	// Draw background
	bgRect := clip.Rect{Max: image.Point{X: gtx.Constraints.Max.X, Y: searchHeight}}.Op()
	paint.FillShape(gtx.Ops, searchBg, bgRect)

	// Draw border
	for _, edge := range []clip.Rect{
		{Min: image.Point{X: 0, Y: 0}, Max: image.Point{X: gtx.Constraints.Max.X, Y: 1}},
		{Min: image.Point{X: 0, Y: searchHeight - 1}, Max: image.Point{X: gtx.Constraints.Max.X, Y: searchHeight}},
		{Min: image.Point{X: 0, Y: 0}, Max: image.Point{X: 1, Y: searchHeight}},
		{Min: image.Point{X: gtx.Constraints.Max.X - 1, Y: 0}, Max: image.Point{X: gtx.Constraints.Max.X, Y: searchHeight}},
	} {
		paint.FillShape(gtx.Ops, borderColor, edge.Op())
	}

	// Layout editor
	editorStyle := material.Editor(w.theme, &w.searchEditor, "Search sessions...")
	editorStyle.Color = color.NRGBA{R: 224, G: 224, B: 224, A: 255}
	editorStyle.HintColor = color.NRGBA{R: 136, G: 136, B: 136, A: 255}
	editorStyle.TextSize = unit.Sp(14)

	stack := op.Offset(image.Pt(8, 6)).Push(gtx.Ops)
	editorGtx := gtx
	editorGtx.Constraints.Max.X = gtx.Constraints.Max.X - 16
	editorGtx.Constraints.Max.Y = searchHeight - 12
	editorStyle.Layout(editorGtx)
	stack.Pop()

	return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Max.X, Y: searchHeight}}
}

func (w *ControlWindow) layoutDiscordStatusHeader(gtx layout.Context) layout.Dimensions {
	// Discord status indicator in header (right side)
	dotSize := 8
	var statusColor color.NRGBA
	var statusText string
	if w.app.IsDiscordConnected() {
		statusColor = color.NRGBA{R: 0, G: 255, B: 0, A: 255} // Green
		statusText = "Discord: online"
	} else {
		statusColor = color.NRGBA{R: 136, G: 136, B: 136, A: 255} // Gray
		statusText = "Discord: offline"
	}

	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceEnd}.Layout(gtx,
		// Dot (rendered as small square for simplicity)
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			dotRect := clip.Rect{
				Min: image.Point{X: 0, Y: 0},
				Max: image.Point{X: dotSize, Y: dotSize},
			}.Op()
			paint.FillShape(gtx.Ops, statusColor, dotRect)
			return layout.Dimensions{Size: image.Point{X: dotSize, Y: dotSize}}
		}),
		// Text
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			label := material.Label(w.theme, unit.Sp(12), statusText)
			label.Color = color.NRGBA{R: 136, G: 136, B: 136, A: 255}
			stack := op.Offset(image.Pt(8, 0)).Push(gtx.Ops)
			d := label.Layout(gtx)
			stack.Pop()
			return layout.Dimensions{Size: image.Point{X: d.Size.X + 8, Y: d.Size.Y}}
		}),
	)
}

func (w *ControlWindow) layoutSidebar(gtx layout.Context) layout.Dimensions {
	// Fixed width for sidebar
	gtx.Constraints.Max.X = sidebarWidth
	gtx.Constraints.Min.X = sidebarWidth
	panelHeight := gtx.Constraints.Max.Y

	// Background
	sidebarBg := color.NRGBA{R: 26, G: 26, B: 26, A: 255}
	rect := clip.Rect{Max: image.Point{X: sidebarWidth, Y: panelHeight}}.Op()
	paint.FillShape(gtx.Ops, sidebarBg, rect)

	// Handle right-click on the sidebar background (empty area)
	bgAreaStack := clip.Rect{Max: image.Point{X: sidebarWidth, Y: panelHeight}}.Push(gtx.Ops)
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

	// Get sessions and apply search filter
	allSessions := w.app.ListSessions()
	var sessions []string
	if w.searchQuery != "" {
		for _, name := range allSessions {
			if strings.Contains(strings.ToLower(name), w.searchQuery) {
				sessions = append(sessions, name)
			}
		}
	} else {
		sessions = allSessions
	}

	// Layout sidebar sections vertically
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Header section: "SESSIONS (N)"
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			headerText := fmt.Sprintf("SESSIONS (%d)", len(allSessions))
			label := material.Label(w.theme, unit.Sp(10), headerText)
			label.Color = color.NRGBA{R: 136, G: 136, B: 136, A: 255}
			return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, label.Layout)
			})
		}),
		// Session list (scrollable area)
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			offsetY := 0

			// Show new session input at the top if active
			if w.newSessionState.active {
				stack := op.Offset(image.Pt(0, offsetY)).Push(gtx.Ops)
				d := w.layoutNewSessionTab(gtx)
				stack.Pop()
				offsetY += d.Size.Y
			}

			// Render session list items
			if len(sessions) == 0 && w.searchQuery != "" {
				// Show "No sessions found" message
				label := material.Label(w.theme, unit.Sp(12), "No sessions found")
				label.Color = color.NRGBA{R: 136, G: 136, B: 136, A: 255}
				stack := op.Offset(image.Pt(0, offsetY+20)).Push(gtx.Ops)
				layout.Center.Layout(gtx, label.Layout)
				stack.Pop()
			} else {
				for _, name := range sessions {
					tab := w.tabStates[name]
					if tab == nil {
						continue
					}
					stack := op.Offset(image.Pt(0, offsetY)).Push(gtx.Ops)
					d := w.layoutSessionItem(gtx, tab, offsetY)
					stack.Pop()
					offsetY += d.Size.Y
				}
			}

			return layout.Dimensions{Size: image.Point{X: sidebarWidth, Y: gtx.Constraints.Max.Y}}
		}),
		// Footer section: "NEW SESSION" button
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return w.layoutNewSessionButton(gtx)
		}),
	)
}

func (w *ControlWindow) layoutNewSessionButton(gtx layout.Context) layout.Dimensions {
	buttonHeight := 60
	btnPadding := 12
	innerBtnHeight := 36

	// Button area for event handling
	btnRect := clip.Rect{
		Min: image.Point{X: btnPadding, Y: btnPadding},
		Max: image.Point{X: sidebarWidth - btnPadding, Y: btnPadding + innerBtnHeight},
	}
	btnStack := btnRect.Push(gtx.Ops)
	event.Op(gtx.Ops, w.newSessionBtn)

	// Handle button clicks
	var hovered bool
	for {
		ev, ok := gtx.Event(
			pointer.Filter{Target: w.newSessionBtn, Kinds: pointer.Press | pointer.Enter | pointer.Leave},
		)
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			switch e.Kind {
			case pointer.Enter:
				hovered = true
			case pointer.Leave:
				hovered = false
			case pointer.Press:
				w.startNewSession()
			}
		}
	}
	btnStack.Pop()

	// Button background (cyan with opacity)
	accentColor := color.NRGBA{R: 0, G: 255, B: 200, A: 25}
	if hovered {
		accentColor = color.NRGBA{R: 0, G: 255, B: 200, A: 51}
	}
	bgRect := clip.Rect{
		Min: image.Point{X: btnPadding, Y: btnPadding},
		Max: image.Point{X: sidebarWidth - btnPadding, Y: btnPadding + innerBtnHeight},
	}.Op()
	paint.FillShape(gtx.Ops, accentColor, bgRect)

	// Button border
	borderColor := color.NRGBA{R: 0, G: 255, B: 200, A: 76}
	for _, edge := range []clip.Rect{
		{Min: image.Point{X: btnPadding, Y: btnPadding}, Max: image.Point{X: sidebarWidth - btnPadding, Y: btnPadding + 1}},
		{Min: image.Point{X: btnPadding, Y: btnPadding + innerBtnHeight - 1}, Max: image.Point{X: sidebarWidth - btnPadding, Y: btnPadding + innerBtnHeight}},
		{Min: image.Point{X: btnPadding, Y: btnPadding}, Max: image.Point{X: btnPadding + 1, Y: btnPadding + innerBtnHeight}},
		{Min: image.Point{X: sidebarWidth - btnPadding - 1, Y: btnPadding}, Max: image.Point{X: sidebarWidth - btnPadding, Y: btnPadding + innerBtnHeight}},
	} {
		paint.FillShape(gtx.Ops, borderColor, edge.Op())
	}

	// Button text - centered
	label := material.Label(w.theme, unit.Sp(12), "NEW SESSION")
	label.Color = color.NRGBA{R: 0, G: 255, B: 200, A: 255}
	label.Font.Weight = font.Bold

	// Center the text vertically and horizontally
	textY := btnPadding + (innerBtnHeight-12)/2 - 2 // 12 is approx text height
	labelStack := op.Offset(image.Pt(sidebarWidth/2-50, textY)).Push(gtx.Ops)
	label.Layout(gtx)
	labelStack.Pop()

	return layout.Dimensions{Size: image.Point{X: sidebarWidth, Y: buttonHeight}}
}

func (w *ControlWindow) layoutSessionItem(gtx layout.Context, tab *tabState, offsetY int) layout.Dimensions {
	itemHeight := 36
	isSelected := tab.name == w.selected
	isRenaming := w.renameState.active && w.renameState.sessionName == tab.name

	// Get session color (for the dot)
	state := w.app.GetSession(tab.name)
	var sessionColor color.NRGBA
	if state != nil {
		sessionColor = state.Colors().Background
	} else {
		sessionColor = color.NRGBA{R: 60, G: 60, B: 60, A: 255}
	}

	// Handle input (only if not renaming)
	if !isRenaming {
		areaStack := clip.Rect{Max: image.Point{X: sidebarWidth, Y: itemHeight}}.Push(gtx.Ops)
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
						// Left-click - select tab and focus terminal
						w.setSelected(tab.name)
						w.focusTerminal = true
						w.lastTermSize = image.Point{} // Force resize for new session
						w.contextMenu.visible = false  // Close context menu on left click
					}
				}
			}
		}
		areaStack.Pop()
	}

	// Background color based on state
	activeHighlight := color.NRGBA{R: 45, G: 45, B: 45, A: 255}
	if isSelected || tab.hovered {
		rect := clip.Rect{Max: image.Point{X: sidebarWidth, Y: itemHeight}}.Op()
		paint.FillShape(gtx.Ops, activeHighlight, rect)
	}

	// Left border (2px cyan for active session)
	if isSelected {
		accentColor := color.NRGBA{R: 0, G: 255, B: 200, A: 255}
		borderRect := clip.Rect{Max: image.Point{X: 2, Y: itemHeight}}.Op()
		paint.FillShape(gtx.Ops, accentColor, borderRect)
	}

	// Draw rename input or normal item content
	if isRenaming {
		w.layoutRenameInputInline(gtx, itemHeight)
	} else {
		// Colored dot (8px square, 12px from left edge) - vertically centered with text baseline
		dotSize := 8
		dotX := 12
		textHeight := 14 // Sp(14) approximate height
		textY := (itemHeight - textHeight) / 2
		dotY := textY + (textHeight-dotSize)/2 + 1 // Center dot with text baseline
		dotRect := clip.Rect{
			Min: image.Point{X: dotX, Y: dotY},
			Max: image.Point{X: dotX + dotSize, Y: dotY + dotSize},
		}.Op()
		paint.FillShape(gtx.Ops, sessionColor, dotRect)

		// Session name text (14px, #e0e0e0)
		textColor := color.NRGBA{R: 224, G: 224, B: 224, A: 255}
		label := material.Label(w.theme, unit.Sp(14), tab.name)
		label.Color = textColor

		// Position text: 12px (left margin) + 8px (dot) + 8px (gap from dot)
		textX := 12 + 8 + 8
		stack := op.Offset(image.Pt(textX, textY)).Push(gtx.Ops)
		labelGtx := gtx
		labelGtx.Constraints = layout.Constraints{
			Min: image.Point{X: 0, Y: 0},
			Max: image.Point{X: sidebarWidth - textX - 12, Y: itemHeight},
		}
		label.Layout(labelGtx)
		stack.Pop()
	}

	return layout.Dimensions{Size: image.Point{X: sidebarWidth, Y: itemHeight}}
}

// layoutRenameInputInline draws the rename text input inline in a session item
func (w *ControlWindow) layoutRenameInputInline(gtx layout.Context, height int) {
	// Draw input background (slightly darker)
	inputBg := clip.Rect{
		Min: image.Point{X: 8, Y: 4},
		Max: image.Point{X: sidebarWidth - 8, Y: height - 4},
	}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 20, G: 20, B: 20, A: 255}, inputBg)

	// Draw input border (cyan accent)
	borderColor := color.NRGBA{R: 0, G: 255, B: 200, A: 255}
	for _, edge := range []clip.Rect{
		{Min: image.Point{X: 8, Y: 4}, Max: image.Point{X: sidebarWidth - 8, Y: 5}},                 // top
		{Min: image.Point{X: 8, Y: height - 5}, Max: image.Point{X: sidebarWidth - 8, Y: height - 4}}, // bottom
		{Min: image.Point{X: 8, Y: 4}, Max: image.Point{X: 9, Y: height - 4}},                        // left
		{Min: image.Point{X: sidebarWidth - 9, Y: 4}, Max: image.Point{X: sidebarWidth - 8, Y: height - 4}}, // right
	} {
		paint.FillShape(gtx.Ops, borderColor, edge.Op())
	}

	// Draw text
	label := material.Label(w.theme, unit.Sp(14), w.renameState.newName)
	label.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	stack := op.Offset(image.Pt(12, (height-14)/2)).Push(gtx.Ops)
	labelGtx := gtx
	labelGtx.Constraints = layout.Constraints{
		Min: image.Point{X: 0, Y: 0},
		Max: image.Point{X: sidebarWidth - 24, Y: height},
	}
	label.Layout(labelGtx)
	stack.Pop()

	// Draw cursor
	charWidth := 8 // Approximate pixels per character at this font size
	cursorX := 12 + w.renameState.cursorPos*charWidth
	cursorRect := clip.Rect{
		Min: image.Point{X: cursorX, Y: (height - 18) / 2},
		Max: image.Point{X: cursorX + 1, Y: (height + 18) / 2},
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

	// Fill the entire area with dark background
	bgColor := color.NRGBA{R: 12, G: 12, B: 12, A: 255}
	rect := clip.Rect{Max: gtx.Constraints.Max}.Op()
	paint.FillShape(gtx.Ops, bgColor, rect)

	if state == nil {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}

	// Resize emulator/PTY to fit available space (like TerminalWindow does)
	padding := 8
	availW := gtx.Constraints.Max.X - padding*2
	availH := gtx.Constraints.Max.Y - padding*2
	termSize := image.Point{X: availW, Y: availH}
	if termSize != w.lastTermSize {
		w.lastTermSize = termSize
		cellW := int(float32(w.app.FontSize()) * 0.6)
		cellH := int(float32(w.app.FontSize()) * 1.5)
		if cellW > 0 && cellH > 0 {
			newCols := availW / cellW
			newRows := availH / cellH
			if newCols > 0 && newRows > 0 {
				state.screen.Resize(newCols, newRows)
				state.pty.Resize(pty.Size{Cols: uint16(newCols), Rows: uint16(newRows)})
			}
		}
	}

	// Get or create persistent widget for this session
	// Must persist across frames for event routing to work
	widget, ok := w.termWidgets[w.selected]
	if !ok {
		widget = NewTerminalWidget(state, state.Colors(), w.app.FontSize(), w.shaper)
		w.termWidgets[w.selected] = widget
	}

	// Control center handles keyboard at window level; widget handles only mouse events
	widget.skipKeyboard = true
	widget.requestFocus = false

	// Layout terminal in the available space
	stack := op.Offset(image.Pt(padding, padding)).Push(gtx.Ops)
	paddedGtx := gtx
	paddedGtx.Constraints.Max.X = availW
	paddedGtx.Constraints.Max.Y = availH
	paddedGtx.Constraints.Min = image.Point{}
	widget.Layout(paddedGtx)
	stack.Pop()

	return layout.Dimensions{Size: gtx.Constraints.Max}
}

func (w *ControlWindow) layoutStatusBar(gtx layout.Context) layout.Dimensions {
	statusBarHeight := 32
	// Status bar background (bright cyan accent)
	accentColor := color.NRGBA{R: 0, G: 255, B: 200, A: 255}
	rect := clip.Rect{Max: image.Point{X: gtx.Constraints.Max.X, Y: statusBarHeight}}.Op()
	paint.FillShape(gtx.Ops, accentColor, rect)

	// Display selected session name or empty message
	var statusText string
	if w.selected != "" {
		statusText = w.selected
	} else {
		statusText = "No session selected"
	}

	// Status text (centered both horizontally and vertically, bold, dark text on cyan background)
	label := material.Label(w.theme, unit.Sp(14), statusText)
	label.Color = color.NRGBA{R: 12, G: 12, B: 12, A: 255} // Dark text on bright background
	label.Font.Weight = font.Bold

	// Approximate text dimensions for centering (14sp ≈ 14px height, ~8px per char width)
	approxTextHeight := 14
	approxTextWidth := len(statusText) * 8
	textX := (gtx.Constraints.Max.X - approxTextWidth) / 2
	textY := (statusBarHeight - approxTextHeight) / 2

	textStack := op.Offset(image.Pt(textX, textY)).Push(gtx.Ops)
	label.Layout(gtx)
	textStack.Pop()

	return layout.Dimensions{Size: image.Point{X: gtx.Constraints.Max.X, Y: statusBarHeight}}
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
			w.startNewSession()
			w.window.Invalidate()
		},
	})

	// "New Claude Session" with submenu of ~/src directories
	if dirs := listSrcDirs(); len(dirs) > 0 {
		claudeItem := &menuItem{label: "New Claude \u25b8"}
		for _, dirName := range dirs {
			dn := dirName // capture for closure
			home, _ := os.UserHomeDir()
			fullPath := filepath.Join(home, "src", dn)
			claudeItem.submenu = append(claudeItem.submenu, &menuItem{
				label: dn,
				action: func() {
					w.contextMenu.visible = false
					w.window.Invalidate()
					// Pick a unique session name
					name := dn
					if w.app.GetSession(name) != nil {
						for i := 2; ; i++ {
							candidate := fmt.Sprintf("%s-%d", dn, i)
							if w.app.GetSession(candidate) == nil {
								name = candidate
								break
							}
						}
					}
					go func() {
						err := w.app.AddClaudeSession(name, fullPath)
						if err == nil {
							w.setSelected(name)
							w.focusTerminal = true
							w.window.Invalidate()
						}
					}()
				},
			})
		}
		items = append(items, claudeItem)
	}

	// "New Codex Session" with submenu of ~/src directories
	if dirs := listSrcDirs(); len(dirs) > 0 {
		codexItem := &menuItem{label: "New Codex \u25b8"}
		for _, dirName := range dirs {
			dn := dirName // capture for closure
			home, _ := os.UserHomeDir()
			fullPath := filepath.Join(home, "src", dn)
			codexItem.submenu = append(codexItem.submenu, &menuItem{
				label: dn,
				action: func() {
					w.contextMenu.visible = false
					w.window.Invalidate()
					// Pick a unique session name
					name := dn
					if w.app.GetSession(name) != nil {
						for i := 2; ; i++ {
							candidate := fmt.Sprintf("%s-%d", dn, i)
							if w.app.GetSession(candidate) == nil {
								name = candidate
								break
							}
						}
					}
					go func() {
						err := w.app.AddCodexSession(name, fullPath)
						if err == nil {
							w.setSelected(name)
							w.focusTerminal = true
							w.window.Invalidate()
						}
					}()
				},
			})
		}
		items = append(items, codexItem)
	}

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
			label: "New Color",
			action: func() {
				w.contextMenu.visible = false
				w.window.Invalidate()
				go func() {
					w.app.RecolorSession(sessionName)
				}()
			},
		})

		// Dynamic window items based on whether session has a standalone window
		state := w.app.GetSession(sessionName)
		if state != nil && state.window == nil {
			items = append(items, &menuItem{
				label: "Pop Out",
				action: func() {
					w.contextMenu.visible = false
					w.window.Invalidate()
					go w.app.PopOutSession(sessionName)
				},
			})
		}
		if state != nil && state.window != nil {
			items = append(items, &menuItem{
				label: "Bring to Front",
				action: func() {
					w.contextMenu.visible = false
					w.window.Invalidate()
					go func() {
						if s := w.app.GetSession(sessionName); s != nil && s.window != nil {
							s.window.BringToFront()
						}
					}()
				},
			})
			items = append(items, &menuItem{
				label: "Call Back",
				action: func() {
					w.contextMenu.visible = false
					w.window.Invalidate()
					go w.app.CallBackSession(sessionName)
				},
			})
		}

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
	w.contextMenu.activeSubmenuIdx = -1
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

// listSrcDirs returns directory names under ~/src (non-hidden, sorted)
func listSrcDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(home, "src"))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs
}

// startRename begins the rename operation for a session
func (w *ControlWindow) startRename(sessionName string) {
	w.renameState.active = true
	w.renameState.sessionName = sessionName
	w.renameState.newName = sessionName
	w.renameState.cursorPos = len(sessionName)
}

// insertRenameChar inserts text at the current cursor position during rename
func (w *ControlWindow) insertRenameChar(text string) {
	before := w.renameState.newName[:w.renameState.cursorPos]
	after := w.renameState.newName[w.renameState.cursorPos:]
	w.renameState.newName = before + text + after
	w.renameState.cursorPos += len(text)
}

// cancelRename cancels the rename operation
func (w *ControlWindow) cancelRename() {
	w.renameState.active = false
	w.renameState.sessionName = ""
	w.renameState.newName = ""
	w.renameState.cursorPos = 0
	w.focusTerminal = true // Give focus back to terminal widget
}

// confirmRename applies the rename asynchronously.
// RenameSession runs a tmux subprocess and calls SetTitle (window.Option),
// both of which block/deadlock the Cocoa main thread if called from a frame handler.
func (w *ControlWindow) confirmRename() {
	if w.renameState.newName != "" && w.renameState.newName != w.renameState.sessionName {
		oldName := w.renameState.sessionName
		newName := w.renameState.newName

		// Update selection to track the renamed session
		if w.selected == oldName {
			w.setSelected(newName)
		}

		go func() {
			w.app.RenameSession(oldName, newName)
			w.window.Invalidate()
		}()
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
				case key.NameSpace:
					w.insertRenameChar(" ")
				default:
					// Handle regular character input via key.Event
					// (key.EditEvent may not be delivered depending on focus)
					if len(e.Name) == 1 {
						ch := e.Name[0]
						var text string
						if e.Modifiers.Contain(key.ModShift) {
							text = string(rune(shiftChar(ch)))
						} else if ch >= 'A' && ch <= 'Z' {
							text = string(rune(ch + 32))
						} else {
							text = string(rune(ch))
						}
						w.insertRenameChar(text)
					}
				}
			}
		}
	}
	areaStack.Pop()
}

// startNewSession begins the new session creation flow with inline name input
func (w *ControlWindow) startNewSession() {
	w.newSessionState.active = true
	w.newSessionState.name = ""
	w.newSessionState.cursorPos = 0
}

// cancelNewSession cancels the new session creation
func (w *ControlWindow) cancelNewSession() {
	w.newSessionState.active = false
	w.newSessionState.name = ""
	w.newSessionState.cursorPos = 0
	w.focusTerminal = true
}

// confirmNewSession creates the session with the entered name
func (w *ControlWindow) confirmNewSession() {
	if w.newSessionState.name != "" {
		name := w.newSessionState.name
		go func() {
			err := w.app.AddSession(name, "")
			if err == nil {
				w.setSelected(name)
				w.focusTerminal = true
				w.window.Invalidate()
			}
		}()
	}
	w.cancelNewSession()
}

// layoutNewSessionTab renders the new session name input item
func (w *ControlWindow) layoutNewSessionTab(gtx layout.Context) layout.Dimensions {
	itemHeight := 36

	// Background (highlighted with cyan accent)
	activeHighlight := color.NRGBA{R: 45, G: 45, B: 45, A: 255}
	rect := clip.Rect{Max: image.Point{X: sidebarWidth, Y: itemHeight}}.Op()
	paint.FillShape(gtx.Ops, activeHighlight, rect)

	// Left border (cyan)
	accentColor := color.NRGBA{R: 0, G: 255, B: 200, A: 255}
	borderRect := clip.Rect{Max: image.Point{X: 2, Y: itemHeight}}.Op()
	paint.FillShape(gtx.Ops, accentColor, borderRect)

	// Draw text
	label := material.Label(w.theme, unit.Sp(14), w.newSessionState.name)
	label.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	textX := 12 + 8 + 8 // Match session item text position
	stack := op.Offset(image.Pt(textX, (itemHeight-14)/2)).Push(gtx.Ops)
	labelGtx := gtx
	labelGtx.Constraints = layout.Constraints{
		Min: image.Point{X: 0, Y: 0},
		Max: image.Point{X: sidebarWidth - textX - 12, Y: itemHeight},
	}
	label.Layout(labelGtx)
	stack.Pop()

	// Draw cursor
	charWidth := 8
	cursorX := textX + w.newSessionState.cursorPos*charWidth
	cursorRect := clip.Rect{
		Min: image.Point{X: cursorX, Y: (itemHeight - 18) / 2},
		Max: image.Point{X: cursorX + 1, Y: (itemHeight + 18) / 2},
	}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, cursorRect)

	return layout.Dimensions{Size: image.Point{X: sidebarWidth, Y: itemHeight}}
}

// handleNewSessionInput processes keyboard input during new session creation
func (w *ControlWindow) handleNewSessionInput(gtx layout.Context) {
	// Set up input area
	areaStack := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, w.newSessionState)

	// Request keyboard focus
	gtx.Execute(key.FocusCmd{Tag: w.newSessionState})

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
				before := w.newSessionState.name[:w.newSessionState.cursorPos]
				after := w.newSessionState.name[w.newSessionState.cursorPos:]
				w.newSessionState.name = before + e.Text + after
				w.newSessionState.cursorPos += len(e.Text)
			}
		case key.Event:
			if e.State == key.Press {
				switch e.Name {
				case key.NameReturn, key.NameEnter:
					w.confirmNewSession()
				case key.NameEscape:
					w.cancelNewSession()
				case key.NameDeleteBackward:
					if w.newSessionState.cursorPos > 0 {
						before := w.newSessionState.name[:w.newSessionState.cursorPos-1]
						after := w.newSessionState.name[w.newSessionState.cursorPos:]
						w.newSessionState.name = before + after
						w.newSessionState.cursorPos--
					}
				case key.NameDeleteForward:
					if w.newSessionState.cursorPos < len(w.newSessionState.name) {
						before := w.newSessionState.name[:w.newSessionState.cursorPos]
						after := w.newSessionState.name[w.newSessionState.cursorPos+1:]
						w.newSessionState.name = before + after
					}
				case key.NameLeftArrow:
					if w.newSessionState.cursorPos > 0 {
						w.newSessionState.cursorPos--
					}
				case key.NameRightArrow:
					if w.newSessionState.cursorPos < len(w.newSessionState.name) {
						w.newSessionState.cursorPos++
					}
				case key.NameHome:
					w.newSessionState.cursorPos = 0
				case key.NameEnd:
					w.newSessionState.cursorPos = len(w.newSessionState.name)
				case key.NameSpace:
					before := w.newSessionState.name[:w.newSessionState.cursorPos]
					after := w.newSessionState.name[w.newSessionState.cursorPos:]
					w.newSessionState.name = before + " " + after
					w.newSessionState.cursorPos++
				default:
					// Handle regular character input
					if len(e.Name) == 1 {
						ch := e.Name[0]
						var text string
						if e.Modifiers.Contain(key.ModShift) {
							text = string(rune(shiftChar(ch)))
						} else {
							text = string(rune(ch))
						}
						before := w.newSessionState.name[:w.newSessionState.cursorPos]
						after := w.newSessionState.name[w.newSessionState.cursorPos:]
						w.newSessionState.name = before + text + after
						w.newSessionState.cursorPos += len(text)
					}
				}
			}
		}
	}
	areaStack.Pop()
}

// handleTerminalKeyboard handles keyboard input at the window level and forwards to the PTY.
// This is used instead of widget-level keyboard handling because Gio's focus model
// in the control center steals focus from the embedded terminal widget.
func (w *ControlWindow) handleTerminalKeyboard(gtx layout.Context) {
	state := w.app.GetSession(w.selected)
	if state == nil {
		return
	}

	// Register the control window as the keyboard event target
	areaStack := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, w)
	gtx.Execute(key.FocusCmd{Tag: w})
	areaStack.Pop()

	// Process key events — same filters as standalone TerminalWidget
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
			fmt.Fprintf(os.Stderr, "PASTE-DBG: EditEvent Text=%q len=%d\n", e.Text, len(e.Text))
			state.ClearSelection()
			if len(e.Text) > 0 {
				state.pty.Write([]byte(e.Text))
			}
		case key.Event:
			if e.State == key.Press {
				fmt.Fprintf(os.Stderr, "PASTE-DBG: KeyEvent Name=%q Mods=%v State=%v\n", e.Name, e.Modifiers, e.State)
				if e.Modifiers.Contain(key.ModCommand) && e.Name == "C" {
					// Cmd+C: copy selection
					if state.HasSelection() {
						selectedText := state.GetSelectedText()
						if len(selectedText) > 0 {
							gtx.Execute(clipboard.WriteCmd{
								Type: "application/text",
								Data: io.NopCloser(strings.NewReader(selectedText)),
							})
						}
					}
				} else if e.Modifiers.Contain(key.ModCommand) && e.Name == "X" {
					// Cmd+X: cut (copy + clear selection)
					if state.HasSelection() {
						selectedText := state.GetSelectedText()
						if len(selectedText) > 0 {
							gtx.Execute(clipboard.WriteCmd{
								Type: "application/text",
								Data: io.NopCloser(strings.NewReader(selectedText)),
							})
						}
						state.ClearSelection()
					}
				} else if e.Modifiers.Contain(key.ModCommand) && e.Name == "V" {
					// Cmd+V: paste — request clipboard read
					gtx.Execute(clipboard.ReadCmd{Tag: w})
				} else {
					state.ClearSelection()
					w.forwardKeyToSession(state, e)
				}
			}
		}
	}

	// Process clipboard paste data
	for {
		ev, ok := gtx.Event(
			transfer.TargetFilter{Target: w, Type: "application/text"},
		)
		if !ok {
			break
		}
		if e, ok := ev.(transfer.DataEvent); ok {
			fmt.Fprintf(os.Stderr, "PASTE-DBG: DataEvent Type=%q\n", e.Type)
			data := e.Open()
			if data != nil {
				content, _ := io.ReadAll(data)
				data.Close()
				fmt.Fprintf(os.Stderr, "PASTE-DBG: DataEvent content=%q len=%d\n", content, len(content))
				if len(content) > 0 {
					state.pty.Write(content)
				}
			}
		}
	}
}

// forwardKeyToSession converts a Gio key event to bytes and writes to the session PTY
func (w *ControlWindow) forwardKeyToSession(state *SessionState, e key.Event) {
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

func (w *ControlWindow) layoutContextMenu(gtx layout.Context) {
	if !w.contextMenu.visible {
		return
	}

	itemHeight := 28
	menuWidth := 180
	menuHeight := len(w.contextMenu.items) * itemHeight

	// Menu position
	pos := w.contextMenu.position

	// Draw menu panel
	w.drawMenuPanel(gtx, pos, menuWidth, menuHeight)

	// Draw menu items — track which parent should show a submenu.
	// We use activeSubmenuIdx (sticky) instead of item.hovered to avoid
	// the submenu disappearing when the mouse crosses the gap to the submenu.
	for i, item := range w.contextMenu.items {
		itemY := pos.Y + i*itemHeight
		w.drawMenuItem(gtx, item, pos.X, itemY, menuWidth, itemHeight, false)
		if item.hovered {
			if item.submenu != nil {
				w.contextMenu.activeSubmenuIdx = i
			} else {
				w.contextMenu.activeSubmenuIdx = -1
			}
		}
	}

	// Render submenu if active (no gap — flush against main menu)
	idx := w.contextMenu.activeSubmenuIdx
	if idx >= 0 && idx < len(w.contextMenu.items) {
		parent := w.contextMenu.items[idx]
		if parent.submenu != nil {
			subX := pos.X + menuWidth // No gap
			submenuY := pos.Y + idx*itemHeight
			subWidth := 200
			maxScreenY := gtx.Constraints.Max.Y

			// Calculate how many items fit in one column
			maxItemsPerCol := maxScreenY / itemHeight
			if maxItemsPerCol < 1 {
				maxItemsPerCol = 1
			}
			totalItems := len(parent.submenu)
			numCols := (totalItems + maxItemsPerCol - 1) / maxItemsPerCol
			if numCols < 1 {
				numCols = 1
			}
			// Distribute items evenly across columns
			itemsPerCol := (totalItems + numCols - 1) / numCols

			// Draw one panel per column
			for col := 0; col < numCols; col++ {
				colStart := col * itemsPerCol
				colEnd := colStart + itemsPerCol
				if colEnd > totalItems {
					colEnd = totalItems
				}
				colItems := colEnd - colStart
				colX := subX + col*subWidth
				colHeight := colItems * itemHeight

				// First column clamps Y; additional columns align to top (Y=0)
				colY := submenuY
				if colY+colHeight > maxScreenY {
					colY = maxScreenY - colHeight
				}
				if colY < 0 {
					colY = 0
				}

				w.drawMenuPanel(gtx, image.Point{X: colX, Y: colY}, subWidth, colHeight)
				for i := colStart; i < colEnd; i++ {
					subItemY := colY + (i-colStart)*itemHeight
					w.drawMenuItem(gtx, parent.submenu[i], colX, subItemY, subWidth, itemHeight, true)
				}
			}
		}
	}
}

// drawMenuPanel draws the background, shadow, and border for a menu panel
func (w *ControlWindow) drawMenuPanel(gtx layout.Context, pos image.Point, width, height int) {
	// Shadow
	shadowRect := clip.Rect{
		Min: image.Point{X: pos.X + 2, Y: pos.Y + 2},
		Max: image.Point{X: pos.X + width + 2, Y: pos.Y + height + 2},
	}.Op()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0, G: 0, B: 0, A: 80}, shadowRect)

	// Background (sidebar color)
	sidebarBg := color.NRGBA{R: 26, G: 26, B: 26, A: 255}
	menuRect := clip.Rect{
		Min: pos,
		Max: image.Point{X: pos.X + width, Y: pos.Y + height},
	}.Op()
	paint.FillShape(gtx.Ops, sidebarBg, menuRect)

	// Border
	borderColor := color.NRGBA{R: 51, G: 51, B: 51, A: 255}
	for _, edge := range []clip.Rect{
		{Min: pos, Max: image.Point{X: pos.X + width, Y: pos.Y + 1}},
		{Min: image.Point{X: pos.X, Y: pos.Y + height - 1}, Max: image.Point{X: pos.X + width, Y: pos.Y + height}},
		{Min: pos, Max: image.Point{X: pos.X + 1, Y: pos.Y + height}},
		{Min: image.Point{X: pos.X + width - 1, Y: pos.Y}, Max: image.Point{X: pos.X + width, Y: pos.Y + height}},
	} {
		paint.FillShape(gtx.Ops, borderColor, edge.Op())
	}
}

// drawMenuItem draws a single menu item with hover/click handling.
// isSubmenuItem indicates this is a child in a submenu (not the main menu).
func (w *ControlWindow) drawMenuItem(gtx layout.Context, item *menuItem, x, y, width, height int, isSubmenuItem bool) {
	itemRect := clip.Rect{
		Min: image.Point{X: x + 1, Y: y + 1},
		Max: image.Point{X: x + width - 1, Y: y + height},
	}

	itemStack := itemRect.Push(gtx.Ops)
	event.Op(gtx.Ops, item)
	for {
		ev, ok := gtx.Event(
			pointer.Filter{Target: item, Kinds: pointer.Press | pointer.Enter | pointer.Leave},
		)
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
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

	// Hover highlight
	if item.hovered {
		activeHighlight := color.NRGBA{R: 45, G: 45, B: 45, A: 255}
		hoverRect := clip.Rect{
			Min: image.Point{X: x + 2, Y: y + 1},
			Max: image.Point{X: x + width - 2, Y: y + height - 1},
		}.Op()
		paint.FillShape(gtx.Ops, activeHighlight, hoverRect)
	}

	// Label
	textColor := color.NRGBA{R: 224, G: 224, B: 224, A: 255}
	label := material.Label(w.theme, unit.Sp(13), item.label)
	if item.hovered {
		label.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	} else {
		label.Color = textColor
	}
	labelStack := op.Offset(image.Pt(x+12, y+6)).Push(gtx.Ops)
	labelGtx := gtx
	labelGtx.Constraints = layout.Constraints{
		Min: image.Point{},
		Max: image.Point{X: width - 24, Y: height},
	}
	label.Layout(labelGtx)
	labelStack.Pop()
}

// Close closes the control window
func (w *ControlWindow) Close() {
	w.window.Perform(system.ActionClose)
}

// Invalidate requests a redraw
func (w *ControlWindow) Invalidate() {
	w.window.Invalidate()
}
