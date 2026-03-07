package gui

import (
	"strings"
	"sync/atomic"

	"prompt-grid/src/emulator"
)

// PromptStatus indicates what kind of prompt (if any) the session is at.
type PromptStatus int32

const (
	PromptNone   PromptStatus = iota // Busy / running command
	PromptShell                      // Waiting at bash/zsh/fish prompt
	PromptClaude                     // Claude Code waiting for input
)

// getLineText reads a single screen line as a string, trimming trailing spaces.
func getLineText(screen *emulator.Screen, y int) string {
	cols, _ := screen.Size()
	var buf strings.Builder
	buf.Grow(cols)
	for x := 0; x < cols; x++ {
		r := screen.Cell(x, y).Rune
		if r == 0 {
			buf.WriteByte(' ')
		} else {
			buf.WriteRune(r)
		}
	}
	return strings.TrimRight(buf.String(), " ")
}

// detectPromptStatus scans the screen around the cursor to determine if the
// terminal is sitting at a shell or Claude Code prompt.
func detectPromptStatus(screen *emulator.Screen) PromptStatus {
	if screen == nil {
		return PromptNone
	}

	cursor := screen.Cursor()
	_, rows := screen.Size()

	// Read cursor line text up to cursor position (the part before where the user would type)
	cursorLine := getLineText(screen, cursor.Y)

	// Check Claude Code patterns first (more specific)
	if detectClaude(screen, cursor, rows, cursorLine) {
		return PromptClaude
	}

	// Check shell prompt patterns
	if detectShell(cursorLine, cursor) {
		return PromptShell
	}

	return PromptNone
}

// detectShell checks if the cursor line looks like a shell prompt.
// We examine the text up to the cursor position for common prompt suffixes.
func detectShell(cursorLine string, cursor emulator.Cursor) bool {
	// Get text up to cursor position (cursor.X is a column index, not byte index)
	runes := []rune(cursorLine)
	if cursor.X < len(runes) {
		runes = runes[:cursor.X]
	}
	line := strings.TrimRight(string(runes), " ")

	if len(line) == 0 {
		return false
	}

	// Common prompt endings: "$ ", "# ", "% ", "> ", "❯"
	// Check for these at the end of the text before cursor
	suffixes := []string{"$", "#", "%", ">", "❯", "➜"}
	for _, s := range suffixes {
		if strings.HasSuffix(line, s) {
			return true
		}
	}

	// Some prompts put the indicator at the start (e.g. zsh oh-my-zsh: "➜ dirname")
	// If line starts with a known prompt char followed by space, it's a prompt
	trimmedLine := strings.TrimLeft(line, " ")
	prefixes := []string{"➜ ", "❯ "}
	for _, p := range prefixes {
		if strings.HasPrefix(trimmedLine, p) {
			return true
		}
	}

	return false
}

// detectClaude checks for Claude Code's input prompt patterns.
// Claude Code shows "> " at the start of the cursor line when waiting for input,
// and "? " when asking a question or showing options.
func detectClaude(screen *emulator.Screen, cursor emulator.Cursor, rows int, cursorLine string) bool {
	// Claude's main input prompt: cursor line starts with "> " (with possible leading space)
	trimmed := strings.TrimLeft(cursorLine, " ")

	// Check cursor line for Claude prompt ">" at start
	if strings.HasPrefix(trimmed, "> ") || trimmed == ">" {
		// Verify this isn't just a shell "> " (heredoc/continuation)
		// Claude prompts have the cursor right after "> " (within a few columns)
		leadingSpaces := len([]rune(cursorLine)) - len([]rune(trimmed))
		promptCols := leadingSpaces + 2 // spaces + "> "
		if trimmed == ">" {
			promptCols = leadingSpaces + 1
		}
		if cursor.X <= promptCols+1 {
			// Look for Claude indicators in lines above
			if hasClaudeIndicators(screen, cursor.Y, rows) {
				return true
			}
		}
	}

	// Claude question prompt: "? " at start of a line near cursor
	if strings.HasPrefix(trimmed, "? ") {
		return true
	}

	// Claude's "❯" prompt indicator — only if Claude indicators present above
	if strings.HasPrefix(trimmed, "❯ ") || trimmed == "❯" {
		if hasClaudeIndicators(screen, cursor.Y, rows) {
			return true
		}
	}

	return false
}

// hasClaudeIndicators checks lines above the cursor for Claude Code signatures.
func hasClaudeIndicators(screen *emulator.Screen, cursorY, rows int) bool {
	// Scan up to 10 lines above cursor for Claude-specific content
	scanLines := 10
	if scanLines > cursorY {
		scanLines = cursorY
	}

	for i := 1; i <= scanLines; i++ {
		line := getLineText(screen, cursorY-i)
		trimmed := strings.TrimSpace(line)

		// Claude Code status indicators
		if strings.Contains(trimmed, "Claude Code") ||
			strings.Contains(trimmed, "claude-code") ||
			strings.Contains(trimmed, "Auto-accept") ||
			strings.Contains(trimmed, "⏎ Send") ||
			strings.Contains(trimmed, "tokens remaining") ||
			strings.Contains(trimmed, "Cost:") ||
			strings.Contains(trimmed, "Duration:") {
			return true
		}
	}

	return false
}

// PromptStatusValue wraps atomic.Int32 for type-safe PromptStatus access.
type PromptStatusValue struct {
	v atomic.Int32
}

func (p *PromptStatusValue) Load() PromptStatus {
	return PromptStatus(p.v.Load())
}

func (p *PromptStatusValue) Store(s PromptStatus) {
	p.v.Store(int32(s))
}

// CompareAndSwap atomically sets new value if current == old. Returns true if swapped.
func (p *PromptStatusValue) CompareAndSwap(old, new PromptStatus) bool {
	return p.v.CompareAndSwap(int32(old), int32(new))
}
