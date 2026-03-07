package gui

import (
	"prompt-grid/src/emulator"
	"testing"
)

// writeString writes a string to the screen starting at the given position.
func writeString(screen *emulator.Screen, x, y int, s string) {
	screen.SetCursor(x, y)
	for _, r := range s {
		screen.Write(r)
	}
}

func TestDetectPromptStatus_NilScreen(t *testing.T) {
	if got := detectPromptStatus(nil); got != PromptNone {
		t.Fatalf("nil screen: got %d, want PromptNone", got)
	}
}

func TestDetectPromptStatus_EmptyScreen(t *testing.T) {
	screen := emulator.NewScreen(80, 24)
	if got := detectPromptStatus(screen); got != PromptNone {
		t.Fatalf("empty screen: got %d, want PromptNone", got)
	}
}

func TestDetectPromptStatus_ShellDollar(t *testing.T) {
	screen := emulator.NewScreen(80, 24)
	writeString(screen, 0, 0, "user@host:~$")
	screen.SetCursor(13, 0) // cursor after "$ "
	if got := detectPromptStatus(screen); got != PromptShell {
		t.Fatalf("shell dollar: got %d, want PromptShell", got)
	}
}

func TestDetectPromptStatus_ShellPercent(t *testing.T) {
	screen := emulator.NewScreen(80, 24)
	writeString(screen, 0, 0, "hostname%")
	screen.SetCursor(10, 0)
	if got := detectPromptStatus(screen); got != PromptShell {
		t.Fatalf("shell percent: got %d, want PromptShell", got)
	}
}

func TestDetectPromptStatus_ShellHash(t *testing.T) {
	screen := emulator.NewScreen(80, 24)
	writeString(screen, 0, 0, "root@host:~#")
	screen.SetCursor(13, 0)
	if got := detectPromptStatus(screen); got != PromptShell {
		t.Fatalf("shell hash: got %d, want PromptShell", got)
	}
}

func TestDetectPromptStatus_ShellArrow(t *testing.T) {
	screen := emulator.NewScreen(80, 24)
	writeString(screen, 0, 5, "➜ mydir")
	screen.SetCursor(9, 5)
	if got := detectPromptStatus(screen); got != PromptShell {
		t.Fatalf("shell arrow: got %d, want PromptShell", got)
	}
}

func TestDetectPromptStatus_ShellChevron(t *testing.T) {
	screen := emulator.NewScreen(80, 24)
	writeString(screen, 0, 3, "❯")
	screen.SetCursor(1, 3)
	// ❯ alone is Claude pattern, but without Claude indicators above, it should
	// fall through to shell detection since ❯ is also in shell suffixes.
	// Actually ❯ at start of line triggers detectClaude first.
	// With no Claude indicators above, detectClaude returns false, and
	// detectShell will match ❯ as a suffix.
	got := detectPromptStatus(screen)
	if got != PromptShell {
		t.Fatalf("shell chevron: got %d, want PromptShell", got)
	}
}

func TestDetectPromptStatus_CommandOutput(t *testing.T) {
	screen := emulator.NewScreen(80, 24)
	// Simulate command output (ls output)
	writeString(screen, 0, 0, "file1.txt  file2.txt  file3.txt")
	writeString(screen, 0, 1, "dir1       dir2       dir3")
	screen.SetCursor(0, 2) // cursor on an empty line below output
	if got := detectPromptStatus(screen); got != PromptNone {
		t.Fatalf("command output: got %d, want PromptNone", got)
	}
}

func TestDetectPromptStatus_RunningProcess(t *testing.T) {
	screen := emulator.NewScreen(80, 24)
	// Simulate a running process like top or vim
	writeString(screen, 0, 0, "PID   USER   %CPU %MEM COMMAND")
	writeString(screen, 0, 1, "1234  root   2.0  1.5 systemd")
	screen.SetCursor(0, 23) // cursor at bottom
	if got := detectPromptStatus(screen); got != PromptNone {
		t.Fatalf("running process: got %d, want PromptNone", got)
	}
}

func TestDetectPromptStatus_ClaudeQuestion(t *testing.T) {
	screen := emulator.NewScreen(120, 24)
	writeString(screen, 0, 10, "? Do you want to proceed?")
	screen.SetCursor(25, 10)
	if got := detectPromptStatus(screen); got != PromptClaude {
		t.Fatalf("claude question: got %d, want PromptClaude", got)
	}
}

func TestDetectPromptStatus_ClaudeInputPrompt(t *testing.T) {
	screen := emulator.NewScreen(120, 24)
	// Claude Code status line a few lines above
	writeString(screen, 0, 18, "  Cost: $0.05  Duration: 30s")
	// Claude input prompt
	writeString(screen, 0, 20, "> ")
	screen.SetCursor(2, 20)
	if got := detectPromptStatus(screen); got != PromptClaude {
		t.Fatalf("claude input prompt: got %d, want PromptClaude", got)
	}
}

func TestDetectPromptStatus_ClaudeInputWithLeadingSpaces(t *testing.T) {
	screen := emulator.NewScreen(120, 24)
	writeString(screen, 0, 15, "  ⏎ Send  ESC Cancel")
	writeString(screen, 0, 17, "  > ")
	screen.SetCursor(4, 17)
	if got := detectPromptStatus(screen); got != PromptClaude {
		t.Fatalf("claude input with spaces: got %d, want PromptClaude", got)
	}
}

func TestDetectPromptStatus_NotClaudeGreaterThan(t *testing.T) {
	// A "> " that is NOT a Claude prompt (no Claude indicators above, cursor far into line)
	screen := emulator.NewScreen(80, 24)
	writeString(screen, 0, 5, "> some heredoc content here")
	screen.SetCursor(30, 5) // cursor well past the "> " prefix
	if got := detectPromptStatus(screen); got != PromptNone {
		// If cursor is far from the prompt prefix, we shouldn't detect Claude.
		// But "> " won't match shell either since ">" isn't at the suffix position.
		// This should be PromptNone.
		t.Fatalf("heredoc >: got %d, want PromptNone", got)
	}
}

func TestDetectPromptStatus_CursorAtTop(t *testing.T) {
	screen := emulator.NewScreen(80, 24)
	writeString(screen, 0, 0, "user@host:~$")
	screen.SetCursor(13, 0) // cursor at top line
	if got := detectPromptStatus(screen); got != PromptShell {
		t.Fatalf("cursor at top: got %d, want PromptShell", got)
	}
}

func TestDetectPromptStatus_PromptStatusValue(t *testing.T) {
	var ps PromptStatusValue
	if ps.Load() != PromptNone {
		t.Fatal("default should be PromptNone")
	}
	ps.Store(PromptShell)
	if ps.Load() != PromptShell {
		t.Fatal("should be PromptShell after store")
	}
	if !ps.CompareAndSwap(PromptShell, PromptClaude) {
		t.Fatal("CAS should succeed")
	}
	if ps.Load() != PromptClaude {
		t.Fatal("should be PromptClaude after CAS")
	}
	if ps.CompareAndSwap(PromptShell, PromptNone) {
		t.Fatal("CAS should fail with wrong old value")
	}
}

func TestGetLineText(t *testing.T) {
	screen := emulator.NewScreen(20, 5)
	writeString(screen, 0, 0, "hello world")
	got := getLineText(screen, 0)
	if got != "hello world" {
		t.Fatalf("getLineText: got %q, want %q", got, "hello world")
	}

	// Empty line should return empty string
	got = getLineText(screen, 1)
	if got != "" {
		t.Fatalf("empty line: got %q, want empty", got)
	}
}

func TestDetectPromptStatus_ShellGT(t *testing.T) {
	// Shell prompt ending with ">"
	screen := emulator.NewScreen(80, 24)
	writeString(screen, 0, 0, "PS C:\\Users\\test>")
	screen.SetCursor(17, 0)
	if got := detectPromptStatus(screen); got != PromptShell {
		t.Fatalf("shell GT: got %d, want PromptShell", got)
	}
}
