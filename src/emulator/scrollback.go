package emulator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"
)

const (
	ringSize     = 100              // Lines kept in memory ring buffer
	diskMaxBytes = 5 * 1024 * 1024 // 5MB max disk file size
	diskTrimTo   = 2 * 1024 * 1024 // Trim to ~2MB when over limit
	cacheWindow  = 1_000            // Lines loaded from disk per cache fill
)

// ScrollbackPath returns the JSONL scrollback file path for a session name.
// Uses the same directory as ptylog.
func ScrollbackPath(name string) string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "prompt-grid", "sessions")
	os.MkdirAll(dir, 0755)
	r := strings.NewReplacer("/", "_", "\\", "_", "\x00", "_")
	return filepath.Join(dir, r.Replace(name)+".scrollback")
}

// DeleteScrollback removes the scrollback file for a session.
func DeleteScrollback(name string) {
	os.Remove(ScrollbackPath(name))
}

// RenameScrollback renames a session's scrollback file.
func RenameScrollback(oldName, newName string) {
	os.Rename(ScrollbackPath(oldName), ScrollbackPath(newName))
}

// Scrollback manages terminal scrollback history with disk persistence.
// Only the most recent ringSize lines are kept in memory; older lines live
// in a JSONL file and are loaded on demand when the user scrolls back.
type Scrollback struct {
	mu sync.Mutex

	// Ring buffer — holds last ringSize lines in memory
	ring     [][]Cell
	ringHead int // Index of the oldest line in the ring
	ringFill int // Number of valid entries (0..ringSize)
	total    int // Total accessible lines (= disk file line count)

	// Disk storage
	path  string
	file  *os.File
	wbuf  *bufio.Writer
	fbytes int64 // Approximate bytes written (for trim check)

	// replay=true suppresses disk writes during ptylog replay
	replay bool

	// View cache — window of disk lines loaded on demand
	cache      [][]Cell
	cacheStart int // Absolute line index of cache[0]
}

// NewScrollback creates an in-memory-only scrollback (no disk backing).
// Used in tests and when no path is available.
func NewScrollback() *Scrollback {
	return &Scrollback{
		ring: make([][]Cell, ringSize),
	}
}

// NewScrollbackWithPath creates a disk-backed scrollback.
// Existing lines from a previous run are loaded from path into the ring.
func NewScrollbackWithPath(path string) (*Scrollback, error) {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	sb := &Scrollback{
		ring: make([][]Cell, ringSize),
		path: path,
		file: f,
	}

	// Count total disk lines and load last ringSize into ring
	sb.total = sb.loadFromFile(f)

	// Seek to end for appending
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return nil, err
	}
	sb.fbytes = size
	sb.wbuf = bufio.NewWriterSize(f, 64*1024)

	return sb, nil
}

// loadFromFile reads all lines from f, sets ring to last ringSize lines, returns total count.
// f must be positioned at start. Caller is responsible for seeking to end afterward.
func (s *Scrollback) loadFromFile(f *os.File) int {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 2*1024*1024), 2*1024*1024)

	// Collect all raw lines (just bytes, not decoded yet)
	var rawLines [][]byte
	for scanner.Scan() {
		b := make([]byte, len(scanner.Bytes()))
		copy(b, scanner.Bytes())
		rawLines = append(rawLines, b)
	}

	total := len(rawLines)

	// Load last ringSize into ring
	start := total - ringSize
	if start < 0 {
		start = 0
	}
	tail := rawLines[start:]
	for i, raw := range tail {
		s.ring[i] = decodeLine(raw)
	}
	s.ringFill = len(tail)
	s.ringHead = 0

	return total
}

// SetReplayMode enables or disables replay mode.
// In replay mode, Push() does NOT modify the ring or write to disk.
// The ring keeps what was loaded from disk at startup.
// Call SetReplayMode(true) before ptylog.ReplayLog, false afterward.
func (s *Scrollback) SetReplayMode(on bool) {
	s.mu.Lock()
	s.replay = on
	s.mu.Unlock()
}

// Push adds lines to the scrollback.
func (s *Scrollback) Push(lines ...[]Cell) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.replay {
		// Replay mode: no-op — ring was populated from disk at startup
		return
	}

	for _, line := range lines {
		lineCopy := make([]Cell, len(line))
		copy(lineCopy, line)

		// Write to disk if backed
		if s.wbuf != nil {
			s.writeLineLocked(lineCopy)
		}

		// Update ring buffer
		if s.ringFill < ringSize {
			idx := (s.ringHead + s.ringFill) % ringSize
			s.ring[idx] = lineCopy
			s.ringFill++
		} else {
			// Ring full: overwrite oldest
			s.ring[s.ringHead] = lineCopy
			s.ringHead = (s.ringHead + 1) % ringSize
		}
		s.total++
	}

	if s.wbuf != nil {
		_ = s.wbuf.Flush()
		s.checkTrimLocked()
	}
}

// Count returns the total number of accessible scrollback lines.
func (s *Scrollback) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

// Line returns the line at absolute index i (0 = oldest).
// Returns nil if out of range. Loads from disk cache on cache miss.
func (s *Scrollback) Line(i int) []Cell {
	s.mu.Lock()
	defer s.mu.Unlock()

	if i < 0 || i >= s.total {
		return nil
	}

	// Check ring first (most recent lines)
	ringStart := s.total - s.ringFill
	if i >= ringStart {
		offset := i - ringStart
		return s.ring[(s.ringHead+offset)%ringSize]
	}

	// No disk — old lines are gone
	if s.path == "" {
		return nil
	}

	// Check cache
	if len(s.cache) > 0 && i >= s.cacheStart && i < s.cacheStart+len(s.cache) {
		return s.cache[i-s.cacheStart]
	}

	// Load window from disk
	start := i - cacheWindow/2
	if start < 0 {
		start = 0
	}
	end := start + cacheWindow
	if end > ringStart {
		end = ringStart
	}

	s.cache = s.loadDiskRange(start, end)
	s.cacheStart = start

	if i >= s.cacheStart && i < s.cacheStart+len(s.cache) {
		return s.cache[i-s.cacheStart]
	}
	return nil
}

// Lines returns a slice of lines in [start, end).
func (s *Scrollback) Lines(start, end int) [][]Cell {
	count := s.Count()
	if start < 0 {
		start = 0
	}
	if end > count {
		end = count
	}
	if start >= end {
		return nil
	}
	result := make([][]Cell, end-start)
	for i := start; i < end; i++ {
		result[i-start] = s.Line(i)
	}
	return result
}

// Clear removes all lines from memory and truncates the disk file.
func (s *Scrollback) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ring = make([][]Cell, ringSize)
	s.ringHead = 0
	s.ringFill = 0
	s.total = 0
	s.cache = nil
	s.cacheStart = 0

	if s.file != nil {
		_ = s.wbuf.Flush()
		_ = s.file.Truncate(0)
		_, _ = s.file.Seek(0, io.SeekEnd)
		s.wbuf.Reset(s.file)
		s.fbytes = 0
	}
}

// Close flushes and closes the disk file.
func (s *Scrollback) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.wbuf != nil {
		_ = s.wbuf.Flush()
	}
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
		s.wbuf = nil
	}
}

// MemoryBytes returns an estimate of in-memory bytes used.
func (s *Scrollback) MemoryBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cellSize := int(unsafe.Sizeof(Cell{}))
	total := 0
	for i := 0; i < s.ringFill; i++ {
		line := s.ring[(s.ringHead+i)%ringSize]
		total += len(line) * cellSize
	}
	return total
}

// writeLineLocked encodes one line and appends to the buffered writer.
// Caller must hold s.mu.
func (s *Scrollback) writeLineLocked(line []Cell) {
	// Find last non-empty cell (trim trailing blanks to save space)
	last := -1
	for i := len(line) - 1; i >= 0; i-- {
		c := line[i]
		if c.Rune != 0 && c.Rune != ' ' {
			last = i
			break
		}
		if c.FG.Type != ColorDefault || c.BG.Type != ColorDefault || c.Attrs != 0 {
			last = i
			break
		}
	}

	// Encode as JSON array of [rune, fgPacked, bgPacked, attrs]
	encoded := make([][4]int64, last+1)
	for i := 0; i <= last; i++ {
		c := line[i]
		encoded[i] = [4]int64{
			int64(c.Rune),
			packColor(c.FG),
			packColor(c.BG),
			int64(c.Attrs),
		}
	}

	data, err := json.Marshal(encoded)
	if err != nil {
		return
	}
	n, _ := s.wbuf.Write(data)
	s.wbuf.WriteByte('\n')
	s.fbytes += int64(n) + 1
}

// packColor encodes a Color into a single int64.
func packColor(c Color) int64 {
	switch c.Type {
	case ColorIndexed:
		return (1 << 24) | int64(c.Index)
	case ColorRGB:
		return (2 << 24) | int64(c.R)<<16 | int64(c.G)<<8 | int64(c.B)
	default:
		return 0
	}
}

// unpackColor decodes a packed int64 into a Color.
func unpackColor(v int64) Color {
	switch v >> 24 {
	case 1:
		return Color{Type: ColorIndexed, Index: uint8(v & 0xFF)}
	case 2:
		return Color{Type: ColorRGB, R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v)}
	default:
		return Color{Type: ColorDefault}
	}
}

// decodeLine parses a JSON-encoded line back into a []Cell.
func decodeLine(data []byte) []Cell {
	if len(data) == 0 || bytes.Equal(data, []byte("[]")) {
		return nil
	}
	var encoded [][4]int64
	if err := json.Unmarshal(data, &encoded); err != nil {
		return nil
	}
	line := make([]Cell, len(encoded))
	for i, e := range encoded {
		line[i] = Cell{
			Rune:  rune(e[0]),
			FG:    unpackColor(e[1]),
			BG:    unpackColor(e[2]),
			Attrs: AttrFlags(e[3]),
		}
	}
	return line
}

// loadDiskRange reads lines [start, end) from the disk file.
// Opens a separate read-only FD to avoid position conflict with the writer.
// Caller must hold s.mu.
func (s *Scrollback) loadDiskRange(start, end int) [][]Cell {
	if s.path == "" || start >= end {
		return nil
	}

	// Flush any buffered writes first
	if s.wbuf != nil {
		_ = s.wbuf.Flush()
	}

	// Open a separate read FD
	rf, err := os.Open(s.path)
	if err != nil {
		return nil
	}
	defer rf.Close()

	scanner := bufio.NewScanner(rf)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var result [][]Cell
	lineNum := 0
	for scanner.Scan() {
		if lineNum >= end {
			break
		}
		if lineNum >= start {
			result = append(result, decodeLine(scanner.Bytes()))
		}
		lineNum++
	}
	return result
}

// checkTrimLocked trims the disk file if it exceeds diskMaxBytes.
// Caller must hold s.mu.
func (s *Scrollback) checkTrimLocked() {
	if s.fbytes < diskMaxBytes {
		return
	}
	s.trimDiskLocked()
}

// trimDiskLocked reads the file, drops lines from the front to reach diskTrimTo bytes,
// rewrites the file, and updates total/bytes.
func (s *Scrollback) trimDiskLocked() {
	if s.file == nil {
		return
	}

	// Flush first
	_ = s.wbuf.Flush()

	// Read entire file
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}

	if int64(len(data)) <= diskTrimTo {
		s.fbytes = int64(len(data))
		return
	}

	// Find cut point: discard first (len-diskTrimTo) bytes, then align to next newline
	cutPoint := int64(len(data)) - diskTrimTo
	for cutPoint < int64(len(data)) {
		if data[cutPoint] == '\n' {
			cutPoint++
			break
		}
		cutPoint++
	}

	kept := data[cutPoint:]

	// Rewrite file
	_ = s.file.Truncate(0)
	_, _ = s.file.Seek(0, io.SeekStart)
	_, _ = s.file.Write(kept)
	_ = s.file.Sync()
	_, _ = s.file.Seek(0, io.SeekEnd)
	s.wbuf.Reset(s.file)
	s.fbytes = int64(len(kept))

	// Recount lines (total changed)
	s.total = bytes.Count(kept, []byte{'\n'})

	// Invalidate cache
	s.cache = nil

	// Reload ring from new file tail
	rf, err := os.Open(s.path)
	if err != nil {
		return
	}
	defer rf.Close()
	s.total = s.loadFromFile(rf)
}
