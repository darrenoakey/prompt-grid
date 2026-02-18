package emulator

import "unsafe"

const chunkSize = 1024  // Lines per chunk
const maxLines = 10_000 // Maximum scrollback lines per session (~10 chunks, ~416 screens)

// Scrollback manages scrollback history using chunked storage, capped at maxLines
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

	// Trim oldest chunks if over the cap
	s.trim()
}

// trim drops oldest chunks to keep count <= maxLines
func (s *Scrollback) trim() {
	if s.count <= maxLines {
		return
	}

	// Calculate how many full chunks to drop
	excess := s.count - maxLines
	chunksToDrop := excess / chunkSize
	if chunksToDrop == 0 {
		return
	}

	// Drop oldest chunks
	s.chunks = s.chunks[chunksToDrop:]
	s.count -= chunksToDrop * chunkSize
}

// MemoryBytes estimates the memory usage of the scrollback buffer
func (s *Scrollback) MemoryBytes() int {
	cellSize := int(unsafe.Sizeof(Cell{}))
	total := 0
	for _, chunk := range s.chunks {
		for _, line := range chunk {
			total += len(line) * cellSize
		}
	}
	return total
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
