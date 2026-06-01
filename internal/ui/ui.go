// Package ui provides interactive TUI components using charmbracelet/huh and lipgloss.
package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"github.com/ntalmon/aka/aka-cli/internal/history"
	"github.com/ntalmon/aka/aka-cli/internal/llm"
)

var (
	neonCyan     = lipgloss.Color("#22D3EE")
	claudeOrange = lipgloss.Color("#FF9900")
	cyberRed     = lipgloss.Color("#FF003C")
	neonGreen    = lipgloss.Color("#22C55E")
	darkGray     = lipgloss.Color("#303030")
	lightGray    = lipgloss.Color("#A0A0A0")
	brightWhite  = lipgloss.Color("#FFFFFF")

	titleStyle         = lipgloss.NewStyle().Bold(true).Foreground(neonCyan)
	removeStyle        = lipgloss.NewStyle().Foreground(cyberRed)
	addStyle           = lipgloss.NewStyle().Foreground(neonGreen)
	headerStyle        = lipgloss.NewStyle().Bold(true).Foreground(claudeOrange)
	mutedStyle         = lipgloss.NewStyle().Foreground(lightGray)
	successStyle       = lipgloss.NewStyle().Bold(true).Foreground(neonCyan)
	aliasCodeStyle     = lipgloss.NewStyle().Foreground(neonCyan).Bold(true).Background(darkGray).Padding(0, 1)
	aliasLabelStyle    = lipgloss.NewStyle().Foreground(neonCyan)
	templateStyle      = lipgloss.NewStyle().Foreground(lightGray)
	overviewIndexStyle = lipgloss.NewStyle().Bold(true).Foreground(claudeOrange)
	sectionStyle       = lipgloss.NewStyle().Bold(true).Foreground(neonCyan)
	nameStyle          = lipgloss.NewStyle().Bold(true).Foreground(brightWhite)
	commandLineStyle   = lipgloss.NewStyle().Foreground(lightGray)
	successBarStyle    = lipgloss.NewStyle().
				BorderLeft(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(neonCyan).
				PaddingLeft(1).
				Foreground(neonCyan)

	suggestionBoxStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(neonCyan).
				Padding(1, 2).
				MarginBottom(1)
)

const asciiArt = `    ___    __ __ ___
   /   |  / //_//   |
  / /| | / ,<  / /| |
 / ___ |/ /| |/ ___ |
/_/  |_/_/ |_/_/  |_|`

func PrintBanner() {
	fmt.Println(titleStyle.Render(asciiArt))
	fmt.Println("" + lipgloss.NewStyle().Foreground(claudeOrange).Render("⚡") + " " + mutedStyle.Render("AKA — Your AI Shell Assistant") + "\n")
}

// PrintStep prints a muted tree-connector progress line: "  └─ <text>".
func PrintStep(text string) {
	fmt.Println(mutedStyle.Render("  └─ " + text))
}

// PrintStepLabeled prints a progress line with an orange label: "  └─ <label> <text>".
func PrintStepLabeled(label, text string) {
	fmt.Println(mutedStyle.Render("  └─ ") + headerStyle.Render(label) + " " + mutedStyle.Render(text))
}

// PrintCensorDiff prints a diff of commands modified by the censor pass.
// Unchanged commands are omitted; changed ones show the original in red and
// the censored replacement in green.
func PrintCensorDiff(original, censored []history.Entry) {
	const maxLines = 50 // each changed entry prints 2 lines
	lines := 0
	changed := 0
	for i, orig := range original {
		if i >= len(censored) {
			break
		}
		if orig.Command != censored[i].Command {
			changed++
			if lines+2 <= maxLines {
				fmt.Println(removeStyle.Render("- " + orig.Command))
				fmt.Println(addStyle.Render("+ " + censored[i].Command))
				lines += 2
			}
		}
	}
	if changed == 0 {
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(neonGreen).Render("  ✓ No sensitive data detected."))
	} else {
		shown := lines / 2
		suffix := ""
		if shown < changed {
			suffix = fmt.Sprintf(" (%d more not shown)", changed-shown)
		}
		fmt.Println(mutedStyle.Render(fmt.Sprintf("  %d of %d command(s) modified by censor.%s", changed, len(original), suffix)))
	}
	fmt.Println()
}

// ReviewCensored asks whether to send the censored commands to the LLM.
// Returns the entries to send (possibly edited); back is true when the user
// navigated back (only possible when withBack is true), and (nil, false, nil)
// means the user aborted.
func ReviewCensored(original, censored []history.Entry, withBack bool) (entries []history.Entry, back bool, err error) {
	const (
		optSend  = "send"
		optEdit  = "edit"
		optAbort = "abort"
	)
	anyCensored := false
	for i := range censored {
		if i < len(original) && original[i].Command != censored[i].Command {
			anyCensored = true
			break
		}
	}
	opts := []huh.Option[string]{
		huh.NewOption(fmt.Sprintf("Yes, send %d commands", len(censored)), optSend),
	}
	if anyCensored {
		opts = append(opts, huh.NewOption("Edit censored commands manually first", optEdit))
	}
	opts = append(opts, huh.NewOption("No, abort", optAbort))
	choice, wentBack, err := runSelectWithBack(fmt.Sprintf("Send %d commands to the LLM?", len(censored)), "", opts, withBack)
	if err != nil {
		return nil, false, err
	}
	if wentBack {
		return nil, true, nil
	}
	switch choice {
	case optAbort:
		return nil, false, nil
	case optEdit:
		edited, eerr := editEntriesInEditor(censored)
		return edited, false, eerr
	default:
		return censored, false, nil
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

// reviewDecision records what the user decided for one suggestion.
type reviewDecision struct {
	accepted bool
	name     string // effective alias name (original or edited)
}

// reviewFlowModel runs the entire suggestion review as a single bubbletea
// program so navigating between suggestions happens in-place.
type reviewFlowModel struct {
	suggestions []llm.Suggestion
	decisions   []reviewDecision
	current     int
	editingName bool
	choice      string
	newName     string
	active      *huh.Form
	termWidth   int
	aborted     bool
}

// splitTemplate splits a shell template into individual command lines by && and newlines.
func splitTemplate(tmpl string) []string {
	s := strings.ReplaceAll(tmpl, "&&", "\n")
	s = strings.ReplaceAll(s, " ;", "\n")
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return []string{tmpl}
	}
	return lines
}

func (m *reviewFlowModel) suggestionView() string {
	s := m.suggestions[m.current]
	boxW := m.termWidth - 4
	if boxW < 40 {
		boxW = 40
	}
	bs := suggestionBoxStyle.Width(boxW)

	header := overviewIndexStyle.Render(fmt.Sprintf("❯ [%d/%d]", m.current+1, len(m.suggestions))) +
		" " + nameStyle.Render(sanitizeForDisplay(s.Name))

	cmdLines := splitTemplate(sanitizeForDisplay(s.Template))
	parts := []string{header}
	for _, c := range cmdLines {
		parts = append(parts, commandLineStyle.Render("  │ "+c))
	}
	parts = append(parts, aliasLabelStyle.Render("↳ runs as:")+"  "+
		aliasCodeStyle.Render(sanitizeForDisplay(buildInvocation(s))))

	if s.Rationale != "" {
		r := sanitizeForDisplay(s.Rationale)
		if idx := strings.IndexAny(r, ".!?"); idx >= 0 && idx < len(r)-1 {
			r = r[:idx+1]
		}
		if len(r) > 120 {
			r = r[:117] + "..."
		}
		parts = append(parts, mutedStyle.Render(r))
	}
	return bs.Render(lipgloss.JoinVertical(lipgloss.Left, parts...)) + "\n"
}

func (m *reviewFlowModel) toReview() tea.Cmd {
	m.editingName = false
	m.choice = ""
	opts := []huh.Option[string]{
		huh.NewOption("Accept", "accept"),
		huh.NewOption(fmt.Sprintf("Edit name  (current: %s)", sanitizeForDisplay(m.suggestions[m.current].Name)), "edit"),
		huh.NewOption("Skip", "skip"),
	}
	m.active = newBackableSelect("Accept this suggestion?", opts, &m.choice).WithKeyMap(noFilterKeyMap())
	m.active.CancelCmd = tea.Quit
	return m.active.Init()
}

func (m *reviewFlowModel) toEdit() tea.Cmd {
	m.editingName = true
	m.newName = ""
	// A text input: left/right move the cursor, so back is via empty submit.
	m.active = huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title(fmt.Sprintf("New alias name (current: %s):", sanitizeForDisplay(m.suggestions[m.current].Name))).
			Description("Leave empty to go back.").
			Value(&m.newName),
	)).WithTheme(cyberTheme())
	m.active.CancelCmd = tea.Quit
	return m.active.Init()
}

func (m *reviewFlowModel) advance() tea.Cmd {
	m.current++
	if m.current >= len(m.suggestions) {
		return tea.Quit
	}
	return m.toReview()
}

func (m *reviewFlowModel) goBack() tea.Cmd {
	m.current--
	m.decisions[m.current] = reviewDecision{} // clear previous decision
	return m.toReview()
}

func (m *reviewFlowModel) Init() tea.Cmd {
	tw, _, err := term.GetSize(uintptr(os.Stdout.Fd()))
	if err != nil || tw < 40 {
		tw = 80
	}
	if tw > 100 {
		tw = 100
	}
	m.termWidth = tw
	return m.toReview()
}

func (m *reviewFlowModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// On the select screen, ← goes back to the previous suggestion. In the edit
	// text input, ← is left to move the cursor (back there is via empty submit).
	if !m.editingName && m.current > 0 {
		if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyLeft {
			return m, m.goBack()
		}
	}
	updated, cmd := m.active.Update(msg)
	if f, ok := updated.(*huh.Form); ok {
		m.active = f
	}
	switch m.active.State {
	case huh.StateAborted:
		m.aborted = true
		return m, tea.Quit
	case huh.StateCompleted:
		if m.editingName {
			name := strings.TrimSpace(m.newName)
			if name == "" {
				return m, m.toReview() // empty = go back to the select
			}
			m.decisions[m.current] = reviewDecision{accepted: true, name: name}
			return m, m.advance()
		}
		switch m.choice {
		case "accept":
			m.decisions[m.current] = reviewDecision{accepted: true, name: m.suggestions[m.current].Name}
			return m, m.advance()
		case "edit":
			return m, m.toEdit()
		default: // skip
			m.decisions[m.current] = reviewDecision{accepted: false}
			return m, m.advance()
		}
	}
	return m, cmd
}

func (m *reviewFlowModel) View() string {
	if m.active == nil {
		return ""
	}
	if m.editingName {
		return m.active.View()
	}
	// Back to the previous suggestion is available from the second one onward.
	return m.suggestionView() + m.active.View() + footerWithBack(m.active, m.current > 0)
}

// indexedSugg pairs a display index with a suggestion for overview rendering.
type indexedSugg struct {
	idx int
	s   llm.Suggestion
}

// PrintSuggestionsOverview prints the grouped "✨ AKA FOUND N UPGRADES" summary
// before the interactive per-suggestion review begins.
func PrintSuggestionsOverview(suggestions []llm.Suggestion) {
	n := len(suggestions)
	label := "UPGRADES"
	if n == 1 {
		label = "UPGRADE"
	}
	fmt.Println(nameStyle.Render(fmt.Sprintf("✨ AKA FOUND %d %s", n, label)))
	fmt.Println(mutedStyle.Render(strings.Repeat("─", 32)))

	var workflows, oneliners []indexedSugg
	for i, s := range suggestions {
		if s.Kind == "function" {
			workflows = append(workflows, indexedSugg{i + 1, s})
		} else {
			oneliners = append(oneliners, indexedSugg{i + 1, s})
		}
	}

	if len(workflows) > 0 {
		fmt.Println()
		fmt.Println(sectionStyle.Render("WORKFLOWS (MULTI-COMMAND)"))
		for _, is := range workflows {
			printOverviewEntry(is.idx, is.s)
		}
	}
	if len(oneliners) > 0 {
		fmt.Println()
		fmt.Println(sectionStyle.Render("ALIASES (ONE-LINERS)"))
		for _, is := range oneliners {
			printOverviewEntry(is.idx, is.s)
		}
	}
	fmt.Println()
}

func printOverviewEntry(idx int, s llm.Suggestion) {
	prefix := overviewIndexStyle.Render(fmt.Sprintf("❯ [%d]", idx)) + " "
	name := sanitizeForDisplay(s.Name)
	cmdLines := splitTemplate(sanitizeForDisplay(s.Template))

	if s.Kind != "function" || len(cmdLines) == 1 {
		fmt.Println(prefix + nameStyle.Render(name) +
			mutedStyle.Render("  →  ") + commandLineStyle.Render(cmdLines[0]))
		return
	}

	fmt.Println(prefix + nameStyle.Render(name))
	if s.Rationale != "" {
		r := sanitizeForDisplay(s.Rationale)
		if i := strings.IndexAny(r, ".!?"); i >= 0 && i < len(r)-1 {
			r = r[:i+1]
		}
		if len(r) > 100 {
			r = r[:97] + "..."
		}
		fmt.Println(mutedStyle.Render("     " + r))
	}
	for _, c := range cmdLines {
		fmt.Println(commandLineStyle.Render("     │ " + c))
	}
}

// ReviewSuggestions presents suggestions one at a time with Accept/Edit/Skip/Back.
// Navigating between suggestions happens in-place; ← also goes back.
// Returns the accepted suggestions.
func ReviewSuggestions(suggestions []llm.Suggestion) ([]llm.Suggestion, error) {
	if len(suggestions) == 0 {
		fmt.Println(mutedStyle.Render("No suggestions returned by the LLM."))
		return nil, nil
	}
	m := &reviewFlowModel{
		suggestions: suggestions,
		decisions:   make([]reviewDecision, len(suggestions)),
	}
	result, err := tea.NewProgram(m,
		tea.WithOutput(os.Stderr),
		tea.WithContext(context.Background()),
		tea.WithReportFocus(),
	).Run()
	if err != nil {
		return nil, fmt.Errorf("review suggestions: %w", err)
	}
	flow := result.(*reviewFlowModel)
	if flow.aborted {
		return nil, huh.ErrUserAborted
	}
	var accepted []llm.Suggestion
	for i, d := range flow.decisions {
		if d.accepted {
			s := suggestions[i]
			s.Name = d.name
			accepted = append(accepted, s)
			fmt.Println(successBarStyle.Render(fmt.Sprintf("✅ Alias %s accepted", sanitizeForDisplay(s.Name))))
		} else if i < flow.current {
			fmt.Println(mutedStyle.Render(fmt.Sprintf("   ✗ %s — skipped", sanitizeForDisplay(suggestions[i].Name))))
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

// backSentinel is returned by select prompts when the user picks "← Back".
const backSentinel = "__back__"

// backHelpKey is a display-only binding so "← back" appears in the help footer.
// The left arrow is intercepted by the wrapping model before huh sees it, so
// this binding is never matched against input — it exists purely for the footer.
var backHelpKey = key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "back"))

// newBackableSelect builds a single-option select form with huh's own footer
// suppressed, so the caller can render a footer that includes the back hint.
func newBackableSelect(title string, opts []huh.Option[string], value *string) *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().Title(title).Options(opts...).Value(value),
		),
	).WithTheme(cyberTheme()).WithShowHelp(false)
}

// footerWithBack renders the active field's help footer, inserting the "← back"
// hint right after the up/down bindings when withBack is true, so the footer
// reads: up • down • back • filter • submit. For select fields KeyBinds returns
// Up, Down, Left, Right, … so index 2 is the slot just after Down.
func footerWithBack(form *huh.Form, withBack bool) string {
	binds := form.KeyBinds()
	if withBack {
		at := 2
		if at > len(binds) {
			at = len(binds)
		}
		out := make([]key.Binding, 0, len(binds)+1)
		out = append(out, binds[:at]...)
		out = append(out, backHelpKey)
		out = append(out, binds[at:]...)
		binds = out
	}
	h := form.Help()
	return h.ShortHelpView(binds)
}

// backableModel wraps a huh.Form and intercepts the left arrow key so the
// caller can treat it as "go back" without the form consuming it. When
// withBack is true the footer includes "← back"; when false it still shows
// the navigation hint (↑↓ / enter) for consistency with other menus.
type backableModel struct {
	form     *huh.Form
	withBack bool
	wentBack bool
}

func (m backableModel) Init() tea.Cmd { return m.form.Init() }

func (m backableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.withBack {
		if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyLeft {
			m.wentBack = true
			return m, tea.Quit
		}
	}
	updated, cmd := m.form.Update(msg)
	if f, ok := updated.(*huh.Form); ok {
		m.form = f
	}
	return m, cmd
}

func (m backableModel) View() string { return m.form.View() }

// runFormWithBack runs a huh.Form but intercepts the left arrow key as "back".
// Returns wentBack=true when the user pressed ←; the Value pointer is populated
// on normal completion. huh.ErrUserAborted is returned on Ctrl+C / ESC.
func runFormWithBack(form *huh.Form) (wentBack bool, err error) {
	form.SubmitCmd = tea.Quit
	form.CancelCmd = tea.Quit

	result, runErr := tea.NewProgram(
		backableModel{form: form, withBack: true},
		tea.WithOutput(os.Stderr),
		tea.WithContext(context.Background()),
		tea.WithReportFocus(),
	).Run()
	if runErr != nil {
		return false, fmt.Errorf("form: %w", runErr)
	}
	m := result.(backableModel)
	if m.wentBack {
		return true, nil
	}
	if m.form.State == huh.StateAborted {
		return false, huh.ErrUserAborted
	}
	return false, nil
}

// simpleSelectModel is a minimal custom bubbletea select list that renders its
// own footer. It avoids wrapping huh so the footer line is never clipped by
// huh's internal escape sequences confusing bubbletea's line counter.
type simpleSelectModel struct {
	title    string
	opts     []huh.Option[string]
	cursor   int
	withBack bool
	Chosen   string
	Aborted  bool
	WentBack bool
}

func (m *simpleSelectModel) Init() tea.Cmd { return nil }

func (m *simpleSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.Aborted = true
			return m, tea.Quit
		case tea.KeyLeft:
			if m.withBack {
				m.WentBack = true
				return m, tea.Quit
			}
		case tea.KeyUp:
			if m.cursor > 0 {
				m.cursor--
			}
		case tea.KeyDown:
			if m.cursor < len(m.opts)-1 {
				m.cursor++
			}
		case tea.KeyEnter:
			m.Chosen = m.opts[m.cursor].Value
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *simpleSelectModel) View() string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(nameStyle.Render(m.title))
	sb.WriteString("\n\n")
	for i, opt := range m.opts {
		selector := "  "
		if i == m.cursor {
			selector = lipgloss.NewStyle().Foreground(claudeOrange).Render("▶ ")
		}
		var label string
		if i == m.cursor {
			label = lipgloss.NewStyle().Foreground(neonCyan).Render(opt.Key)
		} else {
			label = mutedStyle.Render(opt.Key)
		}
		fmt.Fprintf(&sb, "%s%s\n", selector, label)
	}
	sb.WriteString("\n")
	hint := "↑↓ navigate  •  enter confirm"
	if m.withBack {
		hint += "  •  ← back"
	}
	sb.WriteString(mutedStyle.Render(hint) + "\n")
	return sb.String()
}

// runSelectWithBack renders a single select prompt. When withBack is true the
// left arrow / "← back" footer returns wentBack=true; otherwise it returns the
// chosen option value. desc is an optional description line (empty = none).
func runSelectWithBack(title, desc string, opts []huh.Option[string], withBack bool) (value string, wentBack bool, err error) {
	_ = desc // currently unused; callers pass "" for the censor review
	m := &simpleSelectModel{title: title, opts: opts, withBack: withBack}
	result, runErr := tea.NewProgram(m,
		tea.WithOutput(os.Stderr),
		tea.WithContext(context.Background()),
		tea.WithReportFocus(),
	).Run()
	if runErr != nil {
		return "", false, fmt.Errorf("select: %w", runErr)
	}
	sm := result.(*simpleSelectModel)
	if sm.WentBack {
		return "", true, nil
	}
	if sm.Aborted {
		return "", false, huh.ErrUserAborted
	}
	return sm.Chosen, false, nil
}

// promptProviderSelect shows the provider picker. When withBack is true, the
// left arrow goes back (returning backSentinel) and a "← back" footer hint is
// shown.
func promptProviderSelect(withBack bool) (string, error) {
	opts := make([]huh.Option[string], 0, len(llm.SupportedProviders))
	for _, p := range llm.SupportedProviders {
		opts = append(opts, huh.NewOption(p.Label, p.ID))
	}
	var selected string
	if !withBack {
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().Title("Choose an LLM provider:").Options(opts...).Value(&selected),
			),
		).WithTheme(cyberTheme())
		return selected, form.Run()
	}
	form := newBackableSelect("Choose an LLM provider:", opts, &selected)
	wentBack, err := runFormWithBack(form)
	if err != nil {
		return "", err
	}
	if wentBack {
		return backSentinel, nil
	}
	return selected, nil
}

// promptModelSelect shows the model picker for provider. When withBack is true,
// the left arrow goes back (returning backSentinel) and a "← back" footer hint
// is shown.
func promptModelSelect(provider string, withBack bool) (string, error) {
	models := llm.ModelsForProvider(provider)
	opts := make([]huh.Option[string], 0, len(models))
	for _, m := range models {
		opts = append(opts, huh.NewOption(m.Label, m.ID))
	}
	var selected string
	if !withBack {
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().Title("Choose a model:").Options(opts...).Value(&selected),
			),
		).WithTheme(cyberTheme())
		return selected, form.Run()
	}
	form := newBackableSelect("Choose a model:", opts, &selected)
	wentBack, err := runFormWithBack(form)
	if err != nil {
		return "", err
	}
	if wentBack {
		return backSentinel, nil
	}
	return selected, nil
}

// PromptProvider lets the user pick an LLM provider.
func PromptProvider() (string, error) {
	return promptProviderSelect(false)
}

// PromptModel lets the user pick from the supported models for a provider.
// Returns the selected model ID.
func PromptModel(provider string) (string, error) {
	return promptModelSelect(provider, false)
}

// PromptModelWithBack shows model selection with a "← back" footer hint; the
// left arrow goes back. Returns wentBack=true if the user chose to go back.
func PromptModelWithBack(provider string) (model string, wentBack bool, err error) {
	m, err := promptModelSelect(provider, true)
	if err != nil {
		return "", false, err
	}
	if m == backSentinel {
		return "", true, nil
	}
	return m, false, nil
}

// switchFlowModel runs the set-model flow as a single bubbletea program so
// step transitions (model → provider → new model, and back) happen in-place.
type switchFlowModel struct {
	currentProvider string
	step            int // 0=model, 1=provider, 2=newModel
	modelSel        string
	providerSel     string
	newModelSel     string
	active          *huh.Form
	ResultProvider  string
	ResultModel     string
	aborted         bool
}

const switchSentinel = "__switch_provider__"

func (m *switchFlowModel) toStep(step int) tea.Cmd {
	m.step = step
	switch step {
	case 0:
		m.modelSel = ""
		models := llm.ModelsForProvider(m.currentProvider)
		opts := make([]huh.Option[string], len(models)+1)
		for i, mod := range models {
			opts[i] = huh.NewOption(mod.Label, mod.ID)
		}
		opts[len(models)] = huh.NewOption("Switch to a different provider...", switchSentinel)
		m.active = newBackableSelect("Choose a model:", opts, &m.modelSel)
	case 1:
		m.providerSel = ""
		opts := make([]huh.Option[string], 0, len(llm.SupportedProviders))
		for _, p := range llm.SupportedProviders {
			opts = append(opts, huh.NewOption(p.Label, p.ID))
		}
		m.active = newBackableSelect("Choose an LLM provider:", opts, &m.providerSel)
	case 2:
		m.newModelSel = ""
		models := llm.ModelsForProvider(m.providerSel)
		opts := make([]huh.Option[string], 0, len(models))
		for _, mod := range models {
			opts = append(opts, huh.NewOption(mod.Label, mod.ID))
		}
		m.active = newBackableSelect("Choose a model:", opts, &m.newModelSel)
	}
	m.active.CancelCmd = tea.Quit
	return m.active.Init()
}

func (m *switchFlowModel) Init() tea.Cmd { return m.toStep(0) }

func (m *switchFlowModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.step > 0 {
		if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyLeft {
			return m, m.toStep(m.step - 1)
		}
	}
	updated, cmd := m.active.Update(msg)
	if f, ok := updated.(*huh.Form); ok {
		m.active = f
	}
	switch m.active.State {
	case huh.StateAborted:
		m.aborted = true
		return m, tea.Quit
	case huh.StateCompleted:
		return m, m.complete()
	}
	return m, cmd
}

func (m *switchFlowModel) complete() tea.Cmd {
	switch m.step {
	case 0:
		if m.modelSel == switchSentinel {
			return m.toStep(1)
		}
		m.ResultProvider, m.ResultModel = m.currentProvider, m.modelSel
		return tea.Quit
	case 1:
		return m.toStep(2)
	case 2:
		m.ResultProvider, m.ResultModel = m.providerSel, m.newModelSel
		return tea.Quit
	}
	return tea.Quit
}

func (m *switchFlowModel) View() string {
	if m.active == nil {
		return ""
	}
	// Back is available on every step except the first model picker.
	return m.active.View() + footerWithBack(m.active, m.step > 0)
}

// PromptModelOrSwitchProvider shows models for currentProvider with an extra
// "Switch to a different provider..." option at the bottom. Returns the
// (provider, model) pair the user chose. All step transitions happen in-place
// (single bubbletea program); ← also navigates back.
func PromptModelOrSwitchProvider(currentProvider string) (string, string, error) {
	m := &switchFlowModel{currentProvider: currentProvider}
	result, err := tea.NewProgram(m,
		tea.WithOutput(os.Stderr),
		tea.WithContext(context.Background()),
		tea.WithReportFocus(),
	).Run()
	if err != nil {
		return "", "", fmt.Errorf("set-model: %w", err)
	}
	flow := result.(*switchFlowModel)
	if flow.aborted {
		return "", "", huh.ErrUserAborted
	}
	return flow.ResultProvider, flow.ResultModel, nil
}

// PromptAPIKey prompts the user to enter an API key for the given provider.
// When withBack is true, a hint is shown that leaving the field empty goes back.
// An empty return value (with nil error) means the user chose to go back.
func PromptAPIKey(provider string, withBack bool) (string, error) {
	title, desc := providerKeyPrompt(provider)
	if withBack {
		desc += "\nLeave empty to go back."
	}
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

// PrintWarning prints a prominent warning line to stdout.
func PrintWarning(msg string) {
	fmt.Println(removeStyle.Render("⚠  " + msg))
}

// PrintSuccess prints a success message.
func PrintSuccess(msg string) {
	fmt.Println(successStyle.Render(msg))
}

// histScopeOptKind enumerates the options in the history scope picker.
type histScopeOptKind int

const (
	histOptKindFull histScopeOptKind = iota
	histOptKindDiff
	histOptKindCustom
	histOptKindAbort
)

// historyScopeModel is a single-screen history scope picker. All options are
// visible simultaneously; the "last N commands" row embeds an inline text input
// so the number is editable without any step transition or screen change.
// Limit: 0 = full history, -1 = diff, N > 0 = last N commands.
type historyScopeModel struct {
	totalCount int
	diffCount  int

	opts   []histScopeOptKind
	cursor int
	input  textinput.Model

	Limit   int
	Aborted bool
}

func newHistoryScopeModel(totalCount, diffCount, defaultN int) *historyScopeModel {
	ti := textinput.New()
	ti.SetValue(strconv.Itoa(defaultN))
	ti.Width = 6
	ti.CharLimit = 8
	ti.Prompt = ""
	ti.TextStyle = lipgloss.NewStyle().Foreground(neonCyan)
	ti.CursorStyle = lipgloss.NewStyle().Foreground(claudeOrange)

	opts := []histScopeOptKind{histOptKindFull}
	if diffCount > 40 && diffCount < totalCount {
		opts = append(opts, histOptKindDiff)
	}
	opts = append(opts, histOptKindCustom, histOptKindAbort)

	return &historyScopeModel{
		totalCount: totalCount,
		diffCount:  diffCount,
		opts:       opts,
		input:      ti,
	}
}

func (m *historyScopeModel) currentOpt() histScopeOptKind { return m.opts[m.cursor] }

func (m *historyScopeModel) Init() tea.Cmd { return nil }

func (m *historyScopeModel) selectCurrent() (tea.Model, tea.Cmd) {
	switch m.currentOpt() {
	case histOptKindFull:
		m.Limit = 0
	case histOptKindDiff:
		m.Limit = -1
	case histOptKindCustom:
		n, err := strconv.Atoi(strings.TrimSpace(m.input.Value()))
		if err != nil || n <= 0 {
			return m, nil // invalid number — keep waiting
		}
		m.Limit = n
	default: // abort
		m.Aborted = true
	}
	return m, tea.Quit
}

func (m *historyScopeModel) moveCursor(delta int) tea.Cmd {
	next := m.cursor + delta
	if next < 0 || next >= len(m.opts) {
		return nil
	}
	m.cursor = next
	if m.currentOpt() == histOptKindCustom {
		return m.input.Focus()
	}
	m.input.Blur()
	return nil
}

func (m *historyScopeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.Aborted = true
			return m, tea.Quit
		case tea.KeyLeft:
			// ← aborts on non-input rows; on the custom row it moves the cursor within the text.
			if m.currentOpt() != histOptKindCustom {
				m.Aborted = true
				return m, tea.Quit
			}
		case tea.KeyUp:
			return m, m.moveCursor(-1)
		case tea.KeyDown:
			return m, m.moveCursor(1)
		case tea.KeyEnter:
			return m.selectCurrent()
		}
	}
	// Remaining messages (textinput.Blink and unhandled keys) go to the inline
	// input when the custom row is active.
	if m.currentOpt() == histOptKindCustom {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *historyScopeModel) View() string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(nameStyle.Render("How much history to send to the LLM?"))
	sb.WriteString("\n\n")

	onCustom := m.currentOpt() == histOptKindCustom

	for i, opt := range m.opts {
		selected := i == m.cursor
		selector := "  "
		if selected {
			selector = lipgloss.NewStyle().Foreground(claudeOrange).Render("▶ ")
		}

		var label string
		switch opt {
		case histOptKindFull:
			raw := fmt.Sprintf("Send full history (%d commands)", m.totalCount)
			if selected {
				label = lipgloss.NewStyle().Foreground(neonCyan).Render(raw)
			} else {
				label = mutedStyle.Render(raw)
			}
		case histOptKindDiff:
			raw := fmt.Sprintf("Send %d new commands (since last run)", m.diffCount)
			if selected {
				label = lipgloss.NewStyle().Foreground(neonCyan).Render(raw)
			} else {
				label = mutedStyle.Render(raw)
			}
		case histOptKindCustom:
			raw := fmt.Sprintf("Send last %s commands", m.input.Value())
			if selected {
				label = lipgloss.NewStyle().Foreground(neonCyan).Render(raw)
			} else {
				label = mutedStyle.Render(raw)
			}
		case histOptKindAbort:
			if selected {
				label = lipgloss.NewStyle().Foreground(neonCyan).Render("Abort")
			} else {
				label = mutedStyle.Render("Abort")
			}
		}

		sb.WriteString(selector + label + "\n")
	}

	sb.WriteString("\n")
	if onCustom {
		fmt.Fprintf(&sb, "  %s %s\n\n",
			lipgloss.NewStyle().Foreground(claudeOrange).Render("How many?"),
			m.input.View(),
		)
	}
	footer := "↑↓ navigate  •  enter confirm  •  ← abort"
	if onCustom {
		footer = "↑↓ navigate  •  type number  •  enter confirm"
	}
	sb.WriteString(mutedStyle.Render(footer) + "\n")
	return sb.String()
}

// PromptHistoryScope shows a single-screen history scope picker.
// Returns limit (0=full history, -1=diff, N>0=last N commands), aborted, and any error.
func PromptHistoryScope(totalCount, diffCount, defaultN int) (limit int, aborted bool, err error) {
	m := newHistoryScopeModel(totalCount, diffCount, defaultN)
	result, runErr := tea.NewProgram(m,
		tea.WithOutput(os.Stderr),
		tea.WithContext(context.Background()),
		tea.WithReportFocus(),
	).Run()
	if runErr != nil {
		return 0, false, fmt.Errorf("history scope: %w", runErr)
	}
	fm := result.(*historyScopeModel)
	return fm.Limit, fm.Aborted, nil
}

// noFilterKeyMap returns a keymap with the "/" filter key disabled, for
// short selects where search is unnecessary and the keypress would be confusing.
func noFilterKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Select.Filter.SetEnabled(false)
	return km
}

func cyberTheme() *huh.Theme {
	t := huh.ThemeBase()
	t.Focused.Title = t.Focused.Title.Foreground(brightWhite)
	t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(claudeOrange).SetString("▶ ")
	t.Focused.SelectedOption = lipgloss.NewStyle().Foreground(neonCyan)
	t.Focused.TextInput.Prompt = lipgloss.NewStyle().Foreground(claudeOrange).SetString("> ")
	t.Focused.TextInput.Cursor = lipgloss.NewStyle().Foreground(neonCyan)
	t.Focused.FocusedButton = lipgloss.NewStyle().Background(neonCyan).Foreground(darkGray).Padding(0, 1)
	t.Focused.BlurredButton = lipgloss.NewStyle().Foreground(lightGray).Padding(0, 1)
	return t
}
