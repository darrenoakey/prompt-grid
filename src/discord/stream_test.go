package discord

import (
	"strings"
	"testing"
	"time"
)

func TestStreamerDefaultInactive(t *testing.T) {
	s := &Streamer{}
	if s.IsActive() {
		t.Fatal("new streamer should be inactive by default")
	}
}

func TestStreamerActivateDeactivate(t *testing.T) {
	s := &Streamer{}

	s.Activate()
	if !s.IsActive() {
		t.Fatal("streamer should be active after activation")
	}

	s.Deactivate()
	if s.IsActive() {
		t.Fatal("streamer should be inactive after deactivation")
	}
}

func TestStreamerLazyTimeout(t *testing.T) {
	s := &Streamer{
		running: true,
	}
	s.mu.Lock()
	s.active = true
	s.lastDiscordMsg = time.Now().Add(-2 * time.Hour) // 2 hours ago
	s.lastChange = time.Now()
	s.mu.Unlock()

	// pollOnce should deactivate due to timeout.
	// captureSnapshot will return nil (no state), but the timeout check
	// happens before snapshot processing.
	s.pollOnce()

	if s.IsActive() {
		t.Fatal("streamer should have been deactivated after 1hr timeout")
	}
}

func TestStreamerInactivePollSkips(t *testing.T) {
	s := &Streamer{
		running: true,
	}
	// Inactive by default — pollOnce should return immediately without panic.
	s.pollOnce()
}

func TestChannelNameForSession(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		expects string
	}{
		{name: "basic", input: "My Session", expects: "my-session"},
		{name: "symbols", input: "A/B\\C:D", expects: "a-b-c-d"},
		{name: "empty", input: "   ", expects: "session"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := channelNameForSession(tt.input)
			if got != tt.expects {
				t.Fatalf("channelNameForSession(%q) = %q, want %q", tt.input, got, tt.expects)
			}
		})
	}
}

func TestStripClaudeFooter(t *testing.T) {
	lines := []string{
		"output line 1",
		"output line 2",
		"",
		"----------------------------------------",
		"> prompt",
		"----------------------------------------",
		"status line",
	}

	got := stripClaudeFooter(lines)
	want := []string{"output line 1", "output line 2"}
	if !equalLines(got, want) {
		t.Fatalf("stripClaudeFooter() = %#v, want %#v", got, want)
	}
}

func TestStripClaudeFooterWithoutPattern(t *testing.T) {
	lines := []string{"line 1", "line 2", "line 3"}
	got := stripClaudeFooter(lines)
	if !equalLines(got, lines) {
		t.Fatalf("stripClaudeFooter() = %#v, want %#v", got, lines)
	}
}

func TestSharedSuffixPrefix(t *testing.T) {
	previous := []string{"a", "b", "c", "d"}
	current := []string{"c", "d", "e", "f"}

	overlap := sharedSuffixPrefix(previous, current)
	if overlap != 2 {
		t.Fatalf("sharedSuffixPrefix() = %d, want 2", overlap)
	}

	delta := current[overlap:]
	want := []string{"e", "f"}
	if !equalLines(delta, want) {
		t.Fatalf("delta = %#v, want %#v", delta, want)
	}
}

func TestFormatDiscordCodeBlocks(t *testing.T) {
	longLine := strings.Repeat("x", 2200)
	chunks := formatDiscordCodeBlocks([]string{"hello", longLine, "tail"})
	if len(chunks) < 2 {
		t.Fatalf("formatDiscordCodeBlocks() returned %d chunk(s), want at least 2", len(chunks))
	}
	for i, chunk := range chunks {
		if !strings.HasPrefix(chunk, "```text\n") || !strings.HasSuffix(chunk, "\n```") {
			t.Fatalf("chunk %d missing code fence: %q", i, chunk)
		}
		if len(chunk) > 2000 {
			t.Fatalf("chunk %d length = %d, exceeds Discord limit", i, len(chunk))
		}
	}
}
