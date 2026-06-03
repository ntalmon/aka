package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/internal/aliases"
	"github.com/ntalmon/aka/internal/ui"
)

// NewDeleteCmd creates the `aka delete` subcommand.
func NewDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Remove an AKA-managed alias or function by name",
		Args:  cobra.ExactArgs(1),
		RunE:  runDelete,
	}
}

func runDelete(_ *cobra.Command, args []string) error {
	name := args[0]

	shell := detectCurrentShell()
	if shell == "" {
		fmt.Println("Could not detect current shell.")
		fmt.Println("Run 'aka init' to set up AKA for your shell.")
		return nil
	}
	ok, err := requireShellInitialized(shell)
	if err != nil {
		return fmt.Errorf("check shell: %w", err)
	}
	if !ok {
		fmt.Printf("Shell '%s' is not set up with AKA.\n", shell)
		fmt.Printf("Run 'aka init --shell %s' to get started.\n", shell)
		return nil
	}

	entries, err := aliases.LoadInstalled(shell)
	if err != nil {
		return fmt.Errorf("load installed: %w", err)
	}

	idx := -1
	for i, e := range entries {
		if e.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("no alias or function named %q is managed by AKA", name)
	}

	target := entries[idx]
	fmt.Printf("Remove %s %q → %s\n", target.Kind, target.Name, target.Template)
	fmt.Print("Continue? [y/N] ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "y" {
		fmt.Println("Aborted.")
		return nil
	}

	updated := append(entries[:idx], entries[idx+1:]...)

	if err := aliases.WriteAliasesFile(updated, shell); err != nil {
		return fmt.Errorf("write aliases.sh: %w", err)
	}
	if err := aliases.SaveInstalled(updated, shell); err != nil {
		return fmt.Errorf("save installed.json: %w", err)
	}

	ui.PrintSuccess(fmt.Sprintf("Removed %s %q", target.Kind, target.Name))
	return nil
}
