package discord

import (
	"strings"
	"testing"
)

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
