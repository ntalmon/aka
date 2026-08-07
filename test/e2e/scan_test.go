//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/ntalmon/aka/test/e2e/fakellm"
)

// workflowSuggestions is the standard two-suggestion response: one alias and
// one multi-command function.
func workflowSuggestions() []fakellm.Suggestion {
	return []fakellm.Suggestion{
		{
			Name:      "e2egs",
			Kind:      "alias",
			Template:  "git status --short",
			Rationale: "git status is run constantly.",
		},
		{
			Name:        "e2eship",
			Kind:        "function",
			Template:    "git add -A\ngit commit -m \"$1\"\ngit push",
			Params:      []fakellm.Param{{Name: "message", Type: "string", Description: "commit message"}},
			Rationale:   "Combines the add/commit/push sequence.",
			ExampleUses: []string{"e2eship 'fix bug'"},
		},
	}
}

// realisticHistory returns commands that look like genuine sequential work.
func realisticHistory() []string {
	var cmds []string
	for i := 0; i < 20; i++ {
		cmds = append(cmds,
			"git status",
			"git add -A",
			"git commit -m 'work in progress'",
		)
	}
	return cmds
}

func scanEnv(t *testing.T) *Env {
	t.Helper()
	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory(realisticHistory()...)
	env.LLM.Respond(fakellm.Normal(workflowSuggestions()...))
	return env
}

// The full interactive path: scope picker → censor review → suggestion review
// → apply, driven entirely over the PTY.
func TestScanInteractiveAppliesSuggestions(t *testing.T) {
	t.Parallel()

	env := scanEnv(t)

	c := env.Spawn("scan")
	c.Expect("How much history to send to the LLM?")
	c.Send(Enter) // "Send full history"

	c.Expect("commands to the LLM?")
	c.Expect("Yes, send ")
	c.Send(Enter) // send as-is

	c.Expect("AKA FOUND")
	c.Expect("space toggle") // the review list is up

	c.Send(Space)       // select the first suggestion
	c.Send(Down, Space) // select the second
	c.Send(Down)
	c.ExpectRe(`Apply \d+ suggestion\(s\) & exit`)
	c.Send(Enter)

	c.ExpectRe(`Applied \d+ alias\(es\)/function\(s\)!`)
	if code := c.Wait(); code != 0 {
		t.Fatalf("exit code = %d", code)
	}

	aliasesSh := env.AliasesFile()
	for _, name := range []string{"e2egs", "e2eship"} {
		if !strings.Contains(aliasesSh, name) {
			t.Errorf("aliases.sh missing %q:\n%s", name, aliasesSh)
		}
	}

	installed := env.Installed()
	if len(installed) != 2 {
		t.Fatalf("installed.json has %d entries, want 2: %+v", len(installed), installed)
	}
	for _, entry := range installed {
		if entry.Source != "scan" {
			t.Errorf("entry %q Source = %q, want scan", entry.Name, entry.Source)
		}
	}

	// CLAUDE.md documents WriteAliasesFile as always taking a timestamped
	// backup before rewriting aliases.sh, and lists a backups/ directory
	// under ~/.config/aka/<shell>/ as a runtime file. Neither exists in the
	// current code: WriteAliasesFile (internal/aliases/aliases.go) calls
	// atomicWriteFile directly with no backup step, and the BackupDir()
	// helper that once supported `aka undo` was deleted in commit e23930d
	// ("per-shell data layout") without updating the doc comments (Init's
	// still claims "It takes a backup of rcFile before modifying it") or
	// CLAUDE.md. This looks like a real regression — see the task report.
	if backups := env.Backups(); len(backups) != 0 {
		t.Errorf("expected no backups directory to be populated (aka never writes one), got %v", backups)
	}
}

// The generated file must always be valid shell, and the aliases must actually
// resolve in a real interactive shell.
func TestAppliedAliasesResolveInRealShell(t *testing.T) {
	t.Parallel()

	env := scanEnv(t)
	applyBothSuggestions(t, env)

	for _, shellCheck := range []string{"zsh -n", "bash -n"} {
		parts := strings.Fields(shellCheck)
		out, code := runCommand(t, env, parts[0], parts[1], env.ShellPath("aliases.sh"))
		if code != 0 {
			t.Errorf("%s rejected aliases.sh: exit %d\n%s", shellCheck, code, out)
		}
	}

	out, code := env.RunShell("type e2egs")
	if code != 0 {
		t.Fatalf("`type e2egs` exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "e2egs") {
		t.Fatalf("e2egs did not resolve in a real shell:\n%s", out)
	}

	out, code = env.RunShell("type e2eship")
	if code != 0 {
		t.Fatalf("`type e2eship` exited %d\n%s", code, out)
	}
}

func TestScanWritesHistoryCursor(t *testing.T) {
	t.Parallel()

	env := scanEnv(t)
	applyBothSuggestions(t, env)

	// Exact match, not strings.Contains: {"total":60} also substring-matches
	// {"total":160}, {"total":600}, {"total":1760}, etc., so a Contains check
	// here would pass even if the recorded total were wrong by an order of
	// magnitude.
	cursor := env.ReadFile(".config", "aka", "zsh", "history_cursor.json")
	if cursor != `{"total":60}` {
		t.Fatalf("cursor = %q, want %q (60 seeded commands)", cursor, `{"total":60}`)
	}
}

func TestListThenDelete(t *testing.T) {
	t.Parallel()

	env := scanEnv(t)
	applyBothSuggestions(t, env)

	out, code := env.Run("list")
	if code != 0 {
		t.Fatalf("list exit code = %d\n%s", code, out)
	}
	for _, name := range []string{"e2egs", "e2eship"} {
		if !strings.Contains(out, name) {
			t.Errorf("list output missing %q:\n%s", name, out)
		}
	}

	c := env.Spawn("delete", "e2egs")
	c.Expect("Continue? [y/N]")
	c.SendLine("y")
	if code := c.Wait(); code != 0 {
		t.Fatalf("delete exit code = %d", code)
	}

	aliasesSh := env.AliasesFile()
	if strings.Contains(aliasesSh, "e2egs") {
		t.Errorf("e2egs still in aliases.sh after delete:\n%s", aliasesSh)
	}
	if !strings.Contains(aliasesSh, "e2eship") {
		t.Errorf("delete removed the wrong entry:\n%s", aliasesSh)
	}
	if len(env.Installed()) != 1 {
		t.Errorf("installed.json has %d entries, want 1", len(env.Installed()))
	}
	// aka delete goes through the same WriteAliasesFile/SaveInstalled path as
	// scan's apply step, which — see TestScanInteractiveAppliesSuggestions —
	// never writes a backup. Documenting the same actual behaviour here
	// rather than the CLAUDE.md-documented one.
	if backups := env.Backups(); len(backups) != 0 {
		t.Errorf("expected no backups directory to be populated (aka never writes one), got %v", backups)
	}

	if out, _ := env.RunShell("type e2egs"); strings.Contains(out, "alias") {
		t.Errorf("deleted alias still resolves in the shell:\n%s", out)
	}
}

// --history diff below minDiffCount (40) must not call the LLM at all.
func TestScanDiffBelowThresholdMakesNoLLMCall(t *testing.T) {
	t.Parallel()

	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory("git status", "git log", "ls -la")
	env.LLM.Respond(fakellm.Normal(workflowSuggestions()...))

	out, code := env.Run("scan", "--history", "diff", "--censor", "trust")
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	if n := len(env.LLM.Requests()); n != 0 {
		t.Fatalf("made %d LLM request(s) for a below-threshold diff, want 0", n)
	}
}

// Navigating back from the SUGGESTION review returns to the censor review
// without re-entering PromptScanFlow (the scope+censor picker). This does
// NOT cover the in-place scope<->censor transition inside PromptScanFlow —
// see TestBackFromCensorReviewReturnsToScopePicker below for that, and the
// doc comment on this test for why a genuine "was it drawn twice" guard is
// not achievable at either transition with this harness.
//
// On this path, runScan (internal/cli/scan.go ~281-296) calls
// ui.PrintCensorDiff + ui.ReviewCensored(..., false) directly and never
// re-enters ui.PromptScanFlow, so the scope picker cannot possibly render
// again here — the CountOccurrences(...) == 1 assertion below is true BY
// CONSTRUCTION, not because it caught a bug. Renamed (from
// TestBackFromSuggestionsDoesNotDuplicateMenu) and rewritten after the final
// whole-branch review flagged that the original name/comment overclaimed
// what this test proves: the double-menu bug this suite is meant to guard
// against (CLAUDE.md's "Lessons learned") actually lives at a different
// transition — scanFlowModel.updateCensor's tea.KeyLeft handler
// (internal/ui/ui.go, back to sfStepScope) — which this test's back-press
// (at the SUGGESTION review, a separate ui.ReviewSuggestions program) never
// exercises.
//
// What this test DOES prove, and is worth keeping for: going back from the
// suggestion review re-renders the censor review exactly once, and the flow
// can still be driven to completion (Enter -> AKA FOUND) afterward.
//
// Why no test can assert "not drawn twice" as a render-count check at
// EITHER transition: bubbletea's standard renderer only skips re-emitting
// lines that are byte-identical to the previous frame. An in-place
// transition (new step, different title/content) is not identical to the
// previous frame, so it re-emits the changed lines into the byte stream
// regardless of whether the redraw happened in place or a second copy was
// printed below the first. Distinguishing "redrawn in place" from "printed
// twice underneath" requires a terminal-emulator screen model (cursor
// position + a 2D grid, so a redraw's cursor-up-then-overwrite is visible as
// occupying the same rows rather than appending new ones) — Console
// deliberately does not have one, it accumulates a stream
// (raw/stripped strings.Builder), not a screen. A CountOccurrences(...) == 1
// assertion only tells you the substring appears once in that stream; on
// the suggestion-review path that's guaranteed by the code structure (no
// second entry point exists to draw it again), and on the censor-review
// path (a true in-place tea.Model step swap within one tea.NewProgram) it
// would tell you nothing else either, since a single in-place transition
// and an actual double-print both emit the changed text into the stream —
// just at different byte offsets a flat string search can't distinguish.
// A future `Console.VisibleScreen()` that replays raw bytes through a real
// cursor-aware grid (e.g. wrapping a vt10x/similar terminal emulator) could
// close this gap; noted here rather than built, since it's a meaningful
// harness addition on its own, not a one-line fix.
func TestBackFromSuggestionsDoesNotRestartScopeProgram(t *testing.T) {
	t.Parallel()

	env := scanEnv(t)

	c := env.Spawn("scan")
	c.Expect("How much history to send to the LLM?")
	c.Send(Enter)
	c.Expect("commands to the LLM?")
	c.Send(Enter)
	c.Expect("AKA FOUND")
	c.Expect("space toggle")

	c.Send(Left) // back
	c.Expect("Are you sure you want to go back")
	c.Send(Down, Enter) // "Yes, go back"

	// The censor review comes back; the scope picker must not be redrawn
	// (guaranteed by runScan's control flow on this path — see doc comment).
	c.Expect("commands to the LLM?")

	if n := c.CountOccurrences("How much history to send to the LLM?"); n != 1 {
		t.Errorf("scope picker rendered %d times, want 1\n%s", n, c.Screen())
	}

	c.Send(Enter)
	c.Expect("AKA FOUND")
	c.Send(CtrlC)
	_ = c.Wait()
}

// Pressing Left at the CENSOR review (inside ui.PromptScanFlow, before any
// suggestion review exists) is the actual in-place transition CLAUDE.md's
// "Lessons learned" double-menu bug lives at:
// scanFlowModel.updateCensor's tea.KeyLeft case swaps m.step back to
// sfStepScope within the SAME tea.NewProgram (internal/ui/ui.go
// ~1535-1538), rather than starting a second program. No prior test in this
// suite pressed Left at this specific step.
//
// This test asserts the transition works BEHAVIOURALLY — the scope picker
// becomes interactive again and a fresh selection carries all the way
// through to the suggestion review — not that anything was "drawn once".
// See TestBackFromSuggestionsDoesNotRestartScopeProgram's doc comment for
// why a render-count-based double-menu guard isn't achievable with this
// harness at this transition either: a CountOccurrences(...) == 1 check here
// would be just as unable to distinguish "redrawn in place" from "printed
// twice below it" as it is on the suggestion-review path, since both put the
// same changed text into the stream exactly once either way.
func TestBackFromCensorReviewReturnsToScopePicker(t *testing.T) {
	t.Parallel()

	env := scanEnv(t)

	c := env.Spawn("scan")
	c.Expect("How much history to send to the LLM?")
	c.Send(Enter) // "Send full history"

	c.Expect("commands to the LLM?")
	c.Send(Left) // back to the scope picker, in place within PromptScanFlow

	c.Expect("How much history to send to the LLM?")
	c.Send(Enter) // make a fresh selection ("Send full history" again)

	// The censor review must come back and the flow must still reach the
	// suggestion review afterward — proving the in-place back transition
	// left the program in a working state, not a stuck or corrupted one.
	c.Expect("commands to the LLM?")
	c.Send(Enter) // send as-is
	c.Expect("AKA FOUND")

	c.Send(CtrlC)
	_ = c.Wait()
}

// applyBothSuggestions runs a non-interactive scan and accepts both suggestions
// through the review UI, leaving the env with e2egs and e2eship installed.
func applyBothSuggestions(t *testing.T, env *Env) {
	t.Helper()

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Expect("space toggle")
	c.Send(Space)
	c.Send(Down, Space)
	c.Send(Down)
	c.ExpectRe(`Apply \d+ suggestion\(s\) & exit`)
	c.Send(Enter)
	c.ExpectRe(`Applied \d+ alias\(es\)/function\(s\)!`)
	if code := c.Wait(); code != 0 {
		t.Fatalf("scan exit code = %d", code)
	}
}
