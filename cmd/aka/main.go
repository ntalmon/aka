// Command aka is the AKA CLI — shell history analyzer and alias manager.
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
	root := &cobra.Command{
		Use:   "aka",
		Short: "AKA — shell history analyzer and alias manager",
		Long: `AKA (also-known-as) reads your shell history, censors sensitive data,
and uses the Anthropic API to suggest useful shell aliases and functions.

Get started:
  aka init       # one-time setup
  aka analyze    # analyze history and get suggestions
  aka list       # see installed aliases
  aka undo       # revert last change`,
		Version: version,
	}

	root.SilenceErrors = true
	root.SilenceUsage = true
	root.CompletionOptions.DisableDefaultCmd = true

	root.PersistentFlags().Bool("dry-run", false, "Prevent all network calls; print what would be sent")

	root.AddCommand(cli.NewInitCmd())
	root.AddCommand(cli.NewAnalyzeCmd())
	root.AddCommand(cli.NewListCmd())
	root.AddCommand(cli.NewUndoCmd())
	root.AddCommand(cli.NewConfigCmd())

	return root
}
