package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/aka-cli/internal/aliases"
	"github.com/ntalmon/aka/aka-cli/internal/config"
	"github.com/ntalmon/aka/aka-cli/internal/ui"
)

// NewInitCmd creates the `aka init` subcommand.
func NewInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize AKA: create aliases.sh and add source line to your shell rc",
		Long: `aka init creates ~/.config/aka/<shell>/aliases.sh and appends a shell wrapper
and source line to your shell's rc file (.zshrc or .bashrc). This is safe to re-run —
if the shell is already initialized it will tell you.

Fish shell is not supported in v0.1.

Use --shell to explicitly target a shell (default: detected from $SHELL).`,
		RunE: runInit,
	}
	cmd.Flags().String("shell", "", "Shell to initialize (bash or zsh; default: detected from $SHELL)")
	return cmd
}

func runInit(cmd *cobra.Command, _ []string) error {
	shellFlag, _ := cmd.Flags().GetString("shell")

	shell, rcFile, err := resolveInitShell(shellFlag)
	if err != nil {
		return err
	}

	if shell == "fish" {
		return fmt.Errorf("fish shell is not supported in v0.1 — please use bash or zsh")
	}

	fmt.Printf("Detected shell: %s\n", shell)
	fmt.Printf("RC file: %s\n", rcFile)

	// Check if already initialized.
	ok, err := isShellInitialized(shell)
	if err != nil {
		return fmt.Errorf("check shell: %w", err)
	}
	if ok {
		aliasesPath, _ := aliases.AliasesFilePath(shell)
		fmt.Printf("Shell '%s' is already initialized.\n", shell)
		fmt.Printf("  Aliases file: %s\n", aliasesPath)
		fmt.Println("Run 'aka scan' to generate aliases.")
		return nil
	}

	if err := initShell(shell, rcFile); err != nil {
		return err
	}

	// If no API key is configured yet, prompt for it now so the user is ready to run `aka scan`.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if apiKeyForProvider(cfg) == "" && cfg.Provider != "ollama" {
		fmt.Println()
		if err := ensureAPIKey(cfg); err != nil {
			fmt.Printf("Warning: could not configure API key: %v\n", err)
			fmt.Println("You can set it later with: aka config set-key")
		}
	}

	fmt.Println("\nRun `aka scan` to generate your first aliases.")
	fmt.Printf("Then reload your shell: source %s\n", rcFile)
	return nil
}

// initShell wires up aliases.sh and (optionally) completion for the given shell.
// rcFile must already be resolved. It is called by both runInit and runScan.
func initShell(shell, rcFile string) error {
	if err := aliases.Init(rcFile, shell); err != nil {
		return fmt.Errorf("init: %w", err)
	}

	aliasesPath, err := aliases.AliasesFilePath(shell)
	if err != nil {
		return err
	}

	if err := aliases.InitCompletion(shell, rcFile); err != nil {
		return fmt.Errorf("init completion: %w", err)
	}
	completionPath, err := aliases.CompletionFilePath(shell)
	if err != nil {
		return err
	}

	ui.PrintSuccess("✓ AKA initialized!")
	fmt.Printf("  Aliases file: %s\n", aliasesPath)
	fmt.Printf("  Completion:   %s\n", completionPath)
	fmt.Printf("  Source line added to: %s\n", rcFile)
	return nil
}

// resolveInitShell returns the shell and RC file path for the init command.
// If shellOverride is non-empty it is used directly; otherwise the shell is
// detected from $SHELL with a file-existence fallback.
func resolveInitShell(shellOverride string) (shell, rcFile string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	if shellOverride != "" {
		shell = strings.ToLower(shellOverride)
	} else {
		shellEnv := os.Getenv("SHELL")
		switch {
		case strings.Contains(shellEnv, "zsh"):
			shell = "zsh"
		case strings.Contains(shellEnv, "bash"):
			shell = "bash"
		case strings.Contains(shellEnv, "fish"):
			shell = "fish"
		default:
			// Fallback: check which rc file exists.
			if _, err := os.Stat(filepath.Join(home, ".zshrc")); err == nil {
				shell = "zsh"
			} else {
				shell = "bash"
			}
		}
	}

	rcFile, err = rcFileForShell(shell)
	return shell, rcFile, err
}
