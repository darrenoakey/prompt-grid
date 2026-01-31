package discord

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"claude-term/src/render"
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

	h.session.InteractionRespond(h.interaction.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   flags,
		},
	})
}

// respondWithFile sends a response with a file attachment
func (h *CommandHandler) respondWithFile(filename string, data []byte, content string) {
	h.session.InteractionRespond(h.interaction.Interaction, &discordgo.InteractionResponse{
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

	// Send command to session (add newline for enter)
	_, err := state.Session().Write([]byte(cmd + "\n"))
	if err != nil {
		h.respond(fmt.Sprintf("Failed to send command: %v", err), true)
		return
	}

	h.respond(fmt.Sprintf("Sent to **%s**: `%s`", name, cmd), false)
}

// HandleConnect handles the /term connect command
func (h *CommandHandler) HandleConnect(options []*discordgo.ApplicationCommandInteractionDataOption) {
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

	// Stop existing streamer
	if existing := h.bot.GetStreamer(); existing != nil {
		existing.Stop()
	}

	// Create new streamer
	streamer := NewStreamer(h.bot, state)
	h.bot.SetStreamer(streamer)
	streamer.Start()

	h.respond(fmt.Sprintf("Now streaming **%s** to this channel.", name), false)
}

// HandleDisconnect handles the /term disconnect command
func (h *CommandHandler) HandleDisconnect() {
	streamer := h.bot.GetStreamer()
	if streamer == nil {
		h.respond("Not currently streaming any session.", true)
		return
	}

	name := streamer.SessionName()
	streamer.Stop()
	h.bot.SetStreamer(nil)

	h.respond(fmt.Sprintf("Stopped streaming **%s**.", name), false)
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
