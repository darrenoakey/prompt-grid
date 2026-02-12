package gui

import (
	"fmt"
	"os"
	"testing"
	"time"

	"claude-term/src/tmux"
)

var testRealm string

// TestMain sets up realm isolation and runs tests
func TestMain(m *testing.M) {
	// Set up unique realm for this test run - completely isolated from production
	testRealm = fmt.Sprintf("test-%d-%d", os.Getpid(), time.Now().UnixNano())
	os.Setenv(tmux.RealmEnvVar, testRealm)

	// Use a clean shell environment so tests don't wait for user's shell init
	// files (.zshrc, .bashrc, etc.) which can be slow or block indefinitely.
	tmpHome, _ := os.MkdirTemp("", "claude-term-test-home-")
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

	state, err := app.NewSession("test-session", "")
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

	_, err := app.NewSession("test-dup", "")
	if err != nil {
		t.Fatalf("First NewSession() error = %v", err)
	}
	defer app.CloseSession("test-dup")

	_, err = app.NewSession("test-dup", "")
	if err == nil {
		t.Error("Second NewSession() should have failed")
	}

	time.Sleep(100 * time.Millisecond)
}

func TestAppGetSession(t *testing.T) {
	app := NewApp(nil, "")

	_, _ = app.NewSession("test-get", "")
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

	app.NewSession("alpha-list", "")
	defer app.CloseSession("alpha-list")
	app.NewSession("beta-list", "")
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
