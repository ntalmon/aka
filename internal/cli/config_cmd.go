package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/aka-cli/internal/config"
	"github.com/ntalmon/aka/aka-cli/internal/llm"
	"github.com/ntalmon/aka/aka-cli/internal/ui"
)

// NewConfigCmd creates the `aka config` subcommand with sub-subcommands.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage AKA configuration",
	}
	cmd.AddCommand(newSetKeyCmd())
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

	// Resolve provider: explicit flag skips the prompt; otherwise always ask.
	provider := providerFlag
	if provider != "" && provider != "anthropic" && provider != "groq" {
		return fmt.Errorf("unknown provider %q — must be anthropic or groq", provider)
	}
	if provider == "" {
		var err error
		provider, err = ui.PromptProvider()
		if err != nil {
			return fmt.Errorf("select provider: %w", err)
		}
	}

	key, err := ui.PromptAPIKey(provider)
	if err != nil {
		return fmt.Errorf("prompt API key: %w", err)
	}
	if key == "" {
		return fmt.Errorf("API key cannot be empty")
	}

	cfg.Provider = provider
	cfg.Model = llm.ModelsForProvider(provider)[0].ID
	if provider == "groq" {
		cfg.GroqAPIKey = key
	} else {
		cfg.AnthropicAPIKey = key
	}

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	ui.PrintSuccess(fmt.Sprintf("✓ %s API key saved to ~/.config/aka/config.toml", provider))
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
	fmt.Printf("  dry_run:        %v\n", cfg.DryRun)
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
