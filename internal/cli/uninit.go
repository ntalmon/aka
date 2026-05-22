package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

var (
	// reWrapper matches the aka() shell wrapper block added by `aka init`.
	reWrapper = regexp.MustCompile("(?s)\n# Added by aka init[^\n]*\naka\\(\\) \\{.*?\n\\}\n")
	// reAliasSource matches the aliases.sh source line and its comment.
	reAliasSource = regexp.MustCompile("\n# Added by `aka init` — single source[^\n]*\n[^\n]*aliases\\.sh[^\n]*\n")
	// reCompletionSource matches the completion.sh source line and its comment.
	reCompletionSource = regexp.MustCompile("\n# Added by `aka init` — tab completion[^\n]*\n[^\n]*completion\\.sh[^\n]*\n")
)

func NewUninitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninit",
		Short: "Remove AKA from your shell and delete all config and aliases",
		Long: `aka uninit removes everything aka init added to your shell RC file and
deletes ~/.config/aka/ (aliases, config, history cursor, backups).

The aka binary itself is not removed.`,
		RunE: runUninit,
	}
}

func runUninit(_ *cobra.Command, _ []string) error {
	_, rcFile, err := detectShell()
	if err != nil {
		return err
	}

	rcData, err := os.ReadFile(rcFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", rcFile, err)
	}
	rcContent := string(rcData)

	hasWrapper := reWrapper.MatchString(rcContent)
	hasAliasLine := reAliasSource.MatchString(rcContent)
	hasCompletion := reCompletionSource.MatchString(rcContent)
	hasAnyRC := hasWrapper || hasAliasLine || hasCompletion

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	configDir := filepath.Join(home, ".config", "aka")
	_, statErr := os.Stat(configDir)
	hasConfigDir := statErr == nil

	if !hasAnyRC && !hasConfigDir {
		fmt.Println("Nothing to remove — AKA does not appear to be initialized.")
		return nil
	}

	fmt.Println("The following will be removed:")
	if hasWrapper {
		fmt.Printf("  • aka() shell wrapper in %s\n", rcFile)
	}
	if hasAliasLine {
		fmt.Printf("  • aliases.sh source line in %s\n", rcFile)
	}
	if hasCompletion {
		fmt.Printf("  • completion.sh source line in %s\n", rcFile)
	}
	if hasConfigDir {
		fmt.Printf("  • %s/ (aliases, config, history cursor, backups)\n", configDir)
	}

	fmt.Print("\nContinue? [y/N] ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	if answer := strings.TrimSpace(strings.ToLower(scanner.Text())); answer != "y" && answer != "yes" {
		fmt.Println("Aborted.")
		return nil
	}

	if hasAnyRC {
		cleaned := reWrapper.ReplaceAllString(rcContent, "")
		cleaned = reAliasSource.ReplaceAllString(cleaned, "")
		cleaned = reCompletionSource.ReplaceAllString(cleaned, "")
		if err := os.WriteFile(rcFile, []byte(cleaned), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rcFile, err)
		}
		fmt.Printf("✓ Removed aka blocks from %s\n", rcFile)
	}

	if hasConfigDir {
		if err := os.RemoveAll(configDir); err != nil {
			return fmt.Errorf("remove %s: %w", configDir, err)
		}
		fmt.Printf("✓ Deleted %s/\n", configDir)
	}

	fmt.Printf("\nDone. Reload your shell to clear the aka() wrapper: source %s\n", rcFile)
	return nil
}
