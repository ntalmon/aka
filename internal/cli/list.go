package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/aka-cli/internal/aliases"
)

// NewListCmd creates the `aka list` subcommand.
func NewListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all AKA-managed aliases and functions",
		RunE:  runList,
	}
}

func runList(_ *cobra.Command, _ []string) error {
	entries, err := aliases.LoadInstalled()
	if err != nil {
		return fmt.Errorf("load installed: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("No aliases installed yet. Run `aka scan` to get started.")
		return nil
	}

	// Styles.
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	kindStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	templateStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))

	fmt.Println(headerStyle.Render(fmt.Sprintf("%-16s %-10s %-40s %-22s %-22s",
		"NAME", "KIND", "TEMPLATE", "CREATED", "LAST USED")))
	fmt.Println(strings.Repeat("─", 115))

	for _, e := range entries {
		lastUsed := "—"
		if !e.LastUsedAt.IsZero() {
			lastUsed = e.LastUsedAt.Format("2006-01-02 15:04")
		}
		template := e.Template
		if len(template) > 38 {
			template = template[:35] + "..."
		}
		fmt.Printf("%-16s %-10s %-40s %-22s %-22s\n",
			nameStyle.Render(e.Name),
			kindStyle.Render(e.Kind),
			templateStyle.Render(template),
			e.CreatedAt.Format("2006-01-02 15:04"),
			lastUsed,
		)
	}

	fmt.Printf("\n%d alias(es)/function(s) installed\n", len(entries))
	return nil
}
