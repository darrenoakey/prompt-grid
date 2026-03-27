package gui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/unit"

	"prompt-grid/src/config"
	"prompt-grid/src/emulator"
	"prompt-grid/src/pty"
	"prompt-grid/src/ptylog"
	"prompt-grid/src/render"
	"prompt-grid/src/tmux"
	"prompt-grid/src/trace"
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
	sessions        map[string]*SessionState
	mu              sync.RWMutex
	colors          render.DefaultColors
	fontSize        unit.Sp
	controlWin      *ControlWindow
	discordBot      DiscordStatus
	config          *config.Config
	configPath      string
	startupComplete bool // Set after discoverSessions(); session exits after this always clean up

	observersMu sync.RWMutex
	observers   []SessionLifecycleObserver

	// Trace support
	traceMu      sync.RWMutex
	tracer       *trace.Tracer
	traceSession string
}

// SelectionPoint represents a position in the terminal
type SelectionPoint struct {
	X, Y int
}

// SessionState holds state for a single session
type SessionState struct {
	app          *App // Back-reference for tracing
	pty          *pty.Session
	name         string
	sshHost      string
	parser       *emulator.Parser
	screen       *emulator.Screen
	scrollback   *emulator.Scrollback
	window       *TerminalWindow
	colors       render.SessionColor // Unique color for this session
	ptyLog       *ptylog.Writer      // PTY output logger for persistence
	promptStatus PromptStatusValue   // Current prompt detection status (atomic)

	// screenMu protects parser/screen/scrollback/scrollOffset from concurrent
	// access between the PTY data callback (writes) and the Gio render thread (reads).
	screenMu sync.RWMutex

	// Scrollback viewing state
	scrollOffset int  // Lines scrolled up from bottom (0 = viewing live terminal)
	scrollMode   bool // True when user is viewing history (frozen view)

	// Activity tracking
	lastActivity     time.Time // Last time user interacted with session (typing/Discord, for collapse mode)
	lastAutoMenuTime time.Time // Last time auto-menu sent "1" to this session

	// Double-buffer: PTY data is buffered and parsed in drainPendingData().
	pendingMu          sync.Mutex
	pendingData        []byte
	pendingLastRecv    time.Time // Timestamp of most recent PTY data arrival
	pendingLastInvalid time.Time // Last time invalidateSession was called (rate limit)

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

// Screen returns the current screen buffer (main or alternate, depending on
// which the parser is currently writing to). Always use this rather than
// accessing s.screen directly.
func (s *SessionState) Screen() *emulator.Screen {
	if s.parser != nil {
		return s.parser.Screen()
	}
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

// drainPendingData parses all buffered PTY data in a single batch.
// Called before rendering (Gio frame) and before screen reads (prompt detector,
// Discord streamer). This is the double-buffer mechanism: PTY data is buffered
// in OnData and parsed here, so intermediate states (screen cleared mid-redraw)
// are never visible.
func (s *SessionState) drainPendingData() {
	s.pendingMu.Lock()
	data := s.pendingData
	s.pendingData = nil
	s.pendingMu.Unlock()

	if len(data) == 0 {
		return
	}

	s.screenMu.Lock()
	oldCount := s.scrollback.Count()
	s.parser.Parse(data)
	if s.scrollMode {
		newCount := s.scrollback.Count()
		if delta := newCount - oldCount; delta > 0 {
			s.scrollOffset += delta
		}
	}
	s.ResetScrollOffset()
	s.screenMu.Unlock()
}

// traceEvent logs a trace event if this session is being traced.
func (s *SessionState) traceEvent(ev trace.Event) {
	if s.app != nil {
		s.app.traceEvent(s.name, ev)
	}
}

// LastActivity returns the time of the last user interaction.
func (s *SessionState) LastActivity() time.Time {
	return s.lastActivity
}

// TouchActivity marks this session as recently interacted with.
func (s *SessionState) TouchActivity() {
	s.lastActivity = time.Now()
}

// PromptStatus returns the current prompt detection status.
func (s *SessionState) PromptStatus() PromptStatus {
	return s.promptStatus.Load()
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

// AdjustScrollOffset adds delta to scroll offset (positive = scroll up/back in history).
// Automatically enters/exits scroll mode based on resulting offset.
// Called from the Gio main thread only.
func (s *SessionState) AdjustScrollOffset(delta int) {
	s.screenMu.Lock()
	s.SetScrollOffset(s.scrollOffset + delta)
	s.scrollMode = s.scrollOffset > 0
	s.scrollback.SetFrozen(s.scrollMode)
	s.screenMu.Unlock()
}

// ResetScrollOffset snaps back to live view (bottom).
// Skipped when in scroll mode so the user's view stays frozen.
// Called from both PTY callback (under screenMu) and Gio thread.
func (s *SessionState) ResetScrollOffset() {
	if !s.scrollMode {
		s.scrollOffset = 0
	}
}

// ScrollToBottom exits scroll mode and snaps to live view.
// Called from the Gio main thread only.
func (s *SessionState) ScrollToBottom() {
	s.screenMu.Lock()
	s.scrollOffset = 0
	s.scrollMode = false
	s.scrollback.SetFrozen(false)
	s.screenMu.Unlock()
}

// InScrollMode returns true when the user is viewing history.
func (s *SessionState) InScrollMode() bool {
	return s.scrollMode
}

// LockScreen takes a read lock on the screen/scrollback state.
// Use when reading screen content from a non-Gio goroutine (e.g., Discord streamer).
func (s *SessionState) LockScreen() {
	s.drainPendingData()
	s.screenMu.RLock()
}

// UnlockScreen releases the read lock taken by LockScreen.
func (s *SessionState) UnlockScreen() {
	s.screenMu.RUnlock()
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

// SelectionHasExtent returns true only when the selection covers more than a
// single point (i.e. the user actually dragged, not just clicked).
func (s *SessionState) SelectionHasExtent() bool {
	return s.hasSelection && (s.selStart.X != s.selEnd.X || s.selStart.Y != s.selEnd.Y)
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

	cols, _ := s.Screen().Size()
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
			cell := s.Screen().Cell(x, y)
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

	// Shift all activity timestamps so the most recent = now.
	// This preserves relative ordering: if 4 sessions were visible before
	// shutdown, the same 4 will be visible after restart.
	a.normalizeActivityTimes()

	// Clear tmux scrollback on all existing panes immediately after reconnect.
	// Panes retain their old history-limit from before the restart; this resets
	// it per-pane to 1 and flushes any accumulated scrollback.
	tmux.ClearAllHistory()

	// Mark startup complete: any session exit after this point is intentional
	a.startupComplete = true

	// Start background goroutine to track current working directories
	a.startCWDUpdater()

	// Start background goroutine to detect prompt status
	a.startPromptDetector()

	// Periodically clear tmux scrollback to prevent history reflow artifacts
	a.startTmuxHistoryClearer()

	return a
}

// normalizeActivityTimes shifts all session lastActivity timestamps forward so
// that the most recently active session has lastActivity = now. This preserves
// relative ordering across restarts: if 4 sessions were visible before shutdown,
// the same 4 will be visible after startup.
func (a *App) normalizeActivityTimes() {
	a.mu.RLock()
	if len(a.sessions) == 0 {
		a.mu.RUnlock()
		return
	}
	var maxActivity time.Time
	for _, state := range a.sessions {
		if state.lastActivity.After(maxActivity) {
			maxActivity = state.lastActivity
		}
	}
	a.mu.RUnlock()

	// If no session had persisted activity, nothing to shift
	if maxActivity.IsZero() {
		return
	}

	// delta = now - maxActivity; add delta to all timestamps
	now := time.Now()
	delta := now.Sub(maxActivity)

	a.mu.RLock()
	for _, state := range a.sessions {
		if !state.lastActivity.IsZero() {
			state.lastActivity = state.lastActivity.Add(delta)
		}
	}
	a.mu.RUnlock()
}

// startCWDUpdater starts a background goroutine that polls each session's current
// working directory every 30 seconds and saves any changes to config.
// This ensures the saved workDir reflects where the user actually is when the
// app restarts, not just where the session was originally created.
func (a *App) startCWDUpdater() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			a.updateAllCWDs()
		}
	}()
}

// startTmuxHistoryClearer periodically resets tmux's history-limit and clears
// scrollback for all sessions. tmux's internal scrollback causes old content to
// reflow onto the visible screen during resize events, appearing as "replayed" output.
func (a *App) startTmuxHistoryClearer() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			tmux.ClearAllHistory()
		}
	}()
}

// startPromptDetector starts a background goroutine that checks each session's
// screen for prompt patterns every 500ms and updates the atomic status field.
func (a *App) startPromptDetector() {
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			a.updateAllPromptStatuses()
		}
	}()
}

// updateAllPromptStatuses scans each session's screen for prompt patterns.
func (a *App) updateAllPromptStatuses() {
	a.mu.RLock()
	type sessionRef struct {
		state *SessionState
		name  string
	}
	sessions := make([]sessionRef, 0, len(a.sessions))
	for name, state := range a.sessions {
		sessions = append(sessions, sessionRef{state: state, name: name})
	}
	autoMenu := a.config != nil && a.config.GetClaudeAutoMenu()
	a.mu.RUnlock()

	needsInvalidate := false
	now := time.Now()
	for _, ref := range sessions {
		// Skip sessions with no new data since last check (nothing changed)
		ref.state.pendingMu.Lock()
		hasPending := len(ref.state.pendingData) > 0
		ref.state.pendingMu.Unlock()
		if !hasPending {
			continue
		}

		ref.state.drainPendingData()
		ref.state.screenMu.RLock()
		screen := ref.state.Screen()
		newStatus := detectPromptStatus(screen)
		menuDetected := autoMenu && now.Sub(ref.state.lastAutoMenuTime) > 3*time.Second && detectClaudeMenu(screen)
		ref.state.screenMu.RUnlock()

		old := ref.state.promptStatus.Load()
		if old != newStatus {
			ref.state.promptStatus.Store(newStatus)
			needsInvalidate = true
		}

		// Auto-answer Claude numbered menus
		if menuDetected {
			ref.state.lastAutoMenuTime = now
			tmux.SendKeys(ref.name, "1", "Enter")
		}
	}

	if needsInvalidate && a.controlWin != nil {
		a.controlWin.Invalidate()
	}
}

// updateAllCWDs polls tmux for the current working directory of each local
// (non-SSH) session and saves any changes to config.
func (a *App) updateAllCWDs() {
	a.mu.RLock()
	var names []string
	for name, state := range a.sessions {
		if !state.IsSSH() {
			names = append(names, name)
		}
	}
	a.mu.RUnlock()

	if len(names) == 0 || a.config == nil {
		return
	}

	changed := false
	for _, name := range names {
		cwd, err := tmux.GetPaneCurrentPath(name)
		if err != nil || cwd == "" {
			continue
		}
		if info, ok := a.config.GetSessionInfo(name); ok && info.WorkDir != cwd {
			info.WorkDir = cwd
			a.config.SetSessionInfo(name, info)
			changed = true
		}
	}
	if changed {
		a.saveConfig()
	}

	// Also persist lastActivity timestamps for all sessions
	a.saveAllActivityTimes()
}

// saveAllActivityTimes persists each session's lastActivity to config.
func (a *App) saveAllActivityTimes() {
	if a.config == nil {
		return
	}

	a.mu.RLock()
	type activityRef struct {
		name string
		unix int64
	}
	var refs []activityRef
	for name, state := range a.sessions {
		refs = append(refs, activityRef{name: name, unix: state.lastActivity.Unix()})
	}
	a.mu.RUnlock()

	changed := false
	for _, ref := range refs {
		if info, ok := a.config.GetSessionInfo(ref.name); ok {
			if info.LastActivity != ref.unix {
				info.LastActivity = ref.unix
				a.config.SetSessionInfo(ref.name, info)
				changed = true
			}
		}
	}
	if changed {
		a.saveConfig()
	}
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
		// Trace raw PTY data
		a.traceMu.RLock()
		if t := a.tracer; t != nil && a.traceSession == name {
			t.LogPTYData(data)
		}
		a.traceMu.RUnlock()

		// Double-buffer: accumulate data instead of parsing immediately.
		// Parsing happens in drainPendingData() before each render.
		state.pendingMu.Lock()
		state.pendingData = append(state.pendingData, data...)
		now := time.Now()
		state.pendingLastRecv = now
		// Rate-limit invalidation: at most once per 8ms to avoid waking the
		// Gio frame loop on every PTY read during bursts.
		shouldInvalidate := now.Sub(state.pendingLastInvalid) >= 8*time.Millisecond
		if shouldInvalidate {
			state.pendingLastInvalid = now
		}
		state.pendingMu.Unlock()

		// Write to PTY log immediately (persistence, not affected by buffering)
		if state.ptyLog != nil {
			state.ptyLog.Write(data)
		}
		if shouldInvalidate {
			a.invalidateSession(name)
		}
	})

	state.pty.SetOnExit(func(err error) {
		// PTY exited - check if tmux session still exists (detach vs death)
		if !tmux.HasSession(name) {
			a.mu.Lock()
			delete(a.sessions, name)
			a.mu.Unlock()

			// startupComplete means the app is fully running: any exit is intentional
			// (user typed 'exit'). Clean up so the session doesn't resurrect on restart.
			// If startupComplete is false we're still in discoverSessions(), which means
			// a PTY died during startup reconnection — rare, skip cleanup so it can retry.
			shouldCleanup := a.startupComplete

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
	// Kill tmux scrollback BEFORE attaching. On attach, tmux dumps its
	// scrollback + screen through the PTY. Without this, old history floods
	// the parser and appears as replayed content.
	// Must set per-pane history-limit first — existing panes retain their old limit.
	tmux.SetPaneHistoryLimit(name, 1)
	tmux.ClearHistory(name)

	// Create PTY and attach to tmux session
	cols := uint16(120)
	rows := uint16(24)

	ptySess := pty.NewSession(name)

	// Create emulator components
	screen := emulator.NewScreen(int(cols), int(rows))
	sbPath := emulator.ScrollbackPath(name)
	scrollback, err := emulator.NewScrollbackWithPath(sbPath)
	if err != nil {
		scrollback = emulator.NewScrollback() // fallback to in-memory
	}
	parser := emulator.NewParser(screen, scrollback)

	// Truncate the ptylog — scrollback is persisted in the .scrollback file,
	// and tmux redraws the current screen on attach, so replay is unnecessary.
	ptylog.TruncateLog(name)

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

	// Restore persisted lastActivity from config (adjusted in normalizeActivityTimes)
	var lastActivity time.Time
	if sessionInfo.LastActivity > 0 {
		lastActivity = time.Unix(sessionInfo.LastActivity, 0)
	}

	state := &SessionState{
		app:          a,
		pty:          ptySess,
		name:         name,
		sshHost:      sessionInfo.SSHHost,
		parser:       parser,
		screen:       screen,
		scrollback:   scrollback,
		colors:       sessionColor,
		ptyLog:       logWriter,
		lastActivity: lastActivity,
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
		// Claude sessions run claude --continue to resume the last conversation.
		// CLAUDE_BINARY_PATH env var allows overriding the binary path (used in tests).
		claudePath := os.Getenv("CLAUDE_BINARY_PATH")
		if claudePath == "" {
			claudePath = filepath.Join(os.Getenv("HOME"), ".local", "bin", "claude")
		}
		initialCmd = []string{claudePath, "--continue"}
	} else if info.Type == "codex" {
		// Codex sessions run codex --resume to continue the last conversation.
		initialCmd = []string{"codex", "--resume"}
	}
	if err := tmux.NewSession(name, workDir, cols, rows, initialCmd...); err != nil {
		return err
	}

	ptySess := pty.NewSession(name)

	// Create emulator components
	screen := emulator.NewScreen(int(cols), int(rows))
	sbPath := emulator.ScrollbackPath(name)
	scrollback, err := emulator.NewScrollbackWithPath(sbPath)
	if err != nil {
		scrollback = emulator.NewScrollback() // fallback to in-memory
	}
	parser := emulator.NewParser(screen, scrollback)

	// Replay saved PTY log to restore screen state.
	// Truncate the ptylog — scrollback is persisted in the .scrollback file,
	// and tmux redraws the current screen on attach, so replay is unnecessary.
	ptylog.TruncateLog(name)

	// Start PTY log writer
	logWriter, _ := ptylog.NewWriter(name)

	// Look up or assign session color
	sessionColor := a.resolveSessionColor(name)

	// Restore persisted lastActivity from config (adjusted in normalizeActivityTimes)
	var lastActivity time.Time
	if info.LastActivity > 0 {
		lastActivity = time.Unix(info.LastActivity, 0)
	}

	state := &SessionState{
		app:          a,
		pty:          ptySess,
		name:         name,
		sshHost:      info.SSHHost,
		parser:       parser,
		screen:       screen,
		scrollback:   scrollback,
		colors:       sessionColor,
		ptyLog:       logWriter,
		lastActivity: lastActivity,
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

// StartTrace begins tracing the named session, writing JSONL to ~/.config/prompt-grid/traces/.
// Returns the trace file path.
func (a *App) StartTrace(sessionName string) (string, error) {
	a.mu.RLock()
	state, ok := a.sessions[sessionName]
	a.mu.RUnlock()
	if !ok {
		return "", ErrSessionNotFound
	}

	state.screenMu.RLock()
	screen := state.Screen()
	cols, rows := screen.Size()
	scrollbackCount := state.scrollback.Count()
	screenText := captureScreenText(screen, cols, rows)
	state.screenMu.RUnlock()

	tracer, path, err := trace.Start(sessionName, cols, rows, scrollbackCount)
	if err != nil {
		return "", err
	}

	// Log initial screen state
	tracer.Log(trace.Event{
		Type:   "screen",
		Text:   screenText,
		Detail: "initial",
	})

	a.traceMu.Lock()
	a.tracer = tracer
	a.traceSession = sessionName
	a.traceMu.Unlock()

	return path, nil
}

// StopTrace stops the active trace and returns the file path.
func (a *App) StopTrace() string {
	a.traceMu.Lock()
	t := a.tracer
	session := a.traceSession
	a.tracer = nil
	a.traceSession = ""
	a.traceMu.Unlock()

	if t == nil {
		return ""
	}

	// Capture final screen
	a.mu.RLock()
	state, ok := a.sessions[session]
	a.mu.RUnlock()
	if ok {
		state.screenMu.RLock()
		screen := state.Screen()
		cols, rows := screen.Size()
		screenText := captureScreenText(screen, cols, rows)
		state.screenMu.RUnlock()

		t.Log(trace.Event{
			Type:   "screen",
			Text:   screenText,
			Detail: "final",
		})
	}

	return t.Stop()
}

// IsTracing returns true if any session is being traced.
func (a *App) IsTracing() bool {
	a.traceMu.RLock()
	defer a.traceMu.RUnlock()
	return a.tracer != nil
}

// TracingSession returns the name of the session being traced (empty if not tracing).
func (a *App) TracingSession() string {
	a.traceMu.RLock()
	defer a.traceMu.RUnlock()
	return a.traceSession
}

// traceEvent logs an event if the named session is being traced.
func (a *App) traceEvent(sessionName string, ev trace.Event) {
	a.traceMu.RLock()
	t := a.tracer
	ts := a.traceSession
	a.traceMu.RUnlock()
	if t != nil && ts == sessionName {
		t.Log(ev)
	}
}

// captureScreenText returns the current screen content as plain text.
func captureScreenText(screen *emulator.Screen, cols, rows int) string {
	var sb strings.Builder
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			cell := screen.Cell(x, y)
			if cell.Rune == 0 {
				sb.WriteRune(' ')
			} else {
				sb.WriteRune(cell.Rune)
			}
		}
		if y < rows-1 {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
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
	sbPath := emulator.ScrollbackPath(name)
	scrollback, err := emulator.NewScrollbackWithPath(sbPath)
	if err != nil {
		scrollback = emulator.NewScrollback() // fallback to in-memory
	}
	parser := emulator.NewParser(screen, scrollback)

	// Start PTY log writer
	logWriter, _ := ptylog.NewWriter(name)

	// Look up or assign session color
	sessionColor := a.resolveSessionColor(name)

	state := &SessionState{
		app:          a,
		pty:          ptySess,
		name:         name,
		sshHost:      sshHost,
		parser:       parser,
		screen:       screen,
		scrollback:   scrollback,
		colors:       sessionColor,
		ptyLog:       logWriter,
		lastActivity: time.Now(),
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
	sbPath2 := emulator.ScrollbackPath(name)
	scrollback2, err2 := emulator.NewScrollbackWithPath(sbPath2)
	if err2 != nil {
		scrollback2 = emulator.NewScrollback() // fallback to in-memory
	}
	parser := emulator.NewParser(screen, scrollback2)

	// Start PTY log writer
	logWriter, _ := ptylog.NewWriter(name)

	// Look up or assign session color
	sessionColor := a.resolveSessionColor(name)

	state := &SessionState{
		app:          a,
		pty:          ptySess,
		name:         name,
		parser:       parser,
		screen:       screen,
		scrollback:   scrollback2,
		colors:       sessionColor,
		ptyLog:       logWriter,
		lastActivity: time.Now(),
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

// TouchActivity marks a session as recently active (called on user input).
func (a *App) TouchActivity(name string) {
	a.mu.RLock()
	state := a.sessions[name]
	a.mu.RUnlock()
	if state != nil {
		state.lastActivity = time.Now()
	}
}

// IsSessionActive returns true if a session had user input within the given duration.
func (a *App) IsSessionActive(name string, within time.Duration) bool {
	a.mu.RLock()
	state := a.sessions[name]
	a.mu.RUnlock()
	if state == nil {
		return false
	}
	return time.Since(state.lastActivity) <= within
}

// FilteredSessions returns the sessions visible in the sidebar given current
// collapse mode, search query, selected session, and revealed sessions.
// Used by the sidebar layout and by BDD tests.
func (a *App) FilteredSessions(searchQuery, selected string, revealedSessions map[string]bool) (visible []string, hidden int) {
	allSessions := a.ListSessions()
	collapseMode := a.config != nil && a.config.GetCollapseInactive()

	if searchQuery != "" {
		for _, name := range allSessions {
			if strings.Contains(strings.ToLower(name), strings.ToLower(searchQuery)) {
				visible = append(visible, name)
			}
		}
	} else if collapseMode {
		for _, name := range allSessions {
			if a.IsSessionActive(name, 2*time.Hour) || name == selected || revealedSessions[name] {
				visible = append(visible, name)
			} else {
				hidden++
			}
		}
	} else {
		visible = allSessions
	}
	return
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

	if state.scrollback != nil {
		state.scrollback.Close()
	}

	if state.pty != nil {
		state.pty.Close()
	}

	tmux.KillSession(actualName)

	// Remove saved color, window size, session info, PTY log, and scrollback
	ptylog.DeleteLog(actualName)
	emulator.DeleteScrollback(actualName)
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

	// Close old log writer and scrollback, rename files, open new ones
	if state.ptyLog != nil {
		state.ptyLog.Close()
	}
	if state.scrollback != nil {
		state.scrollback.Close()
	}
	ptylog.RenameLog(actualName, newName)
	emulator.RenameScrollback(actualName, newName)
	state.ptyLog, _ = ptylog.NewWriter(newName)
	sbPath := emulator.ScrollbackPath(newName)
	if sb, sbErr := emulator.NewScrollbackWithPath(sbPath); sbErr == nil {
		state.scrollback = sb
	}

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

// invalidateSession signals windows to redraw for a session.
// Only invalidates the control window when this is the selected session,
// avoiding unnecessary redraws from background session output.
func (a *App) invalidateSession(name string) {
	a.mu.RLock()
	state := a.sessions[name]
	a.mu.RUnlock()

	if state != nil && state.window != nil {
		state.window.Invalidate()
	}

	if a.controlWin != nil && a.controlWin.selected == name {
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
