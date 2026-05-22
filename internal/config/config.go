// Package config manages AKA configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds all AKA configuration options.
type Config struct {
	Provider        string `toml:"provider" mapstructure:"provider"` // "anthropic" | "groq" | "openai" | "gemini" | "ollama"
	Model           string `toml:"model" mapstructure:"model"`
	DryRun          bool   `toml:"dry_run" mapstructure:"dry_run"`
	MaxHistory      int    `toml:"max_history" mapstructure:"max_history"`
	AnthropicAPIKey string `toml:"anthropic_api_key" mapstructure:"anthropic_api_key"`
	GroqAPIKey      string `toml:"groq_api_key" mapstructure:"groq_api_key"`
	OpenAIAPIKey    string `toml:"openai_api_key" mapstructure:"openai_api_key"`
	GeminiAPIKey    string `toml:"gemini_api_key" mapstructure:"gemini_api_key"`
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
	v.SetDefault("provider", "anthropic")
	v.SetDefault("model", "claude-haiku-4-5-20251001")
	v.SetDefault("dry_run", false)
	v.SetDefault("max_history", 500)

	v.SetConfigFile(path)
	v.SetConfigType("toml")

	if err := v.ReadInConfig(); err != nil {
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
	v.Set("provider", cfg.Provider)
	v.Set("model", cfg.Model)
	v.Set("dry_run", cfg.DryRun)
	v.Set("max_history", cfg.MaxHistory)
	v.Set("anthropic_api_key", cfg.AnthropicAPIKey)
	v.Set("groq_api_key", cfg.GroqAPIKey)
	v.Set("openai_api_key", cfg.OpenAIAPIKey)
	v.Set("gemini_api_key", cfg.GeminiAPIKey)

	return v.WriteConfigAs(path)
}
