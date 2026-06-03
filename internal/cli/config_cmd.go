package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/internal/config"
	"github.com/ntalmon/aka/internal/ui"
)

// NewConfigCmd creates the `aka config` subcommand with sub-subcommands.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage AKA configuration",
	}
	cmd.AddCommand(newSetModelCmd())
	cmd.AddCommand(newSetKeyCmd())
	cmd.AddCommand(newSetMaxHistoryCmd())
	cmd.AddCommand(newShowConfigCmd())
	return cmd
}

func newSetKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-key",
		Short: "Set an API key in ~/.config/aka/config.toml",
		Long:  "Set an API key in ~/.config/aka/config.toml. Use --provider to specify which provider (anthropic, groq, openai, or gemini).",
		RunE:  runSetKey,
	}
	cmd.Flags().String("provider", "", "Provider to set the key for: anthropic, groq, openai, or gemini (default: uses configured provider)")
	return cmd
}

func runSetKey(cmd *cobra.Command, _ []string) error {
	providerFlag, _ := cmd.Flags().GetString("provider")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	validProviders := map[string]bool{"anthropic": true, "groq": true, "openai": true, "gemini": true}
	if providerFlag != "" && !validProviders[providerFlag] {
		return fmt.Errorf("unknown provider %q — must be anthropic, groq, openai, or gemini", providerFlag)
	}

	const (
		stepProvider = 0
		stepKey      = 1
		stepModel    = 2
	)

	step := stepProvider
	var provider, key string

	if providerFlag != "" {
		provider = providerFlag
		step = stepKey
	}

	for {
		switch step {
		case stepProvider:
			p, perr := ui.PromptProvider()
			if perr != nil {
				return fmt.Errorf("select provider: %w", perr)
			}
			provider = p
			step = stepKey

		case stepKey:
			// withBack=true only when provider was not fixed by flag.
			k, kerr := ui.PromptAPIKey(provider, providerFlag == "")
			if kerr != nil {
				return fmt.Errorf("prompt API key: %w", kerr)
			}
			if k == "" {
				if providerFlag != "" {
					return fmt.Errorf("API key cannot be empty")
				}
				// Empty = go back to provider selection.
				step = stepProvider
				continue
			}
			key = k
			step = stepModel

		case stepModel:
			model, wentBack, merr := ui.PromptModelWithBack(provider)
			if merr != nil {
				return fmt.Errorf("select model: %w", merr)
			}
			if wentBack {
				step = stepKey
				continue
			}
			cfg.Provider = provider
			cfg.Model = model
			switch provider {
			case "groq":
				cfg.GroqAPIKey = key
			case "openai":
				cfg.OpenAIAPIKey = key
			case "gemini":
				cfg.GeminiAPIKey = key
			default:
				cfg.AnthropicAPIKey = key
			}
			if serr := config.Save(cfg); serr != nil {
				return fmt.Errorf("save config: %w", serr)
			}
			ui.PrintSuccess(fmt.Sprintf("✓ %s API key saved to ~/.config/aka/config.toml", provider))
			return nil
		}
	}
}

func newSetModelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-model",
		Short: "Choose the model (or switch provider)",
		RunE:  runSetModel,
	}
}

func runSetModel(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	provider, model, err := ui.PromptModelOrSwitchProvider(cfg.Provider)
	if err != nil {
		return fmt.Errorf("select model: %w", err)
	}
	var msg string
	if provider != cfg.Provider {
		msg = fmt.Sprintf("✓ switched to %s / %s", provider, model)
	} else {
		msg = fmt.Sprintf("✓ model set to %s", model)
	}
	cfg.Provider = provider
	cfg.Model = model
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	ui.PrintSuccess(msg)
	return nil
}

func newSetMaxHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-max-history <count>",
		Short: "Set the max_history limit",
		Long:  "Set max_history, the default number of recent history entries pre-filled in the interactive history scope picker during aka scan.",
		Args:  cobra.ExactArgs(1),
		RunE:  runSetMaxHistory,
	}
}

func runSetMaxHistory(_ *cobra.Command, args []string) error {
	limit, err := strconv.Atoi(args[0])
	if err != nil || limit <= 0 {
		return fmt.Errorf("invalid max_history %q: must be a positive integer", args[0])
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cfg.MaxHistory = limit
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	ui.PrintSuccess(fmt.Sprintf("✓ max_history set to %d", limit))
	return nil
}

func newShowConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show current AKA configuration",
		RunE:  runShowConfig,
	}
}

func runShowConfig(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	fmt.Println("AKA Configuration:")
	fmt.Printf("  provider:       %s\n", cfg.Provider)
	fmt.Printf("  model:          %s\n", cfg.Model)
	fmt.Printf("  max_history:    %d\n", cfg.MaxHistory)

	keyStatus := func(key, provider string) string {
		if key != "" {
			return "[configured]"
		}
		return fmt.Sprintf("[not set — run `aka config set-key --provider %s`]", provider)
	}
	fmt.Printf("  anthropic_key:  %s\n", keyStatus(cfg.AnthropicAPIKey, "anthropic"))
	fmt.Printf("  groq_key:       %s\n", keyStatus(cfg.GroqAPIKey, "groq"))
	fmt.Printf("  openai_key:     %s\n", keyStatus(cfg.OpenAIAPIKey, "openai"))
	fmt.Printf("  gemini_key:     %s\n", keyStatus(cfg.GeminiAPIKey, "gemini"))
	return nil
}
