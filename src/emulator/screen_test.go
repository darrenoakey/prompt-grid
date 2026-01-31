package emulator

import "testing"

func TestNewScreen(t *testing.T) {
	s := NewScreen(80, 24)

	cols, rows := s.Size()
	if cols != 80 || rows != 24 {
		t.Errorf("Size() = (%d, %d), want (80, 24)", cols, rows)
	}

	cursor := s.Cursor()
	if cursor.X != 0 || cursor.Y != 0 {
		t.Errorf("Cursor = (%d, %d), want (0, 0)", cursor.X, cursor.Y)
	}
	if !cursor.Visible {
		t.Error("Cursor should be visible by default")
	}
}

func TestScreenWrite(t *testing.T) {
	s := NewScreen(80, 24)

	s.Write('H')
	s.Write('i')

	if s.Cell(0, 0).Rune != 'H' {
		t.Errorf("Cell(0,0) = %q, want 'H'", s.Cell(0, 0).Rune)
	}
	if s.Cell(1, 0).Rune != 'i' {
		t.Errorf("Cell(1,0) = %q, want 'i'", s.Cell(1, 0).Rune)
	}
	if s.cursor.X != 2 {
		t.Errorf("cursor.X = %d, want 2", s.cursor.X)
	}
}

func TestScreenWriteWrap(t *testing.T) {
	s := NewScreen(5, 3)

	// Write 6 characters to trigger wrap
	for _, r := range "Hello!" {
		s.Write(r)
	}

	// Last char should be on next line
	if s.Cell(0, 1).Rune != '!' {
		t.Errorf("Cell(0,1) = %q, want '!'", s.Cell(0, 1).Rune)
	}
	if s.cursor.Y != 1 {
		t.Errorf("cursor.Y = %d, want 1", s.cursor.Y)
	}
}

func TestScreenScrollUp(t *testing.T) {
	s := NewScreen(10, 3)

	// Fill with letters
	for y := 0; y < 3; y++ {
		for x := 0; x < 10; x++ {
			s.SetCell(x, y, Cell{Rune: rune('A' + y)})
		}
	}

	scrolled := s.ScrollUp(1)

	// Check scrolled off line
	if len(scrolled) != 1 {
		t.Errorf("ScrollUp returned %d lines, want 1", len(scrolled))
	}
	if scrolled[0][0].Rune != 'A' {
		t.Errorf("Scrolled line has %q, want 'A'", scrolled[0][0].Rune)
	}

	// Check remaining lines moved up
	if s.Cell(0, 0).Rune != 'B' {
		t.Errorf("Cell(0,0) = %q, want 'B'", s.Cell(0, 0).Rune)
	}
	if s.Cell(0, 1).Rune != 'C' {
		t.Errorf("Cell(0,1) = %q, want 'C'", s.Cell(0, 1).Rune)
	}
	if s.Cell(0, 2).Rune != ' ' {
		t.Errorf("Cell(0,2) = %q, want ' '", s.Cell(0, 2).Rune)
	}
}

func TestScreenScrollDown(t *testing.T) {
	s := NewScreen(10, 3)

	// Fill with letters
	for y := 0; y < 3; y++ {
		for x := 0; x < 10; x++ {
			s.SetCell(x, y, Cell{Rune: rune('A' + y)})
		}
	}

	s.ScrollDown(1)

	// Check lines moved down
	if s.Cell(0, 0).Rune != ' ' {
		t.Errorf("Cell(0,0) = %q, want ' '", s.Cell(0, 0).Rune)
	}
	if s.Cell(0, 1).Rune != 'A' {
		t.Errorf("Cell(0,1) = %q, want 'A'", s.Cell(0, 1).Rune)
	}
	if s.Cell(0, 2).Rune != 'B' {
		t.Errorf("Cell(0,2) = %q, want 'B'", s.Cell(0, 2).Rune)
	}
}

func TestScreenSetScrollRegion(t *testing.T) {
	s := NewScreen(10, 10)

	s.SetScrollRegion(2, 7)
	top, bot := s.ScrollRegion()
	if top != 2 || bot != 7 {
		t.Errorf("ScrollRegion = (%d, %d), want (2, 7)", top, bot)
	}
}

func TestScreenClearLine(t *testing.T) {
	s := NewScreen(10, 3)

	// Fill line with X
	for x := 0; x < 10; x++ {
		s.SetCell(x, 0, Cell{Rune: 'X'})
	}

	// Clear from cursor (5) to end
	s.SetCursor(5, 0)
	s.ClearLine(0)

	for x := 0; x < 5; x++ {
		if s.Cell(x, 0).Rune != 'X' {
			t.Errorf("Cell(%d,0) = %q, want 'X'", x, s.Cell(x, 0).Rune)
		}
	}
	for x := 5; x < 10; x++ {
		if s.Cell(x, 0).Rune != ' ' {
			t.Errorf("Cell(%d,0) = %q, want ' '", x, s.Cell(x, 0).Rune)
		}
	}
}

func TestScreenClearScreen(t *testing.T) {
	s := NewScreen(10, 10)

	// Fill with X
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			s.SetCell(x, y, Cell{Rune: 'X'})
		}
	}

	s.ClearScreen(2)

	// All should be blank
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			if s.Cell(x, y).Rune != ' ' {
				t.Errorf("Cell(%d,%d) = %q, want ' '", x, y, s.Cell(x, y).Rune)
			}
		}
	}
}

func TestScreenResize(t *testing.T) {
	s := NewScreen(10, 10)

	// Write some content
	for x := 0; x < 5; x++ {
		s.SetCell(x, 0, Cell{Rune: 'A'})
	}

	s.Resize(15, 5)

	cols, rows := s.Size()
	if cols != 15 || rows != 5 {
		t.Errorf("Size() = (%d, %d), want (15, 5)", cols, rows)
	}

	// Content should be preserved
	for x := 0; x < 5; x++ {
		if s.Cell(x, 0).Rune != 'A' {
			t.Errorf("Cell(%d,0) = %q, want 'A'", x, s.Cell(x, 0).Rune)
		}
	}
}

func TestScreenInsertChars(t *testing.T) {
	s := NewScreen(10, 1)

	// Fill with ABCDEFGHIJ
	for x := 0; x < 10; x++ {
		s.SetCell(x, 0, Cell{Rune: rune('A' + x)})
	}

	s.SetCursor(3, 0)
	s.InsertChars(2)

	// Should be ABC__DEFGH (I and J pushed off)
	expected := "ABC  DEFGH"
	for x := 0; x < 10; x++ {
		if s.Cell(x, 0).Rune != rune(expected[x]) {
			t.Errorf("Cell(%d,0) = %q, want %q", x, s.Cell(x, 0).Rune, rune(expected[x]))
		}
	}
}

func TestScreenDeleteChars(t *testing.T) {
	s := NewScreen(10, 1)

	// Fill with ABCDEFGHIJ
	for x := 0; x < 10; x++ {
		s.SetCell(x, 0, Cell{Rune: rune('A' + x)})
	}

	s.SetCursor(3, 0)
	s.DeleteChars(2)

	// Should be ABCFGHIJ__ (D and E deleted)
	expected := "ABCFGHIJ  "
	for x := 0; x < 10; x++ {
		if s.Cell(x, 0).Rune != rune(expected[x]) {
			t.Errorf("Cell(%d,0) = %q, want %q", x, s.Cell(x, 0).Rune, rune(expected[x]))
		}
	}
}

func TestScreenDirtyTracking(t *testing.T) {
	s := NewScreen(10, 5)

	// All lines should be dirty initially
	for y := 0; y < 5; y++ {
		if !s.IsDirty(y) {
			t.Errorf("Line %d should be dirty initially", y)
		}
	}

	s.ClearAllDirty()

	for y := 0; y < 5; y++ {
		if s.IsDirty(y) {
			t.Errorf("Line %d should not be dirty after clear", y)
		}
	}

	// Write to line 2
	s.SetCursor(0, 2)
	s.Write('X')

	if !s.IsDirty(2) {
		t.Error("Line 2 should be dirty after write")
	}
	if s.IsDirty(0) {
		t.Error("Line 0 should not be dirty")
	}
}

func TestScreenAttrs(t *testing.T) {
	s := NewScreen(10, 1)

	attrs := Cell{
		FG:    IndexedColor(1),
		BG:    IndexedColor(4),
		Attrs: AttrBold,
	}
	s.SetAttrs(attrs)

	s.Write('X')

	cell := s.Cell(0, 0)
	if cell.FG != attrs.FG {
		t.Errorf("Cell FG = %v, want %v", cell.FG, attrs.FG)
	}
	if cell.BG != attrs.BG {
		t.Errorf("Cell BG = %v, want %v", cell.BG, attrs.BG)
	}
	if cell.Attrs != attrs.Attrs {
		t.Errorf("Cell Attrs = %v, want %v", cell.Attrs, attrs.Attrs)
	}
}

func TestScreenCursorStyles(t *testing.T) {
	s := NewScreen(10, 1)

	s.SetCursorStyle(CursorBar)
	if s.Cursor().Style != CursorBar {
		t.Errorf("CursorStyle = %v, want CursorBar", s.Cursor().Style)
	}

	s.SetCursorVisible(false)
	if s.Cursor().Visible {
		t.Error("Cursor should be invisible")
	}
}
