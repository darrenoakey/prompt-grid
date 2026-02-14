package ptylog

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	flushInterval = 2 * time.Second
	flushSize     = 64 * 1024       // 64KB
	maxLogSize    = 10 * 1024 * 1024 // 10MB
	truncTarget   = 5 * 1024 * 1024  // 5MB — keep last 5MB after truncation
	replayChunk   = 32 * 1024       // 32KB chunks for replay
)

// Parser is the interface ptylog needs for replay (avoids circular import)
type Parser interface {
	Parse(data []byte)
}

// LogDir returns the directory where PTY logs are stored
func LogDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "claude-term", "sessions")
}

// LogPath returns the log file path for a session name
func LogPath(name string) string {
	return filepath.Join(LogDir(), sanitize(name)+".ptylog")
}

// sanitize replaces characters unsafe for filenames
func sanitize(name string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", "\x00", "_")
	return r.Replace(name)
}

// Writer buffers and writes PTY output to a log file
type Writer struct {
	mu      sync.Mutex
	file    *os.File
	buf     []byte
	size    int64 // Current file size (approximate)
	timer   *time.Timer
	closed  bool
	name    string
}

// NewWriter creates a new PTY log writer for the given session name.
// The log file is opened in append mode.
func NewWriter(name string) (*Writer, error) {
	dir := LogDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	path := LogPath(name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	// Get current file size
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	w := &Writer{
		file: f,
		size: info.Size(),
		name: name,
	}
	return w, nil
}

// Write appends data to the buffer and schedules a flush
func (w *Writer) Write(data []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return
	}

	w.buf = append(w.buf, data...)

	// Flush immediately if buffer is large enough
	if len(w.buf) >= flushSize {
		w.flushLocked()
		return
	}

	// Schedule a delayed flush if not already pending
	if w.timer == nil {
		w.timer = time.AfterFunc(flushInterval, func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			if !w.closed {
				w.flushLocked()
			}
		})
	}
}

// flushLocked writes buffered data to disk. Caller must hold w.mu.
func (w *Writer) flushLocked() {
	if len(w.buf) == 0 {
		return
	}

	n, _ := w.file.Write(w.buf)
	w.size += int64(n)
	w.buf = w.buf[:0]

	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}

	// Truncate if file is too large
	if w.size > maxLogSize {
		w.truncateLocked()
	}
}

// truncateLocked reduces the log file to the last truncTarget bytes.
// Seeks to a safe boundary (newline or ESC byte) to avoid mid-sequence corruption.
func (w *Writer) truncateLocked() {
	w.file.Close()

	path := LogPath(w.name)
	data, err := os.ReadFile(path)
	if err != nil {
		// Re-open in append mode, best effort
		w.file, _ = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		w.size = 0
		return
	}

	if int64(len(data)) <= truncTarget {
		w.file, _ = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		w.size = int64(len(data))
		return
	}

	// Cut from the front, keeping last truncTarget bytes
	cutPoint := int64(len(data)) - truncTarget

	// Seek forward to a safe boundary: newline or ESC (0x1b)
	for cutPoint < int64(len(data)) {
		b := data[cutPoint]
		if b == '\n' || b == 0x1b {
			break
		}
		cutPoint++
	}

	kept := data[cutPoint:]
	os.WriteFile(path, kept, 0644)

	w.file, _ = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	w.size = int64(len(kept))
}

// Close flushes remaining data and closes the file
func (w *Writer) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return
	}
	w.closed = true

	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}

	w.flushLocked()
	w.file.Close()
}

// ReplayLog reads a session's log file and feeds it through a parser in chunks.
// Returns nil if the log file doesn't exist.
func ReplayLog(name string, parser Parser) error {
	path := LogPath(name)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	buf := make([]byte, replayChunk)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			parser.Parse(buf[:n])
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// DeleteLog removes the log file for a session
func DeleteLog(name string) {
	os.Remove(LogPath(name))
}

// RenameLog renames a session's log file
func RenameLog(oldName, newName string) {
	oldPath := LogPath(oldName)
	newPath := LogPath(newName)
	os.Rename(oldPath, newPath)
}
