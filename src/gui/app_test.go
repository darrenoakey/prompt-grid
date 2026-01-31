package gui

import (
	"testing"
)

func TestNewApp(t *testing.T) {
	app := NewApp()
	if app == nil {
		t.Fatal("NewApp() returned nil")
	}
	if app.Manager() == nil {
		t.Error("Manager() should not be nil")
	}
}

func TestAppNewSession(t *testing.T) {
	app := NewApp()

	state, err := app.NewSession("test")
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if state == nil {
		t.Fatal("NewSession() returned nil state")
	}
	if state.Session() == nil {
		t.Error("Session() should not be nil")
	}
	if state.Screen() == nil {
		t.Error("Screen() should not be nil")
	}
	if state.Scrollback() == nil {
		t.Error("Scrollback() should not be nil")
	}
}

func TestAppDuplicateSession(t *testing.T) {
	app := NewApp()

	_, err := app.NewSession("test")
	if err != nil {
		t.Fatalf("First NewSession() error = %v", err)
	}

	_, err = app.NewSession("test")
	if err == nil {
		t.Error("Second NewSession() should have failed")
	}
}

func TestAppGetSession(t *testing.T) {
	app := NewApp()

	_, _ = app.NewSession("test")
	state := app.GetSession("test")
	if state == nil {
		t.Error("GetSession() should find 'test'")
	}

	state = app.GetSession("nonexistent")
	if state != nil {
		t.Error("GetSession() should return nil for nonexistent")
	}
}

func TestAppListSessions(t *testing.T) {
	app := NewApp()

	if len(app.ListSessions()) != 0 {
		t.Error("ListSessions() should be empty initially")
	}

	app.NewSession("alpha")
	app.NewSession("beta")

	list := app.ListSessions()
	if len(list) != 2 {
		t.Errorf("ListSessions() length = %d, want 2", len(list))
	}
	if list[0] != "alpha" || list[1] != "beta" {
		t.Errorf("ListSessions() = %v, want [alpha, beta]", list)
	}
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
