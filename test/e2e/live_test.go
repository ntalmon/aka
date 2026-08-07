//go:build e2e

package e2e

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// validNameRE mirrors the pattern in internal/apply/apply.go. Duplicated rather
// than imported so this file asserts against the documented contract, not the
// implementation.
var validNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,31}$`)

// The live tier talks to the real provider, so it asserts loosely: schema
// validity and name safety, never exact content. Run it to detect provider
// drift; it is skipped by default and in CI.
//
//	AKA_E2E_LIVE=1 ANTHROPIC_API_KEY=sk-... make e2e
func TestLiveProviderReturnsUsableSuggestions(t *testing.T) {
	if os.Getenv("AKA_E2E_LIVE") != "1" {
		t.Skip("set AKA_E2E_LIVE=1 to run the live provider tier")
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY is required for the live tier")
	}

	env := NewEnv(t, "zsh")
	// Talk to the real endpoint: drop the loopback override the harness sets.
	env.ClearLLMOverride()
	env.SeedRC(existingRC)
	env.SeedConfig(ConfigTOML{
		Provider: "anthropic",
		Model:    "claude-haiku-4-5-20251001",
		APIKey:   apiKey,
	})
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory(realisticHistory()...)

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Expect("space toggle")
	c.Send(Space)
	for i := 0; i < 20; i++ {
		c.Send(Down)
	}
	c.ExpectRe(`Apply \d+ suggestion\(s\) & exit`)
	c.Send(Enter)
	c.ExpectRe(`Applied \d+ alias\(es\)/function\(s\)!|No suggestions accepted\.`)
	_ = c.Wait()

	for _, entry := range env.Installed() {
		if !validNameRE.MatchString(entry.Name) {
			t.Errorf("live provider produced an unsafe name %q that was installed", entry.Name)
		}
		if strings.TrimSpace(entry.Template) == "" {
			t.Errorf("entry %q has an empty template", entry.Name)
		}
	}
}
