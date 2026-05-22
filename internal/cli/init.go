package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/aka-cli/internal/aliases"
	"github.com/ntalmon/aka/aka-cli/internal/ui"
)

// NewInitCmd creates the `aka init` subcommand.
func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize AKA: create aliases.sh and add source line to your shell rc",
		Long: `aka init creates ~/.config/aka/aliases.sh and appends a single source line
to your shell's rc file (.zshrc or .bashrc). This is idempotent — safe to run multiple times.

Fish shell is not supported in v0.1.`,
		RunE: runInit,
	}
}

func runInit(_ *cobra.Command, _ []string) error {
	shell, rcFile, err := detectShell()
	if err != nil {
		return err
	}

	if shell == "fish" {
		return fmt.Errorf("fish shell is not supported in v0.1 — please use bash or zsh")
	}

	fmt.Printf("Detected shell: %s\n", shell)
	fmt.Printf("RC file: %s\n", rcFile)

	if err := aliases.Init(rcFile); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	aliasesPath, err := aliases.AliasesFilePath()
	if err != nil {
		return err
	}

	// Ask whether to set up tab-completion.
	enableCompletion, err := ui.PromptEnableCompletion()
	if err != nil {
		return fmt.Errorf("completion prompt: %w", err)
	}

	var completionPath string
	if enableCompletion {
		if err := aliases.InitCompletion(shell, rcFile); err != nil {
			return fmt.Errorf("init completion: %w", err)
		}
		completionPath, err = aliases.CompletionFilePath()
		if err != nil {
			return err
		}
	}

	ui.PrintSuccess("✓ AKA initialized!")
	fmt.Printf("  Aliases file: %s\n", aliasesPath)
	if completionPath != "" {
		fmt.Printf("  Completion:   %s\n", completionPath)
	}
	fmt.Printf("  Source line added to: %s\n", rcFile)
	fmt.Println("\nRun `aka analyze` to generate your first aliases.")
	fmt.Printf("Then reload your shell: source %s\n", rcFile)
	return nil
}

// detectShell determines the shell and returns (shell, rcFilePath, error).
func detectShell() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	shellEnv := os.Getenv("SHELL")
	var shell string
	switch {
	case strings.Contains(shellEnv, "zsh"):
		shell = "zsh"
	case strings.Contains(shellEnv, "bash"):
		shell = "bash"
	case strings.Contains(shellEnv, "fish"):
		shell = "fish"
	default:
		// Try to guess by looking at which rc file exists.
		if _, err := os.Stat(filepath.Join(home, ".zshrc")); err == nil {
			shell = "zsh"
		} else {
			shell = "bash"
		}
	}

	var rcFile string
	switch shell {
	case "zsh":
		rcFile = filepath.Join(home, ".zshrc")
	case "bash":
		// Prefer .bash_profile on macOS, .bashrc on Linux.
		rcFile = filepath.Join(home, ".bashrc")
		if _, err := os.Stat(filepath.Join(home, ".bash_profile")); err == nil {
			// Use .bash_profile only if .bashrc doesn't exist.
			if _, err2 := os.Stat(rcFile); os.IsNotExist(err2) {
				rcFile = filepath.Join(home, ".bash_profile")
			}
		}
	default:
		rcFile = filepath.Join(home, ".bashrc")
	}

	return shell, rcFile, nil
}
