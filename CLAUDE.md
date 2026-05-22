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

After every Go file edit, run `gofmt -w <file>` to format and `go build ./cmd/aka` to verify the binary still compiles. Both are enforced automatically via PostToolUse hooks in `.claude/settings.json`.

## Architecture

The repo root is the Go module (`github.com/ntalmon/aka/aka-cli`).

### Data flow for `aka scan`

```
history.ReadAll → normalize.Normalize → censor.CensorAll → ui.ReviewCensored
  → llm.Provider.Suggest → ui.ReviewSuggestions → apply.Apply
```

Each step is a thin wrapper over its `internal/` package. The cobra subcommand in `internal/cli/scan.go` wires them together.

### Key invariant: `aliases.sh` is never appended to

`internal/aliases/WriteAliasesFile()` always does a **full rewrite** from `installed.json`, sorted by name, with a timestamped backup taken first. `apply.Apply()` and `aka undo` both go through this function. Never write to `aliases.sh` directly.

### Runtime files (all under `~/.config/aka/`)

| File | Purpose |
|---|---|
| `aliases.sh` | The managed aliases/functions file; sourced by the user's shell |
| `installed.json` | Source of truth — `InstalledEntry` records with metadata |
| `config.toml` | User config — provider, model, API keys, max_history, dry_run |
| `backups/` | Timestamped snapshots before every write |

`config.toml` fields: `provider` ("anthropic" \| "groq"), `model`, `anthropic_api_key`, `groq_api_key`, `dry_run`, `max_history`. API keys are stored here in plaintext — no env vars or OS keychain involved.

`max_history` (default 500) caps how many normalized entries are sent to the LLM. When the normalized count exceeds the limit, `ui.ChooseMaxHistory` prompts the user to continue with the current limit, send all entries for this run, or change the limit permanently (saved back to `config.toml`).

History cursor behavior: `minNewEntries` (100) is the threshold below which `ui.ChooseHistoryMode` is shown instead of auto-sending the diff. Within that prompt, the "scan new commands only" option is shown only when `newCount >= 50` — below 50 the user can only choose full history or abort.

### History entries (`internal/history/`)

`ReadAll` returns `[]history.Entry{Timestamp int64, Command string}`. `Timestamp` is a Unix epoch second; **0 means unknown** (plain history files with no timestamp format). Zsh extended history (`: EPOCH:DURATION;CMD`, enabled by `setopt EXTENDED_HISTORY`) and bash `HISTTIMEFORMAT` both populate it. `normalize.Normalize` only trims whitespace and drops blank lines — duplicates and trivial commands are preserved so the LLM sees full sequential workflow patterns.

### Censor pipeline (`internal/censor/`)

`CensorAll()` accepts and returns `[]history.Entry`, preserving timestamps. Internally it extracts the `Command` strings, runs both passes as `[]string`, then re-attaches timestamps. The exported helpers `CensorSecrets` and `ParameterizeVars` still operate on `[]string` directly and are tested that way.

`ui.ReviewCensored` returns `([]history.Entry, error)` — nil means the user aborted. It shows a 3-option prompt: send as-is, edit manually (opens `$EDITOR`, defaults to `vi`), or abort. The edited text is read back line-by-line into new `history.Entry` values.

Two sequential passes:
1. **Pass 1 — secrets**: regex pack (AWS/GitHub/OpenAI/Anthropic keys, JWTs, Bearer tokens, `password=`, URL creds) + Shannon-entropy heuristic (>4.5 bits/char, >20 chars, mixed case+digits). Same literal value across commands → same `<TOKEN_n>` index.
2. **Pass 2 — variable parameterization**: clusters commands by `(binary + flags)` shape key. Value slots with ≥2 distinct values → typed placeholder (`<BRANCH_n>`, `<PATH_n>`, `<HOST_n>`, `<VAR_n>`). This is what drives the LLM to suggest functions with `$1`/`$2` instead of hardcoded aliases.

Both passes are deterministic: candidates are sorted before assigning indices.

### LLM integration (`internal/llm/`)

Two providers implement the `llm.Provider` interface (`Suggest(ctx, []history.Entry) ([]Suggestion, error)`):

- **`anthropic.go`** — Anthropic Messages API with tool use (`tool_choice: {type: "tool", name: "suggest_aliases"}`) for structured output. System prompt is sent with `cache_control: {type: "ephemeral"}` for prompt caching. Default model: `claude-haiku-4-5-20251001`.
- **`groq.go`** — Groq's OpenAI-compatible Chat Completions API with function calling. Default model: `llama-3.3-70b-versatile`.

Both share the same system prompt (defined in `anthropic.go` as `systemPromptText`) and the same `toolOutput` / `Suggestion` types.

`groq.go` returns `*ErrTokenLimit` (instead of a generic error) when Groq rejects the request for exceeding the TPM limit. `runScan` in `scan.go` catches this and retries with the censored slice halved, repeating until it fits or fewer than 10 entries remain.

`Suggest` accepts `[]history.Entry`. `suggest.BuildPrompt` includes `[+Xs]`/`[+Xm]` time-delta columns when timestamps are non-zero, falling back to plain numbering otherwise. This lets the LLM identify workflow sessions (commands seconds apart).

The system prompt distinguishes two suggestion types: **Type A — workflow functions** (3–5 sequential commands combined into one function; at least 2 required per run) and **Type B — one-liners** (short aliases for long single commands). Type A suggestions are always listed first. Pass-through wrappers that merely rename a command without saving keystrokes are explicitly banned. The `description` field in `param` objects is optional in the tool schema (Groq's LLM sometimes omits it).

The `--history N` flag overrides `max_history` for one run and prints a note in the output to distinguish it from a normal run.

Supported providers and their model lists live in `internal/llm/models.go` (`SupportedProviders`, `ModelsForProvider`). Add new providers there and implement the `Provider` interface.

### First-run / key setup

When `aka scan` finds no API key in `config.toml`, it prompts: (1) choose provider, (2) enter API key. The provider's recommended default model is set automatically. `aka config set-key [--provider anthropic|groq]` does the same interactively at any time.

## Local pre-push checks

[lefthook](https://github.com/evilmartians/lefthook) runs `gofmt`, `go vet`, `golangci-lint`, and `govulncheck` automatically before every `git push`. Tests are skipped in the hook (too slow) and remain CI-only.

First-time setup after cloning:
```bash
go install github.com/evilmartians/lefthook@latest
lefthook install
```

To run the checks manually without pushing:
```bash
lefthook run pre-push
```

## Branching and parallel work

Use **trunk-based development**: feature branches → PR → `main`. There is no long-lived `dev` branch.

For parallel features across multiple Claude sessions, use **git worktrees** — each session gets its own directory checked out to its own branch, sharing the same `.git` repo:

```bash
git worktree add ../aka-feature-x feature-x
git worktree add ../aka-feature-y feature-y
```

Never run two sessions in the same working directory on different branches — they will stomp on each other's files.

## Release process

Releases are triggered by pushing a version tag. CI runs tests first; goreleaser then cross-compiles and publishes.

```bash
git tag v1.2.3
git push --tags
```

goreleaser (`.goreleaser.yaml`) handles:
- Cross-compilation: darwin/linux (amd64 + arm64), windows/amd64
- GitHub Release with tarballs and `checksums.txt`
- Homebrew formula pushed to `ntalmon/homebrew-tap`

**Prerequisites for the Homebrew push to work:**
- The `ntalmon/homebrew-tap` GitHub repo must exist
- A `HOMEBREW_TAP_GITHUB_TOKEN` secret (PAT with `repo` scope on the tap repo) must be set in this repo's Actions secrets

Version is injected at build time via `-ldflags "-X main.version={{.Version}}"` — the `var version = "dev"` in `cmd/aka/main.go` is the fallback for local builds.

## Lessons learned

When a significant mistake is made during a session — wrong assumption, bad approach, avoidable breakage — add a concise entry here so future sessions don't repeat it. Format: **what went wrong**, then how to avoid it.

<!-- Add new lessons above this line -->
