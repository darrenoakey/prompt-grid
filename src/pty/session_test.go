package pty

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewSession(t *testing.T) {
	s := NewSession("test")
	if s.Name() != "test" {
		t.Errorf("Name() = %q, want %q", s.Name(), "test")
	}
	if s.Size() != DefaultSize {
		t.Errorf("Size() = %v, want %v", s.Size(), DefaultSize)
	}
}

func TestSessionStartAndWrite(t *testing.T) {
	s := NewSession("test")

	var buf bytes.Buffer
	var mu sync.Mutex
	s.SetOnData(func(data []byte) {
		mu.Lock()
		buf.Write(data)
		mu.Unlock()
	})

	// Start shell
	err := s.StartCommand("sh", []string{"-c", "echo hello && exit 0"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Wait for exit
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for session to exit")
	}

	mu.Lock()
	output := buf.String()
	mu.Unlock()

	if !strings.Contains(output, "hello") {
		t.Errorf("Output = %q, should contain 'hello'", output)
	}
}

func TestSessionResize(t *testing.T) {
	s := NewSession("test")

	// Start a simple command
	err := s.StartCommand("sh", []string{"-c", "sleep 0.1"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Resize
	newSize := Size{Cols: 80, Rows: 40}
	err = s.Resize(newSize)
	if err != nil {
		t.Errorf("Resize() error = %v", err)
	}

	if s.Size() != newSize {
		t.Errorf("Size() = %v, want %v", s.Size(), newSize)
	}

	s.Close()
}

func TestSessionClose(t *testing.T) {
	s := NewSession("test")

	err := s.StartCommand("sh", []string{"-c", "sleep 10"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Close should terminate the process
	err = s.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Verify done channel is closed
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Error("Done channel not closed after Close()")
	}
}

func TestSessionOnExit(t *testing.T) {
	s := NewSession("test")

	exitCalled := make(chan struct{})
	s.SetOnExit(func(err error) {
		close(exitCalled)
	})

	err := s.StartCommand("sh", []string{"-c", "exit 0"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	select {
	case <-exitCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("OnExit callback not called")
	}
}

func TestSessionSSH(t *testing.T) {
	s := NewSession("test")

	if s.IsSSH() {
		t.Error("IsSSH() = true before StartSSH")
	}

	// We can't actually test SSH without a server, but we can test the setup
	// Don't actually start the SSH session, just verify the host is set
	s.sshHost = "user@example.com"

	if !s.IsSSH() {
		t.Error("IsSSH() = false after setting sshHost")
	}
	if s.SSHHost() != "user@example.com" {
		t.Errorf("SSHHost() = %q, want %q", s.SSHHost(), "user@example.com")
	}
}
