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
	// reWrapper matches the aka() shell wrapper block added by `aka init` (any shell variant).
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
		Long: `aka uninit removes the current shell's aka init wiring from your RC file
and deletes ~/.config/aka/<shell>/ (aliases, history cursor).

If no other shells remain, ~/.config/aka/ (including config.toml) is also removed.
The aka binary itself is not removed.`,
		RunE: runUninit,
	}
}

func runUninit(_ *cobra.Command, _ []string) error {
	shell := detectCurrentShell()
	if shell == "" {
		return fmt.Errorf("could not detect current shell — set AKA_SHELL or SHELL")
	}

	rcFile, err := rcFileForShell(shell)
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
	shellDir := filepath.Join(home, ".config", "aka", shell)
	_, statErr := os.Stat(shellDir)
	hasShellDir := statErr == nil

	if !hasAnyRC && !hasShellDir {
		fmt.Printf("Nothing to remove — AKA does not appear to be initialized for %s.\n", shell)
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
	if hasShellDir {
		fmt.Printf("  • %s/ (aliases, history cursor)\n", shellDir)
	}

	// Check if removing this shell dir would leave the global dir empty.
	globalDir := filepath.Join(home, ".config", "aka")
	otherShellsExist := otherShellDirsExist(globalDir, shell)
	if !otherShellsExist {
		fmt.Printf("  • %s/ (config.toml — last shell removed)\n", globalDir)
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

	if hasShellDir {
		if err := os.RemoveAll(shellDir); err != nil {
			return fmt.Errorf("remove %s: %w", shellDir, err)
		}
		fmt.Printf("✓ Deleted %s/\n", shellDir)
	}

	if !otherShellsExist {
		if err := os.RemoveAll(globalDir); err != nil {
			return fmt.Errorf("remove %s: %w", globalDir, err)
		}
		fmt.Printf("✓ Deleted %s/\n", globalDir)
	}

	fmt.Printf("\nDone. Reload your shell to clear the aka() wrapper: source %s\n", rcFile)
	return nil
}

// otherShellDirsExist reports whether any shell subdirectory other than currentShell
// exists inside configBaseDir.
func otherShellDirsExist(configBaseDir, currentShell string) bool {
	entries, err := os.ReadDir(configBaseDir)
	if err != nil {
		return false
	}
	knownShells := map[string]bool{"bash": true, "zsh": true, "fish": true}
	for _, e := range entries {
		if e.IsDir() && knownShells[e.Name()] && e.Name() != currentShell {
			return true
		}
	}
	return false
}
