package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	expect "github.com/Netflix/go-expect"
)

func TestScanCommand_NetworkFailure(t *testing.T) {
	env, homeDir := setupTestEnv(t)

	// Create config.toml with dummy API key
	configDir := filepath.Join(homeDir, ".config", "aka")
	err := os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	err = os.MkdirAll(filepath.Join(configDir, "zsh"), 0755)
	if err != nil {
		t.Fatalf("failed to create zsh dir: %v", err)
	}
	os.WriteFile(filepath.Join(configDir, "zsh", "aliases.sh"), []byte(""), 0644)
	os.WriteFile(filepath.Join(configDir, "zsh", "completion.sh"), []byte(""), 0644)
	os.WriteFile(filepath.Join(homeDir, ".zsh_history"), []byte("ls -la\necho hello\n"), 0644)

	configContent := `provider = "anthropic"
anthropic_api_key = "dummy-key-that-will-fail"
`
	err = os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	c, err := expect.NewConsole(expect.WithStdout(os.Stdout))
	if err != nil {
		t.Fatalf("failed to create console: %v", err)
	}
	defer c.Close()

	cmd := exec.Command(binPath, "scan")
	cmd.Env = env
	cmd.Stdin = c.Tty()
	cmd.Stdout = c.Tty()
	cmd.Stderr = c.Tty()

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start cmd: %v", err)
	}

	// Expect the prompt and send enter (carriage return for bubbletea)
	c.ExpectString("How much history")
	time.Sleep(1 * time.Second)
	c.Send("\r")

	// Second confirmation prompt
	c.ExpectString("Send 2 commands to the LLM?")
	time.Sleep(1 * time.Second)
	c.Send("\r")

	// Wait for the failure message
	// Output should contain something about invalid API key or network failure
	out, err := c.Expect(expect.EOF, expect.WithTimeout(5*time.Second))
	
	if err == nil {
		t.Errorf("expected timeout or EOF from cmd, got none")
	}

	// We expect the command to fail and output an error message about the API request failing
	if !containsFailureMessage(out) {
		t.Errorf("output did not contain expected failure message, got:\n%s", out)
	}

	err = cmd.Wait()
	if err == nil {
		t.Errorf("expected cmd to fail, but it succeeded")
	}
}

func containsFailureMessage(out string) bool {
	// The exact message depends on the API client, but it should mention one of these
	keywords := []string{"invalid x-api-key", "authentication", "unauthorized", "invalid", "fail", "error"}
	outLower := ""
	// Convert out to lower case
	for _, c := range out {
		if c >= 'A' && c <= 'Z' {
			outLower += string(c + 32)
		} else {
			outLower += string(c)
		}
	}

	for _, k := range keywords {
		if contains(outLower, k) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
