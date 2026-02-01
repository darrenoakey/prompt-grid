package gui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"claude-term/src/session"
)

func init() {
	// Set binary path for tests - assumes binary is pre-built
	// Skip if not available (tests will fail gracefully)
	if os.Getenv("CLAUDE_TERM_BIN") == "" {
		// Try to find the binary relative to the test location
		candidates := []string{
			"../../output/claude-term",
			"../../../output/claude-term",
			filepath.Join(os.Getenv("HOME"), "bin", "claude-term"),
		}
		for _, path := range candidates {
			if abs, err := filepath.Abs(path); err == nil {
				if _, err := os.Stat(abs); err == nil {
					os.Setenv("CLAUDE_TERM_BIN", abs)
					break
				}
			}
		}
	}
}

func TestNewApp(t *testing.T) {
	app := NewApp()
	if app == nil {
		t.Fatal("NewApp() returned nil")
	}
}

func TestAppNewSession(t *testing.T) {
	app := NewApp()

	state, err := app.NewSession("test-session", "")
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	defer app.CloseSession("test-session")

	if state == nil {
		t.Fatal("NewSession() returned nil state")
	}
	if state.Client() == nil {
		t.Error("Client() should not be nil")
	}
	if state.Screen() == nil {
		t.Error("Screen() should not be nil")
	}
	if state.Scrollback() == nil {
		t.Error("Scrollback() should not be nil")
	}

	// Give daemon time to clean up
	time.Sleep(100 * time.Millisecond)
}

func TestAppDuplicateSession(t *testing.T) {
	app := NewApp()

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
	app := NewApp()

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
	app := NewApp()

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
	app := NewApp()
	colors := app.Colors()

	if colors.Foreground.A != 255 {
		t.Error("Foreground should be opaque")
	}
	if colors.Background.A != 255 {
		t.Error("Background should be opaque")
	}
}

func TestAppFontSize(t *testing.T) {
	app := NewApp()
	fontSize := app.FontSize()

	if fontSize <= 0 {
		t.Error("FontSize should be positive")
	}
}

func TestSessionDaemonLifecycle(t *testing.T) {
	name := "test-daemon-lifecycle"

	// Spawn daemon
	err := session.SpawnDaemon(name, 80, 24, "")
	if err != nil {
		t.Fatalf("SpawnDaemon() error = %v", err)
	}

	// Check it's alive
	if !session.IsSessionAlive(name) {
		t.Error("Session should be alive after spawn")
	}

	// Connect
	client, err := session.Connect(name)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	// Verify info
	info := client.Info()
	if info.Name != name {
		t.Errorf("Info.Name = %q, want %q", info.Name, name)
	}
	if info.Cols != 80 || info.Rows != 24 {
		t.Errorf("Info size = %dx%d, want 80x24", info.Cols, info.Rows)
	}

	// Terminate
	err = client.Terminate()
	if err != nil {
		t.Errorf("Terminate() error = %v", err)
	}

	// Wait for cleanup - daemon needs time to process close and exit
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if !session.IsSessionAlive(name) {
			return // Success
		}
	}

	t.Error("Session should not be alive after terminate")
}
