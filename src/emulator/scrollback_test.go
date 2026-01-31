package emulator

import "testing"

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

func TestScrollbackChunking(t *testing.T) {
	sb := NewScrollback()

	// Push more than one chunk worth of lines
	numLines := chunkSize + 100
	for i := 0; i < numLines; i++ {
		sb.Push([]Cell{{Rune: rune('A' + (i % 26))}})
	}

	if sb.Count() != numLines {
		t.Errorf("Count() = %d, want %d", sb.Count(), numLines)
	}

	// Verify we can access lines across chunks
	if sb.Line(0)[0].Rune != 'A' {
		t.Errorf("Line(0) = %q, want 'A'", sb.Line(0)[0].Rune)
	}
	if sb.Line(chunkSize)[0].Rune != rune('A'+(chunkSize%26)) {
		t.Errorf("Line(%d) = %q, want %q", chunkSize, sb.Line(chunkSize)[0].Rune, rune('A'+(chunkSize%26)))
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
