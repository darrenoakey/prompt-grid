package emulator

import "testing"

func newTestParser() *Parser {
	screen := NewScreen(80, 24)
	scrollback := NewScrollback()
	return NewParser(screen, scrollback)
}

func TestParserPlainText(t *testing.T) {
	p := newTestParser()

	p.Parse([]byte("Hello"))

	for i, r := range "Hello" {
		if p.screen.Cell(i, 0).Rune != r {
			t.Errorf("Cell(%d,0) = %q, want %q", i, p.screen.Cell(i, 0).Rune, r)
		}
	}
	if p.screen.cursor.X != 5 {
		t.Errorf("cursor.X = %d, want 5", p.screen.cursor.X)
	}
}

func TestParserCR(t *testing.T) {
	p := newTestParser()

	p.Parse([]byte("Hello\r"))
	if p.screen.cursor.X != 0 {
		t.Errorf("cursor.X = %d, want 0", p.screen.cursor.X)
	}
}

func TestParserLF(t *testing.T) {
	p := newTestParser()

	p.Parse([]byte("Hello\n"))
	if p.screen.cursor.Y != 1 {
		t.Errorf("cursor.Y = %d, want 1", p.screen.cursor.Y)
	}
}

func TestParserBS(t *testing.T) {
	p := newTestParser()

	p.Parse([]byte("AB\bC"))
	// A at 0, B at 1, backspace to 1, C at 1
	if p.screen.Cell(0, 0).Rune != 'A' {
		t.Errorf("Cell(0,0) = %q, want 'A'", p.screen.Cell(0, 0).Rune)
	}
	if p.screen.Cell(1, 0).Rune != 'C' {
		t.Errorf("Cell(1,0) = %q, want 'C'", p.screen.Cell(1, 0).Rune)
	}
}

func TestParserTab(t *testing.T) {
	p := newTestParser()

	p.Parse([]byte("A\tB"))
	// A at 0, tab to 8, B at 8
	if p.screen.cursor.X != 9 {
		t.Errorf("cursor.X = %d, want 9", p.screen.cursor.X)
	}
	if p.screen.Cell(8, 0).Rune != 'B' {
		t.Errorf("Cell(8,0) = %q, want 'B'", p.screen.Cell(8, 0).Rune)
	}
}

func TestParserCursorMovement(t *testing.T) {
	p := newTestParser()

	// Move to position 10, 5
	p.Parse([]byte("\x1b[6;11H"))
	if p.screen.cursor.X != 10 || p.screen.cursor.Y != 5 {
		t.Errorf("cursor = (%d,%d), want (10,5)", p.screen.cursor.X, p.screen.cursor.Y)
	}

	// Move up 2
	p.Parse([]byte("\x1b[2A"))
	if p.screen.cursor.Y != 3 {
		t.Errorf("cursor.Y = %d, want 3", p.screen.cursor.Y)
	}

	// Move right 5
	p.Parse([]byte("\x1b[5C"))
	if p.screen.cursor.X != 15 {
		t.Errorf("cursor.X = %d, want 15", p.screen.cursor.X)
	}
}

func TestParserClearScreen(t *testing.T) {
	p := newTestParser()

	// Fill some content
	p.Parse([]byte("Hello"))

	// Clear entire screen
	p.Parse([]byte("\x1b[2J"))

	if p.screen.Cell(0, 0).Rune != ' ' {
		t.Error("Screen should be cleared")
	}
}

func TestParserClearLine(t *testing.T) {
	p := newTestParser()

	p.Parse([]byte("Hello World"))
	p.Parse([]byte("\x1b[6G")) // Move to column 6
	p.Parse([]byte("\x1b[K"))  // Clear to end

	if p.screen.Cell(0, 0).Rune != 'H' {
		t.Error("Start of line should be preserved")
	}
	if p.screen.Cell(5, 0).Rune != ' ' {
		t.Error("Rest of line should be cleared")
	}
}

func TestParserSGRColors(t *testing.T) {
	p := newTestParser()

	// Set foreground red (31)
	p.Parse([]byte("\x1b[31mR"))

	cell := p.screen.Cell(0, 0)
	if cell.FG.Type != ColorIndexed || cell.FG.Index != 1 {
		t.Errorf("FG = %v, want indexed(1)", cell.FG)
	}
}

func TestParserSGR256Color(t *testing.T) {
	p := newTestParser()

	// Set foreground to color 100
	p.Parse([]byte("\x1b[38;5;100mX"))

	cell := p.screen.Cell(0, 0)
	if cell.FG.Type != ColorIndexed || cell.FG.Index != 100 {
		t.Errorf("FG = %v, want indexed(100)", cell.FG)
	}
}

func TestParserSGRRGB(t *testing.T) {
	p := newTestParser()

	// Set foreground to RGB(128, 64, 32)
	p.Parse([]byte("\x1b[38;2;128;64;32mX"))

	cell := p.screen.Cell(0, 0)
	if cell.FG.Type != ColorRGB {
		t.Errorf("FG.Type = %v, want ColorRGB", cell.FG.Type)
	}
	if cell.FG.R != 128 || cell.FG.G != 64 || cell.FG.B != 32 {
		t.Errorf("FG RGB = (%d,%d,%d), want (128,64,32)", cell.FG.R, cell.FG.G, cell.FG.B)
	}
}

func TestParserSGRAttributes(t *testing.T) {
	p := newTestParser()

	// Bold and italic
	p.Parse([]byte("\x1b[1;3mX"))

	cell := p.screen.Cell(0, 0)
	if cell.Attrs&AttrBold == 0 {
		t.Error("AttrBold should be set")
	}
	if cell.Attrs&AttrItalic == 0 {
		t.Error("AttrItalic should be set")
	}
}

func TestParserSGRReset(t *testing.T) {
	p := newTestParser()

	p.Parse([]byte("\x1b[1;31mA\x1b[0mB"))

	cellA := p.screen.Cell(0, 0)
	cellB := p.screen.Cell(1, 0)

	if cellA.Attrs&AttrBold == 0 {
		t.Error("A should be bold")
	}
	if cellB.Attrs&AttrBold != 0 {
		t.Error("B should not be bold")
	}
	if cellB.FG.Type != ColorDefault {
		t.Error("B should have default FG")
	}
}

func TestParserScrollRegion(t *testing.T) {
	p := newTestParser()

	// Set scroll region to lines 5-15
	p.Parse([]byte("\x1b[5;15r"))

	top, bot := p.screen.ScrollRegion()
	if top != 4 || bot != 14 {
		t.Errorf("ScrollRegion = (%d, %d), want (4, 14)", top, bot)
	}
}

func TestParserOSCTitle(t *testing.T) {
	p := newTestParser()

	var title string
	p.SetOnTitle(func(t string) {
		title = t
	})

	p.Parse([]byte("\x1b]0;My Title\x07"))

	if title != "My Title" {
		t.Errorf("title = %q, want %q", title, "My Title")
	}
	if p.Title() != "My Title" {
		t.Errorf("Title() = %q, want %q", p.Title(), "My Title")
	}
}

func TestParserCursorVisibility(t *testing.T) {
	p := newTestParser()

	// Hide cursor
	p.Parse([]byte("\x1b[?25l"))
	if p.screen.Cursor().Visible {
		t.Error("Cursor should be hidden")
	}

	// Show cursor
	p.Parse([]byte("\x1b[?25h"))
	if !p.screen.Cursor().Visible {
		t.Error("Cursor should be visible")
	}
}

func TestParserInsertDeleteLines(t *testing.T) {
	p := newTestParser()

	// Fill first 3 lines (use CR+LF to reset column)
	p.Parse([]byte("AAA\r\nBBB\r\nCCC"))

	// Go to line 2, insert 1 line
	p.Parse([]byte("\x1b[2;1H\x1b[L"))

	if p.screen.Cell(0, 1).Rune != ' ' {
		t.Errorf("Line 2 should be blank after insert, got %q", p.screen.Cell(0, 1).Rune)
	}
	if p.screen.Cell(0, 2).Rune != 'B' {
		t.Errorf("Line 3 should have 'B' after insert, got %q", p.screen.Cell(0, 2).Rune)
	}
}

func TestParserScrollback(t *testing.T) {
	p := newTestParser()

	// Fill screen and scroll
	for i := 0; i < 30; i++ {
		p.Parse([]byte("Line\n"))
	}

	if p.scrollback.Count() < 5 {
		t.Errorf("Scrollback count = %d, expected at least 5", p.scrollback.Count())
	}
}

func TestParserReset(t *testing.T) {
	p := newTestParser()

	p.Parse([]byte("Hello\x1b[1;31m"))
	p.Parse([]byte("\x1bc")) // RIS - full reset

	if p.screen.Cell(0, 0).Rune != ' ' {
		t.Error("Screen should be cleared after reset")
	}
}
