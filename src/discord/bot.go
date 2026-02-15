package discord

import (
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/bwmarrin/discordgo"
	"github.com/zalando/go-keyring"

	"claude-term/src/config"
	"claude-term/src/gui"
	"claude-term/src/tmux"
)

const (
	// Reconnect backoff settings
	minReconnectDelay = 1 * time.Second
	maxReconnectDelay = 10 * time.Minute

	defaultCategoryName = "claude sessions"
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

// Bot manages the Discord connection.
type Bot struct {
	session *discordgo.Session
	cfg     *config.DiscordConfig
	app     *gui.App

	mu              sync.RWMutex
	isConnected     bool
	slashCommands   []*discordgo.ApplicationCommand
	stopReconnect   chan struct{}
	reconnectDelay  time.Duration
	shouldReconnect bool

	categoryID      string
	sessionChannels map[string]string // session name -> discord channel ID
	channelSessions map[string]string // discord channel ID -> session name
	streamers       map[string]*Streamer
}

// NewBot creates a new Discord bot.
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
		sessionChannels: make(map[string]string),
		channelSessions: make(map[string]string),
		streamers:       make(map[string]*Streamer),
	}

	if cfg.CategoryID != "" {
		bot.categoryID = cfg.CategoryID
	}

	// Set required intents for receiving interactions and channel messages.
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentMessageContent

	// Set up handlers
	session.AddHandler(bot.handleReady)
	session.AddHandler(bot.handleInteraction)
	session.AddHandler(bot.handleDisconnect)
	session.AddHandler(bot.handleMessageCreate)

	return bot, nil
}

// Connect connects to Discord.
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

// Disconnect disconnects from Discord and stops reconnection attempts.
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

	for _, streamer := range b.streamers {
		streamer.Stop()
	}
	b.streamers = make(map[string]*Streamer)

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

	go b.syncSessionChannelsAndStreams()
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

func (b *Bot) handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Author == nil {
		return
	}
	if m.Author.Bot {
		return
	}
	if m.GuildID != b.cfg.ServerID {
		return
	}
	if !b.isAuthorized(m.Author.ID, m.Author.Username) {
		return
	}

	sessionName := b.sessionNameForChannel(m.ChannelID)
	if sessionName == "" {
		return
	}
	if m.Content == "" {
		return
	}

	state := b.app.GetSession(sessionName)
	if state == nil {
		_, _ = s.ChannelMessageSend(m.ChannelID, "Session no longer exists.")
		return
	}

	if err := b.SendSessionInput(sessionName, m.Content); err != nil {
		discordLog.Printf("Failed writing Discord message to PTY (%s): %v", sessionName, err)
		_, _ = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Failed to send input: %v", err))
	}
}

func discordContentToInputLines(content string) []string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	if normalized == "" {
		return nil
	}

	lines := strings.Split(normalized, "\n")
	// Ignore trailing empty segment created by an ending newline so we don't
	// synthesize an extra blank command.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// reconnect attempts to reconnect with exponential backoff.
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
					Description: "Ensure a session is connected to its Discord channel",
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
					Description: "Report current always-on streaming mode",
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

	userID, username := interactionIdentity(i)
	discordLog.Printf("Interaction from user: %s (%s)", username, userID)

	// Check authorization
	if !b.isAuthorized(userID, username) {
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

func interactionIdentity(i *discordgo.InteractionCreate) (string, string) {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID, i.Member.User.Username
	}
	if i.User != nil {
		return i.User.ID, i.User.Username
	}
	return "", ""
}

func (b *Bot) isAuthorized(userID, username string) bool {
	for _, id := range b.cfg.AuthorizedUserIDs {
		if id == userID {
			return true
		}
	}
	for _, legacyUser := range b.cfg.AuthorizedUsers {
		if strings.EqualFold(legacyUser, username) {
			return true
		}
	}
	return false
}

func (b *Bot) sessionNameForChannel(channelID string) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.channelSessions[channelID]
}

func (b *Bot) ensureCategoryID() (string, error) {
	categoryName := strings.TrimSpace(b.cfg.CategoryName)
	if categoryName == "" {
		categoryName = defaultCategoryName
	}

	guildChannels, err := b.session.GuildChannels(b.cfg.ServerID)
	if err != nil {
		return "", err
	}

	b.mu.RLock()
	currentCategoryID := b.categoryID
	b.mu.RUnlock()

	if currentCategoryID != "" {
		for _, ch := range guildChannels {
			if ch.ID == currentCategoryID && ch.Type == discordgo.ChannelTypeGuildCategory {
				return currentCategoryID, nil
			}
		}
	}

	if b.cfg.CategoryID != "" {
		for _, ch := range guildChannels {
			if ch.ID == b.cfg.CategoryID && ch.Type == discordgo.ChannelTypeGuildCategory {
				b.mu.Lock()
				b.categoryID = ch.ID
				b.mu.Unlock()
				return ch.ID, nil
			}
		}
	}

	for _, ch := range guildChannels {
		if ch.Type != discordgo.ChannelTypeGuildCategory {
			continue
		}
		if strings.EqualFold(ch.Name, categoryName) {
			b.mu.Lock()
			b.categoryID = ch.ID
			b.mu.Unlock()
			return ch.ID, nil
		}
	}

	created, err := b.session.GuildChannelCreateComplex(b.cfg.ServerID, discordgo.GuildChannelCreateData{
		Name: categoryName,
		Type: discordgo.ChannelTypeGuildCategory,
	})
	if err != nil {
		return "", err
	}

	b.mu.Lock()
	b.categoryID = created.ID
	b.mu.Unlock()
	b.cfg.CategoryID = created.ID
	return created.ID, nil
}

func (b *Bot) syncSessionChannelsAndStreams() {
	categoryID, err := b.ensureCategoryID()
	if err != nil {
		discordLog.Printf("Failed to ensure category: %v", err)
		return
	}

	sessionNames := b.app.ListSessions()
	activeSessions := make(map[string]struct{}, len(sessionNames))
	for _, name := range sessionNames {
		activeSessions[name] = struct{}{}
		channelID, ensureErr := b.ensureSessionChannel(name, categoryID)
		if ensureErr != nil {
			discordLog.Printf("Failed to ensure channel for session %q: %v", name, ensureErr)
			continue
		}
		b.ensureStreamer(name, channelID)
	}

	b.cleanupRemovedSessions(activeSessions)
	b.cleanupOrphanChannels(activeSessions, categoryID)
}

func (b *Bot) cleanupRemovedSessions(activeSessions map[string]struct{}) {
	b.mu.Lock()
	deletes := make([]string, 0)
	for sessionName := range b.sessionChannels {
		if _, ok := activeSessions[sessionName]; !ok {
			deletes = append(deletes, sessionName)
		}
	}
	b.mu.Unlock()

	for _, sessionName := range deletes {
		b.SessionClosed(sessionName)
	}
}

func (b *Bot) cleanupOrphanChannels(activeSessions map[string]struct{}, categoryID string) {
	guildChannels, err := b.session.GuildChannels(b.cfg.ServerID)
	if err != nil {
		discordLog.Printf("Failed to list guild channels for orphan cleanup: %v", err)
		return
	}

	for _, ch := range guildChannels {
		if ch.Type != discordgo.ChannelTypeGuildText || ch.ParentID != categoryID {
			continue
		}

		sessionName := b.sessionNameForChannel(ch.ID)
		if sessionName != "" {
			if _, ok := activeSessions[sessionName]; ok {
				continue
			}
		}

		if _, err := b.session.ChannelDelete(ch.ID); err != nil {
			discordLog.Printf("Failed deleting orphan channel %q (%s): %v", ch.Name, ch.ID, err)
			continue
		}
		discordLog.Printf("Deleted orphan channel %q (%s)", ch.Name, ch.ID)
	}
}

func (b *Bot) ensureSessionChannel(sessionName, categoryID string) (string, error) {
	guildChannels, err := b.session.GuildChannels(b.cfg.ServerID)
	if err != nil {
		return "", err
	}

	channelName := channelNameForSession(sessionName)

	b.mu.RLock()
	existingID := b.sessionChannels[sessionName]
	b.mu.RUnlock()

	if existingID != "" {
		for _, ch := range guildChannels {
			if ch.ID != existingID {
				continue
			}
			if ch.ParentID != categoryID || ch.Name != channelName {
				_, editErr := b.session.ChannelEditComplex(ch.ID, &discordgo.ChannelEdit{
					Name:     channelName,
					ParentID: categoryID,
				})
				if editErr != nil {
					return "", editErr
				}
			}
			b.setSessionChannel(sessionName, ch.ID)
			return ch.ID, nil
		}
	}

	// Reuse channel if one with the same expected name already exists in category.
	for _, ch := range guildChannels {
		if ch.Type == discordgo.ChannelTypeGuildText && ch.ParentID == categoryID && ch.Name == channelName {
			b.setSessionChannel(sessionName, ch.ID)
			return ch.ID, nil
		}
	}

	usedNames := make(map[string]struct{})
	for _, ch := range guildChannels {
		if ch.Type == discordgo.ChannelTypeGuildText && ch.ParentID == categoryID {
			usedNames[ch.Name] = struct{}{}
		}
	}
	if _, exists := usedNames[channelName]; exists {
		channelName = uniqueChannelName(channelName, sessionName, usedNames)
	}

	created, err := b.session.GuildChannelCreateComplex(b.cfg.ServerID, discordgo.GuildChannelCreateData{
		Name:     channelName,
		Type:     discordgo.ChannelTypeGuildText,
		ParentID: categoryID,
	})
	if err != nil {
		return "", err
	}

	b.setSessionChannel(sessionName, created.ID)
	return created.ID, nil
}

func (b *Bot) ensureStreamer(sessionName, channelID string) {
	state := b.app.GetSession(sessionName)
	if state == nil {
		return
	}

	b.mu.Lock()
	existing := b.streamers[sessionName]
	if existing != nil {
		existing.SetChannelID(channelID)
		b.mu.Unlock()
		return
	}

	streamer := NewStreamer(b, state, channelID)
	b.streamers[sessionName] = streamer
	b.mu.Unlock()

	streamer.Start()
}

func (b *Bot) setSessionChannel(sessionName, channelID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessionChannels[sessionName] = channelID
	b.channelSessions[channelID] = sessionName
}

func (b *Bot) removeSessionChannel(sessionName string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	channelID := b.sessionChannels[sessionName]
	if channelID != "" {
		delete(b.channelSessions, channelID)
	}
	delete(b.sessionChannels, sessionName)
	return channelID
}

func (b *Bot) replaceSessionName(oldName, newName string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	channelID := b.sessionChannels[oldName]
	delete(b.sessionChannels, oldName)
	if channelID != "" {
		b.sessionChannels[newName] = channelID
		b.channelSessions[channelID] = newName
	}
	if streamer := b.streamers[oldName]; streamer != nil {
		delete(b.streamers, oldName)
		b.streamers[newName] = streamer
	}
	return channelID
}

func (b *Bot) removeStreamer(sessionName string) {
	b.mu.Lock()
	streamer := b.streamers[sessionName]
	delete(b.streamers, sessionName)
	b.mu.Unlock()
	if streamer != nil {
		streamer.Stop()
	}
}

// SessionAdded handles app session creation events.
func (b *Bot) SessionAdded(name string) {
	go func() {
		categoryID, err := b.ensureCategoryID()
		if err != nil {
			discordLog.Printf("Failed to ensure category for session %q: %v", name, err)
			return
		}
		channelID, err := b.ensureSessionChannel(name, categoryID)
		if err != nil {
			discordLog.Printf("Failed to ensure channel for session %q: %v", name, err)
			return
		}
		b.ensureStreamer(name, channelID)
	}()
}

// SessionRenamed handles app session rename events.
func (b *Bot) SessionRenamed(oldName, newName string) {
	go func() {
		categoryID, err := b.ensureCategoryID()
		if err != nil {
			discordLog.Printf("Failed to ensure category for rename %q->%q: %v", oldName, newName, err)
			return
		}

		channelID := b.replaceSessionName(oldName, newName)
		if channelID == "" {
			channelID, err = b.ensureSessionChannel(newName, categoryID)
			if err != nil {
				discordLog.Printf("Failed to ensure channel after rename %q->%q: %v", oldName, newName, err)
				return
			}
			b.ensureStreamer(newName, channelID)
			return
		}

		newChannelName := channelNameForSession(newName)
		if _, err := b.session.ChannelEditComplex(channelID, &discordgo.ChannelEdit{
			Name:     newChannelName,
			ParentID: categoryID,
		}); err != nil {
			discordLog.Printf("Failed to rename Discord channel for %q->%q: %v", oldName, newName, err)
		}

		b.ensureStreamer(newName, channelID)
	}()
}

// SessionClosed handles app session close events.
func (b *Bot) SessionClosed(name string) {
	go func() {
		b.removeStreamer(name)
		channelID := b.removeSessionChannel(name)
		if channelID == "" {
			// Handle startup or map-reset cases by searching by expected name.
			channelID = b.findSessionChannelIDByName(name)
		}
		if channelID == "" {
			return
		}
		if _, err := b.session.ChannelDelete(channelID); err != nil {
			discordLog.Printf("Failed deleting session channel for %q: %v", name, err)
		}
	}()
}

func (b *Bot) findSessionChannelIDByName(sessionName string) string {
	categoryID, err := b.ensureCategoryID()
	if err != nil {
		return ""
	}
	channels, err := b.session.GuildChannels(b.cfg.ServerID)
	if err != nil {
		return ""
	}
	target := channelNameForSession(sessionName)
	for _, ch := range channels {
		if ch.Type == discordgo.ChannelTypeGuildText && ch.ParentID == categoryID && ch.Name == target {
			return ch.ID
		}
	}
	return ""
}

func channelNameForSession(sessionName string) string {
	name := strings.ToLower(strings.TrimSpace(sessionName))
	if name == "" {
		return "session"
	}

	var b strings.Builder
	b.Grow(len(name))
	lastDash := false
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}

	result := strings.Trim(b.String(), "-")
	if result == "" {
		result = "session"
	}
	if len(result) > 90 {
		result = strings.Trim(result[:90], "-")
		if result == "" {
			result = "session"
		}
	}
	return result
}

func uniqueChannelName(base, sessionName string, used map[string]struct{}) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sessionName))
	suffix := fmt.Sprintf("-%x", h.Sum32())
	maxBase := 100 - len(suffix)
	if maxBase < 1 {
		maxBase = 1
	}
	if len(base) > maxBase {
		base = strings.Trim(base[:maxBase], "-")
		if base == "" {
			base = "session"
		}
	}
	candidate := base + suffix
	if _, exists := used[candidate]; !exists {
		return candidate
	}
	for i := 2; ; i++ {
		altSuffix := fmt.Sprintf("-%d", i)
		maxAltBase := 100 - len(altSuffix)
		altBase := base
		if len(altBase) > maxAltBase {
			altBase = strings.Trim(altBase[:maxAltBase], "-")
			if altBase == "" {
				altBase = "session"
			}
		}
		candidate = altBase + altSuffix
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func (b *Bot) channelForSession(sessionName string) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.sessionChannels[sessionName]
}

func (b *Bot) sendLines(channelID string, lines []string) error {
	if channelID == "" || len(lines) == 0 {
		return nil
	}
	chunks := formatDiscordCodeBlocks(lines)
	for _, content := range chunks {
		if _, err := b.session.ChannelMessageSend(channelID, content); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bot) sendWorking(channelID string) error {
	if channelID == "" {
		return nil
	}
	_, err := b.session.ChannelMessageSend(channelID, "working...")
	return err
}

// SendSessionInput sends message content to a session as literal key presses
// with an Enter key at the end of each line.
func (b *Bot) SendSessionInput(sessionName, content string) error {
	lines := discordContentToInputLines(content)
	if len(lines) == 0 {
		return nil
	}

	for _, line := range lines {
		keyArgs := lineToKeyArgs(line)
		if len(keyArgs) > 0 {
			if err := tmux.SendKeys(sessionName, keyArgs...); err != nil {
				return err
			}
		}
		if err := tmux.SendKeys(sessionName, "Enter"); err != nil {
			return err
		}
	}

	return nil
}

func lineToKeyArgs(line string) []string {
	if line == "" {
		return nil
	}

	keys := make([]string, 0, len([]rune(line)))
	for _, r := range line {
		switch r {
		case ' ':
			keys = append(keys, "Space")
		case '\t':
			keys = append(keys, "Tab")
		default:
			keys = append(keys, string(r))
		}
	}
	return keys
}

func formatDiscordCodeBlocks(lines []string) []string {
	const maxContent = 1900
	chunks := make([]string, 0)
	var sb strings.Builder

	flush := func() {
		if sb.Len() == 0 {
			return
		}
		chunks = append(chunks, "```text\n"+sb.String()+"\n```")
		sb.Reset()
	}

	for _, line := range lines {
		remaining := line
		if remaining == "" {
			if sb.Len()+1 > maxContent {
				flush()
			}
			sb.WriteByte('\n')
			continue
		}

		for len(remaining) > 0 {
			room := maxContent - sb.Len()
			if room <= 0 {
				flush()
				room = maxContent
			}

			if len(remaining) <= room {
				sb.WriteString(remaining)
				remaining = ""
				break
			}

			sb.WriteString(remaining[:room])
			remaining = remaining[room:]
			flush()
		}

		if sb.Len()+1 > maxContent {
			flush()
		}
		sb.WriteByte('\n')
	}

	flush()
	if len(chunks) == 0 {
		return []string{"```text\n\n```"}
	}
	return chunks
}

// App returns the GUI application.
func (b *Bot) App() *gui.App {
	return b.app
}

// Session returns the Discord session.
func (b *Bot) Session() *discordgo.Session {
	return b.session
}

// EnsureSessionStream ensures a session has a Discord channel + streamer and returns channel ID.
func (b *Bot) EnsureSessionStream(sessionName string) (string, error) {
	categoryID, err := b.ensureCategoryID()
	if err != nil {
		return "", err
	}
	channelID, err := b.ensureSessionChannel(sessionName, categoryID)
	if err != nil {
		return "", err
	}
	b.ensureStreamer(sessionName, channelID)
	return channelID, nil
}

// SetToken stores the Discord bot token in the keyring.
func SetToken(token string) error {
	return keyring.Set(serviceName, tokenKey, token)
}

// GetToken retrieves the Discord bot token from the keyring.
func GetToken() (string, error) {
	return keyring.Get(serviceName, tokenKey)
}

// IsConnected returns whether the bot is connected to Discord.
func (b *Bot) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.isConnected
}
