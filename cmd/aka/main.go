// Command aka is the AKA CLI — shell history scanner and alias manager.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ntalmon/aka/aka-cli/internal/cli"
)

// version is set via ldflags: -X main.version=v0.1.0
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cobra.EnableCommandSorting = false
	root := &cobra.Command{
		Use:   "aka",
		Short: "AKA — shell history scanner and alias manager",
		Long: `AKA (also-known-as) reads your shell history, censors sensitive data,
and uses an LLM to suggest useful shell aliases and functions.

Get started:
  aka init          # one-time setup
  aka scan          # scan history and get suggestions
  aka config        # manage provider, model, and API key
  aka list          # see installed aliases
  aka delete <name> # remove an alias or function`,
		Version: version,
	}

	root.SilenceErrors = true
	root.SilenceUsage = true
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(cli.NewInitCmd())
	root.AddCommand(cli.NewScanCmd())
	root.AddCommand(cli.NewConfigCmd())
	root.AddCommand(cli.NewListCmd())
	root.AddCommand(cli.NewDeleteCmd())

	return root
}
