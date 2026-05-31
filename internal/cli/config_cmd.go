package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/aka-cli/internal/config"
	"github.com/ntalmon/aka/aka-cli/internal/ui"
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
		Short: "Store an API key in the OS keyring",
		Long:  "Store an API key in the OS keyring. Use --provider to specify which provider (anthropic or groq).",
		RunE:  runSetKey,
	}
	cmd.Flags().String("provider", "", "Provider to set the key for: anthropic or groq (default: uses configured provider)")
	return cmd
}

func runSetKey(cmd *cobra.Command, _ []string) error {
	providerFlag, _ := cmd.Flags().GetString("provider")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if providerFlag != "" && providerFlag != "anthropic" && providerFlag != "groq" {
		return fmt.Errorf("unknown provider %q — must be anthropic or groq", providerFlag)
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
			if provider == "groq" {
				cfg.GroqAPIKey = key
			} else {
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
		Long:  "Set max_history, the maximum number of normalized shell history entries sent to the LLM during aka scan.",
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

	if cfg.AnthropicAPIKey != "" {
		fmt.Println("  anthropic_key:  [configured]")
	} else {
		fmt.Println("  anthropic_key:  [not set — run `aka config set-key --provider anthropic`]")
	}
	if cfg.GroqAPIKey != "" {
		fmt.Println("  groq_key:       [configured]")
	} else {
		fmt.Println("  groq_key:       [not set — run `aka config set-key --provider groq`]")
	}
	return nil
}
