package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds application configuration
type Config struct {
	Discord DiscordConfig `json:"discord"`
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

	return &cfg, nil
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
