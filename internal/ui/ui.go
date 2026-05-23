// Package ui provides interactive TUI components using charmbracelet/huh and lipgloss.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
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
	titleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	addStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	removeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	headerStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	mutedStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	successStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	labelStyle      = lipgloss.NewStyle().Bold(true)
	aliasCodeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true)
	successBarStyle = lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("10")).
			PaddingLeft(1).
			Foreground(lipgloss.Color("10"))
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
	fmt.Print("  ⚡ AKA — Your AI Shell Assistant\n\n")
}

// ReviewCensored renders a diff of original vs censored commands and asks the user how to proceed.
// Returns the entries to send (possibly edited), or nil if the user aborted.
func ReviewCensored(original, censored []history.Entry) ([]history.Entry, error) {
	printCensorDiff(original, censored)

	const (
		optSend  = "send"
		optEdit  = "edit"
		optAbort = "abort"
	)
	var choice string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(fmt.Sprintf("Send %d commands to the LLM?", len(censored))).
				Options(
					huh.NewOption(fmt.Sprintf("Yes, send %d commands", len(censored)), optSend),
					huh.NewOption("Edit censored commands manually first", optEdit),
					huh.NewOption("No, abort", optAbort),
				).
				Value(&choice),
		),
	)
	if err := form.Run(); err != nil {
		return nil, err
	}
	switch choice {
	case optAbort:
		return nil, nil
	case optEdit:
		return editEntriesInEditor(censored)
	default:
		return censored, nil
	}
}

func printCensorDiff(original, censored []history.Entry) {
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
}

func editEntriesInEditor(entries []history.Entry) ([]history.Entry, error) {
	f, err := os.CreateTemp("", "aka-censor-*.txt")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	for _, e := range entries {
		if _, werr := fmt.Fprintln(f, e.Command); werr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("write temp file: %w", werr)
		}
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close temp file: %w", err)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("editor: %w", err)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("read edited file: %w", err)
	}

	var result []history.Entry
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, history.Entry{Command: line})
		}
	}
	return result, nil
}

// ReviewSuggestions presents suggestions one at a time with a Y/n/e prompt.
// Returns the accepted suggestions.
func ReviewSuggestions(suggestions []llm.Suggestion) ([]llm.Suggestion, error) {
	if len(suggestions) == 0 {
		fmt.Println(mutedStyle.Render("No suggestions returned by the LLM."))
		return nil, nil
	}

	reader := bufio.NewReader(os.Stdin)
	var accepted []llm.Suggestion

	for i, s := range suggestions {
		fmt.Printf("\n%s  %s\n",
			labelStyle.Render(fmt.Sprintf("[%d] PATTERN FOUND:", i+1)),
			sanitizeForDisplay(s.Template),
		)
		fmt.Printf("   %s  %s\n",
			labelStyle.Render("↳ SUGGESTED ALIAS:"),
			aliasCodeStyle.Render("`"+sanitizeForDisplay(buildInvocation(s))+"`"),
		)
		fmt.Printf("\n   Accept suggestion? [Y/n/e] ")

		line, _ := reader.ReadString('\n')
		choice := strings.ToLower(strings.TrimSpace(line))

		switch {
		case choice == "" || strings.HasPrefix(choice, "y"):
			accepted = append(accepted, s)
			fmt.Println(successBarStyle.Render(fmt.Sprintf("✅ Alias %s accepted", sanitizeForDisplay(s.Name))))
		case strings.HasPrefix(choice, "e"):
			fmt.Printf("   New alias name [%s]: ", sanitizeForDisplay(s.Name))
			newName, _ := reader.ReadString('\n')
			newName = strings.TrimSpace(newName)
			if newName != "" {
				s.Name = newName
			}
			accepted = append(accepted, s)
			fmt.Println(successBarStyle.Render(fmt.Sprintf("✅ Alias %s accepted", sanitizeForDisplay(s.Name))))
		default:
			fmt.Println(mutedStyle.Render(fmt.Sprintf("   ✗ %s — skipped", sanitizeForDisplay(s.Name))))
		}
	}

	return accepted, nil
}

func buildInvocation(s llm.Suggestion) string {
	if len(s.Params) == 0 {
		return s.Name
	}
	parts := make([]string, 0, len(s.Params)+1)
	parts = append(parts, s.Name)
	for i := range s.Params {
		parts = append(parts, fmt.Sprintf("\"$%d\"", i+1))
	}
	return strings.Join(parts, " ")
}

// sanitizeForDisplay strips ASCII control characters (except tab and newline) to
// prevent terminal escape-sequence injection from LLM output.
func sanitizeForDisplay(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' {
			return -1
		}
		return r
	}, s)
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

// PromptEnableCompletion asks the user whether to install tab-completion for `aka`.
func PromptEnableCompletion() (bool, error) {
	enable := true
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Enable tab-completion for `aka`?").
				Description("Writes ~/.config/aka/completion.sh and sources it from your rc file.").
				Value(&enable),
		),
	)
	if err := form.Run(); err != nil {
		return false, err
	}
	return enable, nil
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
	if newCount >= 50 {
		opts = append([]huh.Option[string]{
			huh.NewOption(fmt.Sprintf("Scan %d new command(s) only", newCount), "new"),
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
