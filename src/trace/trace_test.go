package trace

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestStartAndStop(t *testing.T) {
	tracer, path, err := Start("test-session", 120, 40, 500)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer os.Remove(path)

	if tracer.Session() != "test-session" {
		t.Errorf("Session = %q, want %q", tracer.Session(), "test-session")
	}
	if !strings.Contains(path, "traces/trace-") {
		t.Errorf("path %q does not contain traces/trace-", path)
	}
	if !strings.HasSuffix(path, ".jsonl") {
		t.Errorf("path %q does not end with .jsonl", path)
	}

	// Log some events
	tracer.Log(Event{Type: "screen", Text: "hello world", Detail: "initial"})
	tracer.LogPTYData([]byte("raw pty bytes"))
	tracer.Log(Event{Type: "key_edit", Text: "a"})
	tracer.Log(Event{Type: "key_press", Key: "Return", Mods: ""})
	tracer.Log(Event{Type: "scroll", Delta: -3})
	tracer.Log(Event{Type: "paste", Text: "pasted text"})

	resultPath := tracer.Stop()
	if resultPath != path {
		t.Errorf("Stop returned %q, want %q", resultPath, path)
	}

	// Read and verify the JSONL file
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		events = append(events, ev)
	}

	// start + screen + pty_data + key_edit + key_press + scroll + paste + stop = 8 events
	if len(events) != 8 {
		t.Fatalf("got %d events, want 8", len(events))
	}

	// Verify start event
	if events[0].Type != "start" {
		t.Errorf("events[0].Type = %q, want start", events[0].Type)
	}
	if events[0].Session != "test-session" {
		t.Errorf("events[0].Session = %q, want test-session", events[0].Session)
	}
	if events[0].Cols != 120 || events[0].Rows != 40 || events[0].Lines != 500 {
		t.Errorf("start event: cols=%d rows=%d lines=%d", events[0].Cols, events[0].Rows, events[0].Lines)
	}

	// Verify all events have timestamps
	for i, ev := range events {
		if ev.Time == "" {
			t.Errorf("events[%d] has empty Time", i)
		}
	}

	// Verify pty_data is base64-encoded
	if events[2].Type != "pty_data" {
		t.Errorf("events[2].Type = %q, want pty_data", events[2].Type)
	}
	if events[2].Data == "" {
		t.Error("pty_data event has empty Data")
	}

	// Verify stop event
	if events[7].Type != "stop" {
		t.Errorf("events[7].Type = %q, want stop", events[7].Type)
	}
}

func TestLogAfterStop(t *testing.T) {
	tracer, path, err := Start("test", 80, 24, 0)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer os.Remove(path)

	tracer.Stop()

	// Should not panic or error
	tracer.Log(Event{Type: "key_edit", Text: "x"})
	tracer.LogPTYData([]byte("data"))
}
