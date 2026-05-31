package cli

import (
	"context"
	"errors"
	"fmt"
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

// NewScanCmd creates the `aka scan` subcommand.
func NewScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan shell history and suggest aliases/functions",
		Long: `aka scan reads your shell history, censors sensitive data, and calls
an LLM to suggest useful shell aliases and functions.`,
		RunE: runScan,
	}
	cmd.Flags().Int("history", 0, "Max number of history entries to use (0 = use config default)")
	cmd.Flags().Bool("full-history", false, "Ignore the history cursor and scan the full history")
	return cmd
}

// minNewEntries is the threshold below which the user is warned and asked how to proceed.
const minNewEntries = 100

func runScan(cmd *cobra.Command, _ []string) error {
	ui.PrintBanner()

	historyN, _ := cmd.Flags().GetInt("history")
	fullHistory, _ := cmd.Flags().GetBool("full-history")

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

	maxHistory := cfg.MaxHistory
	if historyN > 0 {
		maxHistory = historyN
	}

	// Step 1: Check/get API key (Ollama needs no key).
	if err := ensureAPIKey(cfg); err != nil {
		return err
	}
	apiKey := apiKeyForProvider(cfg)

	// Step 2: Read history for the current shell.
	if historyN > 0 {
		fmt.Printf("  Custom limit: %d (--history flag)\n", historyN)
	}
	rawEntries, err := history.ReadAll(shell)
	if err != nil {
		return fmt.Errorf("read history: %w", err)
	}
	totalRaw := len(rawEntries)

	// Resolve the history-scope situation from the cursor. `fullEntries` is what
	// "full history" (and every non-prompting case) sends; `newEntries` is the
	// since-cursor slice used when the user picks "new only". The history-mode
	// prompt is interactive only when there are few new commands.
	fullEntries := rawEntries
	newEntries := rawEntries
	historyPromptApplies := false
	historyNewCount := 0
	if !fullHistory {
		cursor, _ := config.LoadCursor(shell)
		if cursor.Total > 0 {
			newCount := totalRaw - cursor.Total
			if newCount < 0 {
				// History file was rotated/truncated — treat as full history.
				newCount = totalRaw
			}
			if newCount < minNewEntries {
				historyPromptApplies = true
				historyNewCount = newCount
				if newCount > 0 {
					newEntries = rawEntries[cursor.Total:]
				}
			} else {
				fullEntries = rawEntries[cursor.Total:]
				newEntries = fullEntries
				fmt.Printf("  %d new command(s) since last run\n", newCount)
			}
		}
	}

	entriesForMode := func(mode string) []history.Entry {
		if mode == "new" {
			return newEntries
		}
		return fullEntries
	}
	countForMode := func(mode string) int {
		return len(normalize.Normalize(entriesForMode(mode)))
	}

	// Interactive scan flow: scope (history range + max-history limit) → censor
	// review. The scope prompts run as one in-place program (PromptScanScope);
	// the censor review is a separate step. Pressing back at the censor review
	// re-runs the scope program. Choices are cheap/deterministic to recompute,
	// so re-running stays consistent.
	var censored []history.Entry
	for {
		historyMode, maxLimit, maxSave, aborted, scopeErr := ui.PromptScanScope(
			historyPromptApplies, historyNewCount, totalRaw, maxHistory, countForMode)
		if scopeErr != nil {
			return fmt.Errorf("scan scope: %w", scopeErr)
		}
		if aborted {
			fmt.Println("Aborted.")
			return nil
		}

		normalized := normalize.Normalize(entriesForMode(historyMode))
		if maxLimit > 0 && len(normalized) > maxLimit {
			normalized = normalized[len(normalized)-maxLimit:]
		}
		if len(normalized) == 0 {
			return fmt.Errorf("no commands found after normalization")
		}
		if maxLimit != -1 {
			fmt.Printf("  Sending %d commands\n", len(normalized))
		}

		candidate, _ := censor.CensorAll(normalized)
		backAvail := historyPromptApplies || maxLimit != -1
		result, back, rerr := ui.ReviewCensored(normalized, candidate, backAvail)
		if rerr != nil {
			return fmt.Errorf("censor review: %w", rerr)
		}
		if back {
			continue // re-run the scope program
		}
		if result == nil {
			fmt.Println("Aborted — no data sent.")
			return nil
		}

		// Persist a changed max_history limit only once the flow commits.
		if maxSave && maxLimit > 0 {
			cfg.MaxHistory = maxLimit
			if saveErr := config.Save(cfg); saveErr != nil {
				fmt.Printf("Warning: could not save config: %v\n", saveErr)
			}
		}
		censored = result
		break
	}

	// Step 6: Call LLM.
	provName := cfg.Provider
	if provName == "" {
		provName = "anthropic"
	}
	fmt.Printf("🧠 Analyzing last %d commands for patterns using %s/%s...\n", len(censored), provName, cfg.Model)
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
	fmt.Printf("\n✨ Discovered %d potential productivity enhancements!\n", len(suggestions))

	// Persist cursor so the next run only processes new commands.
	if !fullHistory {
		_ = config.SaveCursor(config.HistoryCursor{Total: totalRaw}, shell)
	}

	// Step 7: Interactive review.
	accepted, err := ui.ReviewSuggestions(suggestions)
	if err != nil {
		return fmt.Errorf("review suggestions: %w", err)
	}

	if len(accepted) == 0 {
		fmt.Println("No suggestions accepted.")
		return nil
	}

	// Step 8: Apply accepted suggestions.
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
