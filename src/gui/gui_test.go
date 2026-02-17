package gui

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"prompt-grid/src/config"
)

// --- Session State Tests ---

func TestSessionStateScrollOffset(t *testing.T) {
	app := NewApp(nil, "")
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
	app := NewApp(nil, "")
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
	app := NewApp(nil, "")
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
	app := NewApp(nil, "")
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
	app := NewApp(nil, "")
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
	app := NewApp(nil, "")
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
	app := NewApp(nil, "")
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
	app := NewApp(nil, "")
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
	app := NewApp(nil, "")
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
	app := NewApp(nil, "")
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
	app := NewApp(nil, "")
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
	app := NewApp(nil, "")
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

// --- Rename Tests (BDD-style using TestDriver) ---

func TestRenameCancelPreservesOriginalName(t *testing.T) {
	app := NewApp(nil, "")
	driver := NewTestDriver(app)

	// Given: I have an open control center with two sessions
	err := driver.CreateSession("session1")
	if err != nil {
		t.Fatalf("CreateSession(session1) error = %v", err)
	}
	defer driver.CloseSession("session1")

	err = driver.CreateSession("session2")
	if err != nil {
		t.Fatalf("CreateSession(session2) error = %v", err)
	}
	defer driver.CloseSession("session2")

	driver.EnsureControlWindow()

	// When: I right-click on session1 and select rename
	driver.StartRename("session1")

	// Then: the cursor should be in an edit box at the end of "session1"
	if !driver.IsRenaming() {
		t.Fatal("Expected rename to be active")
	}
	if name := driver.GetRenameName(); name != "session1" {
		t.Errorf("Rename input = %q, want %q", name, "session1")
	}
	if pos := driver.GetRenameCursorPos(); pos != len("session1") {
		t.Errorf("Cursor position = %d, want %d", pos, len("session1"))
	}

	// When: I change the name to "bob"
	driver.TypeInRename("bob")

	if name := driver.GetRenameName(); name != "bob" {
		t.Errorf("After typing, rename input = %q, want %q", name, "bob")
	}

	// When: I press escape
	driver.CancelRename()

	// Then: rename should be cancelled
	if driver.IsRenaming() {
		t.Error("Rename should not be active after cancel")
	}

	// Then: there should be two sessions with original names (escape cancels)
	sessions := driver.ListSessions()
	if len(sessions) != 2 {
		t.Fatalf("Expected 2 sessions, got %d: %v", len(sessions), sessions)
	}
	hasSession1, hasSession2 := false, false
	for _, name := range sessions {
		if name == "session1" {
			hasSession1 = true
		}
		if name == "session2" {
			hasSession2 = true
		}
	}
	if !hasSession1 || !hasSession2 {
		t.Errorf("Expected sessions [session1, session2], got %v", sessions)
	}
}

func TestRenameConfirmChangesName(t *testing.T) {
	app := NewApp(nil, "")
	driver := NewTestDriver(app)

	// Given: I have an open control center with two sessions
	err := driver.CreateSession("session1")
	if err != nil {
		t.Fatalf("CreateSession(session1) error = %v", err)
	}

	err = driver.CreateSession("session2")
	if err != nil {
		t.Fatalf("CreateSession(session2) error = %v", err)
	}
	defer driver.CloseSession("session2")

	driver.EnsureControlWindow()

	// When: I right-click on session1 and select rename
	driver.StartRename("session1")

	// Then: rename should be active with session1's name
	if !driver.IsRenaming() {
		t.Fatal("Expected rename to be active")
	}
	if name := driver.GetRenameSessionName(); name != "session1" {
		t.Errorf("Rename session = %q, want %q", name, "session1")
	}

	// When: I change the name to "bob"
	driver.TypeInRename("bob")

	// When: I press enter (confirm)
	driver.ConfirmRename()

	// Then: rename should be deactivated
	if driver.IsRenaming() {
		t.Error("Rename should not be active after confirm")
	}

	// Then: wait for async rename to complete
	if !driver.WaitForSessionName("bob", 5*time.Second) {
		t.Fatal("Session 'bob' did not appear after rename")
	}

	// Then: there should be two sessions, bob and session2
	sessions := driver.ListSessions()
	hasBob, hasSession2 := false, false
	for _, name := range sessions {
		if name == "bob" {
			hasBob = true
		}
		if name == "session2" {
			hasSession2 = true
		}
	}
	if !hasBob || !hasSession2 {
		t.Errorf("Expected sessions [bob, session2], got %v", sessions)
	}

	// Verify old name is gone
	if driver.WaitForSessionName("session1", 100*time.Millisecond) {
		t.Error("Session 'session1' should not exist after rename")
	}

	// Cleanup
	driver.CloseSession("bob")
}

func TestRenameNoChangeIsNoop(t *testing.T) {
	app := NewApp(nil, "")
	driver := NewTestDriver(app)

	// Given: a session
	err := driver.CreateSession("unchanged")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("unchanged")

	driver.EnsureControlWindow()

	// When: I start rename but don't change the name and press enter
	driver.StartRename("unchanged")
	driver.ConfirmRename()

	// Then: session name should be unchanged
	sessions := driver.ListSessions()
	found := false
	for _, name := range sessions {
		if name == "unchanged" {
			found = true
		}
	}
	if !found {
		t.Errorf("Session 'unchanged' should still exist, got %v", sessions)
	}
}

func TestRenameEmptyNameIsNoop(t *testing.T) {
	app := NewApp(nil, "")
	driver := NewTestDriver(app)

	// Given: a session
	err := driver.CreateSession("notempty")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("notempty")

	driver.EnsureControlWindow()

	// When: I start rename, clear the name, and press enter
	driver.StartRename("notempty")
	driver.TypeInRename("")
	driver.ConfirmRename()

	// Then: session should keep its original name
	sessions := driver.ListSessions()
	found := false
	for _, name := range sessions {
		if name == "notempty" {
			found = true
		}
	}
	if !found {
		t.Errorf("Session 'notempty' should still exist after empty rename, got %v", sessions)
	}
}

// --- Cell Attribute Tests ---

func TestCellAttributes(t *testing.T) {
	app := NewApp(nil, "")
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

// --- Color Persistence Tests ---

func TestColorPersistence(t *testing.T) {
	cfg := &config.Config{}
	app := NewApp(cfg, "")
	driver := NewTestDriver(app)

	// Create session — should assign a color and save to config
	err := driver.CreateSession("test-persist-color")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	// Get assigned color
	originalColor := driver.GetSessionColor("test-persist-color")
	if originalColor.A == 0 {
		t.Fatal("Session should have a color assigned")
	}

	// Verify color index was saved in config
	idx, ok := cfg.GetSessionColorIndex("test-persist-color")
	if !ok {
		t.Fatal("Color index should be saved in config")
	}

	// Close and recreate session
	driver.CloseSession("test-persist-color")
	time.Sleep(100 * time.Millisecond)

	// Color mapping should have been deleted on close
	_, ok = cfg.GetSessionColorIndex("test-persist-color")
	if ok {
		t.Fatal("Color index should be deleted after close")
	}

	// Manually set the color index back (simulating config loaded from disk)
	cfg.SetSessionColorIndex("test-persist-color2", idx)

	// Create session with same saved index
	err = driver.CreateSession("test-persist-color2")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-persist-color2")

	// Should get the same color as before
	restoredColor := driver.GetSessionColor("test-persist-color2")
	if restoredColor != originalColor {
		t.Errorf("Restored color %v != original %v", restoredColor, originalColor)
	}
}

func TestRecolorSession(t *testing.T) {
	cfg := &config.Config{}
	app := NewApp(cfg, "")
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-recolor")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-recolor")

	originalColor := driver.GetSessionColor("test-recolor")
	originalIdx, _ := cfg.GetSessionColorIndex("test-recolor")

	// Recolor until we get a different color (random, so loop)
	var newColor color.NRGBA
	for i := 0; i < 100; i++ {
		app.RecolorSession("test-recolor")
		newColor = driver.GetSessionColor("test-recolor")
		if newColor != originalColor {
			break
		}
	}

	if newColor == originalColor {
		t.Error("After recoloring, color should change (tried 100 times)")
	}

	// Verify config was updated
	newIdx, ok := cfg.GetSessionColorIndex("test-recolor")
	if !ok {
		t.Fatal("Config should have new color index")
	}
	if newIdx == originalIdx && newColor != originalColor {
		t.Error("Config index should have changed")
	}
}

func TestRenamePreservesColor(t *testing.T) {
	cfg := &config.Config{}
	app := NewApp(cfg, "")
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-rename-color")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	originalColor := driver.GetSessionColor("test-rename-color")
	originalIdx, _ := cfg.GetSessionColorIndex("test-rename-color")

	// Rename the session
	err = app.RenameSession("test-rename-color", "test-renamed-color")
	if err != nil {
		t.Fatalf("RenameSession() error = %v", err)
	}
	defer driver.CloseSession("test-renamed-color")

	// Color should be preserved under new name
	renamedColor := driver.GetSessionColor("test-renamed-color")
	if renamedColor != originalColor {
		t.Errorf("Color after rename %v != original %v", renamedColor, originalColor)
	}

	// Config should have new name, not old
	newIdx, ok := cfg.GetSessionColorIndex("test-renamed-color")
	if !ok {
		t.Fatal("Config should have color index under new name")
	}
	if newIdx != originalIdx {
		t.Errorf("Color index changed after rename: %d != %d", newIdx, originalIdx)
	}

	_, ok = cfg.GetSessionColorIndex("test-rename-color")
	if ok {
		t.Error("Config should not have color index under old name")
	}
}

// --- Pop Out / Call Back Tests ---

func TestSessionCreatedWithoutWindow(t *testing.T) {
	app := NewApp(nil, "")
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-no-window")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-no-window")

	// Session should exist but have no standalone window
	if driver.HasWindow("test-no-window") {
		t.Error("New session should not have a standalone window")
	}

	// Session should be functional (can receive input)
	driver.TypeText("test-no-window", "echo 'HEADLESS_TEST'\r")
	if !driver.WaitForContent("test-no-window", "HEADLESS_TEST", 3*time.Second) {
		t.Error("Session should be functional without a window")
	}
}

func TestCallBackSessionStillExists(t *testing.T) {
	app := NewApp(nil, "")
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-callback")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	defer driver.CloseSession("test-callback")

	// Session starts without a window
	if driver.HasWindow("test-callback") {
		t.Error("Session should start without window")
	}

	// After call back on a session with no window, session should still exist
	driver.CallBack("test-callback")
	state := app.GetSession("test-callback")
	if state == nil {
		t.Error("Session should still exist after call back")
	}
}

func TestCloseDeletesWindowSize(t *testing.T) {
	cfg := &config.Config{}
	app := NewApp(cfg, "")
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-close-size")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	// Manually set a window size in config
	cfg.SetWindowSize("test-close-size", 800, 600)

	// Close should delete the window size
	driver.CloseSession("test-close-size")
	time.Sleep(100 * time.Millisecond)

	_, ok := cfg.GetWindowSize("test-close-size")
	if ok {
		t.Error("Window size should be deleted after close")
	}
}

func TestRenamePreservesWindowSize(t *testing.T) {
	cfg := &config.Config{}
	app := NewApp(cfg, "")
	driver := NewTestDriver(app)

	err := driver.CreateSession("test-rename-size")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	// Set a window size
	cfg.SetWindowSize("test-rename-size", 1024, 768)

	// Rename the session
	err = app.RenameSession("test-rename-size", "test-renamed-size")
	if err != nil {
		t.Fatalf("RenameSession() error = %v", err)
	}
	defer driver.CloseSession("test-renamed-size")

	// Window size should be under new name
	size, ok := cfg.GetWindowSize("test-renamed-size")
	if !ok {
		t.Fatal("Window size should exist under new name")
	}
	if size[0] != 1024 || size[1] != 768 {
		t.Errorf("Window size = %v, want [1024, 768]", size)
	}

	// Old name should be gone
	_, ok = cfg.GetWindowSize("test-rename-size")
	if ok {
		t.Error("Window size should not exist under old name")
	}
}

func TestWindowSizeConfig(t *testing.T) {
	cfg := &config.Config{}

	// Initially no sizes
	_, ok := cfg.GetWindowSize("test")
	if ok {
		t.Error("Should not have window size initially")
	}

	// Set and get
	cfg.SetWindowSize("test", 800, 600)
	size, ok := cfg.GetWindowSize("test")
	if !ok || size[0] != 800 || size[1] != 600 {
		t.Errorf("GetWindowSize = %v, %v; want [800,600], true", size, ok)
	}

	// Rename
	cfg.RenameWindowSize("test", "test2")
	_, ok = cfg.GetWindowSize("test")
	if ok {
		t.Error("Old name should be gone after rename")
	}
	size, ok = cfg.GetWindowSize("test2")
	if !ok || size[0] != 800 || size[1] != 600 {
		t.Errorf("After rename, GetWindowSize = %v, %v; want [800,600], true", size, ok)
	}

	// Delete
	cfg.DeleteWindowSize("test2")
	_, ok = cfg.GetWindowSize("test2")
	if ok {
		t.Error("Should be gone after delete")
	}
}
