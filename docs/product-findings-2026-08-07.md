# Product findings surfaced by the e2e suite

**Date:** 2026-08-07
**Source:** building the end-to-end integration suite (`test/e2e/`)

Every item below was found by testing the shipped binary end to end, verified against source,
and **deliberately not fixed** — each is documented by a test that asserts current behaviour,
so closing any of them will fail loudly rather than silently drift.

None of these were visible to the existing unit tests.

---

## 1. No backup mechanism exists

**Severity: high — no recovery path for destructive operations.**

`CLAUDE.md` claimed `WriteAliasesFile()` "always does a full rewrite … with a timestamped
backup taken first", and documented a `backups/` runtime directory. Neither exists.

`grep -rn "Backup" internal/ cmd/` returns nothing outside tests. `WriteAliasesFile` and
`SaveInstalled` call `atomicWriteFile` directly. No `backups/` directory is ever created.

Git archaeology: commit `e23930d` deleted `aliases.BackupDir()`, `aliases.Backup()`, and the
rc-file backup call, leaving the doc comments behind. That commit's tree would not build —
`internal/cli/undo.go` still called the just-deleted `BackupDir()`. The next commit, `9c464d0`,
deleted `undo.go` entirely (replacing `aka undo` with `aka delete`), so the broken state never
shipped, but backups were gone for good.

**Consequence:** `aka scan`'s apply step and `aka delete` both rewrite the user's `aliases.sh`
with no snapshot. A bad apply or an accidental delete cannot be recovered.

**Status:** `CLAUDE.md` corrected to describe reality. The stale doc comment on `Init()` at
`internal/aliases/aliases.go` corrected. Pinned by assertions in `TestScanInteractiveAppliesSuggestions`
and `TestListThenDelete` that no backups are produced.

---

## 2. `migrateFromLegacy` can re-run forever and re-overwrite current data

**Severity: medium — silent data loss under a specific layout.**

`internal/aliases/aliases.go` — the migration copies legacy `aliases.sh` / `installed.json` /
`history_cursor.json` into the per-shell directory but **never deletes the legacy source**, and
its "already migrated" guard stats the **destination** `installed.json`.

**Consequence:** a legacy directory containing `aliases.sh` but no `installed.json` re-migrates
on every subsequent `aka init` / `aka scan` — and each time it **re-overwrites** the current
per-shell `aliases.sh` from stale legacy content — until an `installed.json` happens to appear.

**Status:** the copy-without-cleanup half is pinned by `TestInitMigratesLegacyLayout`. The
re-run-forever half is documented but not yet asserted; a follow-up test (run `init` twice,
assert the migration notice appears twice) would pin it.

---

## 3. Generated zsh completion calls `compdef` unguarded

**Severity: low — cosmetic, but every bare-zsh user sees an error.**

`internal/aliases/aliases.go` — the generated zsh completion script ends with an unconditional
`compdef _aka_completion aka`, with no check that `compinit` has run. On a zsh setup without a
completion framework this prints `command not found: compdef` on every shell start.

**Status:** `TestInitProducesSourceableRC` skips `compdef` lines specifically while still failing
on any other `command not found` / `parse error` / `syntax error`.

---

## 4. `ValidateFunctionTemplate` only rejects `}`

**Severity: low-to-medium — depends how much you trust the LLM and the user's review.**

`internal/aliases/aliases.go` rejects only a line-leading `}`. A function template containing
`$(...)`, backticks, or embedded newlines passes validation and is written verbatim into
`aliases.sh` inside a proper function wrapper.

Mitigating: the user reviews every suggestion before accepting, and `zsh -n` / `bash -n` confirm
the generated file always parses cleanly. So this is a trust question, not a syntax break.

**Status:** pinned by `TestCommandSubstitutionInTemplateIsCurrentlyAccepted`, which carries a
`t.Skip` escape hatch and instructions to delete the test if the validator is ever tightened.

---

## 5. `mysql -p<password>` reaches the LLM uncensored

**Severity: medium — a real secret leaves the machine. Owner has chosen to defer.**

Found by asserting on the **actual bytes** POSTed to the LLM, not on the censor's return value.

Two independent misses:

- The password rule is `(?i)(password|passwd|pass|pwd)=\S+` — it requires an `=`. The
  `-p<value>` idiom has no separator.
- `CensorSecrets` splits on **whitespace**, so the token is `-phunter2supersecret` — exactly
  20 characters. It **clears** `isLikelySecret`'s `len >= 20` gate and the 2-of-3 char-class
  gate, then fails at the entropy check: 3.284 bits/char against a 4.5 threshold, because the
  password reads as ordinary pronounceable words.

**Consequence for any fix:** lowering the length floor would **not** close this. A fix needs
either a rule for glued `-p<value>`-style flags, or a lower entropy threshold — and the latter
would false-positive on ordinary prose.

**Status:** owner decided to defer. Pinned by `TestKnownCensorGapPasswordFlagWithoutEquals`,
which asserts the leak **still occurs**, so closing the gap fails loudly and the test can be
deleted at that point.

---

## Explicitly not a bug

**Unmasked IP addresses.** `ssh deploy@10.1.2.3` sends the IP to the LLM. Ruled working as
designed by the product owner. `internal/censor/censor.go` skips `peekTyp == "IP"`
**unconditionally for every binary**, before the git-specific check is reached — the repo's own
`TestParameterizeVarsIPVaryingNotCensored` shows three distinct IPs staying unmasked. Pass 2
parameterises repeated values; it was never a secret-detection rule.

`CLAUDE.md`'s claim that for git commands "only IPs are parameterized" was false and has been
corrected, along with a placeholder list that mentioned `<PATH_n>` / `<IP_n>` — neither is ever
emitted.

---

## Suggested follow-ups, in priority order

1. Decide on backups (#1). Everything else here is cosmetic by comparison; this one has no
   recovery path.
2. Fix the `migrateFromLegacy` guard to check the source, and delete legacy files after a
   successful copy (#2).
3. Close the `-p<value>` censor gap (#5), if the leak matters more than the false-positive risk.
4. Guard the `compdef` call (#3).
5. Decide whether `ValidateFunctionTemplate` should reject command substitution (#4).
