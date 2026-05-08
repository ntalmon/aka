// Package ui provides interactive TUI components using charmbracelet/huh and lipgloss.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"

	"github.com/ntalmon/aka/aka-cli/internal/history"
	"github.com/ntalmon/aka/aka-cli/internal/llm"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	addStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	removeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	headerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")) // blue
	mutedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // gray
	successStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
)

const asciiArt = `
 █████╗ ██╗  ██╗ █████╗
██╔══██╗██║ ██╔╝██╔══██╗
███████║█████╔╝ ███████║
██╔══██║██╔═██╗ ██╔══██║
██║  ██║██║  ██╗██║  ██║
╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝`

// PrintBanner prints the AKA ASCII art banner.
func PrintBanner() {
	fmt.Println(titleStyle.Render(asciiArt))
	fmt.Println(mutedStyle.Render("  also known as · your shell alias coach\n"))
}

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

	fmt.Print(titleStyle.Render(fmt.Sprintf("Send %d commands to the LLM?", len(censored))) + " [y/N] ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
}

const pageSize = 5

// ReviewSuggestions presents suggestions in batches of pageSize with multi-select.
// The user sees all details for the batch, then picks which to keep, then optionally continues.
// Returns the accepted suggestions.
func ReviewSuggestions(suggestions []llm.Suggestion) ([]llm.Suggestion, error) {
	if len(suggestions) == 0 {
		fmt.Println(mutedStyle.Render("No suggestions returned by the LLM."))
		return nil, nil
	}

	fmt.Println(titleStyle.Render(fmt.Sprintf("\n  %d suggestion(s) ready\n", len(suggestions))))

	var accepted []llm.Suggestion

	for start := 0; start < len(suggestions); {
		end := start + pageSize
		if end > len(suggestions) {
			end = len(suggestions)
		}
		batch := suggestions[start:end]

		for i, s := range batch {
			printSuggestion(start+i+1, len(suggestions), s)
		}

		opts := make([]huh.Option[int], len(batch))
		for i, s := range batch {
			label := fmt.Sprintf("%-14s  [%s]  %s", s.Name, strings.ToLower(s.Kind), s.Template)
			opts[i] = huh.NewOption(label, start+i).Selected(true)
		}

		var selected []int
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewMultiSelect[int]().
					Title("Accept these? (space to toggle, enter to confirm)").
					Options(opts...).
					Value(&selected),
			),
		)
		if err := form.Run(); err != nil {
			if strings.Contains(err.Error(), "EOF") {
				break
			}
			return accepted, err
		}

		keep := make(map[int]bool, len(selected))
		for _, idx := range selected {
			keep[idx] = true
		}
		for i, s := range batch {
			if keep[start+i] {
				accepted = append(accepted, s)
				fmt.Println(successStyle.Render(fmt.Sprintf("  ✓ '%s'", s.Name)))
			} else {
				fmt.Println(mutedStyle.Render(fmt.Sprintf("  ✗ '%s'", s.Name)))
			}
		}
		fmt.Println()

		start = end

		if start < len(suggestions) {
			remaining := len(suggestions) - start
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
	fmt.Println(mutedStyle.Render(strings.Repeat("─", 54)))
	fmt.Printf("%s  %s  %s\n",
		titleStyle.Render(fmt.Sprintf("[%d/%d]", idx, total)),
		titleStyle.Render(s.Name),
		mutedStyle.Render("("+strings.ToLower(s.Kind)+")"),
	)
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

// PromptProvider lets the user pick an LLM provider.
func PromptProvider() (string, error) {
	opts := make([]huh.Option[string], len(llm.SupportedProviders))
	for i, p := range llm.SupportedProviders {
		opts[i] = huh.NewOption(p.Label, p.ID)
	}
	var selected string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose an LLM provider:").
				Options(opts...).
				Value(&selected),
		),
	)
	if err := form.Run(); err != nil {
		return "", err
	}
	return selected, nil
}

// PromptModel lets the user pick from the supported models for a provider.
// Returns the selected model ID.
func PromptModel(provider string) (string, error) {
	models := llm.ModelsForProvider(provider)
	opts := make([]huh.Option[string], len(models))
	for i, m := range models {
		opts[i] = huh.NewOption(m.Label, m.ID)
	}
	var selected string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose a model:").
				Options(opts...).
				Value(&selected),
		),
	)
	if err := form.Run(); err != nil {
		return "", err
	}
	return selected, nil
}

// PromptAPIKey prompts the user to enter an API key for the given provider.
func PromptAPIKey(provider string) (string, error) {
	title, desc := providerKeyPrompt(provider)
	var key string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(title).
				Description(desc).
				EchoMode(huh.EchoModePassword).
				Value(&key),
		),
	)
	if err := form.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(key), nil
}

func providerKeyPrompt(provider string) (title, desc string) {
	switch provider {
	case "groq":
		return "Enter your Groq API key:", "Get your key at https://console.groq.com/keys"
	default:
		return "Enter your Anthropic API key:", "Get your key at https://console.anthropic.com/"
	}
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

// ChooseMaxHistory is shown when the normalized history exceeds the configured
// max_history limit. Returns the limit to apply (0 = no limit) and whether
// the user chose to save the new limit to config.
func ChooseMaxHistory(currentLimit, totalNormalized int) (limit int, saveToConfig bool, err error) {
	const (
		optKeep   = "keep"
		optAll    = "all"
		optChange = "change"
	)
	var choice string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(fmt.Sprintf("%d commands after normalization, but max_history is set to %d.", totalNormalized, currentLimit)).
				Description("Only the most recent commands will be sent unless you adjust the limit.").
				Options(
					huh.NewOption(fmt.Sprintf("Continue with %d most recent (current limit)", currentLimit), optKeep),
					huh.NewOption(fmt.Sprintf("Send all %d commands this run", totalNormalized), optAll),
					huh.NewOption("Change max_history limit", optChange),
				).
				Value(&choice),
		),
	)
	if err = form.Run(); err != nil {
		return currentLimit, false, err
	}
	switch choice {
	case optAll:
		return 0, false, nil
	case optChange:
		var input string
		inputForm := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("New max_history limit:").
					Description("Maximum number of commands to send to the LLM (saved to config).").
					Value(&input),
			),
		)
		if err = inputForm.Run(); err != nil {
			return currentLimit, false, err
		}
		n, parseErr := strconv.Atoi(strings.TrimSpace(input))
		if parseErr != nil || n <= 0 {
			return currentLimit, false, fmt.Errorf("invalid limit %q: must be a positive integer", input)
		}
		return n, true, nil
	default: // optKeep
		return currentLimit, false, nil
	}
}

func joinCommands(entries []history.Entry) string {
	cmds := make([]string, len(entries))
	for i, e := range entries {
		cmds[i] = e.Command
	}
	return strings.Join(cmds, "\n") + "\n"
}
