package config

import (
	"os"
	"path/filepath"
	"testing"
)

// setTempHome points HOME at a temp dir so all config paths are isolated.
func setTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestLoadReturnsDefaults(t *testing.T) {
	setTempHome(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider != "anthropic" {
		t.Errorf("default provider: want anthropic, got %q", cfg.Provider)
	}
	if cfg.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("default model: want claude-haiku-4-5-20251001, got %q", cfg.Model)
	}
	if cfg.MaxHistory != 500 {
		t.Errorf("default max_history: want 500, got %d", cfg.MaxHistory)
	}
	if cfg.AnthropicAPIKey != "" {
		t.Errorf("default anthropic_api_key should be empty, got %q", cfg.AnthropicAPIKey)
	}
	if cfg.GroqAPIKey != "" {
		t.Errorf("default groq_api_key should be empty, got %q", cfg.GroqAPIKey)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	setTempHome(t)

	want := &Config{
		Provider:        "groq",
		Model:           "llama3-70b",
		MaxHistory:      200,
		AnthropicAPIKey: "sk-ant-test",
		GroqAPIKey:      "gsk-test",
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if got.Provider != want.Provider {
		t.Errorf("Provider: want %q got %q", want.Provider, got.Provider)
	}
	if got.Model != want.Model {
		t.Errorf("Model: want %q got %q", want.Model, got.Model)
	}
	if got.MaxHistory != want.MaxHistory {
		t.Errorf("MaxHistory: want %d got %d", want.MaxHistory, got.MaxHistory)
	}
	if got.AnthropicAPIKey != want.AnthropicAPIKey {
		t.Errorf("AnthropicAPIKey: want %q got %q", want.AnthropicAPIKey, got.AnthropicAPIKey)
	}
	if got.GroqAPIKey != want.GroqAPIKey {
		t.Errorf("GroqAPIKey: want %q got %q", want.GroqAPIKey, got.GroqAPIKey)
	}
}

func TestSaveCreatesConfigDir(t *testing.T) {
	dir := setTempHome(t)

	cfg := &Config{Provider: "anthropic", Model: "test", MaxHistory: 10}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfgPath := filepath.Join(dir, ".config", "aka", "config.toml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Errorf("config file not created at %s", cfgPath)
	}
}
