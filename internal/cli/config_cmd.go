package cli

import (
	"fmt"

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
	cmd.AddCommand(newSetKeyCmd())
	cmd.AddCommand(newShowConfigCmd())
	return cmd
}

func newSetKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-key",
		Short: "Set (store) your Anthropic API key in the OS keyring",
		RunE:  runSetKey,
	}
}

func runSetKey(_ *cobra.Command, _ []string) error {
	key, err := ui.PromptAPIKey()
	if err != nil {
		return fmt.Errorf("prompt API key: %w", err)
	}
	if key == "" {
		return fmt.Errorf("API key cannot be empty")
	}
	if err := config.SetAPIKey(key); err != nil {
		return fmt.Errorf("store API key: %w", err)
	}
	ui.PrintSuccess("✓ API key stored in OS keyring")
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
	fmt.Printf("  model:          %s\n", cfg.Model)
	fmt.Printf("  api_key_method: %s\n", cfg.APIKeyMethod)
	fmt.Printf("  dry_run:        %v\n", cfg.DryRun)
	fmt.Printf("  max_history:    %d\n", cfg.MaxHistory)

	// Show whether an API key is configured (but not the key itself).
	_, keyErr := config.GetAPIKey()
	if keyErr == nil {
		fmt.Println("  api_key:        [configured]")
	} else {
		fmt.Println("  api_key:        [not set — run `aka config set-key`]")
	}
	return nil
}
