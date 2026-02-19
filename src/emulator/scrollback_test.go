package emulator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewScrollback(t *testing.T) {
	sb := NewScrollback()
	if sb.Count() != 0 {
		t.Errorf("Count() = %d, want 0", sb.Count())
	}
}

func TestScrollbackPush(t *testing.T) {
	sb := NewScrollback()

	line := []Cell{{Rune: 'A'}, {Rune: 'B'}, {Rune: 'C'}}
	sb.Push(line)

	if sb.Count() != 1 {
		t.Errorf("Count() = %d, want 1", sb.Count())
	}
}

func TestScrollbackLine(t *testing.T) {
	sb := NewScrollback()

	line1 := []Cell{{Rune: 'A'}}
	line2 := []Cell{{Rune: 'B'}}
	sb.Push(line1, line2)

	got := sb.Line(0)
	if len(got) != 1 || got[0].Rune != 'A' {
		t.Errorf("Line(0) = %v, want [A]", got)
	}

	got = sb.Line(1)
	if len(got) != 1 || got[0].Rune != 'B' {
		t.Errorf("Line(1) = %v, want [B]", got)
	}
}

func TestScrollbackLineOutOfRange(t *testing.T) {
	sb := NewScrollback()
	sb.Push([]Cell{{Rune: 'A'}})

	if sb.Line(-1) != nil {
		t.Error("Line(-1) should be nil")
	}
	if sb.Line(1) != nil {
		t.Error("Line(1) should be nil")
	}
}

func TestScrollbackLines(t *testing.T) {
	sb := NewScrollback()

	for i := 0; i < 5; i++ {
		sb.Push([]Cell{{Rune: rune('A' + i)}})
	}

	lines := sb.Lines(1, 4)
	if len(lines) != 3 {
		t.Errorf("Lines() length = %d, want 3", len(lines))
	}
	if lines[0][0].Rune != 'B' {
		t.Errorf("Lines()[0] = %q, want 'B'", lines[0][0].Rune)
	}
	if lines[2][0].Rune != 'D' {
		t.Errorf("Lines()[2] = %q, want 'D'", lines[2][0].Rune)
	}
}

func TestScrollbackClear(t *testing.T) {
	sb := NewScrollback()
	sb.Push([]Cell{{Rune: 'A'}}, []Cell{{Rune: 'B'}})

	sb.Clear()

	if sb.Count() != 0 {
		t.Errorf("Count() after Clear() = %d, want 0", sb.Count())
	}
}

// TestScrollbackChunking verifies pushing lines fills the ring correctly.
// Uses ringSize-10 lines to stay within ring bounds (no disk backing for NewScrollback).
func TestScrollbackChunking(t *testing.T) {
	sb := NewScrollback()

	// Push lines that fit within the ring
	numLines := ringSize - 10
	for i := 0; i < numLines; i++ {
		sb.Push([]Cell{{Rune: rune('A' + (i % 26))}})
	}

	if sb.Count() != numLines {
		t.Errorf("Count() = %d, want %d", sb.Count(), numLines)
	}

	// Verify we can access all lines
	if sb.Line(0)[0].Rune != 'A' {
		t.Errorf("Line(0) = %q, want 'A'", sb.Line(0)[0].Rune)
	}
	// Line at index ringSize/2 should have correct rune
	mid := numLines / 2
	want := rune('A' + (mid % 26))
	if sb.Line(mid)[0].Rune != want {
		t.Errorf("Line(%d) = %q, want %q", mid, sb.Line(mid)[0].Rune, want)
	}
}

func TestScrollbackMemoryBytes(t *testing.T) {
	sb := NewScrollback()

	// Empty scrollback
	if sb.MemoryBytes() != 0 {
		t.Errorf("MemoryBytes() = %d for empty scrollback, want 0", sb.MemoryBytes())
	}

	// Add some lines — stay within ring capacity
	for i := 0; i < 50; i++ {
		sb.Push([]Cell{{Rune: 'A'}, {Rune: 'B'}, {Rune: 'C'}})
	}

	mem := sb.MemoryBytes()
	if mem == 0 {
		t.Error("MemoryBytes() should be non-zero after adding lines")
	}

	// Verify rough correctness: 50 lines * 3 cells each
	// Cell is at least 4 bytes (rune) + color fields
	if mem < 50*3*4 {
		t.Errorf("MemoryBytes() = %d, seems too low for 150 cells", mem)
	}
}

func TestScrollbackDataIsolation(t *testing.T) {
	sb := NewScrollback()

	line := []Cell{{Rune: 'A'}}
	sb.Push(line)

	// Modify original - should not affect scrollback
	line[0].Rune = 'Z'

	got := sb.Line(0)
	if got[0].Rune != 'A' {
		t.Errorf("Line(0) = %q, want 'A' (original modified)", got[0].Rune)
	}
}

// TestScrollbackRingOverflow verifies that after pushing more lines than ringSize,
// the oldest lines are accessible (nil for in-memory, correct for disk-backed),
// and the newest are always in the ring.
func TestScrollbackRingOverflow(t *testing.T) {
	sb := NewScrollback()

	numLines := 150 // more than ringSize (100)
	for i := 0; i < numLines; i++ {
		sb.Push([]Cell{{Rune: rune(i + 1)}}) // rune(1) through rune(150)
	}

	if sb.Count() != numLines {
		t.Errorf("Count() = %d, want %d", sb.Count(), numLines)
	}

	// Lines 0..49 fell out of the ring (no disk), should be nil
	for i := 0; i < numLines-ringSize; i++ {
		if sb.Line(i) != nil {
			t.Errorf("Line(%d) should be nil (out of ring), got non-nil", i)
		}
	}

	// Lines 50..149 are in the ring and should be correct
	for i := numLines - ringSize; i < numLines; i++ {
		got := sb.Line(i)
		if got == nil {
			t.Errorf("Line(%d) is nil, want non-nil", i)
			continue
		}
		want := rune(i + 1)
		if got[0].Rune != want {
			t.Errorf("Line(%d)[0].Rune = %d, want %d", i, got[0].Rune, want)
		}
	}
}

// TestScrollbackWithPath verifies disk-backed scrollback creates a file and persists lines.
// Uses rune values starting at 0x100 (Latin Extended) to avoid control/space trimming.
func TestScrollbackWithPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.scrollback")

	sb, err := NewScrollbackWithPath(path)
	if err != nil {
		t.Fatalf("NewScrollbackWithPath: %v", err)
	}
	defer sb.Close()

	// Push 200 lines (more than ringSize).
	// Use rune base 0x100 so none are space/control chars (which get trimmed on write).
	const runeBase = 0x100
	for i := 0; i < 200; i++ {
		sb.Push([]Cell{{Rune: rune(runeBase + i)}})
	}

	if sb.Count() != 200 {
		t.Errorf("Count() = %d, want 200", sb.Count())
	}

	// All lines should be accessible via disk
	for i := 0; i < 200; i++ {
		got := sb.Line(i)
		if got == nil {
			t.Errorf("Line(%d) is nil on disk-backed scrollback", i)
			continue
		}
		want := rune(runeBase + i)
		if got[0].Rune != want {
			t.Errorf("Line(%d)[0].Rune = %d, want %d", i, got[0].Rune, want)
		}
	}

	// Verify file was created
	if _, err := os.Stat(path); err != nil {
		t.Errorf("scrollback file not created: %v", err)
	}
}

// TestScrollbackWithPathReopen verifies lines persist across close/reopen.
func TestScrollbackWithPathReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.scrollback")

	// Write 50 lines
	sb, err := NewScrollbackWithPath(path)
	if err != nil {
		t.Fatalf("NewScrollbackWithPath: %v", err)
	}
	for i := 0; i < 50; i++ {
		sb.Push([]Cell{{Rune: rune('A' + (i % 26))}})
	}
	sb.Close()

	// Reopen — should load 50 lines from disk into ring
	sb2, err := NewScrollbackWithPath(path)
	if err != nil {
		t.Fatalf("NewScrollbackWithPath (reopen): %v", err)
	}
	defer sb2.Close()

	if sb2.Count() != 50 {
		t.Errorf("Count() after reopen = %d, want 50", sb2.Count())
	}

	// Verify first and last line
	got := sb2.Line(0)
	if got == nil || got[0].Rune != 'A' {
		t.Errorf("Line(0) after reopen = %v, want [A]", got)
	}
	got = sb2.Line(49)
	want := rune('A' + (49 % 26))
	if got == nil || got[0].Rune != want {
		t.Errorf("Line(49) after reopen = %v, want %q", got, want)
	}
}

// TestScrollbackReplayMode verifies that replay mode suppresses writes.
func TestScrollbackReplayMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.scrollback")

	sb, err := NewScrollbackWithPath(path)
	if err != nil {
		t.Fatalf("NewScrollbackWithPath: %v", err)
	}
	defer sb.Close()

	// Push 5 lines normally
	for i := 0; i < 5; i++ {
		sb.Push([]Cell{{Rune: rune('A' + i)}})
	}
	if sb.Count() != 5 {
		t.Fatalf("Count() = %d, want 5 before replay mode", sb.Count())
	}

	// Enable replay mode and try to push more — should be no-op
	sb.SetReplayMode(true)
	for i := 0; i < 10; i++ {
		sb.Push([]Cell{{Rune: rune('Z')}})
	}

	if sb.Count() != 5 {
		t.Errorf("Count() = %d after replay push, want 5 (replay suppressed writes)", sb.Count())
	}

	// Disable replay mode — further pushes should work
	sb.SetReplayMode(false)
	sb.Push([]Cell{{Rune: 'X'}})
	if sb.Count() != 6 {
		t.Errorf("Count() = %d after post-replay push, want 6", sb.Count())
	}
}

// TestScrollbackDiskLineColors verifies that colors survive the JSONL round-trip.
func TestScrollbackDiskLineColors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.scrollback")

	sb, err := NewScrollbackWithPath(path)
	if err != nil {
		t.Fatalf("NewScrollbackWithPath: %v", err)
	}

	// Push 150 lines so line 0 goes to disk
	colorLine := []Cell{{
		Rune:  'X',
		FG:    Color{Type: ColorIndexed, Index: 42},
		BG:    Color{Type: ColorRGB, R: 10, G: 20, B: 30},
		Attrs: AttrBold | AttrUnderline,
	}}
	sb.Push(colorLine)
	for i := 1; i < 150; i++ {
		sb.Push([]Cell{{Rune: rune('A' + (i % 26))}})
	}

	// Line 0 should be on disk — verify round-trip
	got := sb.Line(0)
	if got == nil {
		t.Fatal("Line(0) is nil on disk-backed scrollback with 150 lines")
	}
	c := got[0]
	if c.Rune != 'X' {
		t.Errorf("Rune = %q, want 'X'", c.Rune)
	}
	if c.FG.Type != ColorIndexed || c.FG.Index != 42 {
		t.Errorf("FG = %+v, want ColorIndexed index=42", c.FG)
	}
	if c.BG.Type != ColorRGB || c.BG.R != 10 || c.BG.G != 20 || c.BG.B != 30 {
		t.Errorf("BG = %+v, want ColorRGB (10,20,30)", c.BG)
	}
	if c.Attrs != AttrBold|AttrUnderline {
		t.Errorf("Attrs = %d, want Bold|Underline", c.Attrs)
	}

	sb.Close()
}

// TestScrollbackClearWithDisk verifies Clear resets disk file too.
func TestScrollbackClearWithDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.scrollback")

	sb, err := NewScrollbackWithPath(path)
	if err != nil {
		t.Fatalf("NewScrollbackWithPath: %v", err)
	}
	defer sb.Close()

	for i := 0; i < 50; i++ {
		sb.Push([]Cell{{Rune: 'A'}})
	}
	if sb.Count() != 50 {
		t.Fatalf("Count() = %d, want 50", sb.Count())
	}

	sb.Clear()

	if sb.Count() != 0 {
		t.Errorf("Count() after Clear() = %d, want 0", sb.Count())
	}

	// After clear, file should be empty (or tiny)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat after Clear: %v", err)
	}
	if info.Size() > 0 {
		t.Errorf("file size after Clear = %d, want 0", info.Size())
	}
}
