# AKA — shell history analyzer and alias manager

AKA (`aka`) reads your shell history, censors sensitive data locally, and uses an LLM to suggest useful shell aliases and functions — then writes the ones you accept into a single dedicated file (`~/.config/aka/aliases.sh`) that your shell sources automatically.

## Install

**Linux / macOS (one-liner)**
```sh
curl -fsSL https://raw.githubusercontent.com/ntalmon/aka-cli/main/install.sh | sh
```

**macOS (Homebrew)**
```sh
brew install ntalmon/tap/aka
```

**Go developers**
```sh
go install github.com/ntalmon/aka/aka-cli/cmd/aka@latest
```

**Manual** — download a pre-built binary from [Releases](https://github.com/ntalmon/aka-cli/releases).

## Quick start

```sh
aka init            # one-time setup: creates aliases.sh, adds source line to .zshrc/.bashrc
aka config set-key  # store your API key
aka scan            # scan history, review censored diff, get and accept suggestions
aka list            # see installed aliases and functions
aka undo            # revert the last change
```
