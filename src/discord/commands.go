package discord

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"prompt-grid/src/gui"
	"prompt-grid/src/render"
)

// CommandHandler handles Discord slash commands
type CommandHandler struct {
	bot         *Bot
	session     *discordgo.Session
	interaction *discordgo.InteractionCreate
}

// NewCommandHandler creates a new command handler
func NewCommandHandler(bot *Bot, session *discordgo.Session, interaction *discordgo.InteractionCreate) *CommandHandler {
	return &CommandHandler{
		bot:         bot,
		session:     session,
		interaction: interaction,
	}
}

// respond sends a response to the interaction
func (h *CommandHandler) respond(content string, ephemeral bool) {
	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}

	err := h.session.InteractionRespond(h.interaction.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   flags,
		},
	})
	if err != nil {
		discordLog.Printf("Failed to respond: %v", err)
	} else {
		discordLog.Printf("Responded with: %s", content)
	}
}

// respondWithFile sends a response with a file attachment
func (h *CommandHandler) respondWithFile(filename string, data []byte, content string) {
	err := h.session.InteractionRespond(h.interaction.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Files: []*discordgo.File{
				{
					Name:        filename,
					ContentType: "image/png",
					Reader:      bytes.NewReader(data),
				},
			},
		},
	})
	if err != nil {
		discordLog.Printf("Failed to respond with file: %v", err)
	} else {
		discordLog.Printf("Responded with file: %s (%d bytes)", filename, len(data))
	}
}

// followUp sends a follow-up message after a deferred response
func (h *CommandHandler) followUp(content string) {
	_, err := h.session.FollowupMessageCreate(h.interaction.Interaction, true, &discordgo.WebhookParams{
		Content: content,
	})
	if err != nil {
		discordLog.Printf("Failed to follow up: %v", err)
	} else {
		discordLog.Printf("Followed up with: %s", content)
	}
}

// followUpWithFile sends a follow-up message with a file after a deferred response
func (h *CommandHandler) followUpWithFile(filename string, data []byte, content string) {
	_, err := h.session.FollowupMessageCreate(h.interaction.Interaction, true, &discordgo.WebhookParams{
		Content: content,
		Files: []*discordgo.File{
			{
				Name:        filename,
				ContentType: "image/png",
				Reader:      bytes.NewReader(data),
			},
		},
	})
	if err != nil {
		discordLog.Printf("Failed to follow up with file: %v", err)
	} else {
		discordLog.Printf("Followed up with file: %s (%d bytes)", filename, len(data))
	}
}

// getOption finds an option by name
func getOption(options []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range options {
		if opt.Name == name {
			return opt.StringValue()
		}
	}
	return ""
}

// HandleList handles the /term list command
func (h *CommandHandler) HandleList() {
	sessions := h.bot.App().ListSessions()

	if len(sessions) == 0 {
		h.respond("No active sessions.", true)
		return
	}

	var sb strings.Builder
	sb.WriteString("**Active Sessions:**\n")
	for _, name := range sessions {
		sb.WriteString(fmt.Sprintf("• %s\n", name))
	}

	h.respond(sb.String(), true)
}

// HandleScreenshot handles the /term screenshot command
func (h *CommandHandler) HandleScreenshot(options []*discordgo.ApplicationCommandInteractionDataOption) {
	name := getOption(options, "name")
	if name == "" {
		h.respond("Session name is required.", true)
		return
	}

	state := h.bot.App().GetSession(name)
	if state == nil {
		h.respond(fmt.Sprintf("Session '%s' not found.", name), true)
		return
	}

	// Create image renderer
	screen := state.Screen()
	cols, rows := screen.Size()

	renderer, err := render.NewImageRenderer(cols, rows, 14)
	if err != nil {
		h.respond(fmt.Sprintf("Failed to create renderer: %v", err), true)
		return
	}

	// Render screen
	colors := h.bot.App().Colors()
	render.RenderScreen(renderer, screen, colors)

	// Encode to PNG
	var buf bytes.Buffer
	if err := renderer.WritePNG(&buf); err != nil {
		h.respond(fmt.Sprintf("Failed to encode screenshot: %v", err), true)
		return
	}

	h.respondWithFile(fmt.Sprintf("%s.png", name), buf.Bytes(), fmt.Sprintf("Screenshot of **%s**", name))
}

// HandleRun handles the /term run command
func (h *CommandHandler) HandleRun(options []*discordgo.ApplicationCommandInteractionDataOption) {
	name := getOption(options, "name")
	cmd := getOption(options, "command")

	if name == "" || cmd == "" {
		h.respond("Session name and command are required.", true)
		return
	}

	state := h.bot.App().GetSession(name)
	if state == nil {
		h.respond(fmt.Sprintf("Session '%s' not found.", name), true)
		return
	}

	// Send command using tmux key simulation so Enter behaves like a real keypress.
	err := h.bot.SendSessionInput(name, cmd)
	if err != nil {
		h.respond(fmt.Sprintf("Failed to send command: %v", err), true)
		return
	}

	// Try quick wait first (1 second)
	screenshot := h.waitForStableOutput(state, 1*time.Second)
	if screenshot != nil {
		// Got stable output quickly - respond directly
		h.respondWithFile(fmt.Sprintf("%s.png", name), screenshot, fmt.Sprintf("**%s** → `%s`", name, cmd))
		return
	}

	// Need more time - use deferred response
	h.session.InteractionRespond(h.interaction.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Wait longer for output to stabilize
	screenshot = h.waitForStableOutput(state, 10*time.Second)
	if screenshot == nil {
		h.followUp(fmt.Sprintf("Sent `%s` but failed to capture output", cmd))
		return
	}

	h.followUpWithFile(fmt.Sprintf("%s.png", name), screenshot, fmt.Sprintf("**%s** → `%s`", name, cmd))
}

// waitForStableOutput waits until screen output stabilizes or timeout
// It first waits for the screen to change (command started), then waits for it to stabilize
func (h *CommandHandler) waitForStableOutput(state *gui.SessionState, timeout time.Duration) []byte {
	screen := state.Screen()
	cols, rows := screen.Size()
	colors := h.bot.App().Colors()

	takeScreenshot := func() []byte {
		renderer, err := render.NewImageRenderer(cols, rows, 14)
		if err != nil {
			return nil
		}
		render.RenderScreen(renderer, screen, colors)
		var buf bytes.Buffer
		if err := renderer.WritePNG(&buf); err != nil {
			return nil
		}
		return buf.Bytes()
	}

	// Take initial screenshot before command output appears
	initialScreenshot := takeScreenshot()
	if initialScreenshot == nil {
		return nil
	}

	deadline := time.Now().Add(timeout)
	var lastScreenshot []byte
	stableCount := 0
	hasChanged := false

	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)

		currentScreenshot := takeScreenshot()
		if currentScreenshot == nil {
			continue
		}

		// First, wait for screen to change (command started producing output)
		if !hasChanged {
			if !bytes.Equal(currentScreenshot, initialScreenshot) {
				hasChanged = true
				lastScreenshot = currentScreenshot
				stableCount = 0
			}
			continue
		}

		// Now wait for screen to stabilize
		if bytes.Equal(currentScreenshot, lastScreenshot) {
			stableCount++
			// Consider stable after 4 consecutive unchanged checks (200ms)
			if stableCount >= 4 {
				return currentScreenshot
			}
		} else {
			stableCount = 0
			lastScreenshot = currentScreenshot
		}
	}

	// Timeout - return last screenshot if we have one
	if lastScreenshot != nil {
		return lastScreenshot
	}
	return initialScreenshot
}

// HandleConnect handles the /term connect command
func (h *CommandHandler) HandleConnect(options []*discordgo.ApplicationCommandInteractionDataOption) {
	name := getOption(options, "name")
	if name == "" {
		h.respond("Session name is required.", true)
		return
	}

	if h.bot.App().GetSession(name) == nil {
		h.respond(fmt.Sprintf("Session '%s' not found.", name), true)
		return
	}

	channelID, err := h.bot.EnsureSessionStream(name)
	if err != nil {
		h.respond(fmt.Sprintf("Failed to connect session stream: %v", err), true)
		return
	}

	h.respond(fmt.Sprintf("Session **%s** is streaming in <#%s>.", name, channelID), false)
}

// HandleDisconnect handles the /term disconnect command
func (h *CommandHandler) HandleDisconnect() {
	h.respond("Streaming is automatic per session. Close a session to stop its stream.", true)
}

// HandleFocus handles the /term focus command
func (h *CommandHandler) HandleFocus(options []*discordgo.ApplicationCommandInteractionDataOption) {
	name := getOption(options, "name")
	if name == "" {
		h.respond("Session name is required.", true)
		return
	}

	state := h.bot.App().GetSession(name)
	if state == nil {
		h.respond(fmt.Sprintf("Session '%s' not found.", name), true)
		return
	}

	// Note: Bringing window to front requires platform-specific code
	// For now, just acknowledge
	h.respond(fmt.Sprintf("Requested focus for **%s** (platform-dependent).", name), false)
}

// HandleClose handles the /term close command
func (h *CommandHandler) HandleClose(options []*discordgo.ApplicationCommandInteractionDataOption) {
	name := getOption(options, "name")
	if name == "" {
		h.respond("Session name is required.", true)
		return
	}

	err := h.bot.App().CloseSession(name)
	if err != nil {
		h.respond(fmt.Sprintf("Failed to close session: %v", err), true)
		return
	}

	h.respond(fmt.Sprintf("Closed session **%s**.", name), false)
}

// HandleNew handles the /term new command
func (h *CommandHandler) HandleNew(options []*discordgo.ApplicationCommandInteractionDataOption) {
	name := getOption(options, "name")
	if name == "" {
		h.respond("Session name is required.", true)
		return
	}

	sshHost := getOption(options, "ssh")

	// Check if session already exists
	if h.bot.App().GetSession(name) != nil {
		h.respond(fmt.Sprintf("Session **%s** already exists.", name), true)
		return
	}

	// Create the session
	err := h.bot.App().AddSession(name, sshHost)
	if err != nil {
		h.respond(fmt.Sprintf("Failed to create session: %v", err), true)
		return
	}

	if sshHost != "" {
		h.respond(fmt.Sprintf("Created SSH session **%s** → `%s`", name, sshHost), false)
	} else {
		h.respond(fmt.Sprintf("Created session **%s**", name), false)
	}
}
