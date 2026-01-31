package pty

import (
	"testing"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m.Count() != 0 {
		t.Errorf("Count() = %d, want 0", m.Count())
	}
}

func TestManagerNewSession(t *testing.T) {
	m := NewManager()

	s, err := m.NewSession("test")
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if s.Name() != "test" {
		t.Errorf("Name() = %q, want %q", s.Name(), "test")
	}
	if m.Count() != 1 {
		t.Errorf("Count() = %d, want 1", m.Count())
	}
}

func TestManagerDuplicateSession(t *testing.T) {
	m := NewManager()

	_, err := m.NewSession("test")
	if err != nil {
		t.Fatalf("First NewSession() error = %v", err)
	}

	_, err = m.NewSession("test")
	if err == nil {
		t.Error("Second NewSession() should have failed")
	}
}

func TestManagerGet(t *testing.T) {
	m := NewManager()

	s, _ := m.NewSession("test")
	got := m.Get("test")
	if got != s {
		t.Error("Get() returned wrong session")
	}

	if m.Get("nonexistent") != nil {
		t.Error("Get() should return nil for nonexistent session")
	}
}

func TestManagerList(t *testing.T) {
	m := NewManager()

	m.NewSession("charlie")
	m.NewSession("alpha")
	m.NewSession("beta")

	list := m.List()
	expected := []string{"alpha", "beta", "charlie"}

	if len(list) != len(expected) {
		t.Fatalf("List() length = %d, want %d", len(list), len(expected))
	}

	for i, name := range expected {
		if list[i] != name {
			t.Errorf("List()[%d] = %q, want %q", i, list[i], name)
		}
	}
}

func TestManagerClose(t *testing.T) {
	m := NewManager()

	s, _ := m.NewSession("test")
	s.StartCommand("sh", []string{"-c", "sleep 10"})

	err := m.Close("test")
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Wait for session cleanup
	<-s.Done()
}

func TestManagerCloseNonexistent(t *testing.T) {
	m := NewManager()

	err := m.Close("nonexistent")
	if err == nil {
		t.Error("Close() should fail for nonexistent session")
	}
}

func TestManagerOnCreate(t *testing.T) {
	m := NewManager()

	var created *Session
	m.SetOnCreate(func(s *Session) {
		created = s
	})

	s, _ := m.NewSession("test")
	if created != s {
		t.Error("OnCreate callback received wrong session")
	}
}

func TestManagerOnClose(t *testing.T) {
	m := NewManager()

	closeCalled := make(chan *Session, 1)
	m.SetOnClose(func(s *Session) {
		closeCalled <- s
	})

	s, _ := m.NewSession("test")
	s.StartCommand("sh", []string{"-c", "exit 0"})

	// Wait for session to exit and trigger onClose
	<-s.Done()

	select {
	case closed := <-closeCalled:
		if closed != s {
			t.Error("OnClose callback received wrong session")
		}
	default:
		// Give it a moment
		select {
		case closed := <-closeCalled:
			if closed != s {
				t.Error("OnClose callback received wrong session")
			}
		}
	}
}

func TestManagerCloseAll(t *testing.T) {
	m := NewManager()

	s1, _ := m.NewSession("s1")
	s2, _ := m.NewSession("s2")
	s1.StartCommand("sh", []string{"-c", "sleep 10"})
	s2.StartCommand("sh", []string{"-c", "sleep 10"})

	m.CloseAll()

	<-s1.Done()
	<-s2.Done()

	// Sessions should be removed after close
	if m.Count() != 0 {
		t.Errorf("Count() = %d after CloseAll, want 0", m.Count())
	}
}
