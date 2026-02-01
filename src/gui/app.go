package gui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/unit"

	"claude-term/src/emulator"
	"claude-term/src/render"
	"claude-term/src/session"
)

// ErrSessionNotFound is returned when a session is not found
var ErrSessionNotFound = errors.New("session not found")

// DiscordStatus provides Discord connection status
type DiscordStatus interface {
	IsConnected() bool
}

// App coordinates the entire application
type App struct {
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
	client     *session.Client
	parser     *emulator.Parser
	screen     *emulator.Screen
	scrollback *emulator.Scrollback
	window     *TerminalWindow
	colors     render.SessionColor // Unique color for this session

	// Selection state
	selStart     SelectionPoint
	selEnd       SelectionPoint
	selecting    bool // Mouse is currently being dragged
	hasSelection bool // There is an active selection
}

// Client returns the session client
func (s *SessionState) Client() *session.Client {
	return s.client
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
	a := &App{
		sessions: make(map[string]*SessionState),
		colors:   render.DefaultColorScheme(),
		fontSize: 14,
	}

	// Discover and reconnect to existing session daemons
	a.discoverSessions()

	return a
}

// discoverSessions finds and reconnects to existing session daemons
func (a *App) discoverSessions() {
	names := session.ListSessions()
	for _, name := range names {
		a.reconnectSession(name)
	}
}

// reconnectSession connects to an existing session daemon
func (a *App) reconnectSession(name string) error {
	client, err := session.Connect(name)
	if err != nil {
		return err
	}

	// Create emulator components
	info := client.Info()
	screen := emulator.NewScreen(int(info.Cols), int(info.Rows))
	scrollback := emulator.NewScrollback()
	parser := emulator.NewParser(screen, scrollback)

	state := &SessionState{
		client:     client,
		parser:     parser,
		screen:     screen,
		scrollback: scrollback,
		colors:     render.RandomSessionColor(),
	}

	// Connect client data to parser
	client.SetOnData(func(data []byte) {
		parser.Parse(data)
		a.invalidateSession(name)
	})

	client.SetOnExit(func(err error) {
		a.mu.Lock()
		delete(a.sessions, name)
		a.mu.Unlock()
		if a.controlWin != nil {
			a.controlWin.Invalidate()
		}
	})

	client.Start()

	a.mu.Lock()
	a.sessions[name] = state
	a.mu.Unlock()

	// Create terminal window
	go func() {
		termWin, err := a.CreateTerminalWindow(name)
		if err != nil {
			return
		}
		termWin.Run()
		a.CloseSession(name)
	}()

	return nil
}

// Colors returns the current color scheme
func (a *App) Colors() render.DefaultColors {
	return a.colors
}

// FontSize returns the current font size
func (a *App) FontSize() unit.Sp {
	return a.fontSize
}

// NewSession creates a new session by spawning a daemon and connecting
func (a *App) NewSession(name string, sshHost string) (*SessionState, error) {
	a.mu.Lock()
	if _, exists := a.sessions[name]; exists {
		a.mu.Unlock()
		return nil, fmt.Errorf("session %q already exists", name)
	}
	a.mu.Unlock()

	// Spawn session daemon
	cols := uint16(120)
	rows := uint16(24)
	if err := session.SpawnDaemon(name, cols, rows, sshHost); err != nil {
		return nil, err
	}

	// Connect to daemon
	client, err := session.Connect(name)
	if err != nil {
		return nil, err
	}

	// Create emulator components
	screen := emulator.NewScreen(int(cols), int(rows))
	scrollback := emulator.NewScrollback()
	parser := emulator.NewParser(screen, scrollback)

	state := &SessionState{
		client:     client,
		parser:     parser,
		screen:     screen,
		scrollback: scrollback,
		colors:     render.RandomSessionColor(),
	}

	// Connect client data to parser
	client.SetOnData(func(data []byte) {
		parser.Parse(data)
		a.invalidateSession(name)
	})

	client.SetOnExit(func(err error) {
		a.mu.Lock()
		delete(a.sessions, name)
		a.mu.Unlock()
		if a.controlWin != nil {
			a.controlWin.Invalidate()
		}
	})

	client.Start()

	a.mu.Lock()
	a.sessions[name] = state
	a.mu.Unlock()

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
	a.mu.RLock()
	defer a.mu.RUnlock()

	names := make([]string, 0, len(a.sessions))
	for name := range a.sessions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

	if state == nil {
		return ErrSessionNotFound
	}

	if state.window != nil {
		state.window.Close()
	}

	if state.client != nil {
		return state.client.Terminate()
	}

	return nil
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
		return nil, ErrSessionNotFound
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
	// Create and start session (spawns daemon, connects)
	_, err := a.NewSession(name, sshHost)
	if err != nil {
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
