# AKA — shell history analyzer and alias manager

AKA (`aka`) reads your shell history, censors sensitive data locally, and uses an LLM to suggest useful shell aliases and functions — then writes the ones you accept into a single dedicated file (`~/.config/aka/aliases.sh`) that your shell sources automatically.

## Install

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
                    # prompts for provider, API key, and model on first run
aka scan            # scan history, review censored diff, get and accept suggestions
aka list            # see all installed aliases and functions
aka delete <name>   # remove a managed alias or function
```

## How it works

1. **Read history** — reads your shell history file (`~/.zsh_history`, `~/.bash_history`, etc.)
2. **Censor** — two-pass local scrub: regex pack strips secrets (API keys, tokens, git commit hashes/messages, `password=`, JWT/Bearer tokens, URL credentials) and a Shannon-entropy heuristic catches unlabelled high-entropy strings. Then a second pass parameterizes repeated command patterns, replacing differing values with typed placeholders (`<PATH_n>`, `<HOST_n>`, etc.) to abstract workflow shapes without exposing data.
3. **Review** — shows you the censored diff before anything leaves your machine. You can edit it manually, send as-is, or abort.
4. **Suggest** — sends the anonymized history to the LLM. Gets back two types of suggestions:
   - **Workflow functions** (3–5 sequential commands combined into one shell function)
   - **One-liners** (short aliases for long single commands)
5. **Accept** — review each suggestion interactively. Accepted aliases/functions are written to `aliases.sh` and tracked in `installed.json`.

## Supported providers

| Provider | Key required | Notes |
|---|---|---|
| Anthropic (Claude) | Yes | Default — `claude-haiku-4-5` |
| Groq | Yes | Fast, generous free tier |
| OpenAI | Yes | GPT-4o and others |
| Google Gemini | Yes | Gemini 2.0 Flash and others |
| Ollama | No | Runs locally, no API key needed |

Set or change provider/key/model at any time:
```sh
aka config set-key    # set API key (prompts for provider, key, and model)
aka config set-model  # switch model or provider
aka config show       # show current configuration
```

## Flags

### `aka scan`

```
--history <value>   History scope: a number (last N commands), "diff" (since last run),
                    or "full" (entire history). Omit to choose interactively.

--censor <mode>     "manual"  interactive censor review (default)
                    "trust"   censor runs, review skipped
                    "none"    no censoring — raw commands sent to LLM (WARNING: may expose secrets)
```

### `aka init`

```
--shell <shell>     Shell to initialize: bash or zsh (default: detected from $SHELL)
```

## Runtime files

All per-shell files live under `~/.config/aka/<shell>/`:

| File | Purpose |
|---|---|
| `aliases.sh` | The managed aliases/functions file; sourced by your shell |
| `installed.json` | Source of truth — alias records with metadata |
| `history_cursor.json` | Tracks last-seen history position for `--history diff` |
| `completion.sh` | Generated tab-completion script |
| `backups/` | Timestamped snapshots taken before every write |

Global config: `~/.config/aka/config.toml` — provider, model, API keys, `max_history`.

## Shell support

- **zsh** — full support, including extended history timestamps
- **bash** — full support, including `HISTTIMEFORMAT` timestamps
- **fish** — not yet supported
