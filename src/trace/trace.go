package trace

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event is a single trace event written as JSONL.
type Event struct {
	Time    string `json:"time"`
	Type    string `json:"type"`              // start, stop, pty_data, key_press, key_edit, scroll, paste, screen
	Session string `json:"session,omitempty"`
	Data    string `json:"data,omitempty"`     // base64 for raw PTY data
	Key     string `json:"key,omitempty"`      // key name for key_press
	Mods    string `json:"mods,omitempty"`     // modifier keys
	Text    string `json:"text,omitempty"`     // text content (edit, paste, screen)
	Delta   int    `json:"delta,omitempty"`    // scroll delta
	Cols    int    `json:"cols,omitempty"`     // terminal columns
	Rows    int    `json:"rows,omitempty"`     // terminal rows
	Lines   int    `json:"lines,omitempty"`    // scrollback line count
	Detail  string `json:"detail,omitempty"`   // extra context
}

// Tracer writes trace events to a JSONL file.
type Tracer struct {
	file    *os.File
	path    string
	mu      sync.Mutex
	session string
}

// Start creates a new trace file and writes the start event.
func Start(session string, cols, rows, scrollbackLines int) (*Tracer, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("home dir: %w", err)
	}

	dir := filepath.Join(home, ".config", "prompt-grid", "traces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("create traces dir: %w", err)
	}

	filename := fmt.Sprintf("trace-%s.jsonl", time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, filename)

	f, err := os.Create(path)
	if err != nil {
		return nil, "", fmt.Errorf("create trace file: %w", err)
	}

	t := &Tracer{file: f, path: path, session: session}
	t.Log(Event{
		Type:    "start",
		Session: session,
		Cols:    cols,
		Rows:    rows,
		Lines:   scrollbackLines,
	})

	return t, path, nil
}

// Log writes an event to the trace file.
func (t *Tracer) Log(ev Event) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.file == nil {
		return
	}

	ev.Time = time.Now().Format(time.RFC3339Nano)
	if ev.Session == "" {
		ev.Session = t.session
	}

	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	t.file.Write(data)
	t.file.Write([]byte("\n"))
}

// LogPTYData logs raw PTY output bytes as base64.
func (t *Tracer) LogPTYData(raw []byte) {
	t.Log(Event{
		Type: "pty_data",
		Data: base64.StdEncoding.EncodeToString(raw),
	})
}

// Stop writes the stop event and closes the file. Returns the file path.
func (t *Tracer) Stop() string {
	t.Log(Event{Type: "stop"})
	t.mu.Lock()
	defer t.mu.Unlock()
	path := t.path
	if t.file != nil {
		t.file.Close()
		t.file = nil
	}
	return path
}

// Session returns the session name being traced.
func (t *Tracer) Session() string {
	return t.session
}

// Path returns the trace file path.
func (t *Tracer) Path() string {
	return t.path
}
