package gui

import (
	"image/color"
	"regexp"
	"strings"
	"time"

	"prompt-grid/src/emulator"
)

// TestDriver provides programmatic control of GUI for testing.
// It allows tests to simulate user actions and query GUI state.
type TestDriver struct {
	app *App
}

// NewTestDriver creates a new test driver for the given app
func NewTestDriver(app *App) *TestDriver {
	return &TestDriver{app: app}
}

// --- Session Management ---

// CreateSession creates a new session and waits for it to be ready
func (d *TestDriver) CreateSession(name string) error {
	_, err := d.app.NewSession(name, "", "")
	if err != nil {
		return err
	}
	// Wait for session to be ready (shell prompt)
	d.WaitForOutput(name, time.Second*5)
	return nil
}

// CloseSession closes a session
func (d *TestDriver) CloseSession(name string) error {
	return d.app.CloseSession(name)
}

// ListSessions returns all session names
func (d *TestDriver) ListSessions() []string {
	return d.app.ListSessions()
}

// --- Input Actions ---

// TypeText sends text to a session as if typed by the user
func (d *TestDriver) TypeText(sessionName, text string) {
	state := d.app.GetSession(sessionName)
	if state == nil || state.pty == nil {
		return
	}
	state.pty.Write([]byte(text))
}

// SendKeys sends special key sequences (e.g., Enter, Ctrl+C)
func (d *TestDriver) SendKeys(sessionName string, keys ...byte) {
	state := d.app.GetSession(sessionName)
	if state == nil || state.pty == nil {
		return
	}
	state.pty.Write(keys)
}

// SendEnter sends Enter key
func (d *TestDriver) SendEnter(sessionName string) {
	d.SendKeys(sessionName, '\r')
}

// SendCtrlC sends Ctrl+C
func (d *TestDriver) SendCtrlC(sessionName string) {
	d.SendKeys(sessionName, 0x03)
}

// --- Selection Actions ---

// StartSelection begins a selection at cell position
func (d *TestDriver) StartSelection(sessionName string, x, y int) {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return
	}
	state.StartSelection(x, y)
}

// UpdateSelection updates selection endpoint
func (d *TestDriver) UpdateSelection(sessionName string, x, y int) {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return
	}
	state.UpdateSelection(x, y)
}

// EndSelection completes the selection
func (d *TestDriver) EndSelection(sessionName string) {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return
	}
	state.EndSelection()
}

// ClearSelection clears the current selection
func (d *TestDriver) ClearSelection(sessionName string) {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return
	}
	state.ClearSelection()
}

// SelectRange selects from (x1,y1) to (x2,y2)
func (d *TestDriver) SelectRange(sessionName string, x1, y1, x2, y2 int) {
	d.StartSelection(sessionName, x1, y1)
	d.UpdateSelection(sessionName, x2, y2)
	d.EndSelection(sessionName)
}

// --- Scrollback Actions ---

// ScrollUp scrolls up by n lines in scrollback
func (d *TestDriver) ScrollUp(sessionName string, lines int) {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return
	}
	state.AdjustScrollOffset(lines)
}

// ScrollDown scrolls down by n lines (toward live view)
func (d *TestDriver) ScrollDown(sessionName string, lines int) {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return
	}
	state.AdjustScrollOffset(-lines)
}

// ScrollToTop scrolls to the top of scrollback
func (d *TestDriver) ScrollToTop(sessionName string) {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return
	}
	state.SetScrollOffset(state.scrollback.Count())
}

// ScrollToBottom scrolls to live view
func (d *TestDriver) ScrollToBottom(sessionName string) {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return
	}
	state.ResetScrollOffset()
}

// --- State Queries ---

// GetScreenContent returns the visible screen content as runes
func (d *TestDriver) GetScreenContent(sessionName string) [][]rune {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return nil
	}
	cols, rows := state.Screen().Size()
	content := make([][]rune, rows)
	for y := 0; y < rows; y++ {
		content[y] = make([]rune, cols)
		for x := 0; x < cols; x++ {
			cell := state.Screen().Cell(x, y)
			if cell.Rune == 0 {
				content[y][x] = ' '
			} else {
				content[y][x] = cell.Rune
			}
		}
	}
	return content
}

// GetScreenText returns the visible screen as a string (lines joined by newlines)
func (d *TestDriver) GetScreenText(sessionName string) string {
	content := d.GetScreenContent(sessionName)
	if content == nil {
		return ""
	}
	lines := make([]string, len(content))
	for i, row := range content {
		lines[i] = strings.TrimRight(string(row), " ")
	}
	return strings.Join(lines, "\n")
}

// GetCell returns the cell at the given position
func (d *TestDriver) GetCell(sessionName string, x, y int) emulator.Cell {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return emulator.Cell{}
	}
	return state.Screen().Cell(x, y)
}

// GetCursorPosition returns the cursor position
func (d *TestDriver) GetCursorPosition(sessionName string) (x, y int) {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return 0, 0
	}
	cursor := state.Screen().Cursor()
	return cursor.X, cursor.Y
}

// GetScrollOffset returns the current scroll offset
func (d *TestDriver) GetScrollOffset(sessionName string) int {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return 0
	}
	return state.ScrollOffset()
}

// GetScrollbackCount returns the total lines in scrollback
func (d *TestDriver) GetScrollbackCount(sessionName string) int {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return 0
	}
	return state.scrollback.Count()
}

// GetSelectedText returns the currently selected text
func (d *TestDriver) GetSelectedText(sessionName string) string {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return ""
	}
	return state.GetSelectedText()
}

// HasSelection returns whether there is an active selection
func (d *TestDriver) HasSelection(sessionName string) bool {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return false
	}
	return state.HasSelection()
}

// IsSelected returns whether the given cell is selected
func (d *TestDriver) IsSelected(sessionName string, x, y int) bool {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return false
	}
	return state.IsSelected(x, y)
}

// GetSessionColor returns the session's color scheme
func (d *TestDriver) GetSessionColor(sessionName string) color.NRGBA {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return color.NRGBA{}
	}
	return state.colors.Background
}

// GetScreenSize returns the terminal dimensions
func (d *TestDriver) GetScreenSize(sessionName string) (cols, rows int) {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return 0, 0
	}
	return state.Screen().Size()
}

// --- Wait Helpers ---

// WaitForOutput waits for any output from the session
func (d *TestDriver) WaitForOutput(sessionName string, timeout time.Duration) bool {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return false
	}

	deadline := time.Now().Add(timeout)
	initialContent := d.GetScreenText(sessionName)

	for time.Now().Before(deadline) {
		current := d.GetScreenText(sessionName)
		if current != initialContent {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// WaitForContent waits for specific content to appear on screen
func (d *TestDriver) WaitForContent(sessionName, pattern string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		content := d.GetScreenText(sessionName)
		if strings.Contains(content, pattern) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// WaitForPattern waits for a regex pattern to match screen content
func (d *TestDriver) WaitForPattern(sessionName, pattern string, timeout time.Duration) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		content := d.GetScreenText(sessionName)
		if re.MatchString(content) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// WaitForScrollback waits for scrollback to reach at least n lines
func (d *TestDriver) WaitForScrollback(sessionName string, minLines int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		count := d.GetScrollbackCount(sessionName)
		if count >= minLines {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// --- Control Window / Rename Actions ---

// EnsureControlWindow creates the control window if it doesn't exist
func (d *TestDriver) EnsureControlWindow() {
	if d.app.controlWin == nil {
		d.app.CreateControlWindow()
	}
}

// StartRename begins renaming a session tab
func (d *TestDriver) StartRename(sessionName string) {
	d.EnsureControlWindow()
	d.app.controlWin.startRename(sessionName)
}

// IsRenaming returns whether a rename operation is active
func (d *TestDriver) IsRenaming() bool {
	if d.app.controlWin == nil {
		return false
	}
	return d.app.controlWin.renameState.active
}

// GetRenameSessionName returns the session being renamed
func (d *TestDriver) GetRenameSessionName() string {
	if d.app.controlWin == nil {
		return ""
	}
	return d.app.controlWin.renameState.sessionName
}

// GetRenameName returns the current text in the rename input
func (d *TestDriver) GetRenameName() string {
	if d.app.controlWin == nil {
		return ""
	}
	return d.app.controlWin.renameState.newName
}

// GetRenameCursorPos returns the cursor position in the rename input
func (d *TestDriver) GetRenameCursorPos() int {
	if d.app.controlWin == nil {
		return 0
	}
	return d.app.controlWin.renameState.cursorPos
}

// TypeInRename replaces the rename input text
func (d *TestDriver) TypeInRename(text string) {
	if d.app.controlWin == nil {
		return
	}
	d.app.controlWin.renameState.newName = text
	d.app.controlWin.renameState.cursorPos = len(text)
}

// ConfirmRename confirms the rename (simulates pressing Enter)
func (d *TestDriver) ConfirmRename() {
	if d.app.controlWin == nil {
		return
	}
	d.app.controlWin.confirmRename()
}

// CancelRename cancels the rename (simulates pressing Escape)
func (d *TestDriver) CancelRename() {
	if d.app.controlWin == nil {
		return
	}
	d.app.controlWin.cancelRename()
}

// GetControlSelected returns the currently selected tab in the control window
func (d *TestDriver) GetControlSelected() string {
	if d.app.controlWin == nil {
		return ""
	}
	return d.app.controlWin.selected
}

// SetControlSelected sets the selected tab in the control window
func (d *TestDriver) SetControlSelected(name string) {
	d.EnsureControlWindow()
	d.app.controlWin.selected = name
}

// WaitForSessionName waits for a session name to appear in the session list
func (d *TestDriver) WaitForSessionName(name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, s := range d.app.ListSessions() {
			if s == name {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// WaitForSessionNameGone waits for a session name to disappear from the session list
func (d *TestDriver) WaitForSessionNameGone(name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		found := false
		for _, s := range d.app.ListSessions() {
			if s == name {
				found = true
				break
			}
		}
		if !found {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// --- Pop Out / Call Back ---

// PopOut pops out a session into a standalone window
func (d *TestDriver) PopOut(sessionName string) {
	d.app.PopOutSession(sessionName)
}

// CallBack calls back a session from its standalone window
func (d *TestDriver) CallBack(sessionName string) {
	d.app.CallBackSession(sessionName)
}

// HasWindow returns whether the session has a standalone window
func (d *TestDriver) HasWindow(sessionName string) bool {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return false
	}
	return state.window != nil
}

// WaitForWindow waits for a session to have (or not have) a standalone window
func (d *TestDriver) WaitForWindow(sessionName string, want bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if d.HasWindow(sessionName) == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// --- Scrollback Content Queries ---

// GetScrollbackLine returns a line from scrollback (0 = oldest)
func (d *TestDriver) GetScrollbackLine(sessionName string, index int) string {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return ""
	}
	line := state.scrollback.Line(index)
	if line == nil {
		return ""
	}
	runes := make([]rune, len(line))
	for i, cell := range line {
		if cell.Rune == 0 {
			runes[i] = ' '
		} else {
			runes[i] = cell.Rune
		}
	}
	return strings.TrimRight(string(runes), " ")
}

// GetScrollbackRange returns lines from scrollback [start, end)
func (d *TestDriver) GetScrollbackRange(sessionName string, start, end int) []string {
	state := d.app.GetSession(sessionName)
	if state == nil {
		return nil
	}
	lines := state.scrollback.Lines(start, end)
	result := make([]string, len(lines))
	for i, line := range lines {
		runes := make([]rune, len(line))
		for j, cell := range line {
			if cell.Rune == 0 {
				runes[j] = ' '
			} else {
				runes[j] = cell.Rune
			}
		}
		result[i] = strings.TrimRight(string(runes), " ")
	}
	return result
}
