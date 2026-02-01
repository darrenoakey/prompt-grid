package gui

import (
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/unit"

	"claude-term/src/emulator"
	"claude-term/src/pty"
	"claude-term/src/render"
)

// DiscordStatus provides Discord connection status
type DiscordStatus interface {
	IsConnected() bool
}

// App coordinates the entire application
type App struct {
	manager    *pty.Manager
	sessions   map[string]*SessionState
	mu         sync.RWMutex
	colors     render.DefaultColors
	fontSize   unit.Sp
	controlWin *ControlWindow
	discordBot DiscordStatus
}

// SelectionPoint represents a position in the terminal
type SelectionPoint struct {
	X, Y int
}

// SessionState holds state for a single session
type SessionState struct {
	session    *pty.Session
	parser     *emulator.Parser
	screen     *emulator.Screen
	scrollback *emulator.Scrollback
	window     *TerminalWindow
	colors     render.SessionColor // Unique color for this session

	// Selection state
	selStart    SelectionPoint
	selEnd      SelectionPoint
	selecting   bool // Mouse is currently being dragged
	hasSelection bool // There is an active selection
}

// Session returns the PTY session
func (s *SessionState) Session() *pty.Session {
	return s.session
}

// Screen returns the screen buffer
func (s *SessionState) Screen() *emulator.Screen {
	return s.screen
}

// Scrollback returns the scrollback buffer
func (s *SessionState) Scrollback() *emulator.Scrollback {
	return s.scrollback
}

// Colors returns the session-specific color scheme
func (s *SessionState) Colors() render.SessionColor {
	return s.colors
}

// StartSelection begins a new selection at the given cell position
func (s *SessionState) StartSelection(x, y int) {
	s.selStart = SelectionPoint{X: x, Y: y}
	s.selEnd = SelectionPoint{X: x, Y: y}
	s.selecting = true
	s.hasSelection = true
}

// UpdateSelection updates the end point of the current selection
func (s *SessionState) UpdateSelection(x, y int) {
	if s.selecting {
		s.selEnd = SelectionPoint{X: x, Y: y}
	}
}

// EndSelection finishes the current selection
func (s *SessionState) EndSelection() {
	s.selecting = false
}

// ClearSelection removes the current selection
func (s *SessionState) ClearSelection() {
	s.hasSelection = false
	s.selecting = false
}

// HasSelection returns whether there is an active selection
func (s *SessionState) HasSelection() bool {
	return s.hasSelection
}

// IsSelected returns whether the given cell is within the selection
func (s *SessionState) IsSelected(x, y int) bool {
	if !s.hasSelection {
		return false
	}

	// Normalize selection (start before end)
	startY, startX := s.selStart.Y, s.selStart.X
	endY, endX := s.selEnd.Y, s.selEnd.X

	if startY > endY || (startY == endY && startX > endX) {
		startY, endY = endY, startY
		startX, endX = endX, startX
	}

	// Check if point is in selection range
	if y < startY || y > endY {
		return false
	}
	if y == startY && y == endY {
		return x >= startX && x <= endX
	}
	if y == startY {
		return x >= startX
	}
	if y == endY {
		return x <= endX
	}
	return true
}

// GetSelectedText returns the text within the current selection
func (s *SessionState) GetSelectedText() string {
	if !s.hasSelection {
		return ""
	}

	// Normalize selection
	startY, startX := s.selStart.Y, s.selStart.X
	endY, endX := s.selEnd.Y, s.selEnd.X

	if startY > endY || (startY == endY && startX > endX) {
		startY, endY = endY, startY
		startX, endX = endX, startX
	}

	cols, _ := s.screen.Size()
	var result []rune

	for y := startY; y <= endY; y++ {
		lineStart := 0
		lineEnd := cols - 1

		if y == startY {
			lineStart = startX
		}
		if y == endY {
			lineEnd = endX
		}

		for x := lineStart; x <= lineEnd; x++ {
			cell := s.screen.Cell(x, y)
			if cell.Rune == 0 {
				result = append(result, ' ')
			} else {
				result = append(result, cell.Rune)
			}
		}

		// Add newline between lines (but not at the end)
		if y < endY {
			result = append(result, '\n')
		}
	}

	return string(result)
}

// NewApp creates a new application
func NewApp() *App {
	return &App{
		manager:  pty.NewManager(),
		sessions: make(map[string]*SessionState),
		colors:   render.DefaultColorScheme(),
		fontSize: 14,
	}
}

// Manager returns the session manager
func (a *App) Manager() *pty.Manager {
	return a.manager
}

// Colors returns the current color scheme
func (a *App) Colors() render.DefaultColors {
	return a.colors
}

// FontSize returns the current font size
func (a *App) FontSize() unit.Sp {
	return a.fontSize
}

// NewSession creates a new session with associated GUI state
func (a *App) NewSession(name string) (*SessionState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	session, err := a.manager.NewSession(name)
	if err != nil {
		return nil, err
	}

	// Create emulator components
	screen := emulator.NewScreen(120, 24)
	scrollback := emulator.NewScrollback()
	parser := emulator.NewParser(screen, scrollback)

	state := &SessionState{
		session:    session,
		parser:     parser,
		screen:     screen,
		scrollback: scrollback,
		colors:     render.RandomSessionColor(),
	}

	// Connect PTY output to parser
	session.SetOnData(func(data []byte) {
		parser.Parse(data)
		// Invalidate windows
		a.invalidateSession(name)
	})

	a.sessions[name] = state
	return state, nil
}

// GetSession returns session state by name (case-insensitive)
func (a *App) GetSession(name string) *SessionState {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Try exact match first
	if state, ok := a.sessions[name]; ok {
		return state
	}

	// Try case-insensitive match
	nameLower := strings.ToLower(name)
	for k, state := range a.sessions {
		if strings.ToLower(k) == nameLower {
			return state
		}
	}
	return nil
}

// ListSessions returns all session names
func (a *App) ListSessions() []string {
	return a.manager.List()
}

// CloseSession closes a session (case-insensitive)
func (a *App) CloseSession(name string) error {
	a.mu.Lock()

	// Find the actual key (case-insensitive)
	actualName := name
	nameLower := strings.ToLower(name)
	for k := range a.sessions {
		if strings.ToLower(k) == nameLower {
			actualName = k
			break
		}
	}

	state := a.sessions[actualName]
	delete(a.sessions, actualName)
	a.mu.Unlock()

	if state != nil && state.window != nil {
		state.window.Close()
	}

	return a.manager.Close(actualName)
}

// invalidateSession signals windows to redraw for a session
func (a *App) invalidateSession(name string) {
	a.mu.RLock()
	state := a.sessions[name]
	a.mu.RUnlock()

	if state != nil && state.window != nil {
		state.window.Invalidate()
	}

	if a.controlWin != nil {
		a.controlWin.Invalidate()
	}
}

// CreateTerminalWindow creates a new terminal window for a session
func (a *App) CreateTerminalWindow(name string) (*TerminalWindow, error) {
	state := a.GetSession(name)
	if state == nil {
		return nil, pty.ErrSessionNotFound
	}

	win := NewTerminalWindow(a, state)
	state.window = win

	return win, nil
}

// CreateControlWindow creates the control center window
func (a *App) CreateControlWindow() *ControlWindow {
	if a.controlWin == nil {
		a.controlWin = NewControlWindow(a)
	}
	return a.controlWin
}

// Run starts the application event loop
func (a *App) Run() error {
	app.Main()
	return nil
}

// SetDiscordBot sets the Discord bot reference for status display
func (a *App) SetDiscordBot(bot DiscordStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.discordBot = bot
}

// IsDiscordConnected returns whether Discord is connected
func (a *App) IsDiscordConnected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.discordBot == nil {
		return false
	}
	return a.discordBot.IsConnected()
}

// AddSession creates a new session, starts it, and creates a terminal window
// This is the main entry point for adding sessions (both initial and via IPC)
func (a *App) AddSession(name string, sshHost string) error {
	// Create session
	state, err := a.NewSession(name)
	if err != nil {
		return err
	}

	// Start session
	if sshHost != "" {
		err = state.Session().StartSSH(sshHost)
	} else {
		err = state.Session().Start()
	}
	if err != nil {
		a.CloseSession(name)
		return err
	}

	// Create terminal window in goroutine (Gio windows run their own event loop)
	go func() {
		termWin, err := a.CreateTerminalWindow(name)
		if err != nil {
			return
		}

		// Run terminal window event loop
		termWin.Run()

		// Close session when window closes
		a.CloseSession(name)
	}()

	// Invalidate control window to show new session
	if a.controlWin != nil {
		a.controlWin.Invalidate()
	}

	return nil
}
