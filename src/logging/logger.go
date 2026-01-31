package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EventType represents the type of log event
type EventType string

const (
	EventInput  EventType = "input"
	EventOutput EventType = "output"
	EventStart  EventType = "start"
	EventEnd    EventType = "end"
)

// Event represents a log event
type Event struct {
	Timestamp time.Time `json:"ts"`
	Type      EventType `json:"type"`
	Session   string    `json:"session"`
	Data      string    `json:"data,omitempty"`
}

// Logger writes JSONL logs for sessions
type Logger struct {
	baseDir string
	files   map[string]*os.File
	mu      sync.Mutex
}

// NewLogger creates a new logger with the given base directory
func NewLogger(baseDir string) *Logger {
	return &Logger{
		baseDir: baseDir,
		files:   make(map[string]*os.File),
	}
}

// getLogFile returns or creates a log file for a session
func (l *Logger) getLogFile(session string) (*os.File, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if f, ok := l.files[session]; ok {
		return f, nil
	}

	// Create directory structure: output/YYYY/MM/
	now := time.Now()
	dir := filepath.Join(l.baseDir, now.Format("2006"), now.Format("01"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	// Create filename: YYYYMMDD-HHMMSS-session.jsonl
	filename := fmt.Sprintf("%s-%s.jsonl", now.Format("20060102-150405"), sanitizeFilename(session))
	path := filepath.Join(dir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	l.files[session] = f
	return f, nil
}

// Log writes an event to the session's log file
func (l *Logger) Log(session string, eventType EventType, data string) error {
	f, err := l.getLogFile(session)
	if err != nil {
		return err
	}

	event := Event{
		Timestamp: time.Now().UTC(),
		Type:      eventType,
		Session:   session,
		Data:      data,
	}

	line, err := json.Marshal(event)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	_, err = f.Write(append(line, '\n'))
	return err
}

// LogInput logs input sent to the session
func (l *Logger) LogInput(session, data string) error {
	return l.Log(session, EventInput, data)
}

// LogOutput logs output received from the session
func (l *Logger) LogOutput(session, data string) error {
	return l.Log(session, EventOutput, data)
}

// LogStart logs session start
func (l *Logger) LogStart(session string) error {
	return l.Log(session, EventStart, "")
}

// LogEnd logs session end
func (l *Logger) LogEnd(session string) error {
	return l.Log(session, EventEnd, "")
}

// Close closes all open log files
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var lastErr error
	for _, f := range l.files {
		if err := f.Close(); err != nil {
			lastErr = err
		}
	}
	l.files = make(map[string]*os.File)
	return lastErr
}

// CloseSession closes the log file for a specific session
func (l *Logger) CloseSession(session string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if f, ok := l.files[session]; ok {
		delete(l.files, session)
		return f.Close()
	}
	return nil
}

// sanitizeFilename removes characters that aren't safe for filenames
func sanitizeFilename(name string) string {
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			result = append(result, c)
		} else if c == ' ' {
			result = append(result, '_')
		}
	}
	if len(result) == 0 {
		return "session"
	}
	return string(result)
}
