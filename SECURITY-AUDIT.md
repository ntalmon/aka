# aka-cli Security Audit

A repeatable security review tailored to this project's trust boundaries.
Each section lists the threat model, the specific checks, and the concrete
files/symbols to inspect. A reviewer (human or automated) should be able
to go top-to-bottom and produce a pass/fail per item.

Severity legend: **C**ritical / **H**igh / **M**edium / **L**ow / **I**nfo.

---

## 1. Trust boundary: LLM output → user's shell

The most important boundary in this project. The LLM's `suggest_aliases`
tool output is materialized into `~/.config/aka/aliases.sh`, which is
sourced by the user's shell on every interactive startup. A malicious
or jailbroken model response can therefore achieve **code execution
in the user's interactive shell**.

| # | Severity | Check | Where |
|---|----|----|----|
| 1.1 | **C** | Alias/function `name` from LLM is validated against a strict allowlist (`^[A-Za-z_][A-Za-z0-9_]{0,31}$`) before any shell interpolation. | [internal/apply/apply.go](internal/apply/apply.go), [internal/aliases/aliases.go](internal/aliases/aliases.go) |
| 1.2 | **C** | `NameExistsInShell` does **not** interpolate the name into a `sh -c` string. Currently uses `fmt.Sprintf("type %s …", name)` → command injection if name passes 1.1. Use `exec.Command("sh", "-c", "type \"$1\"", "--", name)` or skip the shell entirely. | [internal/apply/apply.go:25](internal/apply/apply.go#L25) |
| 1.3 | **C** | Function body (`Template`) is sanitized/escaped before being written into `aliases.sh`. Currently `renderFunction` writes the raw LLM string between `function name () { … }` — LLM can inject arbitrary shell. Decide policy: reject, or quote, or sandbox. | [internal/aliases/aliases.go:325](internal/aliases/aliases.go#L325) |
| 1.4 | **H** | `escapeAlias` correctly escapes every shell metacharacter that matters inside single-quoted alias bodies (currently only escapes `'`). Verify against `$`, backtick, `\`, and embedded newlines. | [internal/aliases/aliases.go:316](internal/aliases/aliases.go#L316) |
| 1.5 | **H** | Suggestions are presented to the user with the *exact* string that will be written to disk (no rendering that hides metacharacters or non-printable bytes). Reject suggestions containing control chars. | [internal/ui/ui.go](internal/ui/ui.go) |
| 1.6 | **M** | The auto-reload `aka()` wrapper added to rcfiles re-sources `aliases.sh` after every `aka scan`. Confirm `dry-run` and aborted-review code paths do **not** invoke `WriteAliasesFile`, so a hostile-looking suggestion the user rejected is never loaded. | [internal/aliases/aliases.go:96](internal/aliases/aliases.go#L96), [internal/cli/scan.go](internal/cli/scan.go) |
| 1.7 | **M** | `Params[].Name` is validated as `$1..$9`-safe — LLM-supplied param names are not interpolated into the function body in a way that allows injection. | [internal/aliases/aliases.go:18](internal/aliases/aliases.go#L18) |
| 1.8 | **L** | The "this exists in shell" check (`type`) cannot itself be tricked into reporting false-negative (e.g., name = `-h` → `type -h` prints help instead of looking up a name). | [internal/apply/apply.go:25](internal/apply/apply.go#L25) |

---

## 2. Trust boundary: shell history → LLM provider

Shell history routinely contains paths, hostnames, usernames, internal
URLs, and (despite the best intentions of users) raw secrets. This is
the privacy boundary. A miss here means a real secret leaves the
machine.

| # | Severity | Check | Where |
|---|----|----|----|
| 2.1 | **H** | Secret regex pack covers the agreed corpus: AWS access keys, AWS secret keys (40-char b64), GCP service-account JSON markers, GitHub PATs/fine-grained/Apps tokens, OpenAI/Anthropic/Groq/Gemini keys, Slack tokens (`xox[abprs]-`), Stripe (`sk_live_`, `pk_live_`), JWTs, Bearer tokens, Basic-auth URLs, `.env` `KEY=VAL`. Track misses with a known-corpus test. | [internal/censor/censor.go:27](internal/censor/censor.go#L27) |
| 2.2 | **H** | Entropy heuristic threshold is regression-tested. `isLikelySecret` currently requires upper+lower+digit + entropy > 4.5 + length ≥ 20; verify that real secrets without an uppercase character (many `sk-…`-style keys) still trip a regex (since the heuristic would skip them). | [internal/censor/censor.go:65](internal/censor/censor.go#L65) |
| 2.3 | **H** | `--dry-run` output never includes uncensored history. Manually diff `aka scan --dry-run` against `~/.zsh_history` containing planted canaries. | [internal/cli/scan.go:344](internal/cli/scan.go#L344) |
| 2.4 | **M** | IPv4 censoring (`IP_n`) covers IPv6 too, or the policy explicitly excludes IPv6 with rationale. | [internal/censor/censor.go:208](internal/censor/censor.go#L208) |
| 2.5 | **M** | Hostname inference does not leak internal hostnames (e.g., `api.internal.acme.corp`). Currently anything containing a `.` is labelled `HOST` and replaced — confirm. | [internal/censor/censor.go:256](internal/censor/censor.go#L256) |
| 2.6 | **M** | `ui.ReviewCensored` truly shows the user the post-censor text before transmission, and the "edit manually" path cannot be skipped via env (`$EDITOR=/bin/true`) without an explicit confirmation. | [internal/ui/ui.go](internal/ui/ui.go) |
| 2.7 | **M** | TLS: each provider uses `http.DefaultTransport` with system roots and `MinVersion` ≥ TLS 1.2. No `InsecureSkipVerify`. | [internal/llm/*.go](internal/llm/) |
| 2.8 | **M** | API base URLs are constants — not derived from config or env — preventing accidental redirection through a proxy attacker. (Ollama is local-only.) | [internal/llm/anthropic.go:18](internal/llm/anthropic.go#L18), [internal/llm/groq.go](internal/llm/groq.go), [internal/llm/openai.go](internal/llm/openai.go), [internal/llm/gemini.go](internal/llm/gemini.go) |
| 2.9 | **M** | Request bodies are *not* logged by any debug/verbose flag (would re-leak the censored-but-still-private history). Grep for `log.`, `fmt.Print*` near request building. | [internal/llm/](internal/llm/) |
| 2.10 | **L** | Ollama endpoint is hard-coded to `127.0.0.1`/`localhost` only (not user-configurable to a remote IP), or explicitly documented as a remote-leak risk. | [internal/llm/ollama.go](internal/llm/ollama.go) |
| 2.11 | **L** | LLM provider error messages are sanitized before printing — provider 4xx responses occasionally echo the request body. | [internal/llm/*.go](internal/llm/) error-handling paths |

---

## 3. On-disk secrets & config

| # | Severity | Check | Where |
|---|----|----|----|
| 3.1 | **H** | `~/.config/aka/config.toml` is created with mode `0600` (currently `Save` lets viper choose — verify the actual on-disk perms). | [internal/config/config.go:72](internal/config/config.go#L72) |
| 3.2 | **H** | `~/.config/aka` directory is created `0700` (currently `0755`). API keys live there. | [internal/config/config.go:31](internal/config/config.go#L31), [internal/aliases/aliases.go:42](internal/aliases/aliases.go#L42) |
| 3.3 | **M** | Backup files under `~/.config/aka/backups/` (which may contain LLM-derived aliases, RC-file contents) are written `0600`. | [internal/aliases/aliases.go:264](internal/aliases/aliases.go#L264) |
| 3.4 | **M** | `aka config show` redacts API keys (don't print the raw value). | [internal/cli/config_cmd.go](internal/cli/config_cmd.go) |
| 3.5 | **M** | No keys are leaked into process arguments (`ps`-visible) or env vars set for child processes. Grep `os.Setenv`, `exec.Command` args. | repo-wide |
| 3.6 | **L** | Provide an opt-in pathway to the OS keychain (macOS Keychain, libsecret) and document the plaintext-on-disk trade-off in README. | [README.md](README.md), [internal/config/config.go](internal/config/config.go) |

---

## 4. File-system safety & TOCTOU

| # | Severity | Check | Where |
|---|----|----|----|
| 4.1 | **H** | `WriteAliasesFile`, `SaveInstalled`, and `Save` write atomically: write to `*.tmp` in the same directory, `fsync`, then `os.Rename`. Today they use `os.WriteFile` (non-atomic; an interrupted run can leave a half-written `aliases.sh` that the shell still sources). | [internal/aliases/aliases.go:295](internal/aliases/aliases.go#L295), [internal/aliases/aliases.go:248](internal/aliases/aliases.go#L248), [internal/config/config.go:72](internal/config/config.go#L72) |
| 4.2 | **H** | `os.WriteFile`/`os.OpenFile` paths refuse to follow symlinks pointing outside `~/.config/aka` (defense against a hostile user account sharing the homedir, or against a misconfigured shared host). Use `O_NOFOLLOW` where supported, or `lstat`-check the parent. | [internal/aliases/aliases.go](internal/aliases/aliases.go) |
| 4.3 | **M** | `Init` appends to rcfiles only after taking a backup (currently does). Verify the backup itself is mode `0600`, since rcfiles may contain secrets. | [internal/aliases/aliases.go:118](internal/aliases/aliases.go#L118) |
| 4.4 | **M** | Idempotency: re-running `aka init` does not duplicate the shell wrapper / source line under any rcfile state (existing tests cover this — keep them). | [internal/aliases/aliases_test.go](internal/aliases/aliases_test.go) |
| 4.5 | **M** | `aka undo` cannot be tricked into deleting an entry it didn't install (e.g., name collision with a hand-edited shell function). Today removal goes through `WriteAliasesFile` from `installed.json`, which is correct — guard against future regressions with a test. | [internal/cli/undo.go](internal/cli/undo.go) |
| 4.6 | **L** | Backups are pruned (or at least documented) — they accumulate forever today and may grow to contain many copies of API-key-bearing rcfiles. | [internal/aliases/aliases.go:75](internal/aliases/aliases.go#L75) |

---

## 5. Distribution & supply chain

| # | Severity | Check | Where |
|---|----|----|----|
| 5.1 | **H** | `install.sh` verifies checksums; the script aborts (not warns) when `sha256sum`/`shasum` is absent. Today it warns and proceeds — turn into a hard fail. | [install.sh:55-62](install.sh) |
| 5.2 | **H** | GitHub Release artifacts are signed (cosign / GPG / SLSA provenance), and `install.sh` verifies the signature on `checksums.txt` before trusting it. Without this, a release-asset overwrite (compromised tap or PAT) defeats the checksum step. | [.goreleaser.yaml](.goreleaser.yaml), [install.sh](install.sh) |
| 5.3 | **H** | Release workflow uses the smallest possible `permissions:` (`contents: write` only); no `id-token: write` unless used; no broad `actions: read`. | [.github/workflows/release.yml](.github/workflows/release.yml) |
| 5.4 | **H** | `HOMEBREW_TAP_GITHUB_TOKEN` PAT scope is `repo` on the **tap repo only** (not org-wide). Rotated on a schedule. Document rotation. | repo settings (manual check) |
| 5.5 | **H** | GitHub Actions are pinned by SHA, not by tag (`actions/checkout@v4` → `actions/checkout@<sha>`). Today all three workflows pin by floating tag. | [.github/workflows/](.github/workflows/) |
| 5.6 | **M** | `go.sum` is checked in (it is) and `GOFLAGS=-mod=readonly` is set in CI to fail on out-of-sync modules. | [.github/workflows/ci.yml](.github/workflows/ci.yml) |
| 5.7 | **M** | `golangci-lint` and `govulncheck` are pinned versions, not `@latest`. Today both use `@latest` — a malicious upstream tag would run as root in CI. | [.github/workflows/ci.yml:32-39](.github/workflows/ci.yml) |
| 5.8 | **M** | `claude.yml` only triggers on `@claude` from users with `write` permissions on the repo (currently no guard — *any* commenter can trigger it on a PR, billing your `ANTHROPIC_API_KEY`). Add `if: github.event.comment.author_association == 'OWNER' \|\| 'MEMBER' \|\| 'COLLABORATOR'`. | [.github/workflows/claude.yml](.github/workflows/claude.yml) |
| 5.9 | **M** | Release workflow uses Go 1.22 while CI and `go.mod` are Go 1.25 — CVE-fixes covered by govulncheck on Go 1.25 may not apply to the released binary. Align versions. | [.github/workflows/release.yml:23](.github/workflows/release.yml#L23) vs [.github/workflows/ci.yml:18](.github/workflows/ci.yml#L18), [go.mod:3](go.mod#L3) |
| 5.10 | **L** | Dependency floor (`go.mod`) does not include known-vulnerable versions of `viper`/`spf13/cast` transitive deps. `govulncheck` already runs — keep it. | [go.sum](go.sum) |
| 5.11 | **L** | `install.sh` quotes `${INSTALL_DIR}` in the PATH hint (it does), and shellcheck passes on it. | [install.sh](install.sh) |

---

## 6. Process & runtime hygiene

| # | Severity | Check | Where |
|---|----|----|----|
| 6.1 | **M** | `exec.Command` invocations have an explicit `argv` and never pass user-controlled strings into `sh -c "…"`. Current single offender is `NameExistsInShell` (see 1.2). Grep `exec.Command\("sh"` and `exec.Command\(".*-c"`. | [internal/apply/apply.go:25](internal/apply/apply.go#L25), repo-wide |
| 6.2 | **M** | `$EDITOR` invocation in `ui.ReviewCensored` resolves via `exec.LookPath`, runs with current env (no PATH override), and on failure does not fall through to "send anyway". | [internal/ui/ui.go](internal/ui/ui.go) |
| 6.3 | **L** | All HTTP clients set a finite `Timeout` (Anthropic uses 120s — confirm Groq/OpenAI/Gemini/Ollama too). | [internal/llm/](internal/llm/) |
| 6.4 | **L** | Context cancellation is honored (`http.NewRequestWithContext`) so a Ctrl-C during a request actually aborts it. Currently true for Anthropic — verify other providers. | [internal/llm/](internal/llm/) |
| 6.5 | **L** | `aka` has no unexpected outbound calls. Run `aka scan --dry-run` under `lsof`/`tcpdump` and confirm zero network. | manual |

---

## 7. Static & dynamic analysis tooling

Run these as part of the audit; failures are findings.

| Tool | Purpose | Already in CI? |
|----|----|----|
| `govulncheck ./...` | Known Go CVEs | ✅ ([.github/workflows/ci.yml](.github/workflows/ci.yml)) |
| `gosec ./...` | Go security lints (hardcoded creds, weak crypto, command injection) | ❌ |
| `golangci-lint` w/ `gosec`, `bodyclose`, `errcheck`, `gocritic`, `gas` | Broader linting | partial ✅ |
| `staticcheck` | Bug-finding | via golangci-lint |
| `semgrep --config p/golang --config p/security-audit` | Custom rules (e.g. ban `fmt.Sprintf` into `exec.Command`) | ❌ |
| `osv-scanner` | Dependency vulns beyond stdlib | ❌ |
| `trivy fs --scanners vuln,secret,config` | Filesystem scan incl. accidental committed secrets | ❌ |
| `shellcheck install.sh aliases.sh-template` | Shell-script bugs | ❌ |
| `actionlint` | GitHub Actions correctness/security | ❌ |
| `zizmor` | GitHub Actions security audit (untrusted input, perms) | ❌ |

Dynamic:

- **Fuzz** `censor.CensorSecrets` and `apply.NameExistsInShell` against a corpus of real-world secrets and adversarial alias names.
- **Property test** `WriteAliasesFile`: round-tripping `installed.json` → `aliases.sh` → re-parse must preserve names exactly.
- **End-to-end smoke**: run `aka scan --dry-run` against a planted history file containing canaries (`AKIA…`, `sk-ant-…`, `Bearer eyJ…`) and assert none appear in the request body.

---

## 8. Findings index (filled in per-run)

For each check above that fails:

```
ID: 1.2
Severity: Critical
Status: Open
Summary: Shell command injection via LLM-controlled alias name in NameExistsInShell.
Reproduction: ...
Recommendation: ...
Owner: @ntalmon
Tracking: gh issue #...
```
