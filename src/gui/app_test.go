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

func TestLastActivitySetOnCreate(t *testing.T) {
	app := NewApp(nil, "")
	before := time.Now()

	state, err := app.NewSession("test-activity-create", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer app.CloseSession("test-activity-create")

	after := time.Now()
	activity := state.LastActivity()
	if activity.Before(before) || activity.After(after) {
		t.Errorf("lastActivity = %v, want between %v and %v", activity, before, after)
	}

	time.Sleep(100 * time.Millisecond)
}

func TestLastActivityUpdatedOnOutput(t *testing.T) {
	app := NewApp(nil, "")

	state, err := app.NewSession("test-activity-output", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer app.CloseSession("test-activity-output")

	// Wait for initial shell output to settle
	time.Sleep(500 * time.Millisecond)

	// Record the activity time, then send a command to trigger new output
	activityBefore := state.LastActivity()
	time.Sleep(10 * time.Millisecond) // ensure time moves forward
	tmux.SendKeys("test-activity-output", "echo hello", "Enter")

	// Wait for PTY output
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if state.LastActivity().After(activityBefore) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !state.LastActivity().After(activityBefore) {
		t.Error("lastActivity should be updated after PTY output")
	}

	time.Sleep(100 * time.Millisecond)
}

func TestIsSessionActive(t *testing.T) {
	app := NewApp(nil, "")

	_, err := app.NewSession("test-active-check", "", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer app.CloseSession("test-active-check")

	// Just created — should be active within 2 hours
	if !app.IsSessionActive("test-active-check", 2*time.Hour) {
		t.Error("newly created session should be active")
	}

	// Should not be active within 0 duration (already in the past)
	if app.IsSessionActive("test-active-check", 0) {
		t.Error("session should not be active with zero duration")
	}

	// Nonexistent session
	if app.IsSessionActive("nonexistent", 2*time.Hour) {
		t.Error("nonexistent session should not be active")
	}

	time.Sleep(100 * time.Millisecond)
}

func TestCollapseInactiveConfig(t *testing.T) {
	cfg := &config.Config{}

	// Default is false
	if cfg.GetCollapseInactive() {
		t.Error("default CollapseInactive should be false")
	}

	// Set to true
	cfg.SetCollapseInactive(true)
	if !cfg.GetCollapseInactive() {
		t.Error("CollapseInactive should be true after setting")
	}

	// Set back to false
	cfg.SetCollapseInactive(false)
	if cfg.GetCollapseInactive() {
		t.Error("CollapseInactive should be false after unsetting")
	}
}

func TestCollapseInactiveConfigPersistence(t *testing.T) {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "prompt-grid", "config.json")
	os.MkdirAll(filepath.Dir(cfgPath), 0755)
	cfg := &config.Config{}

	cfg.SetCollapseInactive(true)
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload and verify
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.GetCollapseInactive() {
		t.Error("CollapseInactive should survive save/load")
	}
}

func TestCollapseFilteringLogic(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetCollapseInactive(true)

	app := NewApp(cfg, "")

	// Create two sessions
	_, err := app.NewSession("active-sess", "", "")
	if err != nil {
		t.Fatalf("NewSession active: %v", err)
	}
	defer app.CloseSession("active-sess")

	_, err = app.NewSession("stale-sess", "", "")
	if err != nil {
		t.Fatalf("NewSession stale: %v", err)
	}
	defer app.CloseSession("stale-sess")

	// Both should be active (just created)
	if !app.IsSessionActive("active-sess", 2*time.Hour) {
		t.Error("active-sess should be active")
	}
	if !app.IsSessionActive("stale-sess", 2*time.Hour) {
		t.Error("stale-sess should be active")
	}

	// Manually backdate the stale session's lastActivity
	stale := app.GetSession("stale-sess")
	stale.lastActivity = time.Now().Add(-3 * time.Hour)

	// Now stale-sess should not be active within 2h
	if app.IsSessionActive("stale-sess", 2*time.Hour) {
		t.Error("stale-sess should not be active after backdating")
	}

	// active-sess should still be active
	if !app.IsSessionActive("active-sess", 2*time.Hour) {
		t.Error("active-sess should still be active")
	}

	time.Sleep(100 * time.Millisecond)
}
