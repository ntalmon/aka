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
	"github.com/charmbracelet/x/term"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"

	"github.com/ntalmon/aka/aka-cli/internal/history"
	"github.com/ntalmon/aka/aka-cli/internal/llm"
)

var (
	matrixGreen  = lipgloss.Color("#00FF41")
	neonCyan     = lipgloss.Color("#00FFFF")
	claudeOrange = lipgloss.Color("#FF9900")
	cyberRed     = lipgloss.Color("#FF003C")
	darkGray     = lipgloss.Color("#303030")
	lightGray    = lipgloss.Color("#A0A0A0")

	titleStyle        = lipgloss.NewStyle().Bold(true).Foreground(matrixGreen)
	addStyle          = lipgloss.NewStyle().Foreground(matrixGreen)
	removeStyle       = lipgloss.NewStyle().Foreground(cyberRed)
	headerStyle       = lipgloss.NewStyle().Foreground(neonCyan)
	mutedStyle        = lipgloss.NewStyle().Foreground(lightGray)
	successStyle      = lipgloss.NewStyle().Bold(true).Foreground(matrixGreen)
	aliasCodeStyle    = lipgloss.NewStyle().Foreground(neonCyan).Bold(true).Background(darkGray).Padding(0, 1)
	patternLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(matrixGreen)
	aliasLabelStyle   = lipgloss.NewStyle().Bold(true).Foreground(neonCyan)
	templateStyle     = lipgloss.NewStyle().Foreground(lightGray)
	promptStyle       = lipgloss.NewStyle().Foreground(claudeOrange).Bold(true)
	successBarStyle   = lipgloss.NewStyle().
				BorderLeft(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(matrixGreen).
				PaddingLeft(1).
				Foreground(matrixGreen)

	suggestionBoxStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(matrixGreen).
				Padding(1, 2).
				MarginBottom(1)

	bannerIconStyle     = lipgloss.NewStyle().Foreground(claudeOrange)
	bannerSubtitleStyle = lipgloss.NewStyle().Foreground(neonCyan)
)

const asciiArt = `
 █████╗ ██╗  ██╗ █████╗
██╔══██╗██║ ██╔╝██╔══██╗
███████║█████╔╝ ███████║
██╔══██║██╔═██╗ ██╔══██║
██║  ██║██║  ██╗██║  ██║
╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝`

func PrintBanner() {
	fmt.Println(titleStyle.Render(asciiArt))
	fmt.Print("  " + bannerIconStyle.Render("⚡") + " " + bannerSubtitleStyle.Render("AKA — Your AI Shell Assistant") + "\n\n")
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
	).WithTheme(cyberTheme())
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

	termWidth, _, err := term.GetSize(uintptr(os.Stdout.Fd()))
	if err != nil || termWidth < 40 {
		termWidth = 80
	}
	if termWidth > 100 {
		termWidth = 100
	}
	boxStyle := suggestionBoxStyle.Width(termWidth - 4)

	reader := bufio.NewReader(os.Stdin)
	var accepted []llm.Suggestion

	for i, s := range suggestions {
		pattern := patternLabelStyle.Render(fmt.Sprintf("[%d] PATTERN FOUND:", i+1))
		template := templateStyle.Render(sanitizeForDisplay(s.Template))

		aliasLbl := aliasLabelStyle.Render("↳ SUGGESTED ALIAS:")
		aliasVal := aliasCodeStyle.Render(sanitizeForDisplay(buildInvocation(s)))

		var rationale string
		if s.Rationale != "" {
			r := sanitizeForDisplay(s.Rationale)
			// Keep the first sentence only; truncate at 120 chars as a hard cap.
			if i := strings.IndexAny(r, ".!?"); i >= 0 && i < len(r)-1 {
				r = r[:i+1]
			}
			if len(r) > 120 {
				r = r[:117] + "..."
			}
			rationale = mutedStyle.Render(r)
		}

		content := lipgloss.JoinVertical(lipgloss.Left,
			fmt.Sprintf("%s  %s", pattern, template),
			fmt.Sprintf("%s  %s", aliasLbl, aliasVal),
			rationale,
		)

		fmt.Println(boxStyle.Render(content))

		fmt.Printf("   %s ", promptStyle.Render("Accept suggestion? [Y/n/e]"))

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
	).WithTheme(cyberTheme())
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
	).WithTheme(cyberTheme())
	if err := form.Run(); err != nil {
		return "", err
	}
	return selected, nil
}

// PromptModelOrSwitchProvider shows models for currentProvider with an extra
// "Switch to a different provider..." option at the bottom. Returns the
// (provider, model) pair the user chose — provider may differ from the input
// if they switched.
func PromptModelOrSwitchProvider(currentProvider string) (string, string, error) {
	const switchSentinel = "__switch_provider__"

	models := llm.ModelsForProvider(currentProvider)
	opts := make([]huh.Option[string], len(models)+1)
	for i, m := range models {
		opts[i] = huh.NewOption(m.Label, m.ID)
	}
	opts[len(models)] = huh.NewOption("Switch to a different provider...", switchSentinel)

	var selected string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose a model:").
				Options(opts...).
				Value(&selected),
		),
	).WithTheme(cyberTheme())
	if err := form.Run(); err != nil {
		return "", "", err
	}

	if selected == switchSentinel {
		provider, err := PromptProvider()
		if err != nil {
			return "", "", err
		}
		model, err := PromptModel(provider)
		if err != nil {
			return "", "", err
		}
		return provider, model, nil
	}
	return currentProvider, selected, nil
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
	).WithTheme(cyberTheme())
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

// PromptInitShell asks the user whether to initialize AKA for the given shell right now.
func PromptInitShell(shell string) (bool, error) {
	confirm := true
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Shell '%s' is not set up with AKA. Initialize now?", shell)).
				Value(&confirm),
		),
	).WithTheme(cyberTheme())
	if err := form.Run(); err != nil {
		return false, err
	}
	return confirm, nil
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
	).WithTheme(cyberTheme())
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
	).WithTheme(cyberTheme())
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
		).WithTheme(cyberTheme())
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

func cyberTheme() *huh.Theme {
	t := huh.ThemeBase()
	t.Focused.Title = t.Focused.Title.Foreground(neonCyan)
	t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(claudeOrange).SetString("▶ ")
	t.Focused.SelectedOption = lipgloss.NewStyle().Foreground(matrixGreen)
	t.Focused.TextInput.Prompt = lipgloss.NewStyle().Foreground(claudeOrange).SetString("> ")
	t.Focused.TextInput.Cursor = lipgloss.NewStyle().Foreground(matrixGreen)
	t.Focused.FocusedButton = lipgloss.NewStyle().Background(matrixGreen).Foreground(darkGray).Padding(0, 1)
	t.Focused.BlurredButton = lipgloss.NewStyle().Foreground(lightGray).Padding(0, 1)
	return t
}
