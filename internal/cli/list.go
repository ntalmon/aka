package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/internal/aliases"
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

	if len(entries) == 0 {
		fmt.Println("No aliases installed yet. Run `aka scan` to get started.")
		return nil
	}

	// Column widths. NAME expands to fit data; TEMPLATE fills whatever is left.
	const (
		kindW     = 8
		createdW  = 16
		lastUsedW = 16
		gaps      = 10 // 3 spaces between name/kind/template, 2 between template/dates
		minTmplW  = 15
	)

	termWidth, _, terr := term.GetSize(uintptr(os.Stdout.Fd()))
	if terr != nil || termWidth < 40 {
		termWidth = 80
	}

	nameW := len("NAME")
	for _, e := range entries {
		if n := len(e.Name); n > nameW {
			nameW = n
		}
	}
	if nameW > 20 {
		nameW = 20
	}

	templateW := termWidth - nameW - kindW - createdW - lastUsedW - gaps
	if templateW < minTmplW {
		templateW = minTmplW
	}

	// Styles — same palette as the scan UI.
	cyan := lipgloss.Color("#22D3EE")
	orange := lipgloss.Color("#FF9900")
	gray := lipgloss.Color("#A0A0A0")

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(orange)
	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(cyan)
	kindStyle := lipgloss.NewStyle().Foreground(orange)
	templateStyle := lipgloss.NewStyle().Foreground(gray)
	mutedStyle := lipgloss.NewStyle().Foreground(gray)
	countStyle := lipgloss.NewStyle().Foreground(cyan)

	header := fmt.Sprintf("%-*s   %-*s   %-*s  %-*s  %-*s",
		nameW, "NAME",
		kindW, "KIND",
		templateW, "TEMPLATE",
		createdW, "CREATED",
		lastUsedW, "LAST USED",
	)
	fmt.Println(headerStyle.Render(header))
	fmt.Println(mutedStyle.Render(strings.Repeat("─", nameW+kindW+templateW+createdW+lastUsedW+gaps)))

	for _, e := range entries {
		lastUsed := "—"
		if !e.LastUsedAt.IsZero() {
			lastUsed = e.LastUsedAt.Format("2006-01-02 15:04")
		}
		tmpl := ansi.Truncate(e.Template, templateW, "...")
		fmt.Printf("%s   %s   %s  %s  %s\n",
			nameStyle.Render(fmt.Sprintf("%-*s", nameW, e.Name)),
			kindStyle.Render(fmt.Sprintf("%-*s", kindW, e.Kind)),
			templateStyle.Render(fmt.Sprintf("%-*s", templateW, tmpl)),
			mutedStyle.Render(fmt.Sprintf("%-*s", createdW, e.CreatedAt.Format("2006-01-02 15:04"))),
			mutedStyle.Render(fmt.Sprintf("%-*s", lastUsedW, lastUsed)),
		)
	}

	fmt.Printf("\n%s\n", countStyle.Render(fmt.Sprintf("%d alias(es)/function(s) installed", len(entries))))
	return nil
}
