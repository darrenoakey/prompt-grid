package gui

import (
	"sync"

	"gioui.org/app"
	"gioui.org/unit"

	"claude-term/src/emulator"
	"claude-term/src/pty"
	"claude-term/src/render"
)

// App coordinates the entire application
type App struct {
	manager    *pty.Manager
	sessions   map[string]*SessionState
	mu         sync.RWMutex
	colors     render.DefaultColors
	fontSize   unit.Sp
	controlWin *ControlWindow
}

// SessionState holds state for a single session
type SessionState struct {
	session    *pty.Session
	parser     *emulator.Parser
	screen     *emulator.Screen
	scrollback *emulator.Scrollback
	window     *TerminalWindow
	colors     render.SessionColor // Unique color for this session
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

// GetSession returns session state by name
func (a *App) GetSession(name string) *SessionState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessions[name]
}

// ListSessions returns all session names
func (a *App) ListSessions() []string {
	return a.manager.List()
}

// CloseSession closes a session
func (a *App) CloseSession(name string) error {
	a.mu.Lock()
	state := a.sessions[name]
	delete(a.sessions, name)
	a.mu.Unlock()

	if state != nil && state.window != nil {
		state.window.Close()
	}

	return a.manager.Close(name)
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
