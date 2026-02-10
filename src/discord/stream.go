package discord

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"claude-term/src/gui"
	"claude-term/src/render"
)

const (
	idleTimeout = 1 * time.Second   // Send screenshot after 1s of idle
	maxInterval = 15 * time.Second  // Send screenshot every 15s max during continuous output
)

// Streamer streams terminal screenshots to Discord
type Streamer struct {
	bot           *Bot
	state         *gui.SessionState
	mu            sync.Mutex
	running       bool
	stopCh        chan struct{}
	pendingTimer  *time.Timer
	maxTimer      *time.Timer
	lastSent      time.Time
	lastScreenshot []byte
}

// NewStreamer creates a new streamer
func NewStreamer(bot *Bot, state *gui.SessionState) *Streamer {
	return &Streamer{
		bot:    bot,
		state:  state,
		stopCh: make(chan struct{}),
	}
}

// Start begins streaming
func (s *Streamer) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.lastSent = time.Now()
	s.mu.Unlock()

	// Set up screen change callback
	// Note: In a real implementation, we'd hook into the parser's output callback
	// For now, we'll poll periodically
	go s.pollLoop()
}

// Stop stops streaming
func (s *Streamer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	s.running = false
	close(s.stopCh)

	if s.pendingTimer != nil {
		s.pendingTimer.Stop()
	}
	if s.maxTimer != nil {
		s.maxTimer.Stop()
	}
}

// SessionName returns the name of the streamed session
func (s *Streamer) SessionName() string {
	return s.state.Name()
}

// pollLoop periodically checks for screen changes
func (s *Streamer) pollLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.checkForChanges()
		}
	}
}

func (s *Streamer) checkForChanges() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	// Take a screenshot
	screenshot, err := s.takeScreenshot()
	if err != nil {
		return
	}

	// Check if screen changed
	if bytes.Equal(screenshot, s.lastScreenshot) {
		return
	}

	s.lastScreenshot = screenshot

	// Screen changed - schedule a send
	s.scheduleScreenshot()
}

func (s *Streamer) scheduleScreenshot() {
	now := time.Now()

	// If max interval exceeded, send immediately
	if now.Sub(s.lastSent) >= maxInterval {
		s.sendScreenshot()
		return
	}

	// Reset idle timer
	if s.pendingTimer != nil {
		s.pendingTimer.Stop()
	}

	s.pendingTimer = time.AfterFunc(idleTimeout, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.running {
			s.sendScreenshot()
		}
	})

	// Set up max timer if not already set
	if s.maxTimer == nil {
		s.maxTimer = time.AfterFunc(maxInterval, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.running {
				s.sendScreenshot()
			}
		})
	}
}

func (s *Streamer) sendScreenshot() {
	// Reset timers
	if s.pendingTimer != nil {
		s.pendingTimer.Stop()
		s.pendingTimer = nil
	}
	if s.maxTimer != nil {
		s.maxTimer.Stop()
		s.maxTimer = nil
	}

	s.lastSent = time.Now()

	// Use the last captured screenshot
	if s.lastScreenshot == nil {
		return
	}

	// Send to Discord
	data := s.lastScreenshot
	s.lastScreenshot = nil // Release PNG bytes after send

	s.bot.Session().ChannelMessageSendComplex(s.bot.ChannelID(), &discordgo.MessageSend{
		Content: fmt.Sprintf("**%s** screenshot", s.state.Name()),
		Files: []*discordgo.File{
			{
				Name:        fmt.Sprintf("%s.png", s.state.Name()),
				ContentType: "image/png",
				Reader:      bytes.NewReader(data),
			},
		},
	})
}

func (s *Streamer) takeScreenshot() ([]byte, error) {
	screen := s.state.Screen()
	cols, rows := screen.Size()

	renderer, err := render.NewImageRenderer(cols, rows, 14)
	if err != nil {
		return nil, err
	}

	colors := s.bot.App().Colors()
	render.RenderScreen(renderer, screen, colors)

	var buf bytes.Buffer
	if err := renderer.WritePNG(&buf); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
