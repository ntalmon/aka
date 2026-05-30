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
	entries, err := history.ReadAll(shell)
	if err != nil {
		return fmt.Errorf("read history: %w", err)
	}
	totalRaw := len(entries)

	// Apply history cursor unless --full-history is set.
	if !fullHistory {
		cursor, _ := config.LoadCursor(shell)
		if cursor.Total > 0 {
			newCount := totalRaw - cursor.Total
			if newCount < 0 {
				// History file was rotated/truncated — treat as full history.
				newCount = totalRaw
			}
			if newCount < minNewEntries {
				mode, err := ui.ChooseHistoryMode(newCount, totalRaw)
				if err != nil || mode == "abort" {
					fmt.Println("Aborted.")
					return nil
				}
				if mode == "new" && newCount > 0 {
					entries = entries[cursor.Total:]
				}
				// mode == "full": keep all entries (no slice)
			} else {
				entries = entries[cursor.Total:]
				fmt.Printf("  %d new command(s) since last run\n", newCount)
			}
		}
	}

	// Step 3: Normalize.
	normalized := normalize.Normalize(entries)

	// Limit to maxHistory.
	if maxHistory > 0 && len(normalized) > maxHistory {
		chosenLimit, save, err := ui.ChooseMaxHistory(maxHistory, len(normalized))
		if err != nil {
			return fmt.Errorf("choose max history: %w", err)
		}
		if save {
			cfg.MaxHistory = chosenLimit
			if saveErr := config.Save(cfg); saveErr != nil {
				fmt.Printf("Warning: could not save config: %v\n", saveErr)
			}
		}
		if chosenLimit > 0 && len(normalized) > chosenLimit {
			normalized = normalized[len(normalized)-chosenLimit:]
		}
		fmt.Printf("  Sending %d commands\n", len(normalized))
	}

	if len(normalized) == 0 {
		return fmt.Errorf("no commands found after normalization")
	}

	// Step 4: Censor.
	censored, _ := censor.CensorAll(normalized)

	// Step 5: Show censored diff and ask for confirmation.
	censored, err = ui.ReviewCensored(normalized, censored)
	if err != nil {
		return fmt.Errorf("censor review: %w", err)
	}
	if censored == nil {
		fmt.Println("Aborted — no data sent.")
		return nil
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
func ensureAPIKey(cfg *config.Config) error {
	if apiKeyForProvider(cfg) != "" || cfg.Provider == "ollama" {
		return nil
	}
	fmt.Println("No API key configured.")
	chosenProvider, err := ui.PromptProvider()
	if err != nil {
		return fmt.Errorf("select provider: %w", err)
	}
	cfg.Provider = chosenProvider

	if chosenProvider != "ollama" {
		apiKey, err := ui.PromptAPIKey(chosenProvider)
		if err != nil {
			return fmt.Errorf("get API key: %w", err)
		}
		if apiKey == "" {
			return fmt.Errorf("API key required")
		}
		setAPIKeyForProvider(cfg, chosenProvider, apiKey)
	}

	chosenModel, err := ui.PromptModel(chosenProvider)
	if err != nil {
		return fmt.Errorf("select model: %w", err)
	}
	cfg.Model = chosenModel

	if saveErr := config.Save(cfg); saveErr != nil {
		fmt.Printf("Warning: could not save config: %v\n", saveErr)
	}
	return nil
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
