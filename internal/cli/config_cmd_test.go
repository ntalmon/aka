package cli

import (
	"testing"

	"github.com/ntalmon/aka/internal/config"
)

func TestSetMaxHistoryCmdSavesConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	want := &config.Config{
		Provider:        "groq",
		Model:           "llama-3.3-70b-versatile",
		MaxHistory:      500,
		AnthropicAPIKey: "sk-ant-test",
		GroqAPIKey:      "gsk-test",
	}
	if err := config.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := NewConfigCmd()
	cmd.SetArgs([]string{"set-max-history", "1200"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.MaxHistory != 1200 {
		t.Fatalf("MaxHistory: want 1200 got %d", got.MaxHistory)
	}
	if got.Provider != want.Provider || got.Model != want.Model {
		t.Fatalf("config fields were not preserved: got %#v", got)
	}
	if got.AnthropicAPIKey != want.AnthropicAPIKey || got.GroqAPIKey != want.GroqAPIKey {
		t.Fatalf("API keys were not preserved: got %#v", got)
	}
}

func TestSetMaxHistoryCmdRejectsInvalidValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cmd := NewConfigCmd()
	cmd.SetArgs([]string{"set-max-history", "0"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute returned nil error for invalid max_history")
	}
}
