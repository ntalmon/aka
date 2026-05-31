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
	tea "github.com/charmbracelet/bubbletea"
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

// ReviewCensored renders a diff of original vs censored commands and asks the
// user how to proceed. Returns the entries to send (possibly edited); back is
// true when the user navigated back (only possible when withBack is true), and
// (nil, false, nil) means the user aborted.
func ReviewCensored(original, censored []history.Entry, withBack bool) (entries []history.Entry, back bool, err error) {
	printCensorDiff(original, censored)

	const (
		optSend  = "send"
		optEdit  = "edit"
		optAbort = "abort"
	)
	opts := []huh.Option[string]{
		huh.NewOption(fmt.Sprintf("Yes, send %d commands", len(censored)), optSend),
		huh.NewOption("Edit censored commands manually first", optEdit),
		huh.NewOption("No, abort", optAbort),
	}
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

func (m *reviewFlowModel) suggestionView() string {
	s := m.suggestions[m.current]
	boxW := m.termWidth - 4
	if boxW < 40 {
		boxW = 40
	}
	bs := suggestionBoxStyle.Width(boxW)

	pattern := patternLabelStyle.Render(fmt.Sprintf("[%d/%d] PATTERN FOUND:", m.current+1, len(m.suggestions)))
	tmpl := templateStyle.Render(sanitizeForDisplay(s.Template))
	aliasLbl := aliasLabelStyle.Render("↳ SUGGESTED ALIAS:")
	aliasVal := aliasCodeStyle.Render(sanitizeForDisplay(buildInvocation(s)))

	parts := []string{
		fmt.Sprintf("%s  %s", pattern, tmpl),
		fmt.Sprintf("%s  %s", aliasLbl, aliasVal),
	}
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
	m.active = newBackableSelect("Accept this suggestion?", opts, &m.choice)
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
// caller can treat it as "go back" without the form consuming it. Its footer
// always shows the "← back" hint.
type backableModel struct {
	form     *huh.Form
	wentBack bool
}

func (m backableModel) Init() tea.Cmd { return m.form.Init() }

func (m backableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyLeft {
		m.wentBack = true
		return m, tea.Quit
	}
	updated, cmd := m.form.Update(msg)
	if f, ok := updated.(*huh.Form); ok {
		m.form = f
	}
	return m, cmd
}

func (m backableModel) View() string { return m.form.View() + footerWithBack(m.form, true) }

// runFormWithBack runs a huh.Form but intercepts the left arrow key as "back".
// Returns wentBack=true when the user pressed ←; the Value pointer is populated
// on normal completion. huh.ErrUserAborted is returned on Ctrl+C / ESC.
func runFormWithBack(form *huh.Form) (wentBack bool, err error) {
	form.SubmitCmd = tea.Quit
	form.CancelCmd = tea.Quit

	result, runErr := tea.NewProgram(
		backableModel{form: form},
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

// runSelectWithBack renders a single select prompt. When withBack is true the
// left arrow / "← back" footer returns wentBack=true; otherwise it returns the
// chosen option value. desc is an optional description line (empty = none).
func runSelectWithBack(title, desc string, opts []huh.Option[string], withBack bool) (value string, wentBack bool, err error) {
	var selected string
	sel := huh.NewSelect[string]().Title(title).Options(opts...).Value(&selected)
	if desc != "" {
		sel = sel.Description(desc)
	}
	if !withBack {
		form := huh.NewForm(huh.NewGroup(sel)).WithTheme(cyberTheme())
		return selected, false, form.Run()
	}
	form := huh.NewForm(huh.NewGroup(sel)).WithTheme(cyberTheme()).WithShowHelp(false)
	wentBack, err = runFormWithBack(form)
	return selected, wentBack, err
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

// PrintSuccess prints a success message.
func PrintSuccess(msg string) {
	fmt.Println(successStyle.Render(msg))
}

// Scan-scope step indices.
const (
	scopeHistory  = iota // pick history range (only when historyApplies)
	scopeMax             // max_history limit choice
	scopeMaxInput        // "change limit" text input
)

// Scan-scope option values.
const (
	scopeOptNew    = "new"
	scopeOptFull   = "full"
	scopeOptAbort  = "abort"
	scopeOptKeep   = "keep"
	scopeOptAll    = "all"
	scopeOptChange = "change"
)

// scanScopeModel runs the history-scope and max-history prompts as a single
// bubbletea program so back navigation between them happens in-place (rather
// than reprinting a fresh menu below the previous one). The max-history prompt
// applies only when the normalized count for the chosen mode exceeds the limit,
// which is why the model needs countForMode to recompute on each transition.
type scanScopeModel struct {
	historyApplies  bool
	historyNewCount int
	totalCount      int
	maxHistory      int
	countForMode    func(mode string) int

	step           int
	active         *huh.Form
	historyChoice  string
	maxChoice      string
	maxInput       string
	normalizedSeen int // count shown in the max prompt title

	// results
	historyMode string
	maxLimit    int // -1 = max prompt N/A; 0 = send all; >0 = cap
	maxSave     bool
	aborted     bool
}

func (m *scanScopeModel) toHistory() tea.Cmd {
	m.step = scopeHistory
	m.historyChoice = ""
	opts := []huh.Option[string]{
		huh.NewOption(fmt.Sprintf("Send full history (%d commands)", m.totalCount), scopeOptFull),
		huh.NewOption("Abort", scopeOptAbort),
	}
	title := fmt.Sprintf("Only %d new command(s) since last run.", m.historyNewCount)
	if m.historyNewCount >= 50 {
		opts = append([]huh.Option[string]{
			huh.NewOption(fmt.Sprintf("Scan %d new command(s) only", m.historyNewCount), scopeOptNew),
		}, opts...)
		title = fmt.Sprintf("Only %d new command(s) since last run — not many to work with.", m.historyNewCount)
	}
	m.active = newBackableSelect(title, opts, &m.historyChoice)
	m.active.CancelCmd = tea.Quit
	return m.active.Init()
}

// enterMax decides whether the max-history prompt applies for the chosen mode.
// When it does not, the model is done and quits; otherwise it shows the prompt.
func (m *scanScopeModel) enterMax() tea.Cmd {
	count := m.countForMode(m.historyMode)
	if m.maxHistory <= 0 || count <= m.maxHistory {
		m.maxLimit = -1
		return tea.Quit
	}
	m.normalizedSeen = count
	return m.toMax()
}

func (m *scanScopeModel) toMax() tea.Cmd {
	m.step = scopeMax
	m.maxChoice = ""
	opts := []huh.Option[string]{
		huh.NewOption(fmt.Sprintf("Continue with %d most recent (current limit)", m.maxHistory), scopeOptKeep),
		huh.NewOption(fmt.Sprintf("Send all %d commands this run", m.normalizedSeen), scopeOptAll),
		huh.NewOption("Change max_history limit", scopeOptChange),
	}
	title := fmt.Sprintf("%d commands after normalization, but max_history is set to %d.", m.normalizedSeen, m.maxHistory)
	m.active = huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(title).
			Description("Only the most recent commands will be sent unless you adjust the limit.").
			Options(opts...).
			Value(&m.maxChoice),
	)).WithTheme(cyberTheme()).WithShowHelp(false)
	m.active.CancelCmd = tea.Quit
	return m.active.Init()
}

func (m *scanScopeModel) toMaxInput() tea.Cmd {
	m.step = scopeMaxInput
	m.maxInput = ""
	m.active = huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("New max_history limit:").
			Description("Maximum number of commands to send to the LLM (saved to config).\nLeave empty to go back.").
			Value(&m.maxInput).
			Validate(func(s string) error {
				s = strings.TrimSpace(s)
				if s == "" {
					return nil // empty = go back
				}
				if n, err := strconv.Atoi(s); err != nil || n <= 0 {
					return fmt.Errorf("must be a positive integer")
				}
				return nil
			}),
	)).WithTheme(cyberTheme())
	m.active.CancelCmd = tea.Quit
	return m.active.Init()
}

func (m *scanScopeModel) Init() tea.Cmd {
	if m.historyApplies {
		return m.toHistory()
	}
	return m.enterMax()
}

func (m *scanScopeModel) advance() tea.Cmd {
	switch m.step {
	case scopeHistory:
		switch m.historyChoice {
		case scopeOptAbort:
			m.aborted = true
			return tea.Quit
		case scopeOptNew:
			m.historyMode = "new"
		default:
			m.historyMode = "full"
		}
		return m.enterMax()
	case scopeMax:
		switch m.maxChoice {
		case scopeOptAll:
			m.maxLimit = 0
			return tea.Quit
		case scopeOptChange:
			return m.toMaxInput()
		default: // keep current limit
			m.maxLimit = m.maxHistory
			return tea.Quit
		}
	case scopeMaxInput:
		s := strings.TrimSpace(m.maxInput)
		if s == "" {
			return m.toMax() // empty = back to the max choice
		}
		n, _ := strconv.Atoi(s) // validated in the input
		m.maxLimit = n
		m.maxSave = true
		return tea.Quit
	}
	return tea.Quit
}

// back handles ← on a select step: navigate to the previous step, or — when
// this is the first interactive prompt — back out of the scan entirely.
func (m *scanScopeModel) back() tea.Cmd {
	if m.step == scopeMax && m.historyApplies {
		return m.toHistory()
	}
	// First interactive prompt: back exits the command.
	m.aborted = true
	return tea.Quit
}

func (m *scanScopeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// ← navigates back on the select steps. On the text input ← moves the
	// cursor, so back there is via empty submit (handled in advance).
	if m.step != scopeMaxInput {
		if k, ok := msg.(tea.KeyMsg); ok && k.Type == tea.KeyLeft {
			return m, m.back()
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
		return m, m.advance()
	}
	return m, cmd
}

func (m *scanScopeModel) View() string {
	if m.active == nil {
		return ""
	}
	if m.step == scopeMaxInput {
		return m.active.View() // input keeps its own footer
	}
	// Both select steps offer back (← back): the max step returns to the
	// history step, and the first step backs out of the scan.
	return m.active.View() + footerWithBack(m.active, true)
}

// PromptScanScope runs the history-scope and max-history prompts as one program
// with in-place back navigation. countForMode returns the normalized command
// count for a given history mode ("new"/"full"/"") so the model can decide
// whether the max prompt applies. Returns the chosen history mode (empty when
// the history prompt did not apply), the max limit (-1 = max prompt did not
// apply, 0 = send all, >0 = cap), whether to persist the limit, and whether the
// user aborted.
func PromptScanScope(historyApplies bool, historyNewCount, totalCount, maxHistory int, countForMode func(mode string) int) (historyMode string, maxLimit int, maxSave, aborted bool, err error) {
	m := &scanScopeModel{
		historyApplies:  historyApplies,
		historyNewCount: historyNewCount,
		totalCount:      totalCount,
		maxHistory:      maxHistory,
		countForMode:    countForMode,
		maxLimit:        -1,
	}
	result, runErr := tea.NewProgram(m,
		tea.WithOutput(os.Stderr),
		tea.WithContext(context.Background()),
		tea.WithReportFocus(),
	).Run()
	if runErr != nil {
		return "", -1, false, false, fmt.Errorf("scan scope: %w", runErr)
	}
	fm := result.(*scanScopeModel)
	return fm.historyMode, fm.maxLimit, fm.maxSave, fm.aborted, nil
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
