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

`internal/aliases/WriteAliasesFile()` always does a **full rewrite** from `installed.json`, sorted by name, with a timestamped backup taken first. `apply.Apply()` and `aka delete` both go through this function. Never write to `aliases.sh` directly.

### Runtime files

Per-shell files live under `~/.config/aka/<shell>/` (e.g. `~/.config/aka/zsh/`):

| File | Purpose |
|---|---|
| `aliases.sh` | The managed aliases/functions file; sourced by the user's shell |
| `installed.json` | Source of truth — `InstalledEntry` records with metadata |
| `history_cursor.json` | Tracks last-seen history position |
| `completion.sh` | Generated tab-completion script |
| `backups/` | Timestamped snapshots before every write |

Global (not per-shell):

| File | Purpose |
|---|---|
| `~/.config/aka/config.toml` | User config — provider, model, API keys, max_history, dry_run |

`config.toml` fields: `provider` ("anthropic" \| "groq" \| "openai" \| "gemini" \| "ollama"), `model`, `anthropic_api_key`, `groq_api_key`, `openai_api_key`, `gemini_api_key`, `dry_run`, `max_history`. API keys are stored here in plaintext — no env vars or OS keychain involved. Ollama requires no key.

**Legacy migration**: `migrateFromLegacy()` in `aliases.go` transparently moves data from the old flat `~/.config/aka/` layout to `~/.config/aka/<shell>/` on first use.

`max_history` (default 500) caps how many normalized entries are sent to the LLM. When the normalized count exceeds the limit, the scan-scope prompt (`ui.PromptScanScope`) lets the user continue with the current limit, send all entries for this run, or change the limit permanently (saved back to `config.toml`). The chosen limit is only persisted once the flow commits (after censor review) — aborting or backing out leaves `config.toml` untouched.

History cursor behavior: `minNewEntries` (100) is the threshold below which the history-scope prompt is shown instead of auto-sending the diff. Within that prompt, the "scan new commands only" option is shown only when `newCount >= 50` — below 50 the user can only choose full history or abort.

**Scan flow / back navigation**: the interactive scan flow is scope (history range → max-history limit) → censor review. The two scope prompts run as a **single bubbletea program** (`ui.PromptScanScope` / `scanScopeModel`) so back navigation between them happens **in-place**; `runScan` passes a `countForMode` callback so the model can recompute the normalized count (to decide whether the max prompt applies) without `ui` importing `normalize`. Back is via the left arrow / `← back` footer, shown on both scope select steps: the max step returns to the history step, and on the first interactive prompt `←` backs out of the command entirely (sets `aborted`). The censor review (`ui.ReviewCensored`) is a separate step; pressing back there returns `back=true` and `runScan` re-runs the scope program — there's a censor diff dump in between, so a fresh re-render there is expected, not a visual double-menu. `ReviewCensored`'s back arrow is shown only when a scope prompt preceded it (`backAvail`), otherwise re-running the empty scope program would loop.

### History entries (`internal/history/`)

`ReadAll` returns `[]history.Entry{Timestamp int64, Command string}`. `Timestamp` is a Unix epoch second; **0 means unknown** (plain history files with no timestamp format). Zsh extended history (`: EPOCH:DURATION;CMD`, enabled by `setopt EXTENDED_HISTORY`) and bash `HISTTIMEFORMAT` both populate it. `normalize.Normalize` only trims whitespace and drops blank lines — duplicates and trivial commands are preserved so the LLM sees full sequential workflow patterns.

### Censor pipeline (`internal/censor/`)

`CensorAll()` accepts and returns `[]history.Entry`, preserving timestamps. Internally it extracts the `Command` strings, runs both passes as `[]string`, then re-attaches timestamps. The exported helpers `CensorSecrets` and `ParameterizeVars` still operate on `[]string` directly and are tested that way.

`ui.ReviewCensored(original, censored, withBack)` returns `([]history.Entry, back bool, error)` — `back=true` means the user navigated back (only when `withBack`), and `(nil, false, nil)` means abort. It shows a 3-option prompt: send as-is, edit manually (opens `$EDITOR`, defaults to `vi`), or abort. The edited text is read back line-by-line into new `history.Entry` values.

Two sequential passes:
1. **Pass 1 — secrets**: regex pack (AWS/GitHub/OpenAI/Anthropic/Groq/Slack/Stripe/GCP keys, JWTs, Bearer tokens, `password=`, URL creds, **full 40-char git commit hashes**, **git commit messages** after `-m "..."` / `-m '...'` / `--message=`) + Shannon-entropy heuristic (>4.5 bits/char, >20 chars, 2-of-3 char classes). Same literal value across commands → same placeholder index. Commit message patterns use a `captureGroup` mechanism to replace only the message text while preserving `-m "..."` syntax in the output.
2. **Pass 2 — variable parameterization**: clusters commands by `(binary + flags)` shape key. Value slots with ≥2 distinct values → typed placeholder (`<PATH_n>`, `<HOST_n>`, `<IP_n>`, `<VAR_n>`). Three exclusions prevent false parameterization: (a) **git**: positional args (branch names, remotes, refs) are never parameterized — only IPs are, as they may reveal network topology; (b) **VAR-typed slots with no digit**: values containing only letters and hyphens (e.g. `scan`, `set-model`) are treated as subcommand identifiers, not data — only VAR slots where at least one value contains a digit are parameterized; (c) **paths and port numbers** are always skipped.

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

`aka init` is the primary path: after shell wiring it automatically prompts for provider, API key, and model if none is configured (via `ensureAPIKey()` in `internal/cli/scan.go`, shared with `aka scan`). The flow is provider → API key → model; the model picker (`ui.PromptModel`) lists each provider's models cheapest-first with the cheapest pre-selected. `aka scan` calls the same helper as a fallback for users who skipped init. `aka config set-key [--provider <provider>]` runs the same provider→key→model flow interactively at any time, and `aka config set-model` changes just the model for the configured provider without re-entering the key.

### `aka delete`

`aka delete <name>` removes a single AKA-managed alias or function by name. It detects the current shell via `detectCurrentShell()`, confirms the entry exists in `installed.json`, shows `[y/N]` confirmation, then calls `WriteAliasesFile` and `SaveInstalled` to atomically remove it. The shell wrapper auto-reloads `aliases.sh` after a successful `delete` (same as after `scan`).

### Shell detection helpers (`internal/cli/shell.go`)

Shared utilities used by `list`, `delete`, and other commands:
- `detectCurrentShell()` — reads `$AKA_SHELL` first (set by the shell wrapper injected by `aka init`), falls back to parsing `$SHELL`. This is the canonical way to know which shell's data to read.
- `requireShellInitialized(shell)` — checks that `~/.config/aka/<shell>/` exists; prints a friendly message and returns false if not. Callers should return nil when this returns false.

### `aka uninit`

Removes everything `aka init` added: strips the `aka()` wrapper, `aliases.sh` source line, and `completion.sh` source line from the shell RC file (matched by regex, robust to ordering), then deletes `~/.config/aka/` entirely. Shows a summary of what will be removed and asks a single `[y/N]` prompt before acting. The binary itself is not removed.

## Security

The highest-leverage trust boundary in this project is **LLM output → user's shell**: anything that ends up in an `InstalledEntry.Name` or `InstalledEntry.Template` is eventually sourced by the user's interactive shell. Treat the LLM as an *untrusted input*, not an authority — validate names against a strict identifier regex, escape/reject metacharacters in templates, and never interpolate either into `sh -c` strings (see [internal/apply/apply.go](internal/apply/apply.go) `NameExistsInShell` for the canonical pitfall).

The full checklist — covering this boundary plus history censoring, on-disk secret perms, FS atomicity, and CI/supply chain — lives in [SECURITY-AUDIT.md](SECURITY-AUDIT.md). Run it with the `/security-audit` slash command (optionally scoped: `/security-audit 1` runs only §1). Update the checklist whenever a new trust boundary is introduced (e.g., a new provider, a new on-disk file, a new install path).

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

For parallel features across multiple Claude sessions, use **git worktrees** — each session gets its own directory checked out to its own branch, sharing the same `.git` repo. Keep the main checkout on `main` as your review/merge base and create feature worktrees as siblings of the main checkout (e.g. `../aka-cli.feat-x`):

```bash
git worktree add -b feat-x ../aka-cli.feat-x main
git worktree add -b feat-y ../aka-cli.feat-y main
```

A useful shell function to add to your rc file — creates the worktree as a sibling and cd's in, working correctly from any linked worktree:

```bash
wt() {
  local root
  root=$(git rev-parse --path-format=absolute --git-common-dir | xargs dirname)
  git -C "$root" worktree add -b "$1" "${root}.${1//\//-}" main && cd "${root}.${1//\//-}"
}
```

Cleanup: always use `git worktree remove` + `git worktree prune` rather than `rm -rf` to keep git's bookkeeping clean. Never run two sessions in the same working directory on different branches — they will stomp on each other's files.

**Two worktree gotchas specific to this repo:**

1. **The real `aka` binary is NOT isolated per worktree.** All runtime state resolves from `$HOME` → `~/.config/aka/...` (there is no `AKA_HOME` override), so `aka scan`/`init`/etc. run from different worktrees share one `installed.json`/`aliases.sh` and will clobber each other. Parallel `go test`/`go build` is fine (tests sandbox `HOME` via `t.Setenv`, the build cache is concurrency-safe). For isolated *manual* smoke-testing of the binary, fake the home dir: `HOME=/tmp/aka-$(basename $PWD) go run ./cmd/aka -- init`.
2. **Gitignored/untracked files don't propagate to new worktrees.** Notably `.claude/settings.local.json` (local permission allowlist) won't carry over, so fresh worktrees re-prompt for permissions. Promote stable entries into the tracked `.claude/settings.json` if that's annoying. Hooks (lefthook pre-push, PostToolUse gofmt/build) *are* shared automatically — they live in the common `.git` dir and tracked config.

For fire-and-forget exploration from a single session, prefer a subagent with `isolation: "worktree"` (ephemeral, auto-cleaned) over a manual worktree.

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

**Entropy heuristic required all-3 char classes (upper+lower+digit).** Changed to 2-of-3 so hex/base64 secrets with only two classes are caught. Update the test string too — the old test used lowercase+digits (2 classes) and must now use a truly single-class string to remain a "should not flag" case.

**`golangci-lint` errcheck flags `defer os.Remove(f)`, `defer f.Close()`, `defer resp.Body.Close()`.** Always write these as `defer func() { _ = os.Remove(f) }()` etc. Plain `defer expr` on error-returning functions fails errcheck by default.

**`strings.Builder` + `fmt.Sprintf` inside `WriteString` is flagged by staticcheck.** Use `fmt.Fprintf(&sb, ...)` directly instead.

**Atomic file writes need `_ = tmp.Close()` in error paths.** When calling `tmp.Close()` before an early return (cleanup path), use `_ = tmp.Close()` so errcheck doesn't flag the discarded error.

**Pass 2 parameterization over-fired on CLI subcommands.** `./aka scan/help/config` and `./aka config set-model/set-key` were being replaced with `<VAR_n>` because the parser treats subcommands as positional slots. Fixed with a VAR-typed no-digit guard: only parameterize VAR slots where at least one value contains a digit, distinguishing data (IDs, version numbers) from subcommand-like identifiers (letters+hyphens only).

**Back navigation between prompts: one `tea.NewProgram` per step reprints the menu.** A bubbletea program leaves its final rendered frame in the terminal scrollback when it quits. So a "wizard" implemented as a loop that calls a fresh `huh.Form.Run()` (or one `tea.NewProgram`) per step looks fine going *forward*, but pressing **back** re-runs the previous step's program, which renders a *second* copy of that menu below the abandoned one — the "menu printed twice" bug. Fix: any set of prompts the user can navigate back and forth between must run inside a **single** `tea.NewProgram`, with a model that swaps the active `huh.Form` on each transition (see `scanScopeModel`, `switchFlowModel`, `reviewFlowModel` in `internal/ui/ui.go`). Bubbletea diffs and redraws the same screen region, so transitions update in-place. Separate programs are only acceptable between steps that have substantial output between them (e.g. the censor diff dump before `ReviewCensored`), where a fresh render reads as a new screen rather than a duplicate. If the model needs data from another package (e.g. `normalize`) to decide what to show, pass a callback rather than importing it into `ui`.

<!-- Add new lessons above this line -->
