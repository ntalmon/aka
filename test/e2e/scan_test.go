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

	cursor := env.ReadFile(".config", "aka", "zsh", "history_cursor.json")
	if !strings.Contains(cursor, "60") {
		t.Fatalf("cursor does not record the 60 seeded commands:\n%s", cursor)
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

// Navigating back from the suggestion review must not leave a duplicate menu on
// screen — the bug class documented in CLAUDE.md's "Lessons learned".
func TestBackFromSuggestionsDoesNotDuplicateMenu(t *testing.T) {
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

	// The censor review comes back; the scope picker must not be redrawn.
	c.Expect("commands to the LLM?")

	if n := c.CountOccurrences("How much history to send to the LLM?"); n != 1 {
		t.Errorf("scope picker rendered %d times, want 1\n%s", n, c.Screen())
	}

	c.Send(Enter)
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
