// Package config manages AKA configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/99designs/keyring"
	"github.com/spf13/viper"
)

const (
	keyringService = "aka"
	keyringUser    = "anthropic-api-key"
)

// Config holds all AKA configuration options.
type Config struct {
	Model        string `toml:"model" mapstructure:"model"`
	APIKeyMethod string `toml:"api_key_method" mapstructure:"api_key_method"` // "keyring" | "env"
	DryRun       bool   `toml:"dry_run" mapstructure:"dry_run"`
	MaxHistory   int    `toml:"max_history" mapstructure:"max_history"`
}

// configFilePath returns the path to the config file.
func configFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "aka")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// Load reads ~/.config/aka/config.toml and returns the config with defaults.
func Load() (*Config, error) {
	path, err := configFilePath()
	if err != nil {
		return nil, err
	}

	v := viper.New()
	v.SetDefault("model", "claude-haiku-4-5-20251001")
	v.SetDefault("api_key_method", "keyring")
	v.SetDefault("dry_run", false)
	v.SetDefault("max_history", 500)

	v.SetConfigFile(path)
	v.SetConfigType("toml")

	if err := v.ReadInConfig(); err != nil {
		// File may not exist yet; return defaults.
		if os.IsNotExist(err) {
			cfg := &Config{}
			if err := v.Unmarshal(cfg); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return cfg, nil
}

// Save writes the config to ~/.config/aka/config.toml.
func Save(cfg *Config) error {
	path, err := configFilePath()
	if err != nil {
		return err
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("toml")
	v.Set("model", cfg.Model)
	v.Set("api_key_method", cfg.APIKeyMethod)
	v.Set("dry_run", cfg.DryRun)
	v.Set("max_history", cfg.MaxHistory)

	return v.WriteConfigAs(path)
}

// GetAPIKey tries AKA_API_KEY then ANTHROPIC_API_KEY env vars first, then the OS keyring.
func GetAPIKey() (string, error) {
	// Env var takes priority — avoids touching the keychain when it's set.
	for _, envVar := range []string{"AKA_API_KEY", "ANTHROPIC_API_KEY"} {
		if key := os.Getenv(envVar); key != "" {
			return key, nil
		}
	}

	// Fall back to keyring.
	ring, err := openKeyring()
	if err == nil {
		item, err := ring.Get(keyringUser)
		if err == nil && string(item.Data) != "" {
			return string(item.Data), nil
		}
	}

	return "", fmt.Errorf("no API key found: set AKA_API_KEY or run `aka config set-key`")
}

// SetAPIKey stores the API key in the OS keyring.
func SetAPIKey(key string) error {
	ring, err := openKeyring()
	if err != nil {
		return fmt.Errorf("open keyring: %w", err)
	}
	return ring.Set(keyring.Item{
		Key:         keyringUser,
		Data:        []byte(key),
		Label:       "AKA – Anthropic API Key",
		Description: "Anthropic API key used by the aka CLI",
	})
}

// openKeyring opens the OS keyring with the AKA service name.
func openKeyring() (keyring.Keyring, error) {
	return keyring.Open(keyring.Config{
		ServiceName: keyringService,
	})
}
