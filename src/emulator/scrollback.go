package emulator

const chunkSize = 1024 // Lines per chunk

// Scrollback manages unlimited scrollback history using chunked storage
type Scrollback struct {
	chunks [][][]Cell // Each chunk holds up to chunkSize lines
	count  int        // Total number of lines
}

// NewScrollback creates a new scrollback buffer
func NewScrollback() *Scrollback {
	return &Scrollback{
		chunks: make([][][]Cell, 0),
		count:  0,
	}
}

// Push adds lines to the scrollback buffer
func (s *Scrollback) Push(lines ...[]Cell) {
	for _, line := range lines {
		chunkIdx := s.count / chunkSize

		// Create new chunk if needed
		if chunkIdx >= len(s.chunks) {
			s.chunks = append(s.chunks, make([][]Cell, 0, chunkSize))
		}

		// Copy the line to avoid aliasing
		lineCopy := make([]Cell, len(line))
		copy(lineCopy, line)

		s.chunks[chunkIdx] = append(s.chunks[chunkIdx], lineCopy)
		s.count++
	}
}

// Count returns the number of lines in scrollback
func (s *Scrollback) Count() int {
	return s.count
}

// Line returns the line at the given index (0 = oldest)
func (s *Scrollback) Line(index int) []Cell {
	if index < 0 || index >= s.count {
		return nil
	}

	chunkIdx := index / chunkSize
	lineIdx := index % chunkSize

	if chunkIdx >= len(s.chunks) {
		return nil
	}
	if lineIdx >= len(s.chunks[chunkIdx]) {
		return nil
	}

	return s.chunks[chunkIdx][lineIdx]
}

// Lines returns a range of lines [start, end)
func (s *Scrollback) Lines(start, end int) [][]Cell {
	if start < 0 {
		start = 0
	}
	if end > s.count {
		end = s.count
	}
	if start >= end {
		return nil
	}

	result := make([][]Cell, 0, end-start)
	for i := start; i < end; i++ {
		result = append(result, s.Line(i))
	}
	return result
}

// Clear removes all lines from scrollback
func (s *Scrollback) Clear() {
	s.chunks = make([][][]Cell, 0)
	s.count = 0
}
