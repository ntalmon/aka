package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/aka-cli/internal/aliases"
	"github.com/ntalmon/aka/aka-cli/internal/apply"
	"github.com/ntalmon/aka/aka-cli/internal/censor"
	"github.com/ntalmon/aka/aka-cli/internal/config"
	"github.com/ntalmon/aka/aka-cli/internal/history"
	"github.com/ntalmon/aka/aka-cli/internal/llm"
	"github.com/ntalmon/aka/aka-cli/internal/normalize"
	"github.com/ntalmon/aka/aka-cli/internal/ui"
)

// censorModeFlag is a custom pflag.Value whose Type() returns "<mode>" so that
// --help shows "--censor <mode>" rather than "--censor string".
type censorModeFlag string

func (f *censorModeFlag) String() string     { return string(*f) }
func (f *censorModeFlag) Type() string       { return "<mode>" }
func (f *censorModeFlag) Set(s string) error { *f = censorModeFlag(s); return nil }

// NewScanCmd creates the `aka scan` subcommand.
func NewScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan shell history and suggest aliases/functions",
		Long: `aka scan reads your shell history, censors sensitive data, and calls
an LLM to suggest useful shell aliases and functions.`,
		RunE: runScan,
	}
	cmd.Flags().SortFlags = false
	cmd.Flags().String("history", "", `History scope: a number (last N commands), "diff" (since last run), or "full" (all history). Omit to choose interactively.`)
	censorDefault := censorModeFlag("manual")
	cmd.Flags().Var(&censorDefault, "censor", `Censor mode: "manual" (interactive review, default), "trust" (skip review), "none" (no censoring — raw commands sent to LLM, WARNING: may expose secrets)`)
	return cmd
}

// minDiffCount is the minimum number of new commands required for diff mode to be useful.
const minDiffCount = 40

// parseHistoryFlag parses the --history flag value.
// Returns mode ("full", "diff", "last") and n (only used when mode == "last").
func parseHistoryFlag(s string) (mode string, n int, err error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "full":
		return "full", 0, nil
	case "diff":
		return "diff", 0, nil
	default:
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n <= 0 {
			return "", 0, fmt.Errorf("--history: expected a positive number, \"diff\", or \"full\"; got %q", s)
		}
		return "last", n, nil
	}
}

func runScan(cmd *cobra.Command, _ []string) error {
	ui.PrintBanner()

	historyFlag, _ := cmd.Flags().GetString("history")
	censorMode := cmd.Flags().Lookup("censor").Value.String()

	switch censorMode {
	case "manual", "trust", "none":
	default:
		return fmt.Errorf("--censor must be one of: manual, trust, none (got %q)", censorMode)
	}

	if censorMode == "none" {
		ui.PrintWarning("No censoring — raw commands sent to LLM. May include secrets or API keys.")
		fmt.Println()
	}

	// Detect current shell and verify it has been initialized.
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
		confirm, err := ui.PromptInitShell(shell)
		if err != nil {
			return fmt.Errorf("prompt init shell: %w", err)
		}
		if !confirm {
			fmt.Printf("Run 'aka init --shell %s' whenever you're ready.\n", shell)
			return nil
		}
		rcFile, err := rcFileForShell(shell)
		if err != nil {
			return fmt.Errorf("resolve rc file: %w", err)
		}
		if err := initShell(shell, rcFile); err != nil {
			return err
		}
		fmt.Println()
	}

	// Load config.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Check/get API key (Ollama needs no key).
	if err := ensureAPIKey(cfg); err != nil {
		return err
	}
	apiKey := apiKeyForProvider(cfg)

	// Read history for the current shell.
	rawEntries, err := history.ReadAll(shell)
	if err != nil {
		return fmt.Errorf("read history: %w", err)
	}
	totalRaw := len(rawEntries)

	// Compute diff since last cursor.
	cursor, _ := config.LoadCursor(shell)
	diffCount := totalRaw - cursor.Total
	if diffCount < 0 {
		diffCount = totalRaw
	}
	var diffEntries []history.Entry
	if cursor.Total > 0 && cursor.Total < totalRaw {
		diffEntries = rawEntries[cursor.Total:]
	} else {
		diffEntries = rawEntries
	}

	// Resolve selected entries from --history flag without prompting.
	interactive := historyFlag == ""
	var selectedEntries []history.Entry
	if !interactive {
		mode, n, perr := parseHistoryFlag(historyFlag)
		if perr != nil {
			return perr
		}
		switch mode {
		case "full":
			selectedEntries = rawEntries
			ui.PrintStep(fmt.Sprintf("Using full history (%d commands)", totalRaw))
		case "diff":
			if diffCount <= minDiffCount {
				if diffCount == 0 {
					fmt.Println("No new commands since last scan.")
				} else {
					fmt.Printf("  Only %d new command(s) since last scan — not enough to analyze (minimum: %d).\n", diffCount, minDiffCount)
					fmt.Println("  Run `aka scan` or `aka scan --history full` to include more history.")
				}
				return nil
			}
			selectedEntries = diffEntries
			ui.PrintStep(fmt.Sprintf("Using diff: %d new commands since last run", diffCount))
		case "last":
			if n >= totalRaw {
				selectedEntries = rawEntries
			} else {
				selectedEntries = rawEntries[totalRaw-n:]
			}
			ui.PrintStep(fmt.Sprintf("Using last %d commands (--history flag)", n))
		}
	}

	// Interactive scope + censor: run as a single in-place bubbletea program so
	// back navigation from the censor step re-renders the scope picker in-place.
	var censored []history.Entry
	if interactive && censorMode == "manual" {
		defaultN := cfg.MaxHistory
		if defaultN <= 0 {
			defaultN = 500
		}
		computeFn := func(selected []history.Entry) ([]history.Entry, []history.Entry) {
			norm := normalize.Normalize(selected)
			cens, _ := censor.CensorAll(norm)
			return norm, cens
		}
		toSend, scanAborted, perr := ui.PromptScanFlow(
			rawEntries, diffEntries, totalRaw, diffCount, defaultN, computeFn,
		)
		if perr != nil {
			return fmt.Errorf("scan flow: %w", perr)
		}
		if scanAborted {
			fmt.Println("Aborted.")
			return nil
		}
		censored = toSend
	} else {
		normalized := normalize.Normalize(selectedEntries)
		if len(normalized) == 0 {
			return fmt.Errorf("no commands found after normalization")
		}
		ui.PrintStep(fmt.Sprintf("Reading %d commands...", len(normalized)))

		var toSend []history.Entry
		if censorMode == "none" {
			toSend = normalized
		} else {
			toSend, _ = censor.CensorAll(normalized)
		}
		if censorMode != "manual" {
			ui.PrintStep(fmt.Sprintf("Skipping review — sending %d commands directly", len(toSend)))
		} else {
			// non-interactive + manual censor: show review without back option
			ui.PrintCensorDiff(normalized, toSend)
			result, _, rerr := ui.ReviewCensored(normalized, toSend, false)
			if rerr != nil {
				return fmt.Errorf("censor review: %w", rerr)
			}
			if result == nil {
				fmt.Println("Aborted — no data sent.")
				return nil
			}
			toSend = result
		}
		censored = toSend
	}

	// Call LLM.
	ui.PrintStep(llm.FriendlyModelName(cfg.Provider, cfg.Model) + " is analyzing patterns...")
	provider := buildProvider(cfg, apiKey)
	suggestions, err := provider.Suggest(context.Background(), censored)
	for {
		var tokenErr *llm.ErrTokenLimit
		if !errors.As(err, &tokenErr) || len(censored) <= 10 {
			break
		}
		censored = censored[len(censored)/2:]
		fmt.Printf("  Token limit exceeded; retrying with %d most recent commands...\n", len(censored))
		suggestions, err = provider.Suggest(context.Background(), censored)
	}
	if err != nil {
		return fmt.Errorf("LLM suggest: %w", err)
	}

	// Filter out suggestions whose names already exist in installed aliases or shell config files.
	suggestions = filterExistingSuggestions(suggestions, shell)
	if len(suggestions) == 0 {
		fmt.Println("All suggestions already exist as aliases or functions — nothing new to apply.")
		return nil
	}
	fmt.Println()
	ui.PrintSuggestionsOverview(suggestions)

	// Persist cursor so the next run knows where to start diff.
	_ = config.SaveCursor(config.HistoryCursor{Total: totalRaw}, shell)

	// Interactive review.
	accepted, err := ui.ReviewSuggestions(suggestions)
	if err != nil {
		return fmt.Errorf("review suggestions: %w", err)
	}

	if len(accepted) == 0 {
		fmt.Println("No suggestions accepted.")
		return nil
	}

	// Apply accepted suggestions.
	skipped, err := apply.Apply(accepted, shell)
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}

	if len(skipped) > 0 {
		fmt.Printf("\nSkipped (name conflicts): %s\n", strings.Join(skipped, ", "))
	}

	aliasesPath, _ := config.GetAliasesPath(shell)
	ui.PrintSuccess(fmt.Sprintf("\n✓ Applied %d alias(es)/function(s)!", len(accepted)-len(skipped)))
	fmt.Printf("  Written to: %s\n", aliasesPath)
	fmt.Printf("  Aliases reloaded in current shell (or run: source ~/.config/aka/%s/aliases.sh)\n", shell)
	return nil
}

func filterExistingSuggestions(suggestions []llm.Suggestion, shell string) []llm.Suggestion {
	installed, _ := aliases.LoadInstalled(shell)
	known := make(map[string]bool, len(installed))
	for _, e := range installed {
		known[e.Name] = true
	}
	for name := range aliases.LoadShellDefinedNames() {
		known[name] = true
	}

	filtered := suggestions[:0]
	var skipped []string
	for _, s := range suggestions {
		if known[s.Name] {
			skipped = append(skipped, s.Name)
			continue
		}
		filtered = append(filtered, s)
		known[s.Name] = true // deduplicate within the suggestion list itself
	}
	if len(skipped) > 0 {
		fmt.Printf("  Filtered %d already-defined: %s\n", len(skipped), strings.Join(skipped, ", "))
	}
	return filtered
}

// ensureAPIKey prompts for a provider and API key if none is configured, then saves.
// It is a no-op when a key is already present or the provider is ollama.
// Back navigation is supported between steps.
func ensureAPIKey(cfg *config.Config) error {
	if apiKeyForProvider(cfg) != "" || cfg.Provider == "ollama" {
		return nil
	}
	fmt.Println("No API key configured.")

	const (
		stepProvider = 0
		stepKey      = 1
		stepModel    = 2
	)
	step := stepProvider

	for {
		switch step {
		case stepProvider:
			provider, err := ui.PromptProvider()
			if err != nil {
				return fmt.Errorf("select provider: %w", err)
			}
			cfg.Provider = provider
			if provider == "ollama" {
				step = stepModel
			} else {
				step = stepKey
			}

		case stepKey:
			apiKey, err := ui.PromptAPIKey(cfg.Provider, true)
			if err != nil {
				return fmt.Errorf("get API key: %w", err)
			}
			if apiKey == "" {
				// Empty = go back to provider selection.
				step = stepProvider
				continue
			}
			setAPIKeyForProvider(cfg, cfg.Provider, apiKey)
			step = stepModel

		case stepModel:
			model, wentBack, err := ui.PromptModelWithBack(cfg.Provider)
			if err != nil {
				return fmt.Errorf("select model: %w", err)
			}
			if wentBack {
				if cfg.Provider == "ollama" {
					step = stepProvider
				} else {
					step = stepKey
				}
				continue
			}
			cfg.Model = model
			if saveErr := config.Save(cfg); saveErr != nil {
				fmt.Printf("Warning: could not save config: %v\n", saveErr)
			}
			return nil
		}
	}
}

func apiKeyForProvider(cfg *config.Config) string {
	switch cfg.Provider {
	case "groq":
		return cfg.GroqAPIKey
	case "openai":
		return cfg.OpenAIAPIKey
	case "gemini":
		return cfg.GeminiAPIKey
	case "ollama":
		return "" // no key required
	default: // anthropic
		return cfg.AnthropicAPIKey
	}
}

func setAPIKeyForProvider(cfg *config.Config, provider, key string) {
	switch provider {
	case "groq":
		cfg.GroqAPIKey = key
	case "openai":
		cfg.OpenAIAPIKey = key
	case "gemini":
		cfg.GeminiAPIKey = key
	default: // anthropic
		cfg.AnthropicAPIKey = key
	}
}

func buildProvider(cfg *config.Config, apiKey string) llm.Provider {
	model := cfg.Model
	switch cfg.Provider {
	case "groq":
		if model == "claude-haiku-4-5-20251001" {
			model = llm.DefaultGroqModel
		}
		return llm.NewGroq(apiKey).WithModel(model)
	case "openai":
		return llm.NewOpenAI(apiKey).WithModel(model)
	case "gemini":
		return llm.NewGemini(apiKey).WithModel(model)
	case "ollama":
		return llm.NewOllama(model)
	default: // anthropic
		return llm.New(apiKey).WithModel(model)
	}
}
