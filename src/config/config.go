package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds application configuration
type Config struct {
	Discord       DiscordConfig  `json:"discord"`
	SessionColors map[string]int `json:"session_colors,omitempty"`
	WindowSizes   map[string][2]int `json:"window_sizes,omitempty"`
}

// DiscordConfig holds Discord-specific configuration
type DiscordConfig struct {
	ChannelID       string   `json:"channel_id"`
	ServerID        string   `json:"server_id"`
	AuthorizedUsers []string `json:"authorized_users"`
}

// configSearchPaths returns paths to search for config, in order of priority
func configSearchPaths() []string {
	paths := []string{}

	// First: ~/.config/claude-term/config.json (standard user config location)
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "claude-term", "config.json"))
	}

	// Second: local/ directory relative to executable (for development)
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "local", "config.json"))
	}

	// Third: current directory
	paths = append(paths, "local/config.json")

	return paths
}

// DefaultConfigPath returns the first config path that exists, or the preferred path if none exist
func DefaultConfigPath() string {
	paths := configSearchPaths()

	// Return first path that exists
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// No config exists, return preferred location for creation
	if len(paths) > 0 {
		return paths[0]
	}
	return "local/config.json"
}

// Load loads configuration from a file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	cfg.ensureSessionColors()
	return &cfg, nil
}

// ensureSessionColors initializes the SessionColors map if nil
func (c *Config) ensureSessionColors() {
	if c.SessionColors == nil {
		c.SessionColors = make(map[string]int)
	}
}

// GetSessionColorIndex returns the saved palette index for a session name
func (c *Config) GetSessionColorIndex(name string) (int, bool) {
	c.ensureSessionColors()
	idx, ok := c.SessionColors[name]
	return idx, ok
}

// SetSessionColorIndex sets the palette index for a session name
func (c *Config) SetSessionColorIndex(name string, index int) {
	c.ensureSessionColors()
	c.SessionColors[name] = index
}

// DeleteSessionColor removes the color mapping for a session
func (c *Config) DeleteSessionColor(name string) {
	c.ensureSessionColors()
	delete(c.SessionColors, name)
}

// RenameSessionColor moves a color mapping from oldName to newName
func (c *Config) RenameSessionColor(oldName, newName string) {
	c.ensureSessionColors()
	if idx, ok := c.SessionColors[oldName]; ok {
		c.SessionColors[newName] = idx
		delete(c.SessionColors, oldName)
	}
}

// ensureWindowSizes initializes the WindowSizes map if nil
func (c *Config) ensureWindowSizes() {
	if c.WindowSizes == nil {
		c.WindowSizes = make(map[string][2]int)
	}
}

// GetWindowSize returns the saved window size [width, height] in Dp for a session name
func (c *Config) GetWindowSize(name string) ([2]int, bool) {
	c.ensureWindowSizes()
	size, ok := c.WindowSizes[name]
	return size, ok
}

// SetWindowSize saves the window size [width, height] in Dp for a session name
func (c *Config) SetWindowSize(name string, w, h int) {
	c.ensureWindowSizes()
	c.WindowSizes[name] = [2]int{w, h}
}

// DeleteWindowSize removes the saved window size for a session
func (c *Config) DeleteWindowSize(name string) {
	c.ensureWindowSizes()
	delete(c.WindowSizes, name)
}

// RenameWindowSize moves a window size mapping from oldName to newName
func (c *Config) RenameWindowSize(oldName, newName string) {
	c.ensureWindowSizes()
	if size, ok := c.WindowSizes[oldName]; ok {
		c.WindowSizes[newName] = size
		delete(c.WindowSizes, oldName)
	}
}

// LoadDefault loads configuration from the default path
func LoadDefault() (*Config, error) {
	return Load(DefaultConfigPath())
}

// Save writes configuration to a file
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "    ")
	if err != nil {
		return err
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
