# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

All commands must be run from the repo root.

```bash
go build ./...                          # build
go test -race -count=1 ./...            # run all tests
go test -race ./internal/censor/...    # run a single package's tests
go test -run TestCensorSecrets ./internal/censor/  # run a single test
gofmt -w .                             # format
go vet ./...                           # vet
go run ./cmd/aka -- <args>             # run without installing
go install ./cmd/aka                   # install `aka` binary to $GOPATH/bin
```

CI additionally runs `golangci-lint` — run it locally if adding new packages or exported symbols.

## Architecture

The repo root is the Go module (`github.com/ntalmon/aka/aka-cli`).

### Data flow for `aka analyze`

```
history.ReadAll → normalize.Normalize → censor.CensorAll → ui.ReviewCensored
  → llm.AnthropicProvider.Suggest → ui.ReviewSuggestions → apply.Apply
```

Each step is a thin wrapper over its `internal/` package. The cobra subcommand in `internal/cli/analyze.go` wires them together.

### Key invariant: `aliases.sh` is never appended to

`internal/aliases/WriteAliasesFile()` always does a **full rewrite** from `installed.json`, sorted by name, with a timestamped backup taken first. `apply.Apply()` and `aka undo` both go through this function. Never write to `aliases.sh` directly.

### Runtime files (all under `~/.config/aka/`)

| File | Purpose |
|---|---|
| `aliases.sh` | The managed aliases/functions file; sourced by the user's shell |
| `installed.json` | Source of truth — `InstalledEntry` records with metadata |
| `config.toml` | User config (model, max_history, etc.) |
| `backups/` | Timestamped snapshots before every write |

### History entries (`internal/history/`)

`ReadAll` returns `[]history.Entry{Timestamp int64, Command string}`. `Timestamp` is a Unix epoch second; **0 means unknown** (plain history files with no timestamp format). Zsh extended history (`: EPOCH:DURATION;CMD`, enabled by `setopt EXTENDED_HISTORY`) and bash `HISTTIMEFORMAT` both populate it. `normalize.Normalize` deduplicates by exact command string and carries the **last occurrence's timestamp** on the retained entry.

### Censor pipeline (`internal/censor/`)

`CensorAll()` accepts and returns `[]history.Entry`, preserving timestamps. Internally it extracts the `Command` strings, runs both passes as `[]string`, then re-attaches timestamps. The exported helpers `CensorSecrets` and `ParameterizeVars` still operate on `[]string` directly and are tested that way.

Two sequential passes:
1. **Pass 1 — secrets**: regex pack (AWS/GitHub/OpenAI/Anthropic keys, JWTs, Bearer tokens, `password=`, URL creds) + Shannon-entropy heuristic (>4.5 bits/char, >20 chars, mixed case+digits). Same literal value across commands → same `<TOKEN_n>` index.
2. **Pass 2 — variable parameterization**: clusters commands by `(binary + flags)` shape key. Value slots with ≥2 distinct values → typed placeholder (`<BRANCH_n>`, `<PATH_n>`, `<HOST_n>`, `<VAR_n>`). This is what drives the LLM to suggest functions with `$1`/`$2` instead of hardcoded aliases.

Both passes are deterministic: candidates are sorted before assigning indices.

### LLM integration (`internal/llm/anthropic.go`)

Uses Anthropic tool use with `tool_choice: {type: "tool", name: "suggest_aliases"}` to force structured output. System prompt is sent with `cache_control: {type: "ephemeral"}` for prompt caching. Default model: `claude-haiku-4-5-20251001`.

`Suggest` accepts `[]history.Entry`. `suggest.BuildPrompt` includes `[+Xs]`/`[+Xm]` time-delta columns when timestamps are non-zero, falling back to plain numbering otherwise. This lets the LLM identify workflow sessions (commands seconds apart).

The system prompt instructs the LLM to return **at most 15 suggestions**, ranked by impact tier: multi-step workflow functions > long commands with variable parts > long verbatim commands > short conventional aliases.

### API key lookup order

`config.GetAPIKey()` checks `ANTHROPIC_API_KEY` env var first, then the OS keychain (`99designs/keyring`, service `"aka"`). Always set the env var during development to avoid macOS keychain prompts.

### v0.2 scope (not yet built)

`aka watch` (real-time alias coach) and `aka prune` are not yet built. Packages `internal/matcher/`, `internal/nudgestate/`, `internal/hooks/`, and `internal/prune/` are not yet created.
