package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/aka-cli/internal/aliases"
	"github.com/ntalmon/aka/aka-cli/internal/apply"
	"github.com/ntalmon/aka/aka-cli/internal/censor"
	"github.com/ntalmon/aka/aka-cli/internal/config"
	"github.com/ntalmon/aka/aka-cli/internal/history"
	"github.com/ntalmon/aka/aka-cli/internal/llm"
	"github.com/ntalmon/aka/aka-cli/internal/normalize"
	"github.com/ntalmon/aka/aka-cli/internal/suggest"
	"github.com/ntalmon/aka/aka-cli/internal/ui"
)

// NewScanCmd creates the `aka scan` subcommand.
func NewScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan shell history and suggest aliases/functions",
		Long: `aka scan reads your shell history, censors sensitive data, and calls
the Anthropic API to suggest useful shell aliases and functions.`,
		RunE: runScan,
	}
	cmd.Flags().Bool("dry-run", false, "Print the censored request body and exit without making an API call")
	cmd.Flags().Int("history", 0, "Max number of history entries to use (0 = use config default)")
	cmd.Flags().Bool("full-history", false, "Ignore the history cursor and scan the full history")
	return cmd
}

// minNewEntries is the threshold below which the user is warned and asked how to proceed.
const minNewEntries = 100

func runScan(cmd *cobra.Command, _ []string) error {
	ui.PrintBanner()

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	historyN, _ := cmd.Flags().GetInt("history")
	fullHistory, _ := cmd.Flags().GetBool("full-history")

	// Also check parent persistent flag.
	if !dryRun {
		if v, err := cmd.Root().PersistentFlags().GetBool("dry-run"); err == nil {
			dryRun = v
		}
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

	// Step 1: Check/get API key (unless dry-run; Ollama needs no key).
	var apiKey string
	if !dryRun {
		apiKey = apiKeyForProvider(cfg)
		if apiKey == "" && cfg.Provider != "ollama" {
			fmt.Println("No API key configured.")
			chosenProvider, err := ui.PromptProvider()
			if err != nil {
				return fmt.Errorf("select provider: %w", err)
			}
			cfg.Provider = chosenProvider
			cfg.Model = llm.ModelsForProvider(chosenProvider)[0].ID

			if chosenProvider != "ollama" {
				apiKey, err = ui.PromptAPIKey(chosenProvider)
				if err != nil {
					return fmt.Errorf("get API key: %w", err)
				}
				if apiKey == "" {
					return fmt.Errorf("API key required")
				}
				setAPIKeyForProvider(cfg, chosenProvider, apiKey)
			}
			if saveErr := config.Save(cfg); saveErr != nil {
				fmt.Printf("Warning: could not save config: %v\n", saveErr)
			}
		}
	}

	// Step 2: Read history.
	fmt.Println("Reading shell history...")
	if historyN > 0 {
		fmt.Printf("  Custom limit: %d (--history flag)\n", historyN)
	}
	shellEnv := os.Getenv("SHELL")
	shellName := detectShellName(shellEnv)
	entries, err := history.ReadAll(shellName)
	if err != nil {
		return fmt.Errorf("read history: %w", err)
	}
	totalRaw := len(entries)
	fmt.Printf("  Read %d raw commands\n", totalRaw)

	// Apply history cursor unless --full-history is set or this is a dry-run.
	if !fullHistory && !dryRun {
		cursor, _ := config.LoadCursor()
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
	fmt.Printf("  %d commands after normalization\n", len(normalized))

	// Limit to maxHistory.
	if maxHistory > 0 && len(normalized) > maxHistory {
		if dryRun {
			normalized = normalized[len(normalized)-maxHistory:]
			fmt.Printf("  Capped to %d most recent (max_history limit)\n", maxHistory)
		} else {
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
	}

	if len(normalized) == 0 {
		return fmt.Errorf("no commands found after normalization")
	}

	// Step 4: Censor.
	censored, redactionMap := censor.CensorAll(normalized)
	fmt.Printf("  %d redactions applied\n", len(redactionMap))

	// Step 5: Show censored diff and ask for confirmation (skip in dry-run).
	if !dryRun {
		censored, err = ui.ReviewCensored(normalized, censored)
		if err != nil {
			return fmt.Errorf("censor review: %w", err)
		}
		if censored == nil {
			fmt.Println("Aborted — no data sent.")
			return nil
		}
	}

	// Step 6: If dry-run, print request body and exit.
	if dryRun {
		return printDryRun(cfg.Model, censored)
	}

	// Step 7: Call LLM.
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
	fmt.Printf("  Got %d suggestion(s)\n", len(suggestions))

	// Filter out suggestions whose names already exist in installed aliases or shell config files.
	suggestions = filterExistingSuggestions(suggestions)
	if len(suggestions) == 0 {
		fmt.Println("All suggestions already exist as aliases or functions — nothing new to apply.")
		return nil
	}

	// Persist cursor so the next run only processes new commands.
	if !fullHistory {
		_ = config.SaveCursor(config.HistoryCursor{Total: totalRaw})
	}

	// Step 8: Interactive review.
	accepted, err := ui.ReviewSuggestions(suggestions)
	if err != nil {
		return fmt.Errorf("review suggestions: %w", err)
	}

	if len(accepted) == 0 {
		fmt.Println("No suggestions accepted.")
		return nil
	}

	// Step 9: Apply accepted suggestions.
	skipped, err := apply.Apply(accepted)
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}

	if len(skipped) > 0 {
		fmt.Printf("\nSkipped (name conflicts): %s\n", strings.Join(skipped, ", "))
	}

	aliasesPath, _ := config.GetAliasesPath()
	ui.PrintSuccess(fmt.Sprintf("\n✓ Applied %d alias(es)/function(s)!", len(accepted)-len(skipped)))
	fmt.Printf("  Written to: %s\n", aliasesPath)
	fmt.Println("  Aliases reloaded in current shell (or run: source ~/.config/aka/aliases.sh)")
	return nil
}

func filterExistingSuggestions(suggestions []llm.Suggestion) []llm.Suggestion {
	installed, _ := aliases.LoadInstalled()
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

func detectShellName(shellPath string) string {
	switch {
	case strings.Contains(shellPath, "zsh"):
		return "zsh"
	case strings.Contains(shellPath, "bash"):
		return "bash"
	default:
		return ""
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
		fmt.Printf("\nCalling Groq API (%s)...\n", model)
		return llm.NewGroq(apiKey).WithModel(model)
	case "openai":
		fmt.Printf("\nCalling OpenAI API (%s)...\n", model)
		return llm.NewOpenAI(apiKey).WithModel(model)
	case "gemini":
		fmt.Printf("\nCalling Google Gemini API (%s)...\n", model)
		return llm.NewGemini(apiKey).WithModel(model)
	case "ollama":
		fmt.Printf("\nCalling Ollama (%s)...\n", model)
		return llm.NewOllama(model)
	default: // anthropic
		fmt.Printf("\nCalling Anthropic API (%s)...\n", model)
		return llm.New(apiKey).WithModel(model)
	}
}

func printDryRun(model string, censored []history.Entry) error {
	prompt := suggest.BuildPrompt(censored)
	toolSchema, err := suggest.ToolSchema()
	if err != nil {
		return err
	}

	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": 4096,
		"system": []map[string]interface{}{
			{"type": "text", "text": "[system prompt — see llm/anthropic.go]", "cache_control": map[string]string{"type": "ephemeral"}},
		},
		"tools": []map[string]interface{}{
			{
				"name":         "suggest_aliases",
				"description":  "Suggest shell aliases and functions based on command history patterns.",
				"input_schema": toolSchema,
			},
		},
		"tool_choice": map[string]string{"type": "tool", "name": "suggest_aliases"},
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt},
		},
	}

	data, err := json.MarshalIndent(reqBody, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println("=== DRY RUN: Request Body ===")
	fmt.Println(string(data))
	return nil
}
