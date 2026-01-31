package emulator

// CursorStyle represents the cursor appearance
type CursorStyle uint8

const (
	CursorBlock CursorStyle = iota
	CursorUnderline
	CursorBar
)

// Cursor represents the terminal cursor state
type Cursor struct {
	X, Y     int
	Visible  bool
	Blinking bool
	Style    CursorStyle
}

// Screen represents the active terminal screen buffer
type Screen struct {
	cols, rows int
	cells      [][]Cell
	cursor     Cursor
	scrollTop  int
	scrollBot  int
	dirty      []bool // Tracks which lines need repainting
	attrs      Cell   // Current drawing attributes
}

// NewScreen creates a new screen buffer
func NewScreen(cols, rows int) *Screen {
	s := &Screen{
		cols:      cols,
		rows:      rows,
		scrollTop: 0,
		scrollBot: rows - 1,
		dirty:     make([]bool, rows),
		attrs:     DefaultCell(),
	}
	s.cursor = Cursor{
		X:       0,
		Y:       0,
		Visible: true,
		Style:   CursorBlock,
	}
	s.cells = make([][]Cell, rows)
	for i := range s.cells {
		s.cells[i] = make([]Cell, cols)
		for j := range s.cells[i] {
			s.cells[i][j] = DefaultCell()
		}
		s.dirty[i] = true
	}
	return s
}

// Size returns the screen dimensions
func (s *Screen) Size() (cols, rows int) {
	return s.cols, s.rows
}

// Cursor returns the current cursor state
func (s *Screen) Cursor() Cursor {
	return s.cursor
}

// SetCursor sets the cursor position
func (s *Screen) SetCursor(x, y int) {
	s.cursor.X = clamp(x, 0, s.cols-1)
	s.cursor.Y = clamp(y, 0, s.rows-1)
}

// SetCursorVisible sets cursor visibility
func (s *Screen) SetCursorVisible(visible bool) {
	s.cursor.Visible = visible
}

// SetCursorStyle sets the cursor style
func (s *Screen) SetCursorStyle(style CursorStyle) {
	s.cursor.Style = style
}

// Cell returns the cell at the given position
func (s *Screen) Cell(x, y int) Cell {
	if x < 0 || x >= s.cols || y < 0 || y >= s.rows {
		return DefaultCell()
	}
	return s.cells[y][x]
}

// SetCell sets the cell at the given position
func (s *Screen) SetCell(x, y int, cell Cell) {
	if x < 0 || x >= s.cols || y < 0 || y >= s.rows {
		return
	}
	s.cells[y][x] = cell
	s.dirty[y] = true
}

// Write writes a rune at the current cursor position with current attributes
func (s *Screen) Write(r rune) {
	if s.cursor.X >= s.cols {
		s.cursor.X = 0
		s.cursor.Y++
		if s.cursor.Y > s.scrollBot {
			s.ScrollUp(1)
			s.cursor.Y = s.scrollBot
		}
	}

	cell := Cell{
		Rune:  r,
		FG:    s.attrs.FG,
		BG:    s.attrs.BG,
		Attrs: s.attrs.Attrs,
	}
	s.SetCell(s.cursor.X, s.cursor.Y, cell)
	s.cursor.X++
}

// SetAttrs sets the current drawing attributes
func (s *Screen) SetAttrs(attrs Cell) {
	s.attrs = attrs
}

// Attrs returns the current drawing attributes
func (s *Screen) Attrs() Cell {
	return s.attrs
}

// ResetAttrs resets all drawing attributes to default
func (s *Screen) ResetAttrs() {
	s.attrs = DefaultCell()
}

// SetScrollRegion sets the scrolling region
func (s *Screen) SetScrollRegion(top, bottom int) {
	top = clamp(top, 0, s.rows-1)
	bottom = clamp(bottom, 0, s.rows-1)
	if top < bottom {
		s.scrollTop = top
		s.scrollBot = bottom
	}
}

// ScrollRegion returns the current scroll region
func (s *Screen) ScrollRegion() (top, bottom int) {
	return s.scrollTop, s.scrollBot
}

// ScrollUp scrolls the screen up by n lines within the scroll region
func (s *Screen) ScrollUp(n int) [][]Cell {
	if n <= 0 {
		return nil
	}

	// Collect lines that will scroll off
	scrolledOff := make([][]Cell, 0, n)
	for i := 0; i < n && i <= s.scrollBot-s.scrollTop; i++ {
		line := make([]Cell, s.cols)
		copy(line, s.cells[s.scrollTop+i])
		scrolledOff = append(scrolledOff, line)
	}

	// Scroll the region
	for y := s.scrollTop; y <= s.scrollBot; y++ {
		if y+n <= s.scrollBot {
			copy(s.cells[y], s.cells[y+n])
		} else {
			for x := 0; x < s.cols; x++ {
				s.cells[y][x] = DefaultCell()
			}
		}
		s.dirty[y] = true
	}

	return scrolledOff
}

// ScrollDown scrolls the screen down by n lines within the scroll region
func (s *Screen) ScrollDown(n int) {
	if n <= 0 {
		return
	}

	for y := s.scrollBot; y >= s.scrollTop; y-- {
		if y-n >= s.scrollTop {
			copy(s.cells[y], s.cells[y-n])
		} else {
			for x := 0; x < s.cols; x++ {
				s.cells[y][x] = DefaultCell()
			}
		}
		s.dirty[y] = true
	}
}

// Clear clears the entire screen
func (s *Screen) Clear() {
	for y := 0; y < s.rows; y++ {
		for x := 0; x < s.cols; x++ {
			s.cells[y][x] = DefaultCell()
		}
		s.dirty[y] = true
	}
	s.cursor.X = 0
	s.cursor.Y = 0
}

// ClearLine clears the current line
func (s *Screen) ClearLine(mode int) {
	y := s.cursor.Y
	switch mode {
	case 0: // From cursor to end
		for x := s.cursor.X; x < s.cols; x++ {
			s.cells[y][x] = DefaultCell()
		}
	case 1: // From start to cursor
		for x := 0; x <= s.cursor.X; x++ {
			s.cells[y][x] = DefaultCell()
		}
	case 2: // Entire line
		for x := 0; x < s.cols; x++ {
			s.cells[y][x] = DefaultCell()
		}
	}
	s.dirty[y] = true
}

// ClearScreen clears the screen based on mode
func (s *Screen) ClearScreen(mode int) {
	switch mode {
	case 0: // From cursor to end
		s.ClearLine(0)
		for y := s.cursor.Y + 1; y < s.rows; y++ {
			for x := 0; x < s.cols; x++ {
				s.cells[y][x] = DefaultCell()
			}
			s.dirty[y] = true
		}
	case 1: // From start to cursor
		for y := 0; y < s.cursor.Y; y++ {
			for x := 0; x < s.cols; x++ {
				s.cells[y][x] = DefaultCell()
			}
			s.dirty[y] = true
		}
		s.ClearLine(1)
	case 2, 3: // Entire screen
		s.Clear()
	}
}

// InsertChars inserts n blank characters at cursor
func (s *Screen) InsertChars(n int) {
	y := s.cursor.Y
	for x := s.cols - 1; x >= s.cursor.X+n; x-- {
		s.cells[y][x] = s.cells[y][x-n]
	}
	for x := s.cursor.X; x < s.cursor.X+n && x < s.cols; x++ {
		s.cells[y][x] = DefaultCell()
	}
	s.dirty[y] = true
}

// DeleteChars deletes n characters at cursor
func (s *Screen) DeleteChars(n int) {
	y := s.cursor.Y
	for x := s.cursor.X; x < s.cols-n; x++ {
		s.cells[y][x] = s.cells[y][x+n]
	}
	for x := s.cols - n; x < s.cols; x++ {
		s.cells[y][x] = DefaultCell()
	}
	s.dirty[y] = true
}

// InsertLines inserts n blank lines at cursor
func (s *Screen) InsertLines(n int) {
	if s.cursor.Y < s.scrollTop || s.cursor.Y > s.scrollBot {
		return
	}
	savedTop := s.scrollTop
	s.scrollTop = s.cursor.Y
	s.ScrollDown(n)
	s.scrollTop = savedTop
}

// DeleteLines deletes n lines at cursor
func (s *Screen) DeleteLines(n int) {
	if s.cursor.Y < s.scrollTop || s.cursor.Y > s.scrollBot {
		return
	}
	savedTop := s.scrollTop
	s.scrollTop = s.cursor.Y
	s.ScrollUp(n)
	s.scrollTop = savedTop
}

// IsDirty returns whether the given line needs repainting
func (s *Screen) IsDirty(y int) bool {
	if y < 0 || y >= s.rows {
		return false
	}
	return s.dirty[y]
}

// ClearDirty clears the dirty flag for a line
func (s *Screen) ClearDirty(y int) {
	if y >= 0 && y < s.rows {
		s.dirty[y] = false
	}
}

// ClearAllDirty clears all dirty flags
func (s *Screen) ClearAllDirty() {
	for i := range s.dirty {
		s.dirty[i] = false
	}
}

// MarkAllDirty marks all lines as dirty
func (s *Screen) MarkAllDirty() {
	for i := range s.dirty {
		s.dirty[i] = true
	}
}

// Resize changes the screen size
func (s *Screen) Resize(cols, rows int) {
	newCells := make([][]Cell, rows)
	newDirty := make([]bool, rows)

	for y := 0; y < rows; y++ {
		newCells[y] = make([]Cell, cols)
		for x := 0; x < cols; x++ {
			if y < s.rows && x < s.cols {
				newCells[y][x] = s.cells[y][x]
			} else {
				newCells[y][x] = DefaultCell()
			}
		}
		newDirty[y] = true
	}

	s.cells = newCells
	s.dirty = newDirty
	s.cols = cols
	s.rows = rows
	s.scrollTop = 0
	s.scrollBot = rows - 1

	// Clamp cursor
	s.cursor.X = clamp(s.cursor.X, 0, cols-1)
	s.cursor.Y = clamp(s.cursor.Y, 0, rows-1)
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
