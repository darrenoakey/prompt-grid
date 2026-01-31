package discord

import (
	"fmt"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/zalando/go-keyring"

	"claude-term/src/config"
	"claude-term/src/gui"
)

const (
	serviceName = "claude-term"
	tokenKey    = "discord_bot_token"
)

// Bot manages the Discord connection
type Bot struct {
	session        *discordgo.Session
	cfg            *config.DiscordConfig
	app            *gui.App
	streamer       *Streamer
	mu             sync.RWMutex
	isConnected    bool
	slashCommands  []*discordgo.ApplicationCommand
}

// NewBot creates a new Discord bot
func NewBot(cfg *config.DiscordConfig, app *gui.App) (*Bot, error) {
	// Get token from keyring
	token, err := keyring.Get(serviceName, tokenKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get Discord token from keyring: %w", err)
	}

	// Create Discord session
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	bot := &Bot{
		session: session,
		cfg:     cfg,
		app:     app,
	}

	// Set up handlers
	session.AddHandler(bot.handleReady)
	session.AddHandler(bot.handleInteraction)

	return bot, nil
}

// Connect connects to Discord
func (b *Bot) Connect() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.isConnected {
		return nil
	}

	// Open connection
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("failed to connect to Discord: %w", err)
	}

	b.isConnected = true
	return nil
}

// Disconnect disconnects from Discord
func (b *Bot) Disconnect() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isConnected {
		return nil
	}

	// Remove slash commands
	for _, cmd := range b.slashCommands {
		b.session.ApplicationCommandDelete(b.session.State.User.ID, b.cfg.ServerID, cmd.ID)
	}

	// Stop streamer
	if b.streamer != nil {
		b.streamer.Stop()
	}

	// Close connection
	if err := b.session.Close(); err != nil {
		return err
	}

	b.isConnected = false
	return nil
}

func (b *Bot) handleReady(s *discordgo.Session, r *discordgo.Ready) {
	fmt.Printf("Discord bot logged in as: %v#%v\n", s.State.User.Username, s.State.User.Discriminator)

	// Register slash commands
	b.registerCommands()
}

func (b *Bot) registerCommands() {
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "term",
			Description: "Claude-Term commands",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "list",
					Description: "List all sessions",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "screenshot",
					Description: "Get a screenshot of a session",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "name",
							Description: "Session name",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
				{
					Name:        "run",
					Description: "Send input to a session",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "name",
							Description: "Session name",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
						{
							Name:        "command",
							Description: "Command to run",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
				{
					Name:        "connect",
					Description: "Start streaming screenshots for a session",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "name",
							Description: "Session name",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
				{
					Name:        "disconnect",
					Description: "Stop streaming screenshots",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
				{
					Name:        "focus",
					Description: "Bring a session window to front",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "name",
							Description: "Session name",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
				{
					Name:        "close",
					Description: "Close a session",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "name",
							Description: "Session name",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
			},
		},
	}

	for _, cmd := range commands {
		created, err := b.session.ApplicationCommandCreate(b.session.State.User.ID, b.cfg.ServerID, cmd)
		if err != nil {
			fmt.Printf("Failed to create command %s: %v\n", cmd.Name, err)
			continue
		}
		b.slashCommands = append(b.slashCommands, created)
	}
}

func (b *Bot) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	// Check authorization
	if !b.isAuthorized(i.Member.User.Username) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You are not authorized to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	data := i.ApplicationCommandData()
	if data.Name != "term" {
		return
	}

	if len(data.Options) == 0 {
		return
	}

	subCmd := data.Options[0]
	handler := NewCommandHandler(b, s, i)

	switch subCmd.Name {
	case "list":
		handler.HandleList()
	case "screenshot":
		handler.HandleScreenshot(subCmd.Options)
	case "run":
		handler.HandleRun(subCmd.Options)
	case "connect":
		handler.HandleConnect(subCmd.Options)
	case "disconnect":
		handler.HandleDisconnect()
	case "focus":
		handler.HandleFocus(subCmd.Options)
	case "close":
		handler.HandleClose(subCmd.Options)
	}
}

func (b *Bot) isAuthorized(username string) bool {
	for _, u := range b.cfg.AuthorizedUsers {
		if u == username {
			return true
		}
	}
	return false
}

// App returns the GUI application
func (b *Bot) App() *gui.App {
	return b.app
}

// Session returns the Discord session
func (b *Bot) Session() *discordgo.Session {
	return b.session
}

// ChannelID returns the configured channel ID
func (b *Bot) ChannelID() string {
	return b.cfg.ChannelID
}

// SetStreamer sets the active streamer
func (b *Bot) SetStreamer(s *Streamer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.streamer = s
}

// GetStreamer returns the active streamer
func (b *Bot) GetStreamer() *Streamer {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.streamer
}

// SetToken stores the Discord bot token in the keyring
func SetToken(token string) error {
	return keyring.Set(serviceName, tokenKey, token)
}

// GetToken retrieves the Discord bot token from the keyring
func GetToken() (string, error) {
	return keyring.Get(serviceName, tokenKey)
}
