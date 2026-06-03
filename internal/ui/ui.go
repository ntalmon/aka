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

// renderCensorDiff returns a colored diff string of commands modified by the censor pass.
func renderCensorDiff(original, censored []history.Entry) string {
	const maxLines = 50
	var sb strings.Builder
	lines := 0
	changed := 0
	for i, orig := range original {
		if i >= len(censored) {
			break
		}
		if orig.Command != censored[i].Command {
			changed++
			if lines+2 <= maxLines {
				sb.WriteString(removeStyle.Render("- "+orig.Command) + "\n")
				sb.WriteString(addStyle.Render("+ "+censored[i].Command) + "\n")
				lines += 2
			}
		}
	}
	if changed == 0 {
		sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(neonGreen).Render("  ✓ No sensitive data detected.") + "\n")
	} else {
		shown := lines / 2
		suffix := ""
		if shown < changed {
			suffix = fmt.Sprintf(" (%d more not shown)", changed-shown)
		}
		sb.WriteString(mutedStyle.Render(fmt.Sprintf("  %d of %d command(s) modified by censor.%s", changed, len(original), suffix)) + "\n")
	}
	return sb.String()
}

// PrintCensorDiff prints a diff of commands modified by the censor pass.
// Unchanged commands are omitted; changed ones show the original in red and
// the censored replacement in green.
func PrintCensorDiff(original, censored []history.Entry) {
	fmt.Print(renderCensorDiff(original, censored))
	fmt.Println()
}

// ReviewCensored asks whether to send the censored commands to the LLM.
// Returns the entries to send (possibly edited); back is true when the user
// navigated back (only possible when withBack is true), and (nil, false, nil)
// means the user aborted.
func ReviewCensored(original, censored []history.Entry, withBack bool) (entries []history.Entry, back bool, err error) {
	const (
		optSend    = "send"
		optEdit    = "edit"
		optSendRaw = "send_raw"
		optAbort   = "abort"
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
		opts = append(opts, huh.NewOption("Send without censoring (not recommended)", optSendRaw))
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
	case optSendRaw:
		PrintWarning("No censoring — raw commands sent to LLM. May include secrets or API keys.")
		return original, false, nil
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

// reviewState enumerates the active screen within the suggestion review flow.
type reviewState int

const (
	reviewStateList   reviewState = iota // navigable list of all suggestions
	reviewStateDetail                    // per-suggestion sub-menu (toggle/edit/back)
	reviewStateEdit                      // inline name editor
)

// reviewListModel is the bubbletea model for the suggestion review screen.
// All interactions run inside a single program so transitions are in-place.
type reviewListModel struct {
	suggestions  []llm.Suggestion
	selected     []bool
	names        []string
	cursor       int // 0..len(suggestions); len(suggestions) == "Apply & exit" row
	state        reviewState
	detailCursor int // cursor within the detail sub-menu (0=toggle, 1=edit, 2=back)
	editInput    textinput.Model
	termWidth    int
	Aborted      bool
	Done         bool
}

func newReviewListModel(suggestions []llm.Suggestion) *reviewListModel {
	names := make([]string, len(suggestions))
	for i, s := range suggestions {
		names[i] = s.Name
	}
	return &reviewListModel{
		suggestions: suggestions,
		selected:    make([]bool, len(suggestions)),
		names:       names,
	}
}

func (m *reviewListModel) selectedCount() int {
	n := 0
	for _, s := range m.selected {
		if s {
			n++
		}
	}
	return n
}

func (m *reviewListModel) isOnApply() bool { return m.cursor == len(m.suggestions) }

func (m *reviewListModel) Init() tea.Cmd {
	tw, _, err := term.GetSize(uintptr(os.Stdout.Fd()))
	if err != nil || tw < 40 {
		tw = 80
	}
	if tw > 100 {
		tw = 100
	}
	m.termWidth = tw
	return nil
}

func (m *reviewListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.state {
	case reviewStateList:
		return m.updateList(msg)
	case reviewStateDetail:
		return m.updateDetail(msg)
	case reviewStateEdit:
		return m.updateEdit(msg)
	}
	return m, nil
}

func (m *reviewListModel) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.Aborted = true
			return m, tea.Quit
		case tea.KeyUp:
			if m.cursor > 0 {
				m.cursor--
			}
		case tea.KeyDown:
			if m.cursor < len(m.suggestions) {
				m.cursor++
			}
		case tea.KeyEnter:
			if m.isOnApply() {
				m.Done = true
				return m, tea.Quit
			}
			m.state = reviewStateDetail
			m.detailCursor = 0
		}
	}
	return m, nil
}

func (m *reviewListModel) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.Aborted = true
			return m, tea.Quit
		case tea.KeyLeft:
			m.state = reviewStateList
		case tea.KeyUp:
			if m.detailCursor > 0 {
				m.detailCursor--
			}
		case tea.KeyDown:
			if m.detailCursor < 3 {
				m.detailCursor++
			}
		case tea.KeySpace:
			if m.detailCursor == 0 {
				m.selected[m.cursor] = !m.selected[m.cursor]
			}
		case tea.KeyEnter:
			switch m.detailCursor {
			case 0: // toggle — Enter also works here
				m.selected[m.cursor] = !m.selected[m.cursor]
			case 1: // edit name
				ti := textinput.New()
				ti.SetValue(m.names[m.cursor])
				ti.Width = 30
				ti.CharLimit = 100
				ti.Prompt = ""
				ti.TextStyle = lipgloss.NewStyle().Foreground(neonCyan)
				ti.Cursor.Style = lipgloss.NewStyle().Foreground(claudeOrange)
				m.editInput = ti
				m.state = reviewStateEdit
				return m, m.editInput.Focus()
			case 2: // apply & exit
				m.Done = true
				return m, tea.Quit
			case 3: // back
				m.state = reviewStateList
			}
		}
	}
	return m, nil
}

func (m *reviewListModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.Type {
		case tea.KeyCtrlC:
			m.Aborted = true
			return m, tea.Quit
		case tea.KeyEsc:
			m.state = reviewStateDetail
			return m, nil
		case tea.KeyEnter:
			if name := strings.TrimSpace(m.editInput.Value()); name != "" {
				m.names[m.cursor] = name
			}
			m.state = reviewStateDetail
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.editInput, cmd = m.editInput.Update(msg)
	return m, cmd
}

func (m *reviewListModel) View() string {
	switch m.state {
	case reviewStateList:
		return m.viewList()
	case reviewStateDetail:
		return m.viewDetail()
	case reviewStateEdit:
		return m.viewEdit()
	}
	return ""
}

func (m *reviewListModel) renderListRows(activeCursor int) string {
	var sb strings.Builder
	for i := range m.suggestions {
		onRow := i == activeCursor
		check := "[ ]"
		checkSty := mutedStyle
		if m.selected[i] {
			check = "[✓]"
			checkSty = addStyle
		}
		selector := "  "
		if onRow {
			selector = lipgloss.NewStyle().Foreground(claudeOrange).Render("▶ ")
		}
		name := sanitizeForDisplay(m.names[i])
		var nameLabel string
		if onRow {
			nameLabel = lipgloss.NewStyle().Foreground(neonCyan).Render(name)
		} else {
			nameLabel = mutedStyle.Render(name)
		}
		fmt.Fprintf(&sb, "%s%s %s\n", selector, checkSty.Render(check), nameLabel)
	}
	return sb.String()
}

func (m *reviewListModel) renderApplyRow(isActive bool) string {
	n := m.selectedCount()
	label := fmt.Sprintf("→  Apply %d suggestion(s) & exit", n)
	if isActive {
		return lipgloss.NewStyle().Foreground(claudeOrange).Render("▶ ") +
			lipgloss.NewStyle().Foreground(neonCyan).Bold(true).Render(label) + "\n"
	}
	return "  " + nameStyle.Render(label) + "\n"
}

func (m *reviewListModel) viewList() string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(nameStyle.Render("Review suggestions:"))
	sb.WriteString("\n\n")
	sb.WriteString(m.renderListRows(m.cursor))
	sb.WriteString("  " + mutedStyle.Render(strings.Repeat("─", 28)) + "\n")
	sb.WriteString(m.renderApplyRow(m.isOnApply()))
	if !m.isOnApply() {
		sb.WriteString("\n")
		sb.WriteString(m.suggestionDetail(m.cursor))
	}
	sb.WriteString("\n")
	sb.WriteString(mutedStyle.Render("↑↓ navigate  •  enter open  •  esc abort"))
	sb.WriteString("\n")
	return sb.String()
}

func (m *reviewListModel) viewDetail() string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(nameStyle.Render("Review suggestions:"))
	sb.WriteString("\n\n")
	sb.WriteString(m.renderListRows(m.cursor))
	sb.WriteString("  " + mutedStyle.Render(strings.Repeat("─", 28)) + "\n")
	sb.WriteString(m.renderApplyRow(false))
	sb.WriteString("\n")
	sb.WriteString(m.suggestionDetail(m.cursor))
	sb.WriteString("\n")

	toggleLabel := "[ ] Press space to add suggestion"
	if m.selected[m.cursor] {
		toggleLabel = "[✓] Press space to remove suggestion"
	}
	detailOpts := []string{
		toggleLabel,
		fmt.Sprintf("Edit name  (current: %s)", sanitizeForDisplay(m.names[m.cursor])),
		fmt.Sprintf("Apply %d suggestion(s) & exit", m.selectedCount()),
		"Back",
	}
	for i, opt := range detailOpts {
		active := i == m.detailCursor
		selector := "  "
		if active {
			selector = lipgloss.NewStyle().Foreground(claudeOrange).Render("▶ ")
		}
		var label string
		switch {
		case active && i == 0 && m.selected[m.cursor]:
			label = addStyle.Render(opt)
		case active:
			label = lipgloss.NewStyle().Foreground(neonCyan).Render(opt)
		case i == 0 && m.selected[m.cursor]:
			label = addStyle.Render(opt)
		default:
			label = mutedStyle.Render(opt)
		}
		fmt.Fprintf(&sb, "%s%s\n", selector, label)
	}
	sb.WriteString("\n")
	footer := "↑↓ navigate  •  enter confirm  •  ← back"
	if m.detailCursor == 0 {
		footer = "↑↓ navigate  •  space / enter toggle  •  ← back"
	}
	sb.WriteString(mutedStyle.Render(footer))
	sb.WriteString("\n")
	return sb.String()
}

func (m *reviewListModel) viewEdit() string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(nameStyle.Render(fmt.Sprintf("Rename %s:", sanitizeForDisplay(m.suggestions[m.cursor].Name))))
	sb.WriteString("\n\n")
	fmt.Fprintf(&sb, "  %s %s\n",
		lipgloss.NewStyle().Foreground(claudeOrange).Render(">"),
		m.editInput.View(),
	)
	sb.WriteString("\n")
	sb.WriteString(mutedStyle.Render("enter confirm  •  esc cancel"))
	sb.WriteString("\n")
	return sb.String()
}

func (m *reviewListModel) suggestionDetail(idx int) string {
	s := m.suggestions[idx]
	boxW := m.termWidth - 4
	if boxW < 40 {
		boxW = 40
	}
	bs := suggestionBoxStyle.Width(boxW)
	header := nameStyle.Render(sanitizeForDisplay(m.names[idx]))
	cmdLines := splitTemplate(sanitizeForDisplay(s.Template))
	parts := []string{header}
	for _, c := range cmdLines {
		parts = append(parts, commandLineStyle.Render("  │ "+c))
	}
	invS := s
	invS.Name = m.names[idx]
	parts = append(parts, aliasLabelStyle.Render("↳ runs as:")+"  "+
		aliasCodeStyle.Render(sanitizeForDisplay(buildInvocation(invS))))
	if s.Rationale != "" {
		r := sanitizeForDisplay(s.Rationale)
		if i := strings.IndexAny(r, ".!?"); i >= 0 && i < len(r)-1 {
			r = r[:i+1]
		}
		if len(r) > 120 {
			r = r[:117] + "..."
		}
		parts = append(parts, mutedStyle.Render(r))
	}
	return bs.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

// indexedSugg pairs a display index with a suggestion for overview rendering.
type indexedSugg struct {
	idx int
	s   llm.Suggestion
}

// PrintSuggestionsOverview prints the grouped "✨ AKA FOUND N SUGGESTIONS" summary
// before the interactive per-suggestion review begins.
func PrintSuggestionsOverview(suggestions []llm.Suggestion) {
	n := len(suggestions)
	label := "SUGGESTIONS"
	if n == 1 {
		label = "SUGGESTION"
	}
	fmt.Println(nameStyle.Render(fmt.Sprintf("✨ AKA FOUND %d %s", n, label)))
	fmt.Println(mutedStyle.Render(strings.Repeat("─", 32)))

	var workflows, oneliners []indexedSugg
	for _, s := range suggestions {
		if s.Kind == "function" && len(splitTemplate(s.Template)) > 1 {
			workflows = append(workflows, indexedSugg{0, s})
		} else {
			oneliners = append(oneliners, indexedSugg{0, s})
		}
	}
	displayIdx := 1
	for i := range workflows {
		workflows[i].idx = displayIdx
		displayIdx++
	}
	for i := range oneliners {
		oneliners[i].idx = displayIdx
		displayIdx++
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

// ReviewSuggestions presents all suggestions in a navigable list. The user
// toggles each suggestion on/off, edits names, and applies via "Apply & exit".
// Returns the accepted suggestions.
func ReviewSuggestions(suggestions []llm.Suggestion) ([]llm.Suggestion, error) {
	if len(suggestions) == 0 {
		fmt.Println(mutedStyle.Render("No suggestions returned by the LLM."))
		return nil, nil
	}
	m := newReviewListModel(suggestions)
	result, err := tea.NewProgram(m,
		tea.WithOutput(os.Stderr),
		tea.WithContext(context.Background()),
		tea.WithReportFocus(),
	).Run()
	if err != nil {
		return nil, fmt.Errorf("review suggestions: %w", err)
	}
	flow := result.(*reviewListModel)
	if flow.Aborted {
		return nil, huh.ErrUserAborted
	}
	var accepted []llm.Suggestion
	for i, sel := range flow.selected {
		if sel {
			s := suggestions[i]
			s.Name = flow.names[i]
			accepted = append(accepted, s)
			fmt.Println(successBarStyle.Render(fmt.Sprintf("✅ Alias %s accepted", sanitizeForDisplay(s.Name))))
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
	Done    bool // set when a valid selection was made (not abort)
}

func newHistoryScopeModel(totalCount, diffCount, defaultN int) *historyScopeModel {
	ti := textinput.New()
	ti.SetValue(strconv.Itoa(defaultN))
	ti.Width = 6
	ti.CharLimit = 8
	ti.Prompt = ""
	ti.TextStyle = lipgloss.NewStyle().Foreground(neonCyan)
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(claudeOrange)

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
		m.Done = true
	case histOptKindDiff:
		m.Limit = -1
		m.Done = true
	case histOptKindCustom:
		n, err := strconv.Atoi(strings.TrimSpace(m.input.Value()))
		if err != nil || n <= 0 {
			return m, nil // invalid number — keep waiting
		}
		m.Limit = n
		m.Done = true
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

// scanFlowStep is the active step in the combined scope+censor flow.
type scanFlowStep int

const (
	sfStepScope scanFlowStep = iota
	sfStepCensor
)

// sfCensorOpt is one option in the censor review menu.
type sfCensorOpt struct {
	label string
	val   string // "send", "edit", "send_raw", "abort"
}

// scanFlowModel drives the interactive scope picker → censor review flow as a
// single bubbletea program so back navigation (← on the censor step) is in-place.
type scanFlowModel struct {
	rawEntries  []history.Entry
	diffEntries []history.Entry
	totalCount  int
	diffCount   int
	defaultN    int
	computeFn   func([]history.Entry) (normalized, censored []history.Entry)

	step     scanFlowStep
	scopeMdl *historyScopeModel

	normalized   []history.Entry
	censored     []history.Entry
	diffText     string
	censorOpts   []sfCensorOpt
	censorCursor int

	Aborted      bool
	WantsEdit    bool
	WantsSendRaw bool
	ToSend       []history.Entry
	Err          error
}

func (m *scanFlowModel) Init() tea.Cmd {
	m.scopeMdl = newHistoryScopeModel(m.totalCount, m.diffCount, m.defaultN)
	return m.scopeMdl.Init()
}

func (m *scanFlowModel) transitionToCensor() tea.Cmd {
	var selected []history.Entry
	switch m.scopeMdl.Limit {
	case 0:
		selected = m.rawEntries
	case -1:
		selected = m.diffEntries
	default:
		lim := m.scopeMdl.Limit
		if lim >= m.totalCount {
			selected = m.rawEntries
		} else {
			selected = m.rawEntries[m.totalCount-lim:]
		}
	}

	norm, cens := m.computeFn(selected)
	if len(norm) == 0 {
		m.Err = fmt.Errorf("no commands found after normalization")
		return tea.Quit
	}
	m.normalized = norm
	m.censored = cens

	anyCensored := false
	for i := range norm {
		if i < len(cens) && norm[i].Command != cens[i].Command {
			anyCensored = true
			break
		}
	}

	m.diffText = mutedStyle.Render(fmt.Sprintf("  └─ Reading %d commands...", len(norm))) + "\n" + renderCensorDiff(norm, cens)

	m.censorOpts = []sfCensorOpt{
		{fmt.Sprintf("Yes, send %d commands", len(cens)), "send"},
	}
	if anyCensored {
		m.censorOpts = append(m.censorOpts,
			sfCensorOpt{"Edit censored commands manually first", "edit"},
			sfCensorOpt{"Send without censoring (not recommended)", "send_raw"},
		)
	}
	m.censorOpts = append(m.censorOpts, sfCensorOpt{"No, abort", "abort"})
	m.censorCursor = 0
	m.step = sfStepCensor
	return nil
}

func (m *scanFlowModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.step {
	case sfStepScope:
		return m.updateScope(msg)
	case sfStepCensor:
		return m.updateCensor(msg)
	}
	return m, nil
}

func (m *scanFlowModel) updateScope(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.scopeMdl.Update(msg)
	m.scopeMdl = updated.(*historyScopeModel)
	if m.scopeMdl.Aborted {
		m.Aborted = true
		return m, tea.Quit
	}
	if m.scopeMdl.Done {
		// Discard the tea.Quit from the scope model and transition instead.
		return m, m.transitionToCensor()
	}
	return m, cmd
}

func (m *scanFlowModel) updateCensor(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.Aborted = true
			return m, tea.Quit
		case tea.KeyLeft:
			m.scopeMdl = newHistoryScopeModel(m.totalCount, m.diffCount, m.defaultN)
			m.step = sfStepScope
			return m, m.scopeMdl.Init()
		case tea.KeyUp:
			if m.censorCursor > 0 {
				m.censorCursor--
			}
		case tea.KeyDown:
			if m.censorCursor < len(m.censorOpts)-1 {
				m.censorCursor++
			}
		case tea.KeyEnter:
			switch m.censorOpts[m.censorCursor].val {
			case "send":
				m.ToSend = m.censored
			case "edit":
				m.WantsEdit = true
			case "send_raw":
				m.WantsSendRaw = true
				m.ToSend = m.normalized
			case "abort":
				m.Aborted = true
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *scanFlowModel) View() string {
	switch m.step {
	case sfStepScope:
		return m.scopeMdl.View()
	case sfStepCensor:
		return m.viewCensor()
	}
	return ""
}

func (m *scanFlowModel) viewCensor() string {
	var sb strings.Builder
	sb.WriteString(m.diffText)
	sb.WriteString("\n")
	sb.WriteString(nameStyle.Render(fmt.Sprintf("Send %d commands to the LLM?", len(m.censored))))
	sb.WriteString("\n\n")
	for i, opt := range m.censorOpts {
		selector := "  "
		if i == m.censorCursor {
			selector = lipgloss.NewStyle().Foreground(claudeOrange).Render("▶ ")
		}
		var label string
		if i == m.censorCursor {
			label = lipgloss.NewStyle().Foreground(neonCyan).Render(opt.label)
		} else {
			label = mutedStyle.Render(opt.label)
		}
		fmt.Fprintf(&sb, "%s%s\n", selector, label)
	}
	sb.WriteString("\n")
	sb.WriteString(mutedStyle.Render("↑↓ navigate  •  enter confirm  •  ← back"))
	sb.WriteString("\n")
	return sb.String()
}

// PromptScanFlow runs the interactive scope picker + censor review as a single
// bubbletea program so all transitions (including ← back from censor) are in-place.
// computeFn normalizes and censors the selected entries.
// Returns (nil, true, nil) when the user aborted, or (entries, false, nil) on success.
func PromptScanFlow(
	rawEntries, diffEntries []history.Entry,
	totalCount, diffCount, defaultN int,
	computeFn func([]history.Entry) (normalized, censored []history.Entry),
) (toSend []history.Entry, aborted bool, err error) {
	m := &scanFlowModel{
		rawEntries:  rawEntries,
		diffEntries: diffEntries,
		totalCount:  totalCount,
		diffCount:   diffCount,
		defaultN:    defaultN,
		computeFn:   computeFn,
	}
	result, runErr := tea.NewProgram(m,
		tea.WithOutput(os.Stderr),
		tea.WithContext(context.Background()),
		tea.WithReportFocus(),
	).Run()
	if runErr != nil {
		return nil, false, fmt.Errorf("scan flow: %w", runErr)
	}
	fm := result.(*scanFlowModel)
	if fm.Err != nil {
		return nil, false, fm.Err
	}
	if fm.Aborted {
		return nil, true, nil
	}
	if fm.WantsEdit {
		edited, eerr := editEntriesInEditor(fm.censored)
		if eerr != nil {
			return nil, false, eerr
		}
		return edited, false, nil
	}
	if fm.WantsSendRaw {
		PrintWarning("No censoring — raw commands sent to LLM. May include secrets or API keys.")
	}
	return fm.ToSend, false, nil
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
