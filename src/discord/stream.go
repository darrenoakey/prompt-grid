package discord

import (
	"strings"
	"sync"
	"time"
	"unicode"

	"prompt-grid/src/gui"
)

const (
	streamPollInterval = 250 * time.Millisecond
	idleTimeout        = 10 * time.Second
	heartbeatInterval  = 60 * time.Second
)

// Streamer streams debounced terminal text diffs to Discord.
type Streamer struct {
	bot   *Bot
	state *gui.SessionState

	mu sync.Mutex

	channelID string
	running   bool
	stopCh    chan struct{}
	primed    bool

	lastObserved    []string
	lastSent        []string
	dirty           bool
	lastChange      time.Time
	lastActivity    time.Time
	lastContentSent time.Time
	lastHeartbeat   time.Time
}

// NewStreamer creates a new streamer.
func NewStreamer(bot *Bot, state *gui.SessionState, channelID string) *Streamer {
	return &Streamer{
		bot:       bot,
		state:     state,
		channelID: channelID,
		stopCh:    make(chan struct{}),
	}
}

// Start begins streaming.
func (s *Streamer) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	s.running = true
	s.primed = false
	s.lastChange = now
	s.lastActivity = time.Time{}
	s.lastContentSent = time.Time{}
	s.lastHeartbeat = time.Time{}
	s.mu.Unlock()

	go s.loop()
}

// Stop stops streaming.
func (s *Streamer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stopCh)
}

// SetChannelID updates the destination Discord channel.
func (s *Streamer) SetChannelID(channelID string) {
	s.mu.Lock()
	s.channelID = channelID
	s.mu.Unlock()
}

func (s *Streamer) loop() {
	pollTicker := time.NewTicker(streamPollInterval)
	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer pollTicker.Stop()
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-pollTicker.C:
			s.pollOnce()
		case <-heartbeatTicker.C:
			s.sendHeartbeatIfNeeded()
		}
	}
}

func (s *Streamer) pollOnce() {
	snapshot := s.captureSnapshot()
	now := time.Now()

	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}

	changed := !equalLines(snapshot, s.lastObserved)
	if changed {
		s.lastObserved = cloneLines(snapshot)
		s.lastChange = now
	}

	if !s.primed {
		// Startup warmup: wait until the attached tmux screen has stopped
		// redrawing for idleTimeout, then baseline without sending.
		if now.Sub(s.lastChange) >= idleTimeout {
			s.lastSent = cloneLines(s.lastObserved)
			s.dirty = false
			s.primed = true
			s.lastActivity = time.Time{}
			s.lastContentSent = time.Time{}
			s.lastHeartbeat = time.Time{}
		}
		s.mu.Unlock()
		return
	}

	if changed {
		s.lastObserved = cloneLines(snapshot)
		s.dirty = true
		s.lastChange = now
		s.lastActivity = now
	}

	shouldSend := s.dirty && now.Sub(s.lastChange) >= idleTimeout
	channelID := s.channelID
	toSend := cloneLines(s.lastObserved)
	s.mu.Unlock()

	if shouldSend {
		s.sendSnapshot(channelID, toSend)
	}
}

func (s *Streamer) sendSnapshot(channelID string, snapshot []string) {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	if equalLines(snapshot, s.lastSent) {
		s.dirty = false
		s.mu.Unlock()
		return
	}
	overlap := sharedSuffixPrefix(s.lastSent, snapshot)
	delta := cloneLines(snapshot[overlap:])
	s.mu.Unlock()

	if len(delta) == 0 {
		s.mu.Lock()
		s.dirty = false
		s.mu.Unlock()
		return
	}

	if err := s.bot.sendLines(channelID, delta); err != nil {
		discordLog.Printf("Failed sending screen update for %q: %v", s.state.Name(), err)
		return
	}

	s.mu.Lock()
	s.lastSent = cloneLines(snapshot)
	s.dirty = false
	now := time.Now()
	s.lastContentSent = now
	s.lastHeartbeat = now
	s.mu.Unlock()
}

func (s *Streamer) sendHeartbeatIfNeeded() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	if !s.primed {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	if s.lastActivity.IsZero() {
		s.mu.Unlock()
		return
	}
	active := s.dirty || now.Sub(s.lastActivity) < heartbeatInterval
	if !active || now.Sub(s.lastContentSent) < heartbeatInterval || now.Sub(s.lastHeartbeat) < heartbeatInterval {
		s.mu.Unlock()
		return
	}
	channelID := s.channelID
	s.mu.Unlock()

	if err := s.bot.sendWorking(channelID); err != nil {
		discordLog.Printf("Failed sending heartbeat for %q: %v", s.state.Name(), err)
		return
	}

	s.mu.Lock()
	s.lastHeartbeat = now
	s.mu.Unlock()
}

func (s *Streamer) captureSnapshot() []string {
	screen := s.state.Screen()
	cols, rows := screen.Size()
	if cols <= 0 || rows <= 0 {
		return nil
	}

	lines := make([]string, rows)
	for y := 0; y < rows; y++ {
		row := make([]rune, cols)
		for x := 0; x < cols; x++ {
			cell := screen.Cell(x, y)
			if cell.Rune == 0 {
				row[x] = ' '
			} else {
				row[x] = cell.Rune
			}
		}
		lines[y] = strings.TrimRight(string(row), " ")
	}

	filtered := stripClaudeFooter(lines)
	for len(filtered) > 0 && strings.TrimSpace(filtered[len(filtered)-1]) == "" {
		filtered = filtered[:len(filtered)-1]
	}
	return filtered
}

func stripClaudeFooter(lines []string) []string {
	idx := lastNonBlankLine(lines)
	if idx < 0 {
		return nil
	}
	if idx < 3 {
		return cloneLines(lines[:idx+1])
	}

	// Claude UI footer pattern:
	// [optional blank lines]
	// ---------------------
	// prompt line
	// ---------------------
	// status line
	if isHorizontalSeparator(lines[idx-1]) && isHorizontalSeparator(lines[idx-3]) {
		cut := idx - 3
		for cut > 0 && strings.TrimSpace(lines[cut-1]) == "" {
			cut--
		}
		return cloneLines(lines[:cut])
	}

	return cloneLines(lines[:idx+1])
}

func lastNonBlankLine(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return i
		}
	}
	return -1
}

func isHorizontalSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	runes := []rune(trimmed)
	if len(runes) < 12 {
		return false
	}

	for _, r := range runes {
		switch {
		case r == '-', r == '─', r == '━', r == '═', r == '_':
			continue
		case unicode.IsSpace(r):
			continue
		default:
			return false
		}
	}

	return true
}

func sharedSuffixPrefix(previous, current []string) int {
	max := len(previous)
	if len(current) < max {
		max = len(current)
	}

	for overlap := max; overlap > 0; overlap-- {
		start := len(previous) - overlap
		if equalLines(previous[start:], current[:overlap]) {
			return overlap
		}
	}

	return 0
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, len(lines))
	copy(out, lines)
	return out
}
