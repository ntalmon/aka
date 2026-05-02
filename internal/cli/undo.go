package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/aka-cli/internal/aliases"
	"github.com/ntalmon/aka/aka-cli/internal/ui"
)

// NewUndoCmd creates the `aka undo` subcommand.
func NewUndoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "undo",
		Short: "Restore aliases.sh and installed.json from the most recent backup",
		RunE:  runUndo,
	}
}

func runUndo(_ *cobra.Command, _ []string) error {
	bd, err := aliases.BackupDir()
	if err != nil {
		return err
	}

	// Find the most recent aliases.sh backup.
	entries, err := os.ReadDir(bd)
	if err != nil {
		return fmt.Errorf("read backup dir: %w", err)
	}

	// Find the most recent timestamp prefix.
	var aliasBackups []string
	var installedBackups []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "aliases.sh.") {
			aliasBackups = append(aliasBackups, name)
		}
		if strings.HasPrefix(name, "installed.json.") {
			installedBackups = append(installedBackups, name)
		}
	}

	if len(aliasBackups) == 0 {
		return fmt.Errorf("no backups found in %s", bd)
	}

	// Sort to get the most recent (highest timestamp).
	sort.Strings(aliasBackups)
	sort.Strings(installedBackups)

	latestAlias := filepath.Join(bd, aliasBackups[len(aliasBackups)-1])
	aliasesPath, err := aliases.AliasesFilePath()
	if err != nil {
		return err
	}

	// Restore aliases.sh.
	data, err := os.ReadFile(latestAlias)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	if err := os.WriteFile(aliasesPath, data, 0o644); err != nil {
		return fmt.Errorf("restore aliases.sh: %w", err)
	}

	// Restore installed.json if available.
	if len(installedBackups) > 0 {
		latestInstalled := filepath.Join(bd, installedBackups[len(installedBackups)-1])
		installedPath, err := aliases.InstalledJSONPath()
		if err != nil {
			return err
		}
		iData, err := os.ReadFile(latestInstalled)
		if err == nil {
			if err := os.WriteFile(installedPath, iData, 0o644); err != nil {
				return fmt.Errorf("restore installed.json: %w", err)
			}
		}
	}

	ui.PrintSuccess(fmt.Sprintf("✓ Restored from backup: %s", latestAlias))
	return nil
}
