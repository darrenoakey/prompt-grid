package ptylog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockParser records all data passed to Parse
type mockParser struct {
	data []byte
}

func (p *mockParser) Parse(data []byte) {
	p.data = append(p.data, data...)
}

func TestMain(m *testing.M) {
	// Use temp dir for HOME to avoid polluting real config
	tmpHome, _ := os.MkdirTemp("", "ptylog-test-")
	os.Setenv("HOME", tmpHome)
	code := m.Run()
	os.RemoveAll(tmpHome)
	os.Exit(code)
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"with/slash", "with_slash"},
		{"with\\backslash", "with_backslash"},
		{"normal-name", "normal-name"},
	}
	for _, tt := range tests {
		got := sanitize(tt.input)
		if got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLogPath(t *testing.T) {
	path := LogPath("test-session")
	if !strings.HasSuffix(path, "test-session.ptylog") {
		t.Errorf("LogPath = %q, want suffix test-session.ptylog", path)
	}
	if !strings.Contains(path, "sessions") {
		t.Errorf("LogPath = %q, should contain 'sessions' directory", path)
	}
}

func TestWriterWriteAndFlush(t *testing.T) {
	w, err := NewWriter("test-write-flush")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() {
		w.Close()
		DeleteLog("test-write-flush")
	}()

	// Write some data
	w.Write([]byte("hello world\n"))
	w.Write([]byte("second line\n"))

	// Force close to flush
	w.Close()

	// Read back
	data, err := os.ReadFile(LogPath("test-write-flush"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello world\nsecond line\n" {
		t.Errorf("log content = %q, want %q", data, "hello world\nsecond line\n")
	}
}

func TestWriterTimedFlush(t *testing.T) {
	w, err := NewWriter("test-timed-flush")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() {
		w.Close()
		DeleteLog("test-timed-flush")
	}()

	w.Write([]byte("timed data"))

	// Wait for timed flush (2s interval + margin)
	time.Sleep(3 * time.Second)

	data, err := os.ReadFile(LogPath("test-timed-flush"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "timed data" {
		t.Errorf("after timed flush: %q, want %q", data, "timed data")
	}
}

func TestWriterLargeBufferFlush(t *testing.T) {
	w, err := NewWriter("test-large-flush")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() {
		w.Close()
		DeleteLog("test-large-flush")
	}()

	// Write more than flushSize (64KB) in one call
	bigData := make([]byte, 70*1024)
	for i := range bigData {
		bigData[i] = 'A'
	}
	w.Write(bigData)

	// Should have been flushed immediately
	data, err := os.ReadFile(LogPath("test-large-flush"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) != len(bigData) {
		t.Errorf("flushed size = %d, want %d", len(data), len(bigData))
	}
}

func TestReplayLog(t *testing.T) {
	// Write a log file
	w, err := NewWriter("test-replay")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	testData := "Hello from PTY\x1b[31mred text\x1b[0m\nnewline\n"
	w.Write([]byte(testData))
	w.Close()
	defer DeleteLog("test-replay")

	// Replay through mock parser
	parser := &mockParser{}
	err = ReplayLog("test-replay", parser)
	if err != nil {
		t.Fatalf("ReplayLog: %v", err)
	}

	if string(parser.data) != testData {
		t.Errorf("replayed data = %q, want %q", parser.data, testData)
	}
}

func TestReplayLogNonexistent(t *testing.T) {
	parser := &mockParser{}
	err := ReplayLog("nonexistent-session-xyz", parser)
	if err != nil {
		t.Errorf("ReplayLog for nonexistent should return nil, got %v", err)
	}
	if len(parser.data) != 0 {
		t.Error("should not have replayed any data")
	}
}

func TestDeleteLog(t *testing.T) {
	w, err := NewWriter("test-delete")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	w.Write([]byte("data"))
	w.Close()

	path := LogPath("test-delete")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("log file should exist before delete")
	}

	DeleteLog("test-delete")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("log file should not exist after delete")
	}
}

func TestRenameLog(t *testing.T) {
	w, err := NewWriter("test-rename-old")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	w.Write([]byte("rename data"))
	w.Close()

	RenameLog("test-rename-old", "test-rename-new")
	defer DeleteLog("test-rename-new")

	// Old should be gone
	if _, err := os.Stat(LogPath("test-rename-old")); !os.IsNotExist(err) {
		t.Error("old log should not exist after rename")
	}

	// New should have the data
	data, err := os.ReadFile(LogPath("test-rename-new"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "rename data" {
		t.Errorf("renamed log data = %q, want %q", data, "rename data")
	}
}

func TestTruncation(t *testing.T) {
	w, err := NewWriter("test-truncate")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() {
		w.Close()
		DeleteLog("test-truncate")
	}()

	// Write >10MB to trigger truncation
	// Write in chunks to avoid huge single allocation
	chunk := make([]byte, 1024*1024) // 1MB
	for i := range chunk {
		chunk[i] = byte('A' + (i % 26))
	}
	// Add newlines for safe boundary seeking
	for i := 0; i < len(chunk); i += 1000 {
		chunk[i] = '\n'
	}

	for i := 0; i < 12; i++ {
		w.Write(chunk)
	}

	// Force final flush
	w.Close()

	// Check file size is within expected range (around 5MB target)
	data, err := os.ReadFile(LogPath("test-truncate"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Should be roughly truncTarget (5MB) after truncation
	if int64(len(data)) > maxLogSize {
		t.Errorf("log size = %d, should be <= %d after truncation", len(data), maxLogSize)
	}
	if int64(len(data)) < truncTarget/2 {
		t.Errorf("log size = %d, seems too small (expected ~%d)", len(data), truncTarget)
	}
}

func TestWriterDoubleClose(t *testing.T) {
	w, err := NewWriter("test-double-close")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer DeleteLog("test-double-close")

	w.Write([]byte("data"))
	w.Close()
	w.Close() // Should not panic
}

func TestWriterWriteAfterClose(t *testing.T) {
	w, err := NewWriter("test-write-after-close")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer DeleteLog("test-write-after-close")

	w.Close()
	w.Write([]byte("should be ignored")) // Should not panic
}

func TestLogDirCreated(t *testing.T) {
	// Remove the log dir and verify NewWriter creates it
	dir := LogDir()
	os.RemoveAll(dir)

	w, err := NewWriter("test-dir-create")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	w.Close()
	defer DeleteLog("test-dir-create")

	if _, err := os.Stat(filepath.Dir(LogPath("test-dir-create"))); os.IsNotExist(err) {
		t.Error("LogDir should have been created by NewWriter")
	}
}
