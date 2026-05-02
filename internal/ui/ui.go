// Package ui provides interactive TUI components using charmbracelet/huh and lipgloss.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"

	"github.com/ntalmon/aka/aka-cli/internal/history"
	"github.com/ntalmon/aka/aka-cli/internal/llm"
)

// Styles.
var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	addStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	removeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	headerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")) // blue
	mutedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // gray
	successStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
)

// ReviewCensored renders a diff of original vs censored commands and asks the user to confirm.
// Returns true if the user accepts the censored version.
func ReviewCensored(original, censored []history.Entry) (bool, error) {
	origText := joinCommands(original)
	censoredText := joinCommands(censored)

	edits := myers.ComputeEdits(span.URIFromPath("original"), origText, censoredText)
	diff := fmt.Sprint(gotextdiff.ToUnified("original (local only)", "censored (will be sent)", origText, edits))

	fmt.Println(titleStyle.Render("=== Censor Preview ==="))
	fmt.Println(mutedStyle.Render("The following diff shows what will be sent to the LLM."))
	fmt.Println(mutedStyle.Render("Red lines stay local only; green lines replace them in the API call.\n"))

	if diff == "" {
		fmt.Println(successStyle.Render("No sensitive data detected — commands will be sent as-is."))
	} else {
		// Print with color.
		for _, line := range strings.Split(diff, "\n") {
			switch {
			case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
				fmt.Println(headerStyle.Render(line))
			case strings.HasPrefix(line, "+"):
				fmt.Println(addStyle.Render(line))
			case strings.HasPrefix(line, "-"):
				fmt.Println(removeStyle.Render(line))
			default:
				fmt.Println(line)
			}
		}
	}

	fmt.Print(titleStyle.Render("Send the censored commands to the Anthropic API?") + " [y/N] ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
}

const pageSize = 3

// ReviewSuggestions presents suggestions interactively in pages of pageSize.
// After each page the user is asked whether to see more.
// Returns the accepted (and possibly renamed) suggestions.
func ReviewSuggestions(suggestions []llm.Suggestion) ([]llm.Suggestion, error) {
	if len(suggestions) == 0 {
		fmt.Println(mutedStyle.Render("No suggestions returned by the LLM."))
		return nil, nil
	}

	fmt.Println(titleStyle.Render(fmt.Sprintf("\n=== %d Suggestion(s) from AKA ===\n", len(suggestions))))

	var accepted []llm.Suggestion

	for i, s := range suggestions {
		printSuggestion(i+1, len(suggestions), s)

		opts := []huh.Option[string]{
			huh.NewOption("Accept", "accept"),
			huh.NewOption("Reject", "reject"),
			huh.NewOption("Edit name", "edit"),
		}
		if i >= pageSize {
			opts = append(opts, huh.NewOption("Reject remaining and finish", "stop"))
		}

		var action string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("What would you like to do?").
					Options(opts...).
					Value(&action),
			),
		)
		if err := form.Run(); err != nil {
			if strings.Contains(err.Error(), "EOF") {
				continue
			}
			return accepted, err
		}

		if action == "stop" {
			fmt.Println(mutedStyle.Render("  Stopped — remaining suggestions skipped.\n"))
			break
		}

		switch action {
		case "accept":
			accepted = append(accepted, s)
			fmt.Println(successStyle.Render(fmt.Sprintf("  ✓ Accepted '%s'\n", s.Name)))
		case "reject":
			fmt.Println(mutedStyle.Render(fmt.Sprintf("  ✗ Rejected '%s'\n", s.Name)))
		case "edit":
			var newName string
			nameForm := huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title("Enter new name for the alias/function:").
						Value(&newName).
						Placeholder(s.Name),
				),
			)
			if err := nameForm.Run(); err != nil {
				return accepted, err
			}
			if strings.TrimSpace(newName) == "" {
				newName = s.Name
			}
			s.Name = strings.TrimSpace(newName)
			accepted = append(accepted, s)
			fmt.Println(successStyle.Render(fmt.Sprintf("  ✓ Accepted as '%s'\n", s.Name)))
		}

		// After every page (except the last item), ask whether to continue.
		isPageBoundary := (i+1)%pageSize == 0
		isLast := i == len(suggestions)-1
		if isPageBoundary && !isLast {
			remaining := len(suggestions) - (i + 1)
			var more bool
			moreForm := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("See %d more suggestion(s)?", remaining)).
						Value(&more),
				),
			)
			if err := moreForm.Run(); err != nil {
				if strings.Contains(err.Error(), "EOF") {
					break
				}
				return accepted, err
			}
			if !more {
				break
			}
		}
	}
	return accepted, nil
}

// printSuggestion renders a single suggestion to stdout.
func printSuggestion(idx, total int, s llm.Suggestion) {
	fmt.Printf("%s\n", titleStyle.Render(fmt.Sprintf("[%d/%d] %s  (%s)", idx, total, s.Name, s.Kind)))
	fmt.Printf("  %s  %s\n", headerStyle.Render("Template:"), s.Template)

	if len(s.Params) > 0 {
		fmt.Printf("  %s\n", headerStyle.Render("Parameters:"))
		for _, p := range s.Params {
			fmt.Printf("    $%s (%s) — %s\n", p.Name, p.Type, p.Description)
		}
	}

	fmt.Printf("  %s  %s\n", headerStyle.Render("Rationale:"), s.Rationale)

	if len(s.ExampleUses) > 0 {
		fmt.Printf("  %s\n", headerStyle.Render("Examples:"))
		for _, ex := range s.ExampleUses {
			fmt.Printf("    $ %s\n", ex)
		}
	}
	fmt.Println()
}

// PromptAPIKey prompts the user to enter their Anthropic API key.
func PromptAPIKey() (string, error) {
	var key string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Enter your Anthropic API key:").
				Description("Get your key at https://console.anthropic.com/").
				Password(true).
				Value(&key),
		),
	)
	if err := form.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(key), nil
}

// PrintError prints an error message to stderr.
func PrintError(msg string) {
	fmt.Fprintln(os.Stderr, removeStyle.Render("Error: "+msg))
}

// PrintSuccess prints a success message.
func PrintSuccess(msg string) {
	fmt.Println(successStyle.Render(msg))
}

// ChooseHistoryMode is shown when there are fewer new commands than the
// minimum threshold. Returns "new", "full", or "abort".
func ChooseHistoryMode(newCount, totalCount int) (string, error) {
	var choice string
	opts := []huh.Option[string]{
		huh.NewOption(fmt.Sprintf("Send full history (%d commands)", totalCount), "full"),
		huh.NewOption("Abort", "abort"),
	}
	title := fmt.Sprintf("Only %d new command(s) since last run.", newCount)
	if newCount > 0 {
		opts = append([]huh.Option[string]{
			huh.NewOption(fmt.Sprintf("Analyze %d new command(s) only", newCount), "new"),
		}, opts...)
		title = fmt.Sprintf("Only %d new command(s) since last run — not many to work with.", newCount)
	}
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(title).
				Options(opts...).
				Value(&choice),
		),
	)
	if err := form.Run(); err != nil {
		return "abort", err
	}
	return choice, nil
}

func joinCommands(entries []history.Entry) string {
	cmds := make([]string, len(entries))
	for i, e := range entries {
		cmds[i] = e.Command
	}
	return strings.Join(cmds, "\n") + "\n"
}
