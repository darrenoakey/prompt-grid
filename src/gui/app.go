package gui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/unit"

	"claude-term/src/config"
	"claude-term/src/emulator"
	"claude-term/src/pty"
	"claude-term/src/ptylog"
	"claude-term/src/render"
	"claude-term/src/tmux"
)

// ErrSessionNotFound is returned when a session is not found
var ErrSessionNotFound = errors.New("session not found")

// DiscordStatus provides Discord connection status
type DiscordStatus interface {
	IsConnected() bool
}

// SessionLifecycleObserver receives session lifecycle events.
type SessionLifecycleObserver interface {
	SessionAdded(name string)
	SessionRenamed(oldName, newName string)
	SessionClosed(name string)
}

// App coordinates the entire application
type App struct {
	sessions   map[string]*SessionState
	mu         sync.RWMutex
	colors     render.DefaultColors
	fontSize   unit.Sp
	controlWin *ControlWindow
	discordBot DiscordStatus
	config     *config.Config
	configPath string

	observersMu sync.RWMutex
	observers   []SessionLifecycleObserver
}

// SelectionPoint represents a position in the terminal
type SelectionPoint struct {
	X, Y int
}

// SessionState holds state for a single session
type SessionState struct {
	pty        *pty.Session
	name       string
	sshHost    string
	parser     *emulator.Parser
	screen     *emulator.Screen
	scrollback *emulator.Scrollback
	window     *TerminalWindow
	colors     render.SessionColor // Unique color for this session
	ptyLog     *ptylog.Writer      // PTY output logger for persistence

	// Scrollback viewing state
	scrollOffset int // Lines scrolled up from bottom (0 = viewing live terminal)

	// Selection state
	selStart     SelectionPoint
	selEnd       SelectionPoint
	selecting    bool // Mouse is currently being dragged
	hasSelection bool // There is an active selection
}

// PTY returns the pty session
func (s *SessionState) PTY() *pty.Session {
	return s.pty
}

// Name returns the session name
func (s *SessionState) Name() string {
	return s.name
}

// IsSSH returns true if this is an SSH session
func (s *SessionState) IsSSH() bool {
	return s.sshHost != ""
}

// SSHHost returns the SSH host
func (s *SessionState) SSHHost() string {
	return s.sshHost
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

// ScrollOffset returns the current scroll offset (lines up from bottom)
func (s *SessionState) ScrollOffset() int {
	return s.scrollOffset
}

// SetScrollOffset sets the scroll offset, clamping to valid range
func (s *SessionState) SetScrollOffset(offset int) {
	maxOffset := s.scrollback.Count()
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	s.scrollOffset = offset
}

// AdjustScrollOffset adds delta to scroll offset (positive = scroll up/back in history)
func (s *SessionState) AdjustScrollOffset(delta int) {
	s.SetScrollOffset(s.scrollOffset + delta)
}

// ResetScrollOffset snaps back to live view (bottom)
func (s *SessionState) ResetScrollOffset() {
	s.scrollOffset = 0
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
func NewApp(cfg *config.Config, cfgPath string) *App {
	a := &App{
		sessions:   make(map[string]*SessionState),
		colors:     render.DefaultColorScheme(),
		fontSize:   14,
		config:     cfg,
		configPath: cfgPath,
	}

	// Discover and reconnect to existing tmux sessions
	a.discoverSessions()

	return a
}

// discoverSessions finds and reconnects to existing tmux sessions,
// and recreates sessions that died (e.g., after reboot) from saved config.
func (a *App) discoverSessions() {
	names, _ := tmux.ListSessions()
	if len(names) > 0 {
		// Server is already running — ensure global options are set
		tmux.ConfigureServer()
	}

	// Track which sessions are live in tmux
	liveSet := make(map[string]bool, len(names))
	for _, name := range names {
		liveSet[name] = true
		a.reconnectSession(name)
	}

	// Recreate sessions from config that aren't live (died in reboot)
	if a.config != nil {
		for name, info := range a.config.AllSessions() {
			if !liveSet[name] {
				a.recreateSession(name, info)
			}
		}
	}
}

// setupSessionCallbacks connects PTY data/exit callbacks to parser and log writer.
// Shared between reconnectSession, NewSession, and recreateSession.
func (a *App) setupSessionCallbacks(state *SessionState, name string) {
	state.pty.SetOnData(func(data []byte) {
		state.parser.Parse(data)
		if state.ptyLog != nil {
			state.ptyLog.Write(data)
		}
		state.ResetScrollOffset() // Snap to bottom on new output
		a.invalidateSession(name)
	})

	state.pty.SetOnExit(func(err error) {
		// PTY exited - check if tmux session still exists (detach vs death)
		if !tmux.HasSession(name) {
			a.mu.Lock()
			delete(a.sessions, name)
			a.mu.Unlock()

			// Clean up config and logs for command-type sessions (claude, codex, ssh)
			// that exited intentionally. For regular shell sessions, preserve config
			// so they can be recreated after app/machine restart.
			shouldCleanup := false
			if a.config != nil {
				if info, ok := a.config.GetSessionInfo(name); ok {
					// Clean up if it's a command session (claude, codex, ssh)
					// but preserve shell sessions for restart persistence
					shouldCleanup = info.Type != "shell"
				}
			}

			if shouldCleanup {
				ptylog.DeleteLog(name)
				if a.config != nil {
					a.config.DeleteSessionColor(name)
					a.config.DeleteWindowSize(name)
					a.config.DeleteSessionInfo(name)
					a.saveConfig()
				}
			}

			a.notifySessionClosed(name)
			if a.controlWin != nil {
				a.controlWin.Invalidate()
			}
		}
	})
}

// reconnectSession connects to an existing tmux session
func (a *App) reconnectSession(name string) error {
	// Create PTY and attach to tmux session
	cols := uint16(120)
	rows := uint16(24)

	ptySess := pty.NewSession(name)

	// Create emulator components
	screen := emulator.NewScreen(int(cols), int(rows))
	scrollback := emulator.NewScrollback()
	parser := emulator.NewParser(screen, scrollback)

	// Replay saved PTY log to restore scrollback
	ptylog.ReplayLog(name, parser)

	// Start PTY log writer
	logWriter, _ := ptylog.NewWriter(name)

	// Look up or assign session color
	sessionColor := a.resolveSessionColor(name)

	// Load session info from config (for sshHost, type, workDir)
	var sessionInfo config.SessionInfo
	if a.config != nil {
		if info, ok := a.config.GetSessionInfo(name); ok {
			sessionInfo = info
		} else {
			// Backward compat: create default session info for pre-existing sessions
			sessionInfo = config.SessionInfo{Type: "shell"}
			a.config.SetSessionInfo(name, sessionInfo)
			a.saveConfig()
		}
	}

	state := &SessionState{
		pty:        ptySess,
		name:       name,
		sshHost:    sessionInfo.SSHHost,
		parser:     parser,
		screen:     screen,
		scrollback: scrollback,
		colors:     sessionColor,
		ptyLog:     logWriter,
	}

	// Connect callbacks
	a.setupSessionCallbacks(state, name)

	// Start PTY with tmux attach
	cmd, args := tmux.AttachArgs(name)
	if err := ptySess.StartCommand(cmd, args); err != nil {
		if logWriter != nil {
			logWriter.Close()
		}
		return err
	}

	a.mu.Lock()
	a.sessions[name] = state
	a.mu.Unlock()

	return nil
}

// recreateSession creates a new tmux session from saved config (after reboot).
// It replays the PTY log to restore scrollback, then starts a fresh shell/ssh/claude.
func (a *App) recreateSession(name string, info config.SessionInfo) error {
	cols := uint16(120)
	rows := uint16(24)

	// Create tmux session with saved parameters
	var initialCmd []string
	workDir := info.WorkDir
	if info.SSHHost != "" {
		initialCmd = []string{"ssh", info.SSHHost}
		workDir = "" // SSH sessions don't use local workDir
	} else if info.Type == "claude" {
		// Claude sessions run claude as the command (exits when claude exits)
		claudePath := filepath.Join(os.Getenv("HOME"), ".local", "bin", "claude")
		initialCmd = []string{claudePath}
	} else if info.Type == "codex" {
		// Codex sessions run codex as the command (exits when codex exits)
		// Use just "codex" to let PATH resolution find it
		initialCmd = []string{"codex"}
	}
	if err := tmux.NewSession(name, workDir, cols, rows, initialCmd...); err != nil {
		return err
	}

	ptySess := pty.NewSession(name)

	// Create emulator components
	screen := emulator.NewScreen(int(cols), int(rows))
	scrollback := emulator.NewScrollback()
	parser := emulator.NewParser(screen, scrollback)

	// Replay saved PTY log to restore scrollback
	ptylog.ReplayLog(name, parser)

	// Start PTY log writer
	logWriter, _ := ptylog.NewWriter(name)

	// Look up or assign session color
	sessionColor := a.resolveSessionColor(name)

	state := &SessionState{
		pty:        ptySess,
		name:       name,
		sshHost:    info.SSHHost,
		parser:     parser,
		screen:     screen,
		scrollback: scrollback,
		colors:     sessionColor,
		ptyLog:     logWriter,
	}

	// Connect callbacks
	a.setupSessionCallbacks(state, name)

	// Start PTY with tmux attach
	cmd, args := tmux.AttachArgs(name)
	if err := ptySess.StartCommand(cmd, args); err != nil {
		if logWriter != nil {
			logWriter.Close()
		}
		tmux.KillSession(name)
		return err
	}

	a.mu.Lock()
	a.sessions[name] = state
	a.mu.Unlock()

	return nil
}

// resolveSessionColor looks up a saved palette index or assigns a new random one
func (a *App) resolveSessionColor(name string) render.SessionColor {
	if a.config != nil {
		if idx, ok := a.config.GetSessionColorIndex(name); ok {
			return render.GetSessionColor(idx)
		}
		// Assign new random index and save
		idx := render.RandomSessionColorIndex()
		a.config.SetSessionColorIndex(name, idx)
		a.saveConfig()
		return render.GetSessionColor(idx)
	}
	return render.RandomSessionColor()
}

// saveConfig writes config to disk (best-effort, logs no error)
func (a *App) saveConfig() {
	if a.config != nil && a.configPath != "" {
		a.config.Save(a.configPath)
	}
}

// Colors returns the current color scheme
func (a *App) Colors() render.DefaultColors {
	return a.colors
}

// FontSize returns the current font size
func (a *App) FontSize() unit.Sp {
	return a.fontSize
}

// NewSession creates a new session by creating a tmux session and attaching via PTY.
// workDir sets the initial working directory (empty = tmux default).
func (a *App) NewSession(name, sshHost, workDir string) (*SessionState, error) {
	a.mu.Lock()
	if _, exists := a.sessions[name]; exists {
		a.mu.Unlock()
		return nil, fmt.Errorf("session %q already exists", name)
	}
	a.mu.Unlock()

	// Create tmux session
	cols := uint16(120)
	rows := uint16(24)
	var initialCmd []string
	if sshHost != "" {
		initialCmd = []string{"ssh", sshHost}
	}
	if err := tmux.NewSession(name, workDir, cols, rows, initialCmd...); err != nil {
		return nil, err
	}

	// Create PTY and attach
	ptySess := pty.NewSession(name)

	// Create emulator components
	screen := emulator.NewScreen(int(cols), int(rows))
	scrollback := emulator.NewScrollback()
	parser := emulator.NewParser(screen, scrollback)

	// Start PTY log writer
	logWriter, _ := ptylog.NewWriter(name)

	// Look up or assign session color
	sessionColor := a.resolveSessionColor(name)

	state := &SessionState{
		pty:        ptySess,
		name:       name,
		sshHost:    sshHost,
		parser:     parser,
		screen:     screen,
		scrollback: scrollback,
		colors:     sessionColor,
		ptyLog:     logWriter,
	}

	// Connect callbacks
	a.setupSessionCallbacks(state, name)

	// Save session metadata
	if a.config != nil {
		sessionType := "shell"
		if sshHost != "" {
			sessionType = "ssh"
		}
		a.config.SetSessionInfo(name, config.SessionInfo{
			Type:    sessionType,
			WorkDir: workDir,
			SSHHost: sshHost,
		})
		a.saveConfig()
	}

	// Start PTY with tmux attach
	cmd, args := tmux.AttachArgs(name)
	if err := ptySess.StartCommand(cmd, args); err != nil {
		if logWriter != nil {
			logWriter.Close()
		}
		tmux.KillSession(name) // cleanup on failure
		return nil, err
	}

	a.mu.Lock()
	a.sessions[name] = state
	a.mu.Unlock()
	a.notifySessionAdded(name)

	return state, nil
}

// newSessionWithCommand creates a session that runs a specific command (like claude).
// When the command exits, tmux closes the session automatically.
func (a *App) newSessionWithCommand(name, workDir, command string) (*SessionState, error) {
	a.mu.Lock()
	if _, exists := a.sessions[name]; exists {
		a.mu.Unlock()
		return nil, fmt.Errorf("session %q already exists", name)
	}
	a.mu.Unlock()

	// Create tmux session with command as initial command
	cols := uint16(120)
	rows := uint16(24)
	if err := tmux.NewSession(name, workDir, cols, rows, command); err != nil {
		return nil, err
	}

	// Create PTY and attach
	ptySess := pty.NewSession(name)

	// Create emulator components
	screen := emulator.NewScreen(int(cols), int(rows))
	scrollback := emulator.NewScrollback()
	parser := emulator.NewParser(screen, scrollback)

	// Start PTY log writer
	logWriter, _ := ptylog.NewWriter(name)

	// Look up or assign session color
	sessionColor := a.resolveSessionColor(name)

	state := &SessionState{
		pty:        ptySess,
		name:       name,
		parser:     parser,
		screen:     screen,
		scrollback: scrollback,
		colors:     sessionColor,
		ptyLog:     logWriter,
	}

	// Connect callbacks
	a.setupSessionCallbacks(state, name)

	// Start PTY with tmux attach
	cmd, args := tmux.AttachArgs(name)
	if err := ptySess.StartCommand(cmd, args); err != nil {
		if logWriter != nil {
			logWriter.Close()
		}
		tmux.KillSession(name)
		return nil, err
	}

	a.mu.Lock()
	a.sessions[name] = state
	a.mu.Unlock()
	a.notifySessionAdded(name)

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

// detachSession clears the window reference when a standalone window closes.
// The PTY and tmux session remain alive (session stays functional in control center).
func (a *App) detachSession(name string) {
	state := a.GetSession(name)
	if state != nil {
		state.window = nil
	}
}

// PopOutSession creates a standalone terminal window for a session.
// If the session already has a window, this is a no-op.
func (a *App) PopOutSession(name string) {
	state := a.GetSession(name)
	if state == nil || state.window != nil {
		return
	}

	termWin, err := a.CreateTerminalWindow(name)
	if err != nil {
		return
	}

	// Restore saved window size if available
	if a.config != nil {
		if size, ok := a.config.GetWindowSize(name); ok && size[0] > 0 && size[1] > 0 {
			termWin.window.Option(app.Size(unit.Dp(size[0]), unit.Dp(size[1])))
		}
	}

	// Run window event loop; on exit save size and detach
	go func() {
		termWin.Run()

		// Save window size to config
		if a.config != nil {
			lastSize := termWin.LastSize()
			if lastSize.X > 0 && lastSize.Y > 0 {
				a.config.SetWindowSize(name, lastSize.X, lastSize.Y)
				a.saveConfig()
			}
		}

		a.detachSession(name)
		if a.controlWin != nil {
			a.controlWin.Invalidate()
		}
	}()
}

// CallBackSession closes the standalone window for a session.
// The session remains alive in the control center.
func (a *App) CallBackSession(name string) {
	state := a.GetSession(name)
	if state == nil || state.window == nil {
		return
	}
	state.window.Close()
	// The Run() goroutine from PopOutSession handles cleanup (save size, detach, invalidate)
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

	if state.ptyLog != nil {
		state.ptyLog.Close()
	}

	if state.pty != nil {
		state.pty.Close()
	}

	tmux.KillSession(actualName)

	// Remove saved color, window size, session info, and PTY log
	ptylog.DeleteLog(actualName)
	if a.config != nil {
		a.config.DeleteSessionColor(actualName)
		a.config.DeleteWindowSize(actualName)
		a.config.DeleteSessionInfo(actualName)
		a.saveConfig()
	}

	a.notifySessionClosed(actualName)

	return nil
}

// RenameSession renames a session
func (a *App) RenameSession(oldName, newName string) error {
	a.mu.Lock()

	// Check if new name already exists
	if _, exists := a.sessions[newName]; exists {
		a.mu.Unlock()
		return fmt.Errorf("session %q already exists", newName)
	}

	// Find the actual key (case-insensitive)
	actualName := oldName
	nameLower := strings.ToLower(oldName)
	for k := range a.sessions {
		if strings.ToLower(k) == nameLower {
			actualName = k
			break
		}
	}

	state := a.sessions[actualName]
	if state == nil {
		a.mu.Unlock()
		return ErrSessionNotFound
	}

	// Rename tmux session (subprocess — safe under lock, no main-thread dispatch)
	if err := tmux.RenameSession(actualName, newName); err != nil {
		a.mu.Unlock()
		return err
	}

	// Close old writer, rename log file, open new writer
	if state.ptyLog != nil {
		state.ptyLog.Close()
	}
	ptylog.RenameLog(actualName, newName)
	state.ptyLog, _ = ptylog.NewWriter(newName)

	// Move to new name
	delete(a.sessions, actualName)
	state.name = newName
	a.sessions[newName] = state

	// Move saved color, window size, and session info mappings
	if a.config != nil {
		a.config.RenameSessionColor(actualName, newName)
		a.config.RenameWindowSize(actualName, newName)
		a.config.RenameSessionInfo(actualName, newName)
		a.saveConfig()
	}

	// Capture window ref before releasing lock
	win := state.window
	a.mu.Unlock()

	// Update terminal window title OUTSIDE the lock.
	// SetTitle calls window.Option() which dispatches to the Cocoa main thread.
	// If we held a.mu.Lock() here, any frame handler calling a.mu.RLock() would
	// deadlock (goroutine waits for main thread, main thread waits for lock).
	if win != nil {
		win.SetTitle(newName)
	}

	a.notifySessionRenamed(actualName, newName)

	return nil
}

// RecolorSession assigns a new random color to a session and persists it
func (a *App) RecolorSession(name string) {
	a.mu.Lock()
	state := a.sessions[name]
	if state == nil {
		a.mu.Unlock()
		return
	}

	idx := render.RandomSessionColorIndex()
	state.colors = render.GetSessionColor(idx)

	if a.config != nil {
		a.config.SetSessionColorIndex(name, idx)
		a.saveConfig()
	}

	// Delete stale termWidget so it recreates with new colors
	if a.controlWin != nil {
		delete(a.controlWin.termWidgets, name)
	}
	a.mu.Unlock()

	// Invalidate windows to reflect new color
	a.invalidateSession(name)
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

// AddSessionObserver registers a lifecycle observer.
func (a *App) AddSessionObserver(observer SessionLifecycleObserver) {
	if observer == nil {
		return
	}
	a.observersMu.Lock()
	a.observers = append(a.observers, observer)
	a.observersMu.Unlock()
}

func (a *App) snapshotObservers() []SessionLifecycleObserver {
	a.observersMu.RLock()
	defer a.observersMu.RUnlock()

	observers := make([]SessionLifecycleObserver, len(a.observers))
	copy(observers, a.observers)
	return observers
}

func (a *App) notifySessionAdded(name string) {
	for _, observer := range a.snapshotObservers() {
		observer.SessionAdded(name)
	}
}

func (a *App) notifySessionRenamed(oldName, newName string) {
	for _, observer := range a.snapshotObservers() {
		observer.SessionRenamed(oldName, newName)
	}
}

func (a *App) notifySessionClosed(name string) {
	for _, observer := range a.snapshotObservers() {
		observer.SessionClosed(name)
	}
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

// AddSession creates a new session and shows it in the control center.
// This is the main entry point for adding sessions (both initial and via IPC).
func (a *App) AddSession(name string, sshHost string) error {
	// Default workDir to ~/src for non-SSH sessions
	workDir := ""
	if sshHost == "" {
		home, _ := os.UserHomeDir()
		workDir = filepath.Join(home, "src")
	}

	// Create and start session (creates tmux session, attaches via PTY)
	_, err := a.NewSession(name, sshHost, workDir)
	if err != nil {
		return err
	}

	// Invalidate control window to show new session
	if a.controlWin != nil {
		a.controlWin.Invalidate()
	}

	return nil
}

// AddClaudeSession creates a new session running Claude in the given directory.
// When claude exits, the session closes automatically.
func (a *App) AddClaudeSession(name, dir string) error {
	// Create session with claude as the command (like SSH sessions)
	claudePath := filepath.Join(os.Getenv("HOME"), ".local", "bin", "claude")
	_, err := a.newSessionWithCommand(name, dir, claudePath)
	if err != nil {
		return err
	}

	// Save session type as "claude"
	if a.config != nil {
		a.config.SetSessionInfo(name, config.SessionInfo{
			Type:    "claude",
			WorkDir: dir,
		})
		a.saveConfig()
	}

	if a.controlWin != nil {
		a.controlWin.Invalidate()
	}
	return nil
}

// AddCodexSession creates a new session running Codex in the given directory.
// When codex exits, the session closes automatically.
func (a *App) AddCodexSession(name, dir string) error {
	// Create session with codex as the command (like Claude sessions)
	// Use shell to find codex in PATH (it's at /opt/homebrew/bin/codex)
	_, err := a.newSessionWithCommand(name, dir, "codex")
	if err != nil {
		return err
	}

	// Save session type as "codex"
	if a.config != nil {
		a.config.SetSessionInfo(name, config.SessionInfo{
			Type:    "codex",
			WorkDir: dir,
		})
		a.saveConfig()
	}

	if a.controlWin != nil {
		a.controlWin.Invalidate()
	}
	return nil
}

// FlushAllLogs flushes all PTY log writers to disk.
// Called during graceful shutdown to prevent data loss.
func (a *App) FlushAllLogs() {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, state := range a.sessions {
		if state.ptyLog != nil {
			state.ptyLog.Flush()
		}
	}
}
