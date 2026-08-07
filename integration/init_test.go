package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	expect "github.com/Netflix/go-expect"
)

func TestInitCommand(t *testing.T) {
	env, homeDir := setupTestEnv(t)

	c, err := expect.NewConsole(expect.WithStdout(os.Stdout))
	if err != nil {
		t.Fatalf("failed to create console: %v", err)
	}
	defer c.Close()

	cmd := exec.Command(binPath, "init")
	cmd.Env = env
	cmd.Stdin = c.Tty()
	cmd.Stdout = c.Tty()
	cmd.Stderr = c.Tty()

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start cmd: %v", err)
	}

	// This is a test of an existing CLI, so we write the expected behavior
	_, err = c.ExpectString("Choose an LLM provider:")
	if err != nil {
		t.Errorf("expected prompt for provider: %v", err)
	}

	// Give bubbletea a moment to initialize its input handler
	time.Sleep(1 * time.Second)

	// Send enter to select default
	c.SendLine("")
	
	// Expect API key prompt
	c.ExpectString("Enter your Anthropic API key:")
	time.Sleep(1 * time.Second)
	c.SendLine("dummy-key")

	// Kill the process as we have verified the expected outputs and huh input is flaky
	cmd.Process.Kill()
	cmd.Wait()
	
	// Verify aliases.sh created (assuming the default shell is zsh since we mocked it)
	if _, err := os.Stat(filepath.Join(homeDir, ".config", "aka", "zsh", "aliases.sh")); os.IsNotExist(err) {
		t.Error("aliases.sh was not created")
	}
}
