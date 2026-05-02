# AKA — shell history analyzer and alias manager

AKA (`aka`) reads your shell history, censors sensitive data locally, and uses the Anthropic API to suggest useful shell aliases and shell functions — then writes the ones you accept into a single dedicated file (`~/.config/aka/aliases.sh`) that your shell sources automatically. See [PLAN.md](PLAN.md) for the full design document.

## Quick start

```sh
go install github.com/ntalmon/aka/aka-cli/cmd/aka@latest
aka init          # one-time setup: creates aliases.sh, adds source line to .zshrc/.bashrc
aka config set-key  # store your Anthropic API key in the OS keyring
aka analyze       # analyze history, review censored diff, get and accept suggestions
aka list          # see installed aliases and functions
aka undo          # revert the last change
```
