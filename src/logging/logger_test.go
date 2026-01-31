package logging

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLogger(t *testing.T) {
	l := NewLogger("/tmp/test-logs")
	if l == nil {
		t.Fatal("NewLogger() returned nil")
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"Hello World", "Hello_World"},
		{"test@123", "test123"},
		{"a/b/c", "abc"},
		{"", "session"},
		{"My Project", "My_Project"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoggerWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	l := NewLogger(dir)
	defer l.Close()

	err := l.LogStart("test-session")
	if err != nil {
		t.Fatalf("LogStart() error = %v", err)
	}

	err = l.LogInput("test-session", "echo hello")
	if err != nil {
		t.Fatalf("LogInput() error = %v", err)
	}

	err = l.LogOutput("test-session", "hello\n")
	if err != nil {
		t.Fatalf("LogOutput() error = %v", err)
	}

	err = l.LogEnd("test-session")
	if err != nil {
		t.Fatalf("LogEnd() error = %v", err)
	}

	l.Close()

	// Find and read the log file
	var logFile string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(path, ".jsonl") {
			logFile = path
		}
		return nil
	})

	if logFile == "" {
		t.Fatal("No log file found")
	}

	f, err := os.Open(logFile)
	if err != nil {
		t.Fatalf("Failed to open log file: %v", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("Failed to parse JSON: %v", err)
		}
		events = append(events, event)
	}

	if len(events) != 4 {
		t.Errorf("Got %d events, want 4", len(events))
	}

	// Verify event types
	expectedTypes := []EventType{EventStart, EventInput, EventOutput, EventEnd}
	for i, typ := range expectedTypes {
		if i < len(events) && events[i].Type != typ {
			t.Errorf("events[%d].Type = %v, want %v", i, events[i].Type, typ)
		}
	}
}

func TestLoggerCloseSession(t *testing.T) {
	dir := t.TempDir()
	l := NewLogger(dir)

	l.LogStart("session1")
	l.LogStart("session2")

	err := l.CloseSession("session1")
	if err != nil {
		t.Errorf("CloseSession() error = %v", err)
	}

	// Should be able to still write to session2
	err = l.LogEnd("session2")
	if err != nil {
		t.Errorf("LogEnd() error = %v", err)
	}

	l.Close()
}

func TestLoggerDirectoryStructure(t *testing.T) {
	dir := t.TempDir()
	l := NewLogger(dir)
	defer l.Close()

	l.LogStart("test")
	l.Close()

	// Check directory structure exists (YYYY/MM/)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("Expected 1 directory, got %d", len(entries))
	}

	// Should be a year directory (4 digits)
	yearDir := entries[0].Name()
	if len(yearDir) != 4 {
		t.Errorf("Expected year directory, got %q", yearDir)
	}

	// Check month subdirectory
	monthDirs, err := os.ReadDir(filepath.Join(dir, yearDir))
	if err != nil {
		t.Fatalf("ReadDir(year) error = %v", err)
	}

	if len(monthDirs) != 1 {
		t.Fatalf("Expected 1 month directory, got %d", len(monthDirs))
	}

	monthDir := monthDirs[0].Name()
	if len(monthDir) != 2 {
		t.Errorf("Expected month directory (2 digits), got %q", monthDir)
	}
}
