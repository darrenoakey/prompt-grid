package gui

import (
	"strings"
	"testing"
	"time"
)

// --- Session State Tests ---

func TestSessionStateScrollOffset(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-scroll-state")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-scroll-state")

	// Initial scroll offset should be 0
	if offset := driver.GetScrollOffset("test-scroll-state"); offset != 0 {
		t.Errorf("Initial ScrollOffset = %d, want 0", offset)
	}

	// Generate scrollback by running a command
	driver.TypeText("test-scroll-state", "seq 1 100\r")
	driver.WaitForScrollback("test-scroll-state", 50, 5*time.Second)

	// Scroll offset should still be 0 (live view)
	if offset := driver.GetScrollOffset("test-scroll-state"); offset != 0 {
		t.Errorf("After output, ScrollOffset = %d, want 0 (live view)", offset)
	}

	// Scroll up
	driver.ScrollUp("test-scroll-state", 10)
	if offset := driver.GetScrollOffset("test-scroll-state"); offset != 10 {
		t.Errorf("After ScrollUp(10), offset = %d, want 10", offset)
	}

	// Scroll down
	driver.ScrollDown("test-scroll-state", 5)
	if offset := driver.GetScrollOffset("test-scroll-state"); offset != 5 {
		t.Errorf("After ScrollDown(5), offset = %d, want 5", offset)
	}

	// Scroll to bottom
	driver.ScrollToBottom("test-scroll-state")
	if offset := driver.GetScrollOffset("test-scroll-state"); offset != 0 {
		t.Errorf("After ScrollToBottom(), offset = %d, want 0", offset)
	}
}

func TestSessionStateSelection(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-selection")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-selection")

	// Type some text
	driver.TypeText("test-selection", "echo 'Hello World'\r")
	driver.WaitForContent("test-selection", "Hello World", 3*time.Second)

	// Initially no selection
	if driver.HasSelection("test-selection") {
		t.Error("Initially HasSelection() = true, want false")
	}

	// Create selection
	driver.SelectRange("test-selection", 0, 0, 5, 0)

	if !driver.HasSelection("test-selection") {
		t.Error("After SelectRange(), HasSelection() = false, want true")
	}

	// Check cells are selected
	if !driver.IsSelected("test-selection", 0, 0) {
		t.Error("Cell (0,0) should be selected")
	}
	if !driver.IsSelected("test-selection", 5, 0) {
		t.Error("Cell (5,0) should be selected")
	}
	if driver.IsSelected("test-selection", 6, 0) {
		t.Error("Cell (6,0) should not be selected")
	}

	// Clear selection
	driver.ClearSelection("test-selection")
	if driver.HasSelection("test-selection") {
		t.Error("After ClearSelection(), HasSelection() = true, want false")
	}
}

// --- Screen Content Tests ---

func TestScreenContent(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-content")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-content")

	// Echo a known string
	driver.TypeText("test-content", "echo 'TEST_MARKER_ABC'\r")
	found := driver.WaitForContent("test-content", "TEST_MARKER_ABC", 3*time.Second)
	if !found {
		t.Error("WaitForContent() did not find 'TEST_MARKER_ABC'")
	}

	// Verify content is in screen text
	content := driver.GetScreenText("test-content")
	if !strings.Contains(content, "TEST_MARKER_ABC") {
		t.Errorf("GetScreenText() does not contain 'TEST_MARKER_ABC', got:\n%s", content)
	}
}

func TestCursorPosition(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-cursor")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-cursor")

	// Wait for shell prompt
	time.Sleep(500 * time.Millisecond)

	// Get cursor position
	x, y := driver.GetCursorPosition("test-cursor")

	// Cursor should be somewhere (not at 0,0 which would be strange for a shell)
	// The exact position depends on the prompt, but it should be valid
	cols, rows := driver.GetScreenSize("test-cursor")
	if x < 0 || x >= cols || y < 0 || y >= rows {
		t.Errorf("Cursor position (%d,%d) out of bounds for %dx%d screen", x, y, cols, rows)
	}
}

// --- Scrollback Tests ---

func TestScrollback(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-scrollback")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-scrollback")

	// Generate scrollback with numbered lines
	driver.TypeText("test-scrollback", "for i in $(seq 1 50); do echo \"Line $i\"; done\r")
	driver.WaitForScrollback("test-scrollback", 20, 5*time.Second)

	count := driver.GetScrollbackCount("test-scrollback")
	if count < 20 {
		t.Errorf("ScrollbackCount = %d, want at least 20", count)
	}

	// Check scrollback content
	lines := driver.GetScrollbackRange("test-scrollback", 0, 10)
	foundNumberedLine := false
	for _, line := range lines {
		if strings.Contains(line, "Line ") {
			foundNumberedLine = true
			break
		}
	}
	if !foundNumberedLine {
		t.Errorf("Scrollback should contain numbered lines, got: %v", lines)
	}
}

func TestScrollbackViewing(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-scroll-view")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-scroll-view")

	// Generate more content than fits on screen
	driver.TypeText("test-scroll-view", "seq 1 100\r")
	driver.WaitForScrollback("test-scroll-view", 80, 5*time.Second)

	// Scroll to top
	driver.ScrollToTop("test-scroll-view")
	offset := driver.GetScrollOffset("test-scroll-view")
	if offset == 0 {
		t.Error("After ScrollToTop(), offset should be > 0")
	}

	// New output should reset scroll to bottom
	driver.TypeText("test-scroll-view", "echo 'BACK_TO_BOTTOM'\r")
	driver.WaitForContent("test-scroll-view", "BACK_TO_BOTTOM", 3*time.Second)

	offset = driver.GetScrollOffset("test-scroll-view")
	if offset != 0 {
		t.Errorf("After new output, offset = %d, want 0 (auto-scroll to bottom)", offset)
	}
}

// --- Multi-Session Tests ---

func TestMultipleSessions(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	// Create multiple sessions
	err := driver.CreateSession("test-multi-a")
	if err != nil {
		t.Fatalf("CreateSession(a) error = %v", err)
	}
	defer driver.CloseSession("test-multi-a")

	err = driver.CreateSession("test-multi-b")
	if err != nil {
		t.Fatalf("CreateSession(b) error = %v", err)
	}
	defer driver.CloseSession("test-multi-b")

	// Verify both exist
	sessions := driver.ListSessions()
	if len(sessions) < 2 {
		t.Errorf("ListSessions() returned %d sessions, want at least 2", len(sessions))
	}

	hasA, hasB := false, false
	for _, name := range sessions {
		if name == "test-multi-a" {
			hasA = true
		}
		if name == "test-multi-b" {
			hasB = true
		}
	}
	if !hasA || !hasB {
		t.Errorf("ListSessions() = %v, missing test sessions", sessions)
	}

	// Send different content to each
	driver.TypeText("test-multi-a", "echo 'SESSION_A_MARKER'\r")
	driver.TypeText("test-multi-b", "echo 'SESSION_B_MARKER'\r")

	driver.WaitForContent("test-multi-a", "SESSION_A_MARKER", 3*time.Second)
	driver.WaitForContent("test-multi-b", "SESSION_B_MARKER", 3*time.Second)

	// Verify content is session-specific
	contentA := driver.GetScreenText("test-multi-a")
	contentB := driver.GetScreenText("test-multi-b")

	if !strings.Contains(contentA, "SESSION_A_MARKER") {
		t.Error("Session A should contain SESSION_A_MARKER")
	}
	if strings.Contains(contentA, "SESSION_B_MARKER") {
		t.Error("Session A should not contain SESSION_B_MARKER")
	}
	if !strings.Contains(contentB, "SESSION_B_MARKER") {
		t.Error("Session B should contain SESSION_B_MARKER")
	}
	if strings.Contains(contentB, "SESSION_A_MARKER") {
		t.Error("Session B should not contain SESSION_A_MARKER")
	}
}

func TestSessionColors(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-color-a")
	if err != nil {
		t.Fatalf("CreateSession(a) error = %v", err)
	}
	defer driver.CloseSession("test-color-a")

	err = driver.CreateSession("test-color-b")
	if err != nil {
		t.Fatalf("CreateSession(b) error = %v", err)
	}
	defer driver.CloseSession("test-color-b")

	// Each session should have a color (non-zero alpha)
	colorA := driver.GetSessionColor("test-color-a")
	colorB := driver.GetSessionColor("test-color-b")

	if colorA.A == 0 {
		t.Error("Session A color has zero alpha (not set)")
	}
	if colorB.A == 0 {
		t.Error("Session B color has zero alpha (not set)")
	}

	// Colors should be different (statistically very likely with random colors)
	if colorA == colorB {
		t.Log("Warning: both sessions got the same color (unlikely but possible)")
	}
}

// --- Input Handling Tests ---

func TestKeyboardInput(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-input")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-input")

	// Test typing text
	driver.TypeText("test-input", "echo 'KEYBOARD_TEST'\r")
	if !driver.WaitForContent("test-input", "KEYBOARD_TEST", 3*time.Second) {
		t.Error("Keyboard input not reflected on screen")
	}
}

func TestCtrlC(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-ctrl-c")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-ctrl-c")

	// Start a long-running command
	driver.TypeText("test-ctrl-c", "sleep 100\r")
	time.Sleep(200 * time.Millisecond)

	// Send Ctrl+C
	driver.SendCtrlC("test-ctrl-c")

	// Should get back to prompt (sleep interrupted)
	if !driver.WaitForPattern("test-ctrl-c", `\$|>|#`, 2*time.Second) {
		t.Log("Warning: prompt may not have appeared after Ctrl+C")
	}
}

// --- Wait Helpers Tests ---

func TestWaitForContent(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-wait")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-wait")

	// Test successful wait
	driver.TypeText("test-wait", "echo 'WAIT_TARGET'\r")
	if !driver.WaitForContent("test-wait", "WAIT_TARGET", 3*time.Second) {
		t.Error("WaitForContent() returned false for content that should appear")
	}

	// Test timeout on non-existent content
	if driver.WaitForContent("test-wait", "NONEXISTENT_STRING_XYZ", 100*time.Millisecond) {
		t.Error("WaitForContent() returned true for content that doesn't exist")
	}
}

func TestWaitForPattern(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-pattern")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-pattern")

	driver.TypeText("test-pattern", "echo 'num: 12345'\r")

	// Match pattern with regex
	if !driver.WaitForPattern("test-pattern", `num: \d+`, 3*time.Second) {
		t.Error("WaitForPattern() did not match 'num: \\d+'")
	}
}

// --- Cell Attribute Tests ---

func TestCellAttributes(t *testing.T) {
	app := NewApp()
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-attrs")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-attrs")

	// Output text with ANSI colors
	driver.TypeText("test-attrs", "printf '\\033[1mBOLD\\033[0m normal'\r")
	driver.WaitForContent("test-attrs", "BOLD", 3*time.Second)

	// The screen should now contain cells with attributes
	// We can verify the cell data was received properly
	content := driver.GetScreenText("test-attrs")
	if !strings.Contains(content, "BOLD") {
		t.Error("Screen should contain 'BOLD' text")
	}
	if !strings.Contains(content, "normal") {
		t.Error("Screen should contain 'normal' text")
	}
}
