package discord

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/zalando/go-keyring"

	"claude-term/src/config"
	"claude-term/src/gui"
)

const (
	// Reconnect backoff settings
	minReconnectDelay = 1 * time.Second
	maxReconnectDelay = 10 * time.Minute
)

var discordLog *log.Logger

func init() {
	// Set up logging to file
	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, ".config", "claude-term", "discord.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		discordLog = log.New(os.Stderr, "[discord] ", log.LstdFlags)
	} else {
		discordLog = log.New(f, "", log.LstdFlags)
	}
}

const (
	serviceName = "claude-term"
	tokenKey    = "discord_bot_token"
)

// Bot manages the Discord connection
type Bot struct {
	session         *discordgo.Session
	cfg             *config.DiscordConfig
	app             *gui.App
	streamer        *Streamer
	mu              sync.RWMutex
	isConnected     bool
	slashCommands   []*discordgo.ApplicationCommand
	stopReconnect   chan struct{}
	reconnectDelay  time.Duration
	shouldReconnect bool
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
		session:         session,
		cfg:             cfg,
		app:             app,
		stopReconnect:   make(chan struct{}),
		reconnectDelay:  minReconnectDelay,
		shouldReconnect: true,
	}

	// Set required intents for receiving interactions
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages

	// Set up handlers
	session.AddHandler(bot.handleReady)
	session.AddHandler(bot.handleInteraction)
	session.AddHandler(bot.handleDisconnect)

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

// Disconnect disconnects from Discord and stops reconnection attempts
func (b *Bot) Disconnect() error {
	// Stop reconnection attempts first
	b.mu.Lock()
	b.shouldReconnect = false
	b.mu.Unlock()

	// Signal reconnect goroutine to stop
	select {
	case b.stopReconnect <- struct{}{}:
	default:
	}

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
	discordLog.Printf("Discord bot logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator)

	b.mu.Lock()
	b.isConnected = true
	b.reconnectDelay = minReconnectDelay // Reset backoff on successful connection
	b.mu.Unlock()

	// Register slash commands
	b.registerCommands()

	// Send startup notification
	b.sendStartupNotification()
}

func (b *Bot) handleDisconnect(s *discordgo.Session, d *discordgo.Disconnect) {
	b.mu.Lock()
	wasConnected := b.isConnected
	b.isConnected = false
	shouldReconnect := b.shouldReconnect
	b.mu.Unlock()

	discordLog.Printf("Discord disconnected (was connected: %v)", wasConnected)

	if shouldReconnect {
		go b.reconnect()
	}
}

// reconnect attempts to reconnect with exponential backoff
func (b *Bot) reconnect() {
	b.mu.Lock()
	delay := b.reconnectDelay
	b.mu.Unlock()

	for {
		select {
		case <-b.stopReconnect:
			discordLog.Printf("Reconnection cancelled")
			return
		case <-time.After(delay):
		}

		b.mu.RLock()
		shouldReconnect := b.shouldReconnect
		b.mu.RUnlock()

		if !shouldReconnect {
			return
		}

		discordLog.Printf("Attempting to reconnect (delay was %v)...", delay)

		if err := b.session.Open(); err != nil {
			discordLog.Printf("Reconnection failed: %v", err)

			// Increase backoff
			b.mu.Lock()
			b.reconnectDelay = b.reconnectDelay * 2
			if b.reconnectDelay > maxReconnectDelay {
				b.reconnectDelay = maxReconnectDelay
			}
			delay = b.reconnectDelay
			b.mu.Unlock()

			discordLog.Printf("Will retry in %v", delay)
			continue
		}

		discordLog.Printf("Reconnected successfully")
		return
	}
}

func (b *Bot) sendStartupNotification() {
	if b.cfg.ChannelID == "" {
		return
	}

	hostname, _ := os.Hostname()
	msg := fmt.Sprintf("**Claude-Term** started on `%s`\nUse `/term list` to see sessions, `/term help` for commands.", hostname)

	_, err := b.session.ChannelMessageSend(b.cfg.ChannelID, msg)
	if err != nil {
		fmt.Printf("Failed to send startup notification: %v\n", err)
	}
}

func (b *Bot) registerCommands() {
	discordLog.Printf("Registering slash commands for server: %s", b.cfg.ServerID)

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
				{
					Name:        "new",
					Description: "Create a new terminal session",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "name",
							Description: "Session name",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
						{
							Name:        "ssh",
							Description: "SSH host (optional, for remote sessions)",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    false,
						},
					},
				},
			},
		},
	}

	for _, cmd := range commands {
		created, err := b.session.ApplicationCommandCreate(b.session.State.User.ID, b.cfg.ServerID, cmd)
		if err != nil {
			discordLog.Printf("Failed to create command %s: %v", cmd.Name, err)
			continue
		}
		discordLog.Printf("Registered command: %s (ID: %s)", created.Name, created.ID)
		b.slashCommands = append(b.slashCommands, created)
	}
}

func (b *Bot) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	discordLog.Printf("Received interaction: type=%d", i.Type)

	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	// Get username from Member (guild) or User (DM)
	var username string
	if i.Member != nil && i.Member.User != nil {
		username = i.Member.User.Username
	} else if i.User != nil {
		username = i.User.Username
	}
	discordLog.Printf("Interaction from user: %s", username)

	// Check authorization
	if !b.isAuthorized(username) {
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
	discordLog.Printf("Command: %s", data.Name)
	if data.Name != "term" {
		return
	}

	if len(data.Options) == 0 {
		discordLog.Printf("No subcommand provided")
		return
	}

	subCmd := data.Options[0]
	discordLog.Printf("Subcommand: %s", subCmd.Name)
	handler := NewCommandHandler(b, s, i)

	switch subCmd.Name {
	case "list":
		discordLog.Printf("Handling list command")
		handler.HandleList()
	case "screenshot", "show":
		discordLog.Printf("Handling screenshot command")
		handler.HandleScreenshot(subCmd.Options)
	case "run":
		discordLog.Printf("Handling run command")
		handler.HandleRun(subCmd.Options)
	case "connect":
		discordLog.Printf("Handling connect command")
		handler.HandleConnect(subCmd.Options)
	case "disconnect":
		discordLog.Printf("Handling disconnect command")
		handler.HandleDisconnect()
	case "focus":
		discordLog.Printf("Handling focus command")
		handler.HandleFocus(subCmd.Options)
	case "close":
		discordLog.Printf("Handling close command")
		handler.HandleClose(subCmd.Options)
	case "new":
		discordLog.Printf("Handling new command")
		handler.HandleNew(subCmd.Options)
	default:
		discordLog.Printf("Unknown subcommand: %s", subCmd.Name)
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

// IsConnected returns whether the bot is connected to Discord
func (b *Bot) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.isConnected
}
