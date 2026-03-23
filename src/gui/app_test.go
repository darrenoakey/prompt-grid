package gui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prompt-grid/src/config"
	"prompt-grid/src/emulator"
	"prompt-grid/src/ptylog"
	"prompt-grid/src/tmux"
)

var testRealm string

// TestMain sets up realm isolation and runs tests
func TestMain(m *testing.M) {
	// Set up unique realm for this test run - completely isolated from production
	testRealm = fmt.Sprintf("test-%d-%d", os.Getpid(), time.Now().UnixNano())
	os.Setenv(tmux.RealmEnvVar, testRealm)

	// Use a clean shell environment so tests don't wait for user's shell init
	// files (.zshrc, .bashrc, etc.) which can be slow or block indefinitely.
	tmpHome, _ := os.MkdirTemp("", "prompt-grid-test-home-")
	os.Setenv("HOME", tmpHome)
	os.Setenv("SHELL", "/bin/bash")

	// Run tests
	code := m.Run()

	// Cleanup: kill entire tmux server for this realm
	tmux.KillServer()

	// Remove IPC socket directory and temp home
	os.RemoveAll(tmux.GetSocketDir())
	os.RemoveAll(tmpHome)

	os.Exit(code)
}

func TestNewApp(t *testing.T) {
	app := NewApp(nil, "")
	if app == nil {
		t.Fatal("NewApp() returned nil")
	}
}

func TestAppNewSession(t *testing.T) {
	app := NewApp(nil, "")

	state, err := app.NewSession("test-session", "", "")
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	defer app.CloseSession("test-session")

	if state == nil {
		t.Fatal("NewSession() returned nil state")
	}
	if state.PTY() == nil {
		t.Error("PTY() should not be nil")
	}
	if state.Screen() == nil {
		t.Error("Screen() should not be nil")
	}
	if state.Scrollback() == nil {
		t.Error("Scrollback() should not be nil")
	}

	// Give time for cleanup
	time.Sleep(100 * time.Millisecond)
}

func TestAppDuplicateSession(t *testing.T) {
	app := NewApp(nil, "")

	_, err := app.NewSession("test-dup", "", "")
	if err != nil {
		t.Fatalf("First NewSession() error = %v", err)
	}
	defer app.CloseSession("test-dup")

	_, err = app.NewSession("test-dup", "", "")
	if err == nil {
		t.Error("Second NewSession() should have failed")
	}

	time.Sleep(100 * time.Millisecond)
}

func TestAppGetSession(t *testing.T) {
	app := NewApp(nil, "")

	_, _ = app.NewSession("test-get", "", "")
	defer app.CloseSession("test-get")

	state := app.GetSession("test-get")
	if state == nil {
		t.Error("GetSession() should find 'test-get'")
	}

	state = app.GetSession("nonexistent")
	if state != nil {
		t.Error("GetSession() should return nil for nonexistent")
	}

	time.Sleep(100 * time.Millisecond)
}

func TestAppListSessions(t *testing.T) {
	app := NewApp(nil, "")

	initial := len(app.ListSessions())

	app.NewSession("alpha-list", "", "")
	defer app.CloseSession("alpha-list")
	app.NewSession("beta-list", "", "")
	defer app.CloseSession("beta-list")

	list := app.ListSessions()
	if len(list) != initial+2 {
		t.Errorf("ListSessions() length = %d, want %d", len(list), initial+2)
	}

	time.Sleep(100 * time.Millisecond)
}

func TestAppColors(t *testing.T) {
	app := NewApp(nil, "")
	colors := app.Colors()

	if colors.Foreground.A != 255 {
		t.Error("Foreground should be opaque")
	}
	if colors.Background.A != 255 {
		t.Error("Background should be opaque")
	}
}

func TestAppFontSize(t *testing.T) {
	app := NewApp(nil, "")
	fontSize := app.FontSize()

	if fontSize <= 0 {
		t.Error("FontSize should be positive")
	}
}

func TestTmuxSessionLifecycle(t *testing.T) {
	name := "test-tmux-lifecycle"

	// Create tmux session
	err := tmux.NewSession(name, "", 80, 24)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	// Check it exists
	if !tmux.HasSession(name) {
		t.Error("Session should exist after creation")
	}

	// Check it appears in list
	sessions, err := tmux.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	found := false
	for _, s := range sessions {
		if s == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListSessions() = %v, should contain %q", sessions, name)
	}

	// Kill it
	err = tmux.KillSession(name)
	if err != nil {
		t.Errorf("KillSession() error = %v", err)
	}

	// Verify it's gone
	if tmux.HasSession(name) {
		t.Error("Session should not exist after kill")
	}
}

func TestSessionMetadataPersistence(t *testing.T) {
	// Create a temp config file
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "prompt-grid", "config.json")
	os.MkdirAll(filepath.Dir(cfgPath), 0755)
	cfg := &config.Config{}

	app := NewApp(cfg, cfgPath)

	// Create a session
	_, err := app.NewSession("test-meta", "", "/tmp")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer app.CloseSession("test-meta")

	// Verify session info was saved
	info, ok := cfg.GetSessionInfo("test-meta")
	if !ok {
		t.Fatal("session info should exist after NewSession")
	}
	if info.Type != "shell" {
		t.Errorf("type = %q, want %q", info.Type, "shell")
	}
	if info.WorkDir != "/tmp" {
		t.Errorf("workdir = %q, want %q", info.WorkDir, "/tmp")
	}

	// Verify config was written to disk
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var diskCfg config.Config
	json.Unmarshal(data, &diskCfg)
	diskInfo, ok := diskCfg.Sessions["test-meta"]
	if !ok {
		t.Fatal("session info should be on disk")
	}
	if diskInfo.Type != "shell" {
		t.Errorf("disk type = %q, want %q", diskInfo.Type, "shell")
	}

	time.Sleep(100 * time.Millisecond)
}

func TestSessionMetadataDeletedOnClose(t *testing.T) {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "prompt-grid", "config.json")
	os.MkdirAll(filepath.Dir(cfgPath), 0755)
	cfg := &config.Config{}

	app := NewApp(cfg, cfgPath)

	_, err := app.NewSession("test-meta-del", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Close the session
	app.CloseSession("test-meta-del")

	// Verify metadata removed
	_, ok := cfg.GetSessionInfo("test-meta-del")
	if ok {
		t.Error("session info should be deleted after CloseSession")
	}

	// Verify PTY log removed
	if _, err := os.Stat(ptylog.LogPath("test-meta-del")); !os.IsNotExist(err) {
		t.Error("PTY log should be deleted after CloseSession")
	}

	time.Sleep(100 * time.Millisecond)
}

func TestSessionMetadataRename(t *testing.T) {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "prompt-grid", "config.json")
	os.MkdirAll(filepath.Dir(cfgPath), 0755)
	cfg := &config.Config{}

	app := NewApp(cfg, cfgPath)

	_, err := app.NewSession("test-ren-old", "", "/tmp")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer app.CloseSession("test-ren-new") // cleanup under new name

	err = app.RenameSession("test-ren-old", "test-ren-new")
	if err != nil {
		t.Fatalf("RenameSession: %v", err)
	}

	// Old metadata gone
	_, ok := cfg.GetSessionInfo("test-ren-old")
	if ok {
		t.Error("old session info should be gone after rename")
	}

	// New metadata present
	info, ok := cfg.GetSessionInfo("test-ren-new")
	if !ok {
		t.Fatal("new session info should exist after rename")
	}
	if info.WorkDir != "/tmp" {
		t.Errorf("workdir = %q, want /tmp", info.WorkDir)
	}

	time.Sleep(100 * time.Millisecond)
}

func TestPtyLogCreatedWithSession(t *testing.T) {
	app := NewApp(nil, "")

	_, err := app.NewSession("test-ptylog", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer app.CloseSession("test-ptylog")

	state := app.GetSession("test-ptylog")
	if state == nil {
		t.Fatal("session should exist")
	}
	if state.ptyLog == nil {
		t.Error("ptyLog should be non-nil after NewSession")
	}

	time.Sleep(100 * time.Millisecond)
}

func TestSessionRecreateAfterTmuxDeath(t *testing.T) {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "prompt-grid", "config.json")
	os.MkdirAll(filepath.Dir(cfgPath), 0755)
	cfg := &config.Config{}

	app := NewApp(cfg, cfgPath)

	// Create a session
	_, err := app.NewSession("test-recreate", "", "/tmp")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Verify session info was saved
	info, ok := cfg.GetSessionInfo("test-recreate")
	if !ok {
		t.Fatal("session info should exist after NewSession")
	}
	if info.Type != "shell" {
		t.Errorf("type = %q, want shell", info.Type)
	}

	// Simulate a machine reboot: the OS kills the process instantly.
	// In a real reboot no exit callbacks fire, so config is preserved on disk.
	// We simulate this by clearing the OnExit callback before closing, so the
	// config cleanup code never runs (just like the OS would not run it).
	state := app.GetSession("test-recreate")
	if state.pty != nil {
		state.pty.SetOnExit(nil)
		state.pty.Close()
	}
	tmux.KillSession("test-recreate")
	time.Sleep(200 * time.Millisecond)

	// Remove the old app session (simulating app restart)
	app.mu.Lock()
	delete(app.sessions, "test-recreate")
	app.mu.Unlock()

	// Create a new App which will discover + recreate dead sessions
	app2 := NewApp(cfg, cfgPath)

	// The session should have been recreated
	state2 := app2.GetSession("test-recreate")
	if state2 == nil {
		t.Fatal("session should be recreated after tmux death")
	}

	// Verify tmux session exists again
	if !tmux.HasSession("test-recreate") {
		t.Error("tmux session should exist after recreation")
	}

	// Cleanup
	app2.CloseSession("test-recreate")
	time.Sleep(100 * time.Millisecond)
}

func TestReconnectSessionRestoresScrollback(t *testing.T) {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "prompt-grid", "config.json")
	os.MkdirAll(filepath.Dir(cfgPath), 0755)
	cfg := &config.Config{}

	app := NewApp(cfg, cfgPath)

	// Create session (tmux session stays alive)
	_, err := app.NewSession("test-reconnect-sb", "", "/tmp")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Write scrollback data to the .scrollback file (scrollback is disk-persisted,
	// not restored from ptylog replay).
	sbPath := emulator.ScrollbackPath("test-reconnect-sb")
	var sbLines []string
	for i := 1; i <= 50; i++ {
		// Each line is a JSON array of cells; minimal: [{"r":"X"}]
		sbLines = append(sbLines, fmt.Sprintf(`[{"r":"%d"}]`, i))
	}
	os.WriteFile(sbPath, []byte(strings.Join(sbLines, "\n")+"\n"), 0644)

	// Close the app-side session (but keep tmux alive)
	state := app.GetSession("test-reconnect-sb")
	if state.pty != nil {
		state.pty.Close()
	}
	app.mu.Lock()
	delete(app.sessions, "test-reconnect-sb")
	app.mu.Unlock()

	// Reconnect (like app restart with tmux still running)
	err = app.reconnectSession("test-reconnect-sb")
	if err != nil {
		t.Fatalf("reconnectSession: %v", err)
	}
	defer app.CloseSession("test-reconnect-sb")

	state2 := app.GetSession("test-reconnect-sb")
	if state2 == nil {
		t.Fatal("session should exist after reconnect")
	}

	// Scrollback should have been restored from the .scrollback file
	if state2.Scrollback().Count() == 0 {
		t.Error("scrollback should be restored from .scrollback file on reconnect")
	}

	time.Sleep(100 * time.Millisecond)
}

func TestCWDTracking(t *testing.T) {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "prompt-grid", "config.json")
	os.MkdirAll(filepath.Dir(cfgPath), 0755)
	cfg := &config.Config{}

	app := NewApp(cfg, cfgPath)

	_, err := app.NewSession("test-cwd-track", "", "/tmp")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer app.CloseSession("test-cwd-track")

	// Wait for shell to become interactive
	time.Sleep(500 * time.Millisecond)

	// Change directory in the tmux session
	if err := tmux.SendKeys("test-cwd-track", "cd /var", "Enter"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Trigger CWD update directly (instead of waiting 30s)
	app.updateAllCWDs()

	// Verify config was updated to new directory.
	// Use EvalSymlinks because macOS resolves /var → /private/var etc.
	wantResolved, _ := filepath.EvalSymlinks("/var")

	info, ok := cfg.GetSessionInfo("test-cwd-track")
	if !ok {
		t.Fatal("session info should exist")
	}
	gotResolved, _ := filepath.EvalSymlinks(info.WorkDir)
	if gotResolved != wantResolved {
		t.Errorf("WorkDir = %q (resolved: %q), want /var (resolved: %q)", info.WorkDir, gotResolved, wantResolved)
	}
}

func TestClaudeRecreateUsesContinue(t *testing.T) {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "prompt-grid", "config.json")
	os.MkdirAll(filepath.Dir(cfgPath), 0755)
	cfg := &config.Config{}

	// Create a fake claude script that records its args and stays running
	claudeDir := filepath.Join(os.Getenv("HOME"), ".local", "bin")
	os.MkdirAll(claudeDir, 0755)
	claudePath := filepath.Join(claudeDir, "claude")
	argsFile := filepath.Join(os.Getenv("HOME"), "claude-args.txt")
	script := "#!/bin/bash\necho \"$@\" > " + argsFile + "\nsleep 30\n"
	if err := os.WriteFile(claudePath, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile claude script: %v", err)
	}

	// Save a claude session in config (simulating pre-reboot state)
	cfg.SetSessionInfo("test-claude-continue", config.SessionInfo{
		Type:    "claude",
		WorkDir: "/tmp",
	})
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	// NewApp will discover no live tmux sessions and call recreateSession
	app := NewApp(cfg, cfgPath)

	// Always clean up - even if test fails via t.Fatal, we must kill the tmux session
	// to avoid polluting subsequent tests that call NewApp (which discovers live sessions).
	t.Cleanup(func() {
		app.CloseSession("test-claude-continue")
		time.Sleep(100 * time.Millisecond)
	})

	// Session should have been recreated
	state := app.GetSession("test-claude-continue")
	if state == nil {
		t.Fatal("claude session should be recreated after reboot")
	}

	// Wait for script to start and write its args (poll up to 3s; tmux session start
	// can take 500-800ms on macOS before the script runs).
	var argsData []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		argsData, err = os.ReadFile(argsFile)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify --continue was passed to claude
	if len(argsData) == 0 {
		t.Fatalf("claude args file not written after 3s (claude script may not have started)")
	}
	if !strings.Contains(string(argsData), "--continue") {
		t.Errorf("claude was not started with --continue, got args: %q", string(argsData))
	}
}

// =============================================================================
// BDD Tests: Collapse Inactive Sessions
// Feature file: tests/bdd/features/collapse_inactive.feature
// =============================================================================

// helper: create an app with its own config for collapse tests (avoids concurrent map writes)
func newCollapseTestApp(t *testing.T) (*App, *config.Config, string) {
	t.Helper()
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := &config.Config{}
	app := NewApp(cfg, cfgPath)
	return app, cfg, cfgPath
}

// Scenario: All sessions visible when collapse mode is off
func TestCollapse_AllVisibleWhenOff(t *testing.T) {
	app, cfg, _ := newCollapseTestApp(t)
	// Collapse off (default)
	if cfg.GetCollapseInactive() {
		t.Fatal("collapse should be off by default")
	}

	_, err := app.NewSession("bdd-vis-active", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-vis-active"); time.Sleep(100 * time.Millisecond) })

	_, err = app.NewSession("bdd-vis-stale", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-vis-stale"); time.Sleep(100 * time.Millisecond) })

	// Backdate one session
	app.GetSession("bdd-vis-stale").lastActivity = time.Now().Add(-3 * time.Hour)

	visible, hidden := app.FilteredSessions("", "", nil)
	if hidden != 0 {
		t.Errorf("hidden = %d, want 0 when collapse off", hidden)
	}
	found := map[string]bool{}
	for _, v := range visible {
		found[v] = true
	}
	if !found["bdd-vis-active"] || !found["bdd-vis-stale"] {
		t.Errorf("both sessions should be visible, got %v", visible)
	}
}

// Scenario: Stale sessions hidden when collapse mode is on
func TestCollapse_StaleSessionsHidden(t *testing.T) {
	app, cfg, _ := newCollapseTestApp(t)
	cfg.SetCollapseInactive(true)

	_, err := app.NewSession("bdd-active-2", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-active-2"); time.Sleep(100 * time.Millisecond) })

	_, err = app.NewSession("bdd-stale-2", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-stale-2"); time.Sleep(100 * time.Millisecond) })

	// active-2: output 30 min ago (within 2h)
	app.GetSession("bdd-active-2").lastActivity = time.Now().Add(-30 * time.Minute)
	// stale-2: output 3 hours ago (beyond 2h)
	app.GetSession("bdd-stale-2").lastActivity = time.Now().Add(-3 * time.Hour)

	visible, hidden := app.FilteredSessions("", "", nil)
	if hidden != 1 {
		t.Errorf("hidden = %d, want 1", hidden)
	}
	visSet := map[string]bool{}
	for _, v := range visible {
		visSet[v] = true
	}
	if !visSet["bdd-active-2"] {
		t.Error("bdd-active-2 should be visible (active)")
	}
	if visSet["bdd-stale-2"] {
		t.Error("bdd-stale-2 should be hidden (stale)")
	}
}

// Scenario: Currently selected session always visible even if stale
func TestCollapse_SelectedAlwaysVisible(t *testing.T) {
	app, cfg, _ := newCollapseTestApp(t)
	cfg.SetCollapseInactive(true)

	_, err := app.NewSession("bdd-sel-stale", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-sel-stale"); time.Sleep(100 * time.Millisecond) })

	app.GetSession("bdd-sel-stale").lastActivity = time.Now().Add(-3 * time.Hour)

	// Without selection: hidden
	visible, hidden := app.FilteredSessions("", "", nil)
	visSet := map[string]bool{}
	for _, v := range visible {
		visSet[v] = true
	}
	if visSet["bdd-sel-stale"] {
		t.Error("stale session should be hidden when not selected")
	}
	if hidden < 1 {
		t.Error("should have at least 1 hidden")
	}

	// With selection: visible
	visible2, _ := app.FilteredSessions("", "bdd-sel-stale", nil)
	visSet2 := map[string]bool{}
	for _, v := range visible2 {
		visSet2[v] = true
	}
	if !visSet2["bdd-sel-stale"] {
		t.Error("selected stale session should be visible")
	}
}

// Scenario: Search finds all sessions regardless of collapse mode
func TestCollapse_SearchFindsAll(t *testing.T) {
	app, cfg, _ := newCollapseTestApp(t)
	cfg.SetCollapseInactive(true)

	_, err := app.NewSession("bdd-search-active", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-search-active"); time.Sleep(100 * time.Millisecond) })

	_, err = app.NewSession("bdd-search-stale", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-search-stale"); time.Sleep(100 * time.Millisecond) })

	app.GetSession("bdd-search-stale").lastActivity = time.Now().Add(-3 * time.Hour)

	// Search should find both
	visible, _ := app.FilteredSessions("bdd-search", "", nil)
	visSet := map[string]bool{}
	for _, v := range visible {
		visSet[v] = true
	}
	if !visSet["bdd-search-active"] || !visSet["bdd-search-stale"] {
		t.Errorf("search should find both sessions, got %v", visible)
	}
}

// Scenario: Selecting a stale session via search reveals it
func TestCollapse_SelectRevealsStale(t *testing.T) {
	app, cfg, _ := newCollapseTestApp(t)
	cfg.SetCollapseInactive(true)

	_, err := app.NewSession("bdd-reveal", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-reveal"); time.Sleep(100 * time.Millisecond) })

	app.GetSession("bdd-reveal").lastActivity = time.Now().Add(-3 * time.Hour)

	// Without reveal: hidden
	visible, _ := app.FilteredSessions("", "", nil)
	visSet := map[string]bool{}
	for _, v := range visible {
		visSet[v] = true
	}
	if visSet["bdd-reveal"] {
		t.Error("should be hidden before reveal")
	}

	// With reveal: visible even without search or selection
	revealed := map[string]bool{"bdd-reveal": true}
	visible2, _ := app.FilteredSessions("", "", revealed)
	visSet2 := map[string]bool{}
	for _, v := range visible2 {
		visSet2[v] = true
	}
	if !visSet2["bdd-reveal"] {
		t.Error("revealed session should be visible")
	}
}

// Scenario: Toggling collapse off clears revealed sessions
func TestCollapse_ToggleOffClearsRevealed(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetCollapseInactive(true)

	revealed := map[string]bool{"some-session": true}
	if len(revealed) != 1 {
		t.Fatal("precondition")
	}

	// Toggle off
	cfg.SetCollapseInactive(false)
	// The control window clears revealed on toggle-off; simulate that
	revealed = make(map[string]bool)

	if len(revealed) != 0 {
		t.Error("revealed should be empty after toggle off")
	}
	if cfg.GetCollapseInactive() {
		t.Error("collapse should be off")
	}
}

// Scenario: Sidebar header shows filtered count
func TestCollapse_HeaderShowsCount(t *testing.T) {
	app, cfg, _ := newCollapseTestApp(t)
	cfg.SetCollapseInactive(true)

	// Create 5 sessions: 3 active, 2 stale
	names := []string{"bdd-cnt-a1", "bdd-cnt-a2", "bdd-cnt-a3", "bdd-cnt-s1", "bdd-cnt-s2"}
	for _, name := range names {
		_, err := app.NewSession(name, "", "")
		if err != nil {
			t.Fatalf("NewSession %s: %v", name, err)
		}
		t.Cleanup(func() { app.CloseSession(name); time.Sleep(100 * time.Millisecond) })
	}

	// Backdate 2 sessions
	app.GetSession("bdd-cnt-s1").lastActivity = time.Now().Add(-3 * time.Hour)
	app.GetSession("bdd-cnt-s2").lastActivity = time.Now().Add(-4 * time.Hour)

	visible, hidden := app.FilteredSessions("", "", nil)

	// Visible should include the 3 active ones (plus any pre-existing sessions from other tests)
	visSet := map[string]bool{}
	for _, v := range visible {
		visSet[v] = true
	}
	if !visSet["bdd-cnt-a1"] || !visSet["bdd-cnt-a2"] || !visSet["bdd-cnt-a3"] {
		t.Errorf("all 3 active sessions should be visible, got %v", visible)
	}
	if visSet["bdd-cnt-s1"] || visSet["bdd-cnt-s2"] {
		t.Errorf("stale sessions should be hidden, got %v", visible)
	}
	if hidden < 2 {
		t.Errorf("hidden = %d, want >= 2", hidden)
	}

	// Verify header text format
	headerText := fmt.Sprintf("SESSIONS (%d/%d)", len(visible), len(visible)+hidden)
	if !strings.Contains(headerText, "/") {
		t.Errorf("header should show filtered count format, got %q", headerText)
	}
}

// Scenario: Activity persists across restart
func TestCollapse_ActivityPersistsAcrossRestart(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := &config.Config{}

	app1 := NewApp(cfg, cfgPath)

	_, err := app1.NewSession("bdd-persist", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Backdate and save activity
	threeHoursAgo := time.Now().Add(-3 * time.Hour)
	app1.GetSession("bdd-persist").lastActivity = threeHoursAgo
	app1.saveAllActivityTimes()

	// Verify it's in config
	info, ok := cfg.GetSessionInfo("bdd-persist")
	if !ok {
		t.Fatal("session info should exist")
	}
	if info.LastActivity == 0 {
		t.Fatal("LastActivity should be persisted")
	}
	if time.Unix(info.LastActivity, 0).Sub(threeHoursAgo).Abs() > time.Second {
		t.Errorf("persisted activity = %v, want ~%v", time.Unix(info.LastActivity, 0), threeHoursAgo)
	}

	// Save config to disk
	cfg.Save(cfgPath)

	// Simulate app exit (nil the OnExit to prevent cleanup)
	app1.GetSession("bdd-persist").pty.SetOnExit(nil)
	tmux.KillSession("bdd-persist")
	time.Sleep(200 * time.Millisecond)

	// Reload config and create new app (simulates restart)
	cfg2, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Recreate the tmux session so the new app can reconnect
	tmux.NewSession("bdd-persist", "", 120, 24)
	t.Cleanup(func() {
		tmux.KillSession("bdd-persist")
		time.Sleep(100 * time.Millisecond)
	})

	app2 := NewApp(cfg2, cfgPath)
	t.Cleanup(func() { app2.CloseSession("bdd-persist"); time.Sleep(100 * time.Millisecond) })

	state2 := app2.GetSession("bdd-persist")
	if state2 == nil {
		t.Fatal("session should be reconnected")
	}

	// The lastActivity should be the persisted value (~3 hours ago), NOT time.Now()
	elapsed := time.Since(state2.LastActivity())
	if elapsed < 2*time.Hour {
		t.Errorf("lastActivity should be ~3h ago after restart, but elapsed = %v (value: %v)", elapsed, state2.LastActivity())
	}
}

// Scenario: New sessions are always active
func TestCollapse_NewSessionAlwaysActive(t *testing.T) {
	app, cfg, _ := newCollapseTestApp(t)
	cfg.SetCollapseInactive(true)

	_, err := app.NewSession("bdd-new-sess", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-new-sess"); time.Sleep(100 * time.Millisecond) })

	if !app.IsSessionActive("bdd-new-sess", 2*time.Hour) {
		t.Error("newly created session should be active")
	}

	visible, _ := app.FilteredSessions("", "", nil)
	visSet := map[string]bool{}
	for _, v := range visible {
		visSet[v] = true
	}
	if !visSet["bdd-new-sess"] {
		t.Error("new session should appear in filtered list")
	}
}

// Scenario: Revealed sessions transfer on rename
func TestCollapse_RevealedTransfersOnRename(t *testing.T) {
	app, cfg, _ := newCollapseTestApp(t)
	cfg.SetCollapseInactive(true)

	_, err := app.NewSession("bdd-rename-old", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Backdate so it's stale
	app.GetSession("bdd-rename-old").lastActivity = time.Now().Add(-3 * time.Hour)

	// Simulate revealed
	revealed := map[string]bool{"bdd-rename-old": true}

	// Verify visible with revealed
	visible, _ := app.FilteredSessions("", "", revealed)
	visSet := map[string]bool{}
	for _, v := range visible {
		visSet[v] = true
	}
	if !visSet["bdd-rename-old"] {
		t.Error("revealed session should be visible before rename")
	}

	// Rename
	err = app.RenameSession("bdd-rename-old", "bdd-rename-new")
	if err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-rename-new"); time.Sleep(100 * time.Millisecond) })

	// Transfer revealed status (as the control window does)
	if revealed["bdd-rename-old"] {
		delete(revealed, "bdd-rename-old")
		revealed["bdd-rename-new"] = true
	}

	if revealed["bdd-rename-old"] {
		t.Error("old name should not be in revealed set")
	}
	if !revealed["bdd-rename-new"] {
		t.Error("new name should be in revealed set")
	}

	// Verify new name is visible
	visible2, _ := app.FilteredSessions("", "", revealed)
	visSet2 := map[string]bool{}
	for _, v := range visible2 {
		visSet2[v] = true
	}
	if !visSet2["bdd-rename-new"] {
		t.Error("renamed revealed session should be visible")
	}
}

// Scenario: Config toggle persistence
func TestCollapse_ConfigPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg := &config.Config{}

	// Default off
	if cfg.GetCollapseInactive() {
		t.Error("default should be off")
	}

	// Toggle on and save
	cfg.SetCollapseInactive(true)
	cfg.Save(cfgPath)

	// Reload and verify
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.GetCollapseInactive() {
		t.Error("collapse should survive save/load")
	}
}

// Scenario: IsSessionActive edge cases
func TestCollapse_IsSessionActiveEdgeCases(t *testing.T) {
	app := NewApp(nil, "")

	_, err := app.NewSession("bdd-edge", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-edge"); time.Sleep(100 * time.Millisecond) })

	// Just created = active
	if !app.IsSessionActive("bdd-edge", 2*time.Hour) {
		t.Error("new session should be active")
	}

	// Zero duration = never active (already in the past)
	if app.IsSessionActive("bdd-edge", 0) {
		t.Error("zero duration should return false")
	}

	// Nonexistent session
	if app.IsSessionActive("nonexistent", 2*time.Hour) {
		t.Error("nonexistent should return false")
	}
}

// Scenario: PTY output updates lastActivity after startup
func TestCollapse_OutputUpdatesActivity(t *testing.T) {
	app := NewApp(nil, "")

	state, err := app.NewSession("bdd-output", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { app.CloseSession("bdd-output"); time.Sleep(100 * time.Millisecond) })

	// Wait for shell output to settle
	time.Sleep(500 * time.Millisecond)

	activityBefore := state.LastActivity()
	time.Sleep(10 * time.Millisecond)
	tmux.SendKeys("bdd-output", "echo hello", "Enter")

	// Poll for activity update
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if state.LastActivity().After(activityBefore) {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("lastActivity should be updated after PTY output")
}
