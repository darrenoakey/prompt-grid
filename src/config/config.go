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

// DefaultConfigPath returns the default config file path
func DefaultConfigPath() string {
	// Look in local/ directory relative to executable
	exe, err := os.Executable()
	if err != nil {
		return "local/config.json"
	}
	return filepath.Join(filepath.Dir(exe), "local", "config.json")
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
